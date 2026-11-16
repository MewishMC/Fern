package modules

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Kick and YouTube live status. Both are polled on an interval: Kick through
// its public channel endpoint, YouTube through the Data API with the user's
// own key, which never leaves secrets.dat except in the request to Google.

// Rule triggers for Kick and YouTube.
const (
	TriggerKickLive    = "kick.live"
	TriggerYouTubeLive = "youtube.live"
)

var (
	kickAPIBase    = "https://kick.com/api/v2/channels/"
	youtubeAPIBase = "https://www.googleapis.com/youtube/v3"
	streamHTTP     = &http.Client{Timeout: 10 * time.Second}
)

// livePoller is one polled platform's state.
type livePoller struct {
	mu   sync.Mutex
	link LinkStatus
	data liveStatus
	kick chan struct{}
}

var (
	kickPoll      = &livePoller{link: LinkStatus{State: linkOff}, kick: make(chan struct{}, 1)}
	youtubePoll   = &livePoller{link: LinkStatus{State: linkOff}, kick: make(chan struct{}, 1)}
	kickKickCh    = kickPoll.kick
	youtubeKickCh = youtubePoll.kick
)

func (p *livePoller) set(link LinkStatus, data *liveStatus) {
	p.mu.Lock()
	p.link = link
	if data != nil {
		p.data = *data
	}
	p.mu.Unlock()
	NotifyChange()
}

func (p *livePoller) snapshot() (LinkStatus, liveStatus) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.link, p.data
}

// run polls while wanted, every interval, or 30 seconds after an error.
func (p *livePoller) run(wanted func(Config) (bool, string), interval func(Config) int, poll func(Config) (liveStatus, error)) {
	go func() {
		lastKey := ""
		for {
			cfg := GetConfig()
			on, key := wanted(cfg)
			if key != lastKey {
				lastKey = key
				p.mu.Lock()
				p.data = liveStatus{}
				p.mu.Unlock()
			}
			if !on {
				p.set(LinkStatus{State: linkOff}, nil)
				sleepOrKick(2*time.Second, p.kick)
				continue
			}
			wait := time.Duration(interval(cfg)) * time.Second
			d, err := poll(cfg)
			var setup setupError
			switch {
			case errors.As(err, &setup):
				p.set(LinkStatus{State: linkWaiting, Detail: setup.Error()}, &liveStatus{})
				wait = 3 * time.Second
			case err != nil:
				state := linkWaiting
				var bad badConfigError
				if errors.As(err, &bad) {
					state = linkError
				}
				p.set(LinkStatus{State: state, Detail: err.Error()}, nil)
				wait = min(wait, 30*time.Second)
				if state == linkError {
					wait = time.Duration(interval(cfg)) * time.Second
				}
			default:
				d.ok, d.checkedAt = true, time.Now()
				p.set(LinkStatus{State: linkConnected}, &d)
				NudgeRules()
			}
			sleepOrKick(wait, p.kick)
		}
	}()
}

// setupError is something the user has not filled in yet.
type setupError string

func (e setupError) Error() string { return string(e) }

// badConfigError is an answer the user must fix, like a wrong key.
type badConfigError string

func (e badConfigError) Error() string { return string(e) }

// streamGet fetches JSON. Errors never contain the address, which may hold
// an API key.
func streamGet(target, who string, out any) (int, []byte, error) {
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return 0, nil, errors.New("bad request")
	}
	req.Header.Set("User-Agent", webUserAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := streamHTTP.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("could not reach %s", who)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, body, nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return resp.StatusCode, body, fmt.Errorf("%s sent something unexpected", who)
	}
	return resp.StatusCode, body, nil
}

/* ---------------- Kick ---------------- */

var kickSlugChars = "abcdefghijklmnopqrstuvwxyz0123456789_-"

// kickSlug turns what the user typed into a channel slug.
func kickSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://")
	s = strings.TrimPrefix(s, "www.")
	s = strings.TrimPrefix(strings.TrimPrefix(s, "kick.com/"), "@")
	s, _, _ = strings.Cut(s, "/")
	s, _, _ = strings.Cut(s, "?")
	return onlyRunes(s, kickSlugChars, 25)
}

type kickChannel struct {
	Slug       string `json:"slug"`
	Followers  *int   `json:"followers_count"`
	Followers2 *int   `json:"followersCount"`
	User       struct {
		Username string `json:"username"`
	} `json:"user"`
	Livestream *struct {
		IsLive      bool   `json:"is_live"`
		ViewerCount int    `json:"viewer_count"`
		Viewers     int    `json:"viewers"`
		Title       string `json:"session_title"`
		Categories  []struct {
			Name string `json:"name"`
		} `json:"categories"`
	} `json:"livestream"`
}

