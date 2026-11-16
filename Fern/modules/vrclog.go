package modules

import (
	"fmt"
	"strings"
	"time"
)

// Kinds of LogEvent.
const (
	EventJoin       = "join"
	EventLeave      = "leave"
	EventScreenshot = "screenshot"
	EventLoaded     = "loaded" // a world finished downloading and loading
)

// WorldInfo is the instance VRChat is in, built from its log.
type WorldInfo struct {
	Name     string `json:"name"`
	ID       string `json:"id"`       // wrld_...
	Instance string `json:"instance"` // after the colon, tags included
	Type     string `json:"type"`     // Public, Friends+, Invite, Group+ ...
	Region   string `json:"region"`   // US West, US East, EU, JP
	Players  int    `json:"players"`
	Peak     int    `json:"peak"`     // most players at once in this instance
	JoinedAt int64  `json:"joinedAt"` // unix ms
	Loading  bool   `json:"loading"`  // joined but not loaded in yet
}

// SessionStats covers everything since Fern started.
type SessionStats struct {
	Worlds  int   `json:"worlds"`  // instances joined
	Players int   `json:"players"` // different players met, not counting you
	Peak    int   `json:"peak"`    // most players in one instance
	Since   int64 `json:"since"`   // unix ms
}

// Everything below is guarded by logMu (see logs.go).
var (
	selfName     string
	world        WorldInfo
	inWorld      bool
	roomName     string // last "Entering Room" name, which can come before or after "Joining"
	roomNameAt   time.Time
	loadStart    time.Time
	loadBytes    uint64 // network total when loading started; 0 when not measured
	session      = SessionStats{Since: time.Now().UnixMilli()}
	metPlayers   = map[string]bool{}
	seenPlayers  = map[string]bool{} // everyone seen this session, for "seen again"
	nowFunc      = time.Now
	netTotalFunc = func() (uint64, bool) {
		a, ok := CurrentNetAdapter(GetConfig().NetworkOptions.Adapter)
		return a.TotalDown, ok
	}
)

// logTime reads the "2006.01.02 15:04:05" stamp VRChat starts each line with.
func logTime(line string) time.Time {
	if len(line) >= 19 {
		if t, err := time.ParseInLocation("2006.01.02 15:04:05", line[:19], time.Local); err == nil {
			return t
		}
	}
	return nowFunc()
}

// after returns the text following marker, and whether the marker was found.
func after(line, marker string) (string, bool) {
	i := strings.Index(line, marker)
	if i < 0 {
		return "", false
	}
	return strings.TrimSpace(line[i+len(marker):]), true
}

// splitUser separates "Name (usr_...)" into the name and the id.
func splitUser(s string) (name, id string) {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, " ("); i >= 0 && strings.HasSuffix(s, ")") {
		return strings.TrimSpace(s[:i]), s[i+2 : len(s)-1]
	}
	return s, ""
}

// parseInstance reads the world id, access type and region from a location
// such as wrld_x:12345~hidden(usr_y)~region(eu).
func parseInstance(loc string) (worldID, access, region string) {
	loc = strings.TrimSpace(loc)
	worldID, rest, _ := strings.Cut(loc, ":")
	access, region = "Public", "US West"
	tags := strings.Split(rest, "~")
	has := func(tag string) bool {
		for _, t := range tags {
			if t == tag || strings.HasPrefix(t, tag+"(") {
				return true
			}
		}
		return false
	}
	value := func(tag string) string {
		for _, t := range tags {
			if v, ok := strings.CutPrefix(t, tag+"("); ok {
				return strings.TrimSuffix(v, ")")
			}
		}
		return ""
	}
	switch {
	case has("group"):
		switch value("groupAccessType") {
		case "public":
			access = "Group Public"
		case "plus":
			access = "Group+"
		default:
			access = "Group"
		}
	case has("private"):
		access = "Invite"
		if has("canRequestInvite") {
			access = "Invite+"
		}
	case has("friends"):
		access = "Friends"
	case has("hidden"):
		access = "Friends+"
	}
	switch value("region") {
	case "use":
		region = "US East"
	case "eu":
		region = "EU"
	case "jp":
		region = "JP"
	}
	return worldID, access, region
}

// parseWorldLine handles the lines about worlds, screenshots and the local
// user. It reports whether the line was one of them.
func parseWorldLine(line string, at time.Time) bool {
	if rest, ok := after(line, "[Behaviour] User Authenticated: "); ok {
		selfName, _ = splitUser(rest)
		return true
	}
	if rest, ok := after(line, "[Behaviour] Switching "); ok {
		noteAvatarSwitch(rest)
		return true
	}
	if rest, ok := after(line, "[Behaviour] Entering Room: "); ok {
		noteRoomName(rest, at)
		return true
	}
	if rest, ok := after(line, "[Behaviour] Joining or Creating Room: "); ok {
		noteRoomName(rest, at)
		return true
	}
	if rest, ok := after(line, "[Behaviour] Joining "); ok && strings.HasPrefix(rest, "wrld_") {
		enterInstance(rest, at)
		return true
	}
	if strings.Contains(line, "[Behaviour] OnLeftRoom") {
		leaveInstance()
		return true
	}
	if strings.Contains(line, "[Behaviour] Finished entering world") {
		finishLoading(at)
		return true
	}
	if _, ok := after(line, "[VRC Camera] Took screenshot to: "); ok {
		addActivityAt(at, EventScreenshot, "Took a screenshot")
		return true
	}
	return false
}

func noteRoomName(name string, at time.Time) {
	roomName, roomNameAt = name, at
	// "Entering Room" may follow "Joining": name the instance we just joined.
	if inWorld && world.Name == "" {
		world.Name = name
	}
}

