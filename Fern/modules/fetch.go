package modules

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// webClient is shared by the lyrics and weather lookups. Requests run on
// their own workers, never on the send loop, and give up after a few seconds.
var webClient = &http.Client{Timeout: 8 * time.Second}

// webUserAgent names the app and nothing else.
const webUserAgent = "Fern"

// errRateLimited is returned for HTTP 429; the duration says when to retry.
var errRateLimited = errors.New("rate limited")

// httpStatusError is any other unexpected HTTP status.
type httpStatusError int

func (e httpStatusError) Error() string { return fmt.Sprintf("HTTP %d", int(e)) }

// getJSON fetches url and decodes a JSON reply into out. A 404 returns
// status 404 and no error, so callers can treat "not found" as an answer.
func getJSON(url string, out any) (status int, retryAfter time.Duration, err error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("User-Agent", webUserAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := webClient.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	body := io.LimitReader(resp.Body, 2<<20)
	switch {
	case resp.StatusCode == http.StatusNotFound:
		_, _ = io.Copy(io.Discard, body)
		return resp.StatusCode, 0, nil
	case resp.StatusCode == http.StatusTooManyRequests:
		wait := time.Minute
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
			wait = time.Duration(s) * time.Second
		}
		return resp.StatusCode, wait, errRateLimited
	case resp.StatusCode != http.StatusOK:
		return resp.StatusCode, 0, httpStatusError(resp.StatusCode)
	}
	if err := json.NewDecoder(body).Decode(out); err != nil {
		return resp.StatusCode, 0, err
	}
	return resp.StatusCode, 0, nil
}
