package modules

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The Last.fm line shows what your scrobbler is reporting right now, from any
// player that scrobbles (phone, web player, a second PC), with your play count
// and loved state for the track. It reads the public user.getRecentTracks and
// track.getInfo methods with the user's own API key; the username and key are
// kept in the encrypted secrets store.

// LastFMOptions controls the Last.fm line.
type LastFMOptions struct {
	IntervalSec int    `json:"interval_sec"`
	Icon        string `json:"icon"`
	Text        string `json:"text"` // words before the track, like "Scrobbling"
	ShowArtist  bool   `json:"show_artist"`
	ShowAlbum   bool   `json:"show_album"`
	Separator   string `json:"separator"`
	ShowPlays   bool   `json:"show_plays"`
	PlaysLabel  string `json:"plays_label"`
	ShowLoved   bool   `json:"show_loved"`
	LovedIcon   string `json:"loved_icon"`
	ShowTotal   bool   `json:"show_total"` // your scrobbles in total
	TotalLabel  string `json:"total_label"`
	Compact     bool   `json:"compact"`   // 1.2k instead of 1234
	ShowLast    bool   `json:"show_last"` // the last scrobble while nothing plays
	LastText    string `json:"last_text"`
	Template    string `json:"template"` // replaces the switches when set
	MaxChars    int    `json:"max_chars"`
}

func defaultLastFMOptions() LastFMOptions {
	return LastFMOptions{
		IntervalSec: 15,
		Icon:        "🎧",
		Text:        "Scrobbling",
		ShowArtist:  true,
		Separator:   " — ",
		ShowPlays:   true,
		PlaysLabel:  "plays",
		ShowLoved:   true,
		LovedIcon:   "♥",
		TotalLabel:  "scrobbles",
		Compact:     true,
		LastText:    "Last played",
		MaxChars:    90,
	}
}

func sanitizeLastFM(o *LastFMOptions) {
	def := defaultLastFMOptions()
	words := func(s string, n int) string { return RuneLimit(strings.Join(strings.Fields(s), " "), n) }
	o.IntervalSec = clampInt(o.IntervalSec, 10, 600)
	o.Icon = RuneLimit(strings.TrimSpace(o.Icon), 4)
	o.LovedIcon = RuneLimit(strings.TrimSpace(o.LovedIcon), 4)
	o.Text = words(o.Text, 24)
	o.LastText = words(o.LastText, 24)
	o.PlaysLabel = words(o.PlaysLabel, 16)
	o.TotalLabel = words(o.TotalLabel, 16)
	// The separator keeps its spaces but stays on one line.
	o.Separator = RuneLimit(strings.ReplaceAll(o.Separator, "\n", " "), 8)
	if strings.TrimSpace(o.Separator) == "" {
		o.Separator = def.Separator
	}
	o.Template = RuneLimit(strings.TrimSpace(o.Template), 200)
	o.MaxChars = clampInt(o.MaxChars, 20, 144)
}

// Secrets of the Last.fm line.
const (
	SecretLastFMUser = "lastfm_user"
	SecretLastFMKey  = "lastfm_key"
)

// LastFMInfo is what the panel shows. The username stays out of it.
type LastFMInfo struct {
	Link       LinkStatus `json:"link"`
	NowPlaying bool       `json:"nowPlaying"`
	Title      string     `json:"title"`
	Artist     string     `json:"artist"`
	Album      string     `json:"album"`
	Plays      int        `json:"plays"` // -1 while unknown
	Loved      bool       `json:"loved"`
	Total      int        `json:"total"`
	CheckedAt  int64      `json:"checkedAt"`
}

type lastfmTrack struct {
	ok         bool
	nowPlaying bool
	title      string
	artist     string
	album      string
	loved      bool
	plays      int // -1 while unknown
	total      int
	checkedAt  time.Time
}

