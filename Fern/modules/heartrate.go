package modules

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

// Heart rate readings arrive from one of the sources in heartrate_source.go.
// Every reading goes into a short history; the chatbox line, the panel
// sparkline and the avatar parameters are all computed from it at read time,
// so changing smoothing or the offset applies straight away.

const (
	hrHistory = 10 * time.Minute // readings kept for smoothing, trend and the sparkline
	sparkSpan = 5 * time.Minute  // what the panel sparkline covers
	sparkStep = 2 * time.Second  // one sparkline point per step
	minBPM    = 20               // readings outside 20-255 are treated as noise
	maxBPM    = 255
)

type hrSample struct {
	at  time.Time
	bpm float64
}

// Connection states shown in the panel.
const (
	HRStatusOff        = "off"        // module and avatar output both off
	HRStatusSetup      = "setup"      // the chosen source is missing a token, key or URL
	HRStatusConnecting = "connecting" // dialing, or waiting before a retry
	HRStatusConnected  = "connected"  // connected, no reading yet
	HRStatusLive       = "live"       // readings are arriving
	HRStatusOffline    = "offline"    // connected, but nothing for OfflineSec
	HRStatusError      = "error"      // the last attempt failed; retrying with backoff
)

// HeartRateStatus is what the panel shows on the heart rate page.
type HeartRateStatus struct {
	Status  string `json:"status"`
	Message string `json:"message"` // never contains a token or key
	Via     string `json:"via"`     // websocket or http
	BPM     int    `json:"bpm"`     // as shown in the chatbox, 0 when there is none
	Raw     int    `json:"raw"`     // latest reading before smoothing and offset
	Trend   int    `json:"trend"`   // -1, 0 or 1
	LastMS  int64  `json:"lastMs"`  // when the last reading arrived
	Min     int    `json:"min"`
	Avg     int    `json:"avg"`
	Max     int    `json:"max"`
	Spark   []int  `json:"spark"` // last five minutes, one point per two seconds, 0 for gaps
}

var (
	hrMu       sync.RWMutex
	hrSamples  []hrSample
	hrState    = HRStatusOff
	hrMessage  string
	hrVia      string
	hrStatMin  float64
	hrStatMax  float64
	hrStatSum  float64
	hrStatN    int
	hrLastRead time.Time
)

// addHeartSample records one reading from the active source.
func addHeartSample(bpm float64, at time.Time) {
	if math.IsNaN(bpm) || bpm < minBPM || bpm > maxBPM {
		return
	}
	hrMu.Lock()
	hrSamples = append(hrSamples, hrSample{at: at, bpm: bpm})
	cut := 0
	for cut < len(hrSamples) && at.Sub(hrSamples[cut].at) > hrHistory {
		cut++
	}
	if cut > 0 {
		hrSamples = append(hrSamples[:0], hrSamples[cut:]...)
	}
	if hrStatN == 0 || bpm < hrStatMin {
		hrStatMin = bpm
	}
	if hrStatN == 0 || bpm > hrStatMax {
		hrStatMax = bpm
	}
	hrStatSum += bpm
	hrStatN++
	hrLastRead = at
	hrMu.Unlock()
	if hrStateIs(HRStatusConnected, HRStatusOffline, HRStatusLive) {
		setHeartState(HRStatusLive, "")
	}
}

func hrStateIs(states ...string) bool {
	hrMu.RLock()
	defer hrMu.RUnlock()
	for _, s := range states {
		if hrState == s {
			return true
		}
	}
	return false
}

// setHeartState changes the connection status. Messages come from
// cleanHeartError or fixed strings, never from raw errors.
func setHeartState(state, message string) {
	hrMu.Lock()
	hrState = state
	hrMessage = message
	hrMu.Unlock()
}

func setHeartVia(via string) {
	hrMu.Lock()
	hrVia = via
	hrMu.Unlock()
}