func enterInstance(loc string, at time.Time) {
	id, access, region := parseInstance(loc)
	name := ""
	if roomName != "" && at.Sub(roomNameAt) < time.Minute && at.Sub(roomNameAt) >= -time.Minute {
		name = roomName
	}
	clearPlayers()
	world = WorldInfo{Name: name, ID: id, Instance: instancePart(loc), Type: access, Region: region, JoinedAt: at.UnixMilli(), Loading: true}
	inWorld = true
	session.Worlds++
	loadStart, loadBytes = at, 0
	// Only a live join can be measured; old log lines read at startup cannot.
	if d := nowFunc().Sub(at); d < 10*time.Second && d > -10*time.Second {
		if total, ok := netTotalFunc(); ok {
			loadBytes = total + 1 // +1 keeps a zero total distinct from "not measured"
		}
	}
}

// instancePart is the instance id and tags of "wrld_x:123~region(eu)".
func instancePart(loc string) string {
	_, inst, _ := strings.Cut(strings.TrimSpace(loc), ":")
	if sp := strings.IndexAny(inst, " 	"); sp >= 0 {
		inst = inst[:sp]
	}
	return inst
}

func leaveInstance() {
	clearPlayers()
	world = WorldInfo{}
	inWorld = false
	roomName = ""
}

// finishLoading records how long the world took, and how much was downloaded
// meanwhile when Fern saw it happen live.
func finishLoading(at time.Time) {
	if !inWorld || !world.Loading {
		return
	}
	world.Loading = false
	secs := int(at.Sub(loadStart).Round(time.Second) / time.Second)
	if secs < 0 {
		secs = 0
	}
	name := world.Name
	if name == "" {
		name = "the world"
	}
	text := fmt.Sprintf("Loaded %s in %s", name, shortDuration(secs))
	if loadBytes > 0 {
		if total, ok := netTotalFunc(); ok && total+1 >= loadBytes {
			if got := total + 1 - loadBytes; got >= 1_000_000 {
				text += " · " + formatSize(float64(got), false)
			}
		}
	}
	addActivityAt(at, EventLoaded, text)
}

func shortDuration(secs int) string {
	switch {
	case secs < 60:
		return fmt.Sprintf("%ds", secs)
	case secs < 3600:
		return fmt.Sprintf("%dm %ds", secs/60, secs%60)
	default:
		return fmt.Sprintf("%dh %dm", secs/3600, (secs%3600)/60)
	}
}

// notePlayerJoined updates the session numbers for a join and reports whether
// the player was already seen earlier this session.
func notePlayerJoined(name string, at time.Time) (again bool) {
	finishLoading(at) // the first join (yourself) means the world is loaded
	again = seenPlayers[name] && name != selfName
	seenPlayers[name] = true
	if name != selfName {
		metPlayers[name] = true
		session.Players = len(metPlayers)
	}
	n := len(joined)
	if inWorld {
		world.Peak = max(world.Peak, n)
	}
	session.Peak = max(session.Peak, n)
	return again
}

func clearPlayers() {
	joined = map[string]LogEvent{}
	order = nil
}

// CurrentWorld returns the instance VRChat is in, if known.
func CurrentWorld() (WorldInfo, bool) {
	logMu.Lock()
	defer logMu.Unlock()
	w := world
	w.Players = len(joined)
	return w, inWorld
}

// Session returns the numbers since Fern started.
func Session() SessionStats {
	logMu.Lock()
	defer logMu.Unlock()
	return session
}

// worldLine renders the world/instance module.
func worldLine(cfg Config) string {
	w, ok := CurrentWorld()
	if !ok || !CurrentVRChatStatus().Running {
		return ""
	}
	return formatWorld(cfg.WorldOptions, w, Session(), nowFunc())
}

func formatWorld(o WorldOptions, w WorldInfo, s SessionStats, now time.Time) string {
	var parts []string
	if o.ShowName {
		name := w.Name
		if name == "" {
			name = "Loading world"
		}
		if o.MaxChars > 0 {
			name = RuneLimit(name, o.MaxChars)
		}
		parts = append(parts, name)
	}
	if o.ShowCount {
		count := fmt.Sprintf("%d %s", w.Players, plural(w.Players, "player", "players"))
		if o.ShowPeak && w.Peak > w.Players {
			count += fmt.Sprintf(" (peak %d)", w.Peak)
		}
		parts = append(parts, count)
	}
	var inst []string
	if o.ShowType && w.Type != "" {
		inst = append(inst, w.Type)
	}
	if o.ShowRegion && w.Region != "" {
		inst = append(inst, w.Region)
	}
	if len(inst) > 0 {
		parts = append(parts, strings.Join(inst, " "))
	}
	if o.ShowTime && w.JoinedAt > 0 {
		secs := int(now.Sub(time.UnixMilli(w.JoinedAt)) / time.Second)
		if secs >= 60 {
			parts = append(parts, fmtHoursMinutes(secs))
		}
	}
	if o.ShowStats && s.Worlds > 0 {
		parts = append(parts, fmt.Sprintf("%d %s · %d met", s.Worlds, plural(s.Worlds, "world", "worlds"), s.Players))
	}
	if len(parts) == 0 {
		return ""
	}
	return withLabel(o.Label, strings.Join(parts, " · "))
}

func fmtHoursMinutes(secs int) string {
	if secs < 3600 {
		return fmt.Sprintf("%dm", secs/60)
	}
	return fmt.Sprintf("%dh %dm", secs/3600, (secs%3600)/60)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