var (
	lastfmMu      sync.Mutex
	lastfmLink    = LinkStatus{State: linkOff}
	lastfmNow     lastfmTrack
	lastfmPlays   = map[string]int{} // track key -> your play count
	lastfmKickCh  = make(chan struct{}, 1)
	lastfmAPIBase = "https://ws.audioscrobbler.com/2.0/"
	lastfmHTTP    = &http.Client{Timeout: 10 * time.Second}
)

// TriggerLastFMTrack fires when Last.fm reports a new song playing.
const TriggerLastFMTrack = "lastfm.now_playing"

func init() {
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerLastFMTrack, Label: "Last.fm now scrobbling", Category: "Last.fm",
		Description: "When Last.fm reports a new song playing. {name} in a chat message is the song.",
		Fields: []RuleField{
			{Key: "title", Label: "Song", Type: FieldText, Hint: "Empty for any; /regex/ works", Placeholder: "any song", MaxLen: 200},
			{Key: "artist", Label: "Artist", Type: FieldText, Hint: "Empty for any; /regex/ works", Placeholder: "any artist", MaxLen: 200},
			{Key: "loved", Label: "Only loved songs", Type: FieldBool},
		},
		Match: func(p RuleParams, ev RuleEvent) bool {
			if p.Bool("loved") && ev.Data["loved"] != "1" {
				return false
			}
			return MatchText(p.String("title"), ev.Data["title"]) && MatchText(p.String("artist"), ev.Data["artist"])
		},
	}))
}

func setLastFMLink(state, detail string) {
	lastfmMu.Lock()
	changed := lastfmLink.State != state || lastfmLink.Detail != detail
	lastfmLink = LinkStatus{State: state, Detail: detail}
	lastfmMu.Unlock()
	if changed {
		NotifyChange()
	}
}

// LastFMRefreshNow polls without waiting for the next interval.
func LastFMRefreshNow() { poke(lastfmKickCh) }

// CurrentLastFM returns the Last.fm snapshot for the panel.
func CurrentLastFM() LastFMInfo {
	lastfmMu.Lock()
	defer lastfmMu.Unlock()
	t := lastfmNow
	info := LastFMInfo{
		Link: lastfmLink, NowPlaying: t.nowPlaying, Title: t.title, Artist: t.artist, Album: t.album,
		Plays: t.plays, Loved: t.loved, Total: t.total,
	}
	if !t.ok {
		info.Plays = -1
	}
	if !t.checkedAt.IsZero() {
		info.CheckedAt = t.checkedAt.UnixMilli()
	}
	return info
}

// lastfmError is an error answer from the Last.fm API.
type lastfmError struct {
	Code    int
	Message string
}

func (e *lastfmError) Error() string {
	switch e.Code {
	case 10, 26:
		return "Last.fm does not accept this API key"
	case 6:
		return "Last.fm has no user by that name"
	case 17:
		return "That Last.fm profile hides its listening history"
	case 29:
		return "Last.fm asked Fern to slow down"
	}
	return "Last.fm: " + firstNonEmpty(e.Message, "error "+strconv.Itoa(e.Code))
}

// lastfmBad reports whether the user must fix their settings.
func lastfmBad(err error) bool {
	var e *lastfmError
	return errors.As(err, &e) && (e.Code == 10 || e.Code == 26 || e.Code == 6 || e.Code == 17)
}

type lastfmClient struct {
	http *http.Client
	base string
	user string
	key  string
}

func newLastFMClient(user, key string) lastfmClient {
	return lastfmClient{http: lastfmHTTP, base: lastfmAPIBase, user: user, key: key}
}