// ResetHeartStats starts the session min / avg / max over.
func ResetHeartStats() {
	hrMu.Lock()
	hrStatMin, hrStatMax, hrStatSum, hrStatN = 0, 0, 0, 0
	hrMu.Unlock()
	NotifyChange()
}

// clearHeartSamples forgets every reading, used when the source changes so a
// new person's heart rate is not averaged with the previous one.
func clearHeartSamples() {
	hrMu.Lock()
	hrSamples = nil
	hrLastRead = time.Time{}
	hrStatMin, hrStatMax, hrStatSum, hrStatN = 0, 0, 0, 0
	hrMu.Unlock()
}

func heartSnapshot() (samples []hrSample, last time.Time) {
	hrMu.RLock()
	defer hrMu.RUnlock()
	return append([]hrSample(nil), hrSamples...), hrLastRead
}

// smoothBPM is the mean of the readings in the last window, or the latest
// reading when the window is zero or holds nothing.
func smoothBPM(samples []hrSample, now time.Time, window time.Duration) float64 {
	if len(samples) == 0 {
		return 0
	}
	latest := samples[len(samples)-1].bpm
	if window <= 0 {
		return latest
	}
	sum, n := 0.0, 0
	for i := len(samples) - 1; i >= 0 && now.Sub(samples[i].at) <= window; i-- {
		sum += samples[i].bpm
		n++
	}
	if n == 0 {
		return latest
	}
	return sum / float64(n)
}

// heartTrend compares the mean of the last window with the window before it:
// 1 when it rose by more than sensitivity bpm, -1 when it fell by more, else
// 0. Without readings in both windows there is no trend.
func heartTrend(samples []hrSample, now time.Time, window time.Duration, sensitivity float64) int {
	if window <= 0 {
		return 0
	}
	var recent, older float64
	var nr, no int
	for i := len(samples) - 1; i >= 0; i-- {
		age := now.Sub(samples[i].at)
		switch {
		case age <= window:
			recent += samples[i].bpm
			nr++
		case age <= 2*window:
			older += samples[i].bpm
			no++
		}
		if age > 2*window {
			break
		}
	}
	if nr == 0 || no == 0 {
		return 0
	}
	delta := recent/float64(nr) - older/float64(no)
	switch {
	case delta > sensitivity:
		return 1
	case delta < -sensitivity:
		return -1
	}
	return 0
}

// heartZone is -1 below the low threshold, 1 at or above the high one, else 0.
func heartZone(bpm, low, high int) int {
	switch {
	case low > 0 && bpm < low:
		return -1
	case high > 0 && bpm >= high:
		return 1
	}
	return 0
}

func clampBPM(v float64) int {
	n := int(math.Round(v))
	if n < 0 {
		return 0
	}
	if n > maxBPM {
		return maxBPM
	}
	return n
}

// heartReading is the processed view of the history for one moment.
type heartReading struct {
	ok    bool // a fresh reading exists
	bpm   int  // smoothed, offset and clamped
	raw   int
	trend int
	last  time.Time
}

func readHeart(o HeartOptions, samples []hrSample, last time.Time, now time.Time) heartReading {
	if len(samples) == 0 || last.IsZero() {
		return heartReading{}
	}
	r := heartReading{last: last, raw: clampBPM(samples[len(samples)-1].bpm)}
	r.ok = now.Sub(last) <= time.Duration(o.OfflineSec)*time.Second
	smooth := smoothBPM(samples, now, time.Duration(o.SmoothSec)*time.Second)
	r.bpm = clampBPM(smooth + float64(o.Offset))
	r.trend = heartTrend(samples, now, time.Duration(o.TrendSec)*time.Second, o.TrendSensitivity)
	return r
}

func heartIcon(o HeartOptions, bpm int) string {
	if !o.ShowIcon {
		return ""
	}
	if o.ZoneIcons {
		switch heartZone(bpm, o.LowBPM, o.HighBPM) {
		case -1:
			if o.IconLow != "" {
				return o.IconLow
			}
		case 1:
			if o.IconHigh != "" {
				return o.IconHigh
			}
		}
	}
	return o.Icon
}