func pollKick(cfg Config) (liveStatus, error) {
	slug := cfg.KickOptions.Channel
	if slug == "" {
		return liveStatus{}, setupError("Type your Kick channel name")
	}
	var ch kickChannel
	status, _, err := streamGet(kickAPIBase+url.PathEscape(slug), "Kick", &ch)
	switch {
	case err != nil:
		return liveStatus{}, err
	case status == http.StatusNotFound:
		return liveStatus{}, badConfigError("Kick has no channel called " + slug)
	case status == http.StatusForbidden || status == http.StatusTooManyRequests:
		return liveStatus{}, errors.New("Kick turned the request away (HTTP " + strconv.Itoa(status) + "). Trying again soon.")
	case status != http.StatusOK:
		return liveStatus{}, errors.New("Kick answered HTTP " + strconv.Itoa(status))
	}
	d := liveStatus{channel: firstNonEmpty(ch.User.Username, ch.Slug, slug)}
	switch {
	case ch.Followers != nil:
		d.followers = *ch.Followers
	case ch.Followers2 != nil:
		d.followers = *ch.Followers2
	}
	if ls := ch.Livestream; ls != nil && ls.IsLive {
		d.live, d.viewers, d.title = true, max(ls.ViewerCount, ls.Viewers), strings.TrimSpace(ls.Title)
		if len(ls.Categories) > 0 {
			d.category = ls.Categories[0].Name
		}
	}
	return d, nil
}

func kickLine(cfg Config) string {
	_, d := kickPoll.snapshot()
	return liveStatusLine(cfg.KickOptions, d, "follower")
}

// CurrentKick returns the Kick snapshot.
func CurrentKick() LiveStatusInfo {
	link, d := kickPoll.snapshot()
	return d.info(link)
}

func startKickWorker() {
	kickPoll.run(func(c Config) (bool, string) {
		return c.DisplayOptions.Kick || rulesUseTrigger(c, "kick."), c.KickOptions.Channel
	}, func(c Config) int { return c.KickOptions.IntervalSec }, pollKick)
}

/* ---------------- YouTube ---------------- */

var (
	ytChannelID = regexp.MustCompile(`^UC[A-Za-z0-9_-]{22}$`)
	ytHandle    = regexp.MustCompile(`^@[A-Za-z0-9._-]{1,100}$`)
)

// youTubeChannelRef turns a channel link, @handle or channel id into
// "@handle" or "UC…"; anything else becomes "".
func youTubeChannelRef(s string) string {
	s = strings.TrimSpace(s)
	low := strings.ToLower(s)
	for _, p := range []string{"https://", "http://", "www.", "m.", "youtube.com/"} {
		if strings.HasPrefix(low, p) {
			s, low = s[len(p):], low[len(p):]
		}
	}
	if strings.HasPrefix(low, "channel/") {
		s = s[len("channel/"):]
	}
	s, _, _ = strings.Cut(s, "/")
	s, _, _ = strings.Cut(s, "?")
	if ytChannelID.MatchString(s) {
		return s
	}
	if !strings.HasPrefix(s, "@") {
		s = "@" + s
	}
	if ytHandle.MatchString(s) {
		return s
	}
	return ""
}

// youtubeErr turns a Data API error body into a message without the key.
func youtubeErr(status int, body []byte) error {
	var e struct {
		Error struct {
			Message string `json:"message"`
			Errors  []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &e)
	reason := ""
	if len(e.Error.Errors) > 0 {
		reason = e.Error.Errors[0].Reason
	}
	switch {
	case reason == "quotaExceeded" || reason == "dailyLimitExceeded":
		return errors.New("the API key used up today's YouTube quota. Check less often.")
	case reason == "keyInvalid" || strings.Contains(e.Error.Message, "API key not valid"):
		return badConfigError("YouTube says the API key is not valid")
	case reason == "accessNotConfigured" || strings.Contains(e.Error.Message, "has not been used") || strings.Contains(e.Error.Message, "disabled"):
		return badConfigError("Turn on YouTube Data API v3 for the key's Google Cloud project")
	case status == http.StatusForbidden:
		return badConfigError("YouTube refused the API key (HTTP 403)")
	}
	return fmt.Errorf("YouTube answered HTTP %d", status)
}

func ytGet(path string, q url.Values, key string, out any) error {
	q.Set("key", key)
	status, body, err := streamGet(youtubeAPIBase+path+"?"+q.Encode(), "YouTube", out)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return youtubeErr(status, body)
	}
	return nil
}

