package modules

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Scopes the chat, alerts and actions need, on top of the follower count.
// Sign-ins made before they were added lack them; twitchMissingScopes tells
// the panel to ask for a new sign-in.
const (
	scopeChatRead      = "chat:read"
	scopeBits          = "bits:read"
	scopeSubs          = "channel:read:subscriptions"
	scopeRedemptions   = "channel:read:redemptions"
	scopeFollowers     = "moderator:read:followers"
	scopeAnnouncements = "moderator:manage:announcements"
	scopeShoutouts     = "moderator:manage:shoutouts"
)

// twitchAllScopes is every scope Fern asks for, in sign-in order.
var twitchAllScopes = []string{scopeFollowers, scopeChatRead, scopeBits, scopeSubs, scopeRedemptions, scopeAnnouncements, scopeShoutouts}

// twitchAuth is what the token says about itself.
type twitchAuth struct {
	UserID string
	Login  string
	Scopes []string
}

func (a twitchAuth) has(scope string) bool {
	for _, s := range a.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// twitchMissingScopes lists the scopes a token lacks.
func twitchMissingScopes(a twitchAuth) []string {
	var out []string
	for _, s := range twitchAllScopes {
		if !a.has(s) {
			out = append(out, s)
		}
	}
	return out
}

func (c twitchClient) validate() (twitchAuth, error) {
	req, err := http.NewRequest(http.MethodGet, c.id+"/validate", nil)
	if err != nil {
		return twitchAuth{}, err
	}
	req.Header.Set("Authorization", "OAuth "+c.token)
	var v struct {
		UserID string   `json:"user_id"`
		Login  string   `json:"login"`
		Scopes []string `json:"scopes"`
	}
	if err := c.do(req, &v); err != nil {
		return twitchAuth{}, err
	}
	return twitchAuth{UserID: v.UserID, Login: v.Login, Scopes: v.Scopes}, nil
}

var (
	// twitchRefreshMu makes token refreshes one at a time: Twitch refresh
	// tokens of public apps work once, so two refreshes at the same time
	// would sign the user out.
	twitchRefreshMu sync.Mutex

	twitchAuthMu    sync.Mutex
	twitchAuthCache struct {
		token string
		auth  twitchAuth
		at    time.Time
	}
)

// twitchRefreshToken swaps the refresh token for a new access token after
// failed was rejected. It does nothing when another refresh already
// replaced failed.
func twitchRefreshToken(clientID, failed string) error {
	twitchRefreshMu.Lock()
	defer twitchRefreshMu.Unlock()
	if cur := GetSecret(SecretTwitchToken); cur != failed && cur != "" {
		return nil
	}
	refresh := GetSecret(SecretTwitchRefresh)
	if refresh == "" || clientID == "" {
		return errTwitchAuth
	}
	tokens, err := newTwitchClient(clientID, "").refresh(refresh)
	if err != nil {
		return err
	}
	_ = SetSecret(SecretTwitchToken, tokens.AccessToken)
	if tokens.RefreshToken != "" {
		_ = SetSecret(SecretTwitchRefresh, tokens.RefreshToken)
	}
	return nil
}

// twitchSelf validates the stored token, refreshing it once when Twitch
// rejects it. The answer is cached for a while per token.
func twitchSelf(clientID string) (twitchAuth, string, error) {
	for attempt := 0; attempt < 2; attempt++ {
		token := GetSecret(SecretTwitchToken)
		if token == "" {
			return twitchAuth{}, "", errTwitchAuth
		}
		twitchAuthMu.Lock()
		cached := twitchAuthCache
		twitchAuthMu.Unlock()
		if cached.token == token && time.Since(cached.at) < 30*time.Minute {
			return cached.auth, token, nil
		}
		auth, err := newTwitchClient(clientID, token).validate()
		if err == nil {
			twitchAuthMu.Lock()
			twitchAuthCache.token, twitchAuthCache.auth, twitchAuthCache.at = token, auth, time.Now()
			twitchAuthMu.Unlock()
			return auth, token, nil
		}
		if !errors.Is(err, errTwitchAuth) && cached.token == token {
			// Twitch is unreachable for a moment: the last answer still holds.
			return cached.auth, token, nil
		}
		if !errors.Is(err, errTwitchAuth) || attempt > 0 {
			return twitchAuth{}, "", err
		}
		if rerr := twitchRefreshToken(clientID, token); rerr != nil {
			return twitchAuth{}, "", errTwitchAuth
		}
	}
	return twitchAuth{}, "", errTwitchAuth
}

// forgetTwitchAuth drops the cached token check, e.g. after a 401.
func forgetTwitchAuth() {
	twitchAuthMu.Lock()
	twitchAuthCache.token = ""
	twitchAuthMu.Unlock()
}

// twitchUserID looks up a channel's id by its login.
func (c twitchClient) userID(login string) (id, name string, err error) {
	var users struct {
		Data []struct {
			ID          string `json:"id"`
			Login       string `json:"login"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := c.helix("/users", url.Values{"login": {login}}, &users); err != nil {
		return "", "", err
	}
	if len(users.Data) == 0 {
		return "", "", fmt.Errorf("no Twitch channel called %q", login)
	}
	return users.Data[0].ID, users.Data[0].DisplayName, nil
}

// helixSend sends a JSON body to Helix and returns Twitch's own message for
// an error status.
func (c twitchClient) helixSend(method, path string, q url.Values, body any, out any) error {
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(data)
	}
	target := c.api + path
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	req, err := http.NewRequest(method, target, r)
	if err != nil {
		return err
	}
	req.Header.Set("Client-Id", c.clientID)
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return errors.New("could not reach Twitch")
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		return errTwitchAuth
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &e)
		msg := strings.TrimSpace(e.Message)
		if msg == "" {
			msg = resp.Status
		}
		return &twitchAPIError{Status: resp.StatusCode, Message: msg}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// twitchAPIError is a Helix error with Twitch's explanation.
type twitchAPIError struct {
	Status  int
	Message string
}

func (e *twitchAPIError) Error() string { return "Twitch: " + e.Message }