var trendArrows = map[int]string{1: "↑", -1: "↓", 0: "→"}

// formatHeartLine renders the chatbox line from a processed reading.
func formatHeartLine(o HeartOptions, r heartReading, stats [3]int) string {
	if !r.ok {
		if r.last.IsZero() && o.OfflineText == "" {
			return ""
		}
		return withLabel(o.Label, o.OfflineText)
	}
	var b strings.Builder
	if icon := heartIcon(o, r.bpm); icon != "" {
		b.WriteString(icon)
		b.WriteString(" ")
	}
	fmt.Fprintf(&b, "%d", r.bpm)
	if o.ShowSuffix && o.Suffix != "" {
		b.WriteString(" ")
		b.WriteString(o.Suffix)
	}
	if o.ShowTrend {
		b.WriteString(" ")
		b.WriteString(trendArrows[r.trend])
	}
	if o.ShowZoneText {
		switch heartZone(r.bpm, o.LowBPM, o.HighBPM) {
		case -1:
			if o.LowText != "" {
				b.WriteString(" · " + o.LowText)
			}
		case 1:
			if o.HighText != "" {
				b.WriteString(" · " + o.HighText)
			}
		}
	}
	if o.ShowStats && stats[2] > 0 {
		fmt.Fprintf(&b, " · %d–%d avg %d", stats[0], stats[2], stats[1])
	}
	return withLabel(o.Label, b.String())
}

// heartStats returns the session min, avg and max with the offset applied.
func heartStats(offset int) [3]int {
	hrMu.RLock()
	defer hrMu.RUnlock()
	if hrStatN == 0 {
		return [3]int{}
	}
	off := float64(offset)
	return [3]int{clampBPM(hrStatMin + off), clampBPM(hrStatSum/float64(hrStatN) + off), clampBPM(hrStatMax + off)}
}

func heartRateLine(cfg Config) string {
	o := cfg.HeartRate
	if !heartConfigured(o) {
		return ""
	}
	samples, last := heartSnapshot()
	return formatHeartLine(o, readHeart(o, samples, last, time.Now()), heartStats(o.Offset))
}

// heartSpark buckets the history into one point per step over span, oldest
// first. Buckets without a reading are 0 so the panel can draw gaps.
func heartSpark(samples []hrSample, now time.Time, span, step time.Duration, offset int) []int {
	n := int(span / step)
	out := make([]int, n)
	sums := make([]float64, n)
	counts := make([]int, n)
	start := now.Add(-span)
	for _, s := range samples {
		if s.at.Before(start) || s.at.After(now) {
			continue
		}
		i := int(s.at.Sub(start) / step)
		if i >= n {
			i = n - 1
		}
		sums[i] += s.bpm
		counts[i]++
	}
	for i := range out {
		if counts[i] > 0 {
			out[i] = clampBPM(sums[i]/float64(counts[i]) + float64(offset))
		}
	}
	return out
}

// CurrentHeartRate is the panel's view of the heart rate module.
func CurrentHeartRate() HeartRateStatus {
	o := GetConfig().HeartRate
	samples, last := heartSnapshot()
	now := time.Now()
	r := readHeart(o, samples, last, now)
	hrMu.RLock()
	st := HeartRateStatus{Status: hrState, Message: hrMessage, Via: hrVia}
	hrMu.RUnlock()
	if st.Status == HRStatusLive && !r.ok {
		st.Status = HRStatusOffline
	}
	if r.ok {
		st.BPM = r.bpm
		st.Trend = r.trend
	}
	st.Raw = r.raw
	if !last.IsZero() {
		st.LastMS = last.UnixMilli()
	}
	stats := heartStats(o.Offset)
	st.Min, st.Avg, st.Max = stats[0], stats[1], stats[2]
	st.Spark = heartSpark(samples, now, sparkSpan, sparkStep, o.Offset)
	return st
}