func (c lastfmClient) get(method string, q url.Values, out any) error {
	q.Set("method", method)
	q.Set("api_key", c.key)
	q.Set("format", "json")
	req, err := http.NewRequest(http.MethodGet, c.base+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", webUserAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		// The URL carries the key, so the error (which quotes it) is not passed on.
		return errors.New("could not reach Last.fm")
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	var e struct {
		Error   int    `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &e) == nil && e.Error != 0 {
		return &lastfmError{Code: e.Error, Message: e.Message}
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Last.fm answered HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return errors.New("Last.fm sent something Fern could not read")
	}
	return nil
}

// lastfmText reads Last.fm's {"#text": "..."} values, and plain strings.
type lastfmText string

func (t *lastfmText) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*t = lastfmText(s)
		return nil
	}
	var o struct {
		Text string `json:"#text"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(b, &o); err != nil {
		return nil
	}
	*t = lastfmText(firstNonEmpty(o.Text, o.Name))
	return nil
}

type lastfmRecent struct {
	Name   string     `json:"name"`
	Artist lastfmText `json:"artist"`
	Album  lastfmText `json:"album"`
	Loved  string     `json:"loved"`
	Attr   struct {
		NowPlaying string `json:"nowplaying"`
	} `json:"@attr"`
}

// recent reads the newest scrobble, or the track playing now. Last.fm sends
// "track" as an object when there is only one.
func (c lastfmClient) recent() (lastfmRecent, int, error) {
	var r struct {
		RecentTracks struct {
			Track json.RawMessage `json:"track"`
			Attr  struct {
				Total string `json:"total"`
			} `json:"@attr"`
		} `json:"recenttracks"`
	}
	q := url.Values{"user": {c.user}, "limit": {"1"}, "extended": {"1"}}
	if err := c.get("user.getrecenttracks", q, &r); err != nil {
		return lastfmRecent{}, 0, err
	}
	total, _ := strconv.Atoi(r.RecentTracks.Attr.Total)
	var list []lastfmRecent
	if err := json.Unmarshal(r.RecentTracks.Track, &list); err != nil {
		var one lastfmRecent
		if json.Unmarshal(r.RecentTracks.Track, &one) == nil && one.Name != "" {
			list = []lastfmRecent{one}
		}
	}
	if len(list) == 0 {
		return lastfmRecent{}, total, nil
	}
	return list[0], total, nil
}

// plays reads your play count of a track and whether you loved it.
func (c lastfmClient) plays(artist, title string) (plays int, loved bool, err error) {
	var r struct {
		Track struct {
			UserPlaycount json.Number `json:"userplaycount"`
			UserLoved     string      `json:"userloved"`
		} `json:"track"`
	}
	q := url.Values{"artist": {artist}, "track": {title}, "username": {c.user}, "autocorrect": {"1"}}
	if err := c.get("track.getInfo", q, &r); err != nil {
		return 0, false, err
	}
	n, _ := strconv.Atoi(r.Track.UserPlaycount.String())
	return n, r.Track.UserLoved == "1", nil
}

func lastfmKey(artist, title string) string {
	return strings.ToLower(artist + "\x00" + title)
}

// lastfmWantsPlays reports whether the line shows the play count at all.
func lastfmWantsPlays(o LastFMOptions) bool {
	if o.Template != "" {
		return strings.Contains(o.Template, "{plays") || strings.Contains(o.Template, "{playCount")
	}
	return o.ShowPlays
}

// lastfmPollOnce reads the newest track and, once per song, its play count.
func lastfmPollOnce(c lastfmClient, o LastFMOptions) error {
	r, total, err := c.recent()
	if err != nil {
		return err
	}
	t := lastfmTrack{ok: true, total: total, plays: -1, checkedAt: time.Now()}
	if r.Name != "" {
		t.title, t.artist, t.album = r.Name, string(r.Artist), string(r.Album)
		t.nowPlaying = r.Attr.NowPlaying == "true"
		t.loved = r.Loved == "1"
	}
	lastfmMu.Lock()
	prev := lastfmNow
	key := lastfmKey(t.artist, t.title)
	plays, known := lastfmPlays[key]
	lastfmMu.Unlock()
	if t.title != "" && lastfmWantsPlays(o) {
		// A new song, or the same one scrobbled again, asks again.
		if !known || prev.title != t.title || prev.nowPlaying != t.nowPlaying {
			if n, loved, err := c.plays(t.artist, t.title); err == nil {
				plays, known = n, true
				t.loved = t.loved || loved
				lastfmMu.Lock()
				if len(lastfmPlays) > 200 {
					clear(lastfmPlays)
				}
				lastfmPlays[key] = n
				lastfmMu.Unlock()
			}
		}
		if known {
			t.plays = plays
		}
	}
	lastfmMu.Lock()
	lastfmNow = t
	lastfmMu.Unlock()
	if t.nowPlaying && t.title != "" && (!prev.nowPlaying || lastfmKey(prev.artist, prev.title) != key) {
		loved := "0"
		if t.loved {
			loved = "1"
		}
		FireTrigger(TriggerLastFMTrack, map[string]string{"name": t.title, "title": t.title, "artist": t.artist, "loved": loved})
	}
	NotifyChange()
	return nil
}

func resetLastFM() {
	lastfmMu.Lock()
	lastfmNow = lastfmTrack{}
	lastfmMu.Unlock()
}

func startLastFMWorker() {
	go func() {
		for {
			cfg := GetConfig()
			o := cfg.LastFMOptions
			wait := time.Duration(o.IntervalSec) * time.Second
			user, key := GetSecret(SecretLastFMUser), GetSecret(SecretLastFMKey)
			switch {
			case !cfg.DisplayOptions.LastFM:
				setLastFMLink(linkOff, "")
				resetLastFM()
				wait = 2 * time.Second
			case user == "" || key == "":
				setLastFMLink(linkWaiting, "Add your Last.fm username and API key")
				resetLastFM()
				wait = 2 * time.Second
			default:
				err := lastfmPollOnce(newLastFMClient(user, key), o)
				switch {
				case err == nil:
					setLastFMLink(linkConnected, "")
				case lastfmBad(err):
					setLastFMLink(linkError, err.Error())
					wait = max(wait, time.Minute)
				default:
					setLastFMLink(linkWaiting, err.Error())
					wait = max(wait, 30*time.Second)
				}
			}
			sleepOrKick(wait, lastfmKickCh)
		}
	}()
}

// lastfmLine renders the Last.fm line.
func lastfmLine(cfg Config) string {
	lastfmMu.Lock()
	t := lastfmNow
	lastfmMu.Unlock()
	return formatLastFMLine(cfg.LastFMOptions, t)
}

func formatLastFMLine(o LastFMOptions, t lastfmTrack) string {
	if !t.ok || t.title == "" || (!t.nowPlaying && !o.ShowLast) {
		return ""
	}
	count := strconv.Itoa
	if o.Compact {
		count = compactCount
	}
	words := o.Text
	if !t.nowPlaying {
		words = o.LastText
	}
	track := strings.TrimSpace(t.title)
	if o.ShowArtist && t.artist != "" {
		track = joinNonEmpty(o.Separator, track, t.artist)
	}
	if o.ShowAlbum && t.album != "" {
		track = joinNonEmpty(" · ", track, t.album)
	}
	track = RuneLimit(track, o.MaxChars)
	plays, playCount := "", ""
	if t.plays >= 0 {
		playCount = count(t.plays)
		plays = strings.TrimSpace(playCount + " " + o.PlaysLabel)
	}
	loved := ""
	if t.loved {
		loved = o.LovedIcon
	}
	totalCount := ""
	if t.total > 0 {
		totalCount = count(t.total)
	}
	total := ""
	if totalCount != "" {
		total = strings.TrimSpace(totalCount + " " + o.TotalLabel)
	}
	if o.Template != "" {
		return fillTemplate(o.Template, map[string]string{
			"icon": o.Icon, "text": words, "title": RuneLimit(t.title, o.MaxChars), "artist": t.artist, "album": t.album,
			"track": track, "plays": plays, "playCount": playCount, "loved": loved,
			"total": total, "totalCount": totalCount,
		})
	}
	line := joinNonEmpty(" ", o.Icon, words, track)
	if o.ShowLoved {
		line = joinNonEmpty(" ", line, loved)
	}
	var extra []string
	if o.ShowPlays && plays != "" {
		extra = append(extra, plays)
	}
	if o.ShowTotal && total != "" {
		extra = append(extra, total)
	}
	return joinNonEmpty(" · ", line, joinNonEmpty(" · ", extra...))
}
