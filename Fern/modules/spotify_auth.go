package modules

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Sign in with Spotify: authorization code with PKCE. Fern listens on
// 127.0.0.1 at the redirect port only while a sign-in is pending, the panel
// opens Spotify's consent page, and the browser comes back to the loopback
// callback with a code that Fern swaps for tokens. Spotify requires the
// redirect URI to match the one in the user's app exactly, so the port is a
// setting rather than whatever happens to be free.

// spotifySignInTimeout is how long the callback waits for the browser.
const spotifySignInTimeout = 10 * time.Minute

type spotifyAuthFlow struct {
	state    string
	verifier string
	redirect string
	clientID string
	srv      *http.Server
	timer    *time.Timer
}

var (
	spotifyAuthMu  sync.Mutex
	spotifyAuthNow *spotifyAuthFlow
)

// randomToken is n random bytes in URL-safe base64 without padding.
func randomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// pkceChallenge is the S256 code challenge for a verifier.
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func setSpotifySignIn(s *SpotifySignIn) {
	spotifyMu.Lock()
	spotifySignInNow = s
	spotifyMu.Unlock()
	NotifyChange()
}

// SpotifyStartSignIn opens the loopback callback and publishes the consent
// page URL; the panel opens it in the browser.
func SpotifyStartSignIn() {
	o := GetConfig().SpotifyOptions
	if o.ClientID == "" {
		setSpotifySignIn(&SpotifySignIn{Error: "Add your Spotify client ID first"})
		return
	}
	stopSpotifySignIn()
	flow := &spotifyAuthFlow{
		state:    randomToken(16),
		verifier: randomToken(48), // 64 characters, inside PKCE's 43 to 128
		redirect: SpotifyRedirectURI(o.RedirectPort),
		clientID: o.ClientID,
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", o.RedirectPort))
	if err != nil {
		setSpotifySignIn(&SpotifySignIn{Error: fmt.Sprintf("Port %d is busy. Pick another sign-in port and add the new redirect URI to your Spotify app.", o.RedirectPort)})
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) { spotifyCallback(flow, w, r) })
	flow.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	flow.timer = time.AfterFunc(spotifySignInTimeout, func() {
		if finishSpotifySignIn(flow) {
			setSpotifySignIn(&SpotifySignIn{Error: "The sign-in timed out. Try again."})
		}
	})
	spotifyAuthMu.Lock()
	spotifyAuthNow = flow
	spotifyAuthMu.Unlock()
	go func() { _ = flow.srv.Serve(ln) }()

	q := url.Values{
		"client_id":             {o.ClientID},
		"response_type":         {"code"},
		"redirect_uri":          {flow.redirect},
		"code_challenge_method": {"S256"},
		"code_challenge":        {pkceChallenge(flow.verifier)},
		"state":                 {flow.state},
		"scope":                 {spotifyScopes},
	}
	setSpotifySignIn(&SpotifySignIn{URL: spotifyAccountsBase + "/authorize?" + q.Encode()})
}

// SpotifyCancelSignIn abandons a pending sign-in.
func SpotifyCancelSignIn() {
	stopSpotifySignIn()
	setSpotifySignIn(nil)
}

func stopSpotifySignIn() {
	spotifyAuthMu.Lock()
	flow := spotifyAuthNow
	spotifyAuthMu.Unlock()
	if flow != nil {
		finishSpotifySignIn(flow)
	}
}

// finishSpotifySignIn closes flow's listener. It reports false when flow
// was already finished, so only one outcome is ever shown.
func finishSpotifySignIn(flow *spotifyAuthFlow) bool {
	spotifyAuthMu.Lock()
	if spotifyAuthNow != flow {
		spotifyAuthMu.Unlock()
		return false
	}
	spotifyAuthNow = nil
	spotifyAuthMu.Unlock()
	flow.timer.Stop()
	go func() {
		// Let the answer page reach the browser before the server goes.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = flow.srv.Shutdown(ctx)
	}()
	return true
}

func spotifyCallback(flow *spotifyAuthFlow, w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("state") != flow.state {
		// Not the browser tab Fern opened (or an old one); keep waiting.
		spotifyAnswer(w, http.StatusBadRequest, "This sign-in link is out of date. Start the sign-in again from Fern.")
		return
	}
	if e := q.Get("error"); e != "" {
		reason := "Spotify sign-in failed: " + e
		if e == "access_denied" {
			reason = "The Spotify sign-in was cancelled."
		}
		if finishSpotifySignIn(flow) {
			setSpotifySignIn(&SpotifySignIn{Error: reason})
		}
		spotifyAnswer(w, http.StatusOK, reason+" You can close this tab.")
		return
	}
	code := q.Get("code")
	if code == "" {
		spotifyAnswer(w, http.StatusBadRequest, "Spotify sent no sign-in code.")
		return
	}
	if !finishSpotifySignIn(flow) {
		spotifyAnswer(w, http.StatusOK, "This sign-in has already finished. You can close this tab.")
		return
	}
	c := newSpotifyClient(flow.clientID, "")
	t, err := c.tokenRequest(url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {flow.redirect},
		"code_verifier": {flow.verifier},
	})
	if err != nil {
		setSpotifySignIn(&SpotifySignIn{Error: err.Error()})
		spotifyAnswer(w, http.StatusOK, err.Error())
		return
	}
	storeSpotifyTokens(t)
	resetSpotifyState()
	setSpotifySignIn(nil)
	SpotifyRefreshNow()
	spotifyAnswer(w, http.StatusOK, "Fern is signed in to Spotify. You can close this tab.")
}

// spotifyAnswer is the small page the browser shows after the redirect.
func spotifyAnswer(w http.ResponseWriter, status int, text string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>Fern</title>`+
		`<body style="font-family:system-ui,sans-serif;background:#111;color:#eee;display:grid;place-items:center;height:90vh">`+
		`<p style="font-size:18px">%s</p></body>`, html.EscapeString(text))
}