func pollYouTube(cfg Config) (liveStatus, error) {
	ref := cfg.YouTubeOptions.Channel
	key := GetSecret(SecretYouTubeKey)
	switch {
	case key == "":
		return liveStatus{}, setupError("Add your YouTube Data API key")
	case ref == "":
		return liveStatus{}, setupError("Type your channel's @handle or channel ID")
	}
	var ch struct {
		Items []struct {
			ID      string `json:"id"`
			Snippet struct {
				Title string `json:"title"`
			} `json:"snippet"`
			Statistics struct {
				SubscriberCount string `json:"subscriberCount"`
			} `json:"statistics"`
			ContentDetails struct {
				RelatedPlaylists struct {
					Uploads string `json:"uploads"`
				} `json:"relatedPlaylists"`
			} `json:"contentDetails"`
		} `json:"items"`
	}
	q := url.Values{"part": {"snippet,statistics,contentDetails"}}
	if strings.HasPrefix(ref, "@") {
		q.Set("forHandle", ref)
	} else {
		q.Set("id", ref)
	}
	if err := ytGet("/channels", q, key, &ch); err != nil {
		return liveStatus{}, err
	}
	if len(ch.Items) == 0 {
		return liveStatus{}, badConfigError("YouTube has no channel " + ref)
	}
	c := ch.Items[0]
	d := liveStatus{channel: c.Snippet.Title}
	d.followers, _ = strconv.Atoi(c.Statistics.SubscriberCount)
	uploads := c.ContentDetails.RelatedPlaylists.Uploads
	if uploads == "" {
		return d, nil
	}
	// A live stream is one of the newest uploads; checking the latest few
	// costs 2 quota units, where a search would cost 100.
	var pl struct {
		Items []struct {
			ContentDetails struct {
				VideoID string `json:"videoId"`
			} `json:"contentDetails"`
		} `json:"items"`
	}
	if err := ytGet("/playlistItems", url.Values{"part": {"contentDetails"}, "playlistId": {uploads}, "maxResults": {"10"}}, key, &pl); err != nil {
		return liveStatus{}, err
	}
	ids := make([]string, 0, len(pl.Items))
	for _, it := range pl.Items {
		if it.ContentDetails.VideoID != "" {
			ids = append(ids, it.ContentDetails.VideoID)
		}
	}
	if len(ids) == 0 {
		return d, nil
	}
	var vids struct {
		Items []struct {
			Snippet struct {
				Title                string `json:"title"`
				LiveBroadcastContent string `json:"liveBroadcastContent"`
			} `json:"snippet"`
			LiveStreamingDetails struct {
				ConcurrentViewers string `json:"concurrentViewers"`
				ActualStartTime   string `json:"actualStartTime"`
				ActualEndTime     string `json:"actualEndTime"`
			} `json:"liveStreamingDetails"`
		} `json:"items"`
	}
	if err := ytGet("/videos", url.Values{"part": {"snippet,liveStreamingDetails"}, "id": {strings.Join(ids, ",")}}, key, &vids); err != nil {
		return liveStatus{}, err
	}
	for _, v := range vids.Items {
		ls := v.LiveStreamingDetails
		if v.Snippet.LiveBroadcastContent == "live" || (ls.ActualStartTime != "" && ls.ActualEndTime == "") {
			d.live, d.title = true, v.Snippet.Title
			d.viewers, _ = strconv.Atoi(ls.ConcurrentViewers)
			break
		}
	}
	return d, nil
}

func youtubeLine(cfg Config) string {
	_, d := youtubePoll.snapshot()
	return liveStatusLine(cfg.YouTubeOptions, d, "subscriber")
}

// CurrentYouTube returns the YouTube snapshot.
func CurrentYouTube() LiveStatusInfo {
	link, d := youtubePoll.snapshot()
	return d.info(link)
}

func startYouTubeWorker() {
	youtubePoll.run(func(c Config) (bool, string) {
		return c.DisplayOptions.YouTube || rulesUseTrigger(c, "youtube."), c.YouTubeOptions.Channel + "/" + tokenTag(GetSecret(SecretYouTubeKey))
	}, func(c Config) int { return c.YouTubeOptions.IntervalSec }, pollYouTube)
}

func init() {
	liveTrigger := func(id, label, platform string, p *livePoller) RuleTrigger {
		return RuleTrigger{
			ID: id, Label: label, Category: "Streaming",
			Description: "While the channel set on the " + platform + " page is live",
			Fields:      []RuleField{{Key: "viewers", Label: "Viewers at least", Type: FieldNumber, Min: 0, Max: 10_000_000, Step: 1, Hint: "0 is any"}},
			Check: func(params RuleParams) bool {
				link, d := p.snapshot()
				return link.State == linkConnected && d.ok && d.live && float64(d.viewers) >= params.Number("viewers")
			},
		}
	}
	mustRegister(RegisterRuleTrigger(liveTrigger(TriggerKickLive, "Live on Kick", "Kick", kickPoll)))
	mustRegister(RegisterRuleTrigger(liveTrigger(TriggerYouTubeLive, "Live on YouTube", "YouTube", youtubePoll)))
}