// heartConfigured reports whether the chosen source has what it needs.
func heartConfigured(o HeartOptions) bool {
	switch o.Source {
	case HeartPulsoid:
		return o.PulsoidToken != ""
	case HeartHypeRate:
		return o.HypeRateKey != "" && o.HypeRateID != ""
	case HeartCustom:
		return o.CustomURL != ""
	}
	return false
}

// pulsoidTokenFrom accepts a bare token or the whole redirect URL Pulsoid
// shows after authorizing, and returns just the token.
func pulsoidTokenFrom(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "access_token="); i >= 0 {
		s = s[i+len("access_token="):]
		if j := strings.IndexAny(s, "&#"); j >= 0 {
			s = s[:j]
		}
	}
	return strings.TrimSpace(s)
}

// heartOneLine collapses whitespace and caps s at n runes.
func heartOneLine(s string, n int) string {
	return RuneLimit(strings.Join(strings.Fields(s), " "), n)
}

// paramName keeps an avatar parameter name to the characters VRChat allows in
// an OSC address; anything else is dropped.
func paramName(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		if r == '/' || r == ' ' || r == '#' || r == '*' || r == ',' || r == '?' || r == '[' || r == ']' || r == '{' || r == '}' {
			continue
		}
		b.WriteRune(r)
	}
	return RuneLimit(b.String(), 64)
}

func (o *HeartOptions) sanitize() {
	switch o.Source {
	case HeartPulsoid, HeartHypeRate, HeartCustom:
	default:
		o.Source = HeartPulsoid
	}
	o.PulsoidToken = RuneLimit(pulsoidTokenFrom(o.PulsoidToken), 256)
	o.HypeRateKey = RuneLimit(strings.TrimSpace(o.HypeRateKey), 256)
	o.HypeRateID = RuneLimit(strings.TrimSpace(o.HypeRateID), 64)
	o.CustomURL = RuneLimit(strings.TrimSpace(o.CustomURL), 1024)
	o.CustomPath = RuneLimit(strings.TrimSpace(o.CustomPath), 128)
	if o.PollMS == 0 {
		o.PollMS = 1000
	}
	o.PollMS = clampInt(o.PollMS, 250, 60000)
	o.Label = RuneLimit(strings.TrimSpace(o.Label), 16)
	o.Icon = RuneLimit(strings.TrimSpace(o.Icon), 4)
	o.IconLow = RuneLimit(strings.TrimSpace(o.IconLow), 4)
	o.IconHigh = RuneLimit(strings.TrimSpace(o.IconHigh), 4)
	o.Suffix = heartOneLine(o.Suffix, 8)
	o.TrendSec = clampInt(o.TrendSec, 2, 120)
	if math.IsNaN(o.TrendSensitivity) || o.TrendSensitivity < 0.5 {
		o.TrendSensitivity = 0.5
	}
	if o.TrendSensitivity > 30 {
		o.TrendSensitivity = 30
	}
	o.TrendSensitivity = math.Round(o.TrendSensitivity*2) / 2
	o.SmoothSec = clampInt(o.SmoothSec, 0, 60)
	o.Offset = clampInt(o.Offset, -50, 50)
	o.LowBPM = clampInt(o.LowBPM, 0, 250)
	o.HighBPM = clampInt(o.HighBPM, 0, 250)
	o.LowText = heartOneLine(o.LowText, 24)
	o.HighText = heartOneLine(o.HighText, 24)
	o.OfflineSec = clampInt(o.OfflineSec, 5, 600)
	o.OfflineText = heartOneLine(o.OfflineText, 40)
	o.ParamInt = paramName(o.ParamInt)
	o.ParamFloat = paramName(o.ParamFloat)
	o.ParamBeat = paramName(o.ParamBeat)
	o.ParamConnected = paramName(o.ParamConnected)
	switch o.FloatRange {
	case "signed", "unsigned":
	default:
		o.FloatRange = "signed"
	}
	switch o.BeatMode {
	case "toggle", "pulse":
	default:
		o.BeatMode = "toggle"
	}
}
