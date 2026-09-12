// Package youtube is promin's side of the YouTube section: a thin client for
// the ytx sidecar (sign-in, feeds, search, video pages, media track URLs) and
// a cached SponsorBlock lookup. It knows nothing about InnerTube itself — that
// lives in the sidecar so it can be updated independently when YouTube moves.
//
// One ytx account per promin profile: the account id is the profile's user id.
package youtube

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// ErrDisabled: no sidecar configured (PROMIN_YTX_URL empty).
var ErrDisabled = errors.New("youtube: disabled")

// Error is a sidecar error with its HTTP status and code, passed through to
// the API caller (404 not_linked, 409 unplayable, 502 youtube_upstream…).
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("youtube: %s (%d): %s", e.Code, e.Status, e.Message)
}

type Client struct {
	base string
	http *http.Client
	sb   *http.Client

	mu   sync.Mutex
	segs map[string]segEntry // videoID → SponsorBlock segments
}

func New(base string) *Client {
	if base == "" {
		return nil
	}
	return &Client{
		base: base,
		http: &http.Client{Timeout: 60 * time.Second},
		sb:   &http.Client{Timeout: 10 * time.Second},
		segs: map[string]segEntry{},
	}
}

// Enabled reports whether the section is configured (nil-safe).
func (c *Client) Enabled() bool { return c != nil }

func account(userID int64) string { return strconv.FormatInt(userID, 10) }

func (c *Client) do(ctx context.Context, method, path string, q url.Values, out any) error {
	return c.doBody(ctx, method, path, q, nil, out)
}

// doBody is do with an optional JSON payload.
func (c *Client) doBody(ctx context.Context, method, path string, q url.Values, payload any, out any) error {
	if c == nil {
		return ErrDisabled
	}
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var reqBody io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reqBody)
	if err != nil {
		return err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return &Error{Status: http.StatusBadGateway, Code: "ytx_unreachable", Message: err.Error()}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var e struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		if e.Error.Code == "" {
			e.Error.Code = "ytx_error"
		}
		return &Error{Status: resp.StatusCode, Code: e.Error.Code, Message: e.Error.Message}
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.Unmarshal(body, out)
}

// Account is the sign-in state of a profile.
type Account struct {
	Linked          bool   `json:"linked"`
	LinkedAt        int64  `json:"linked_at,omitempty"`
	Pending         bool   `json:"pending"`
	UserCode        string `json:"user_code,omitempty"`
	VerificationURL string `json:"verification_url,omitempty"`
	ExpiresAt       int64  `json:"expires_at,omitempty"`
	Error           string `json:"error,omitempty"`
}

func (c *Client) Account(ctx context.Context, userID int64) (Account, error) {
	var a Account
	err := c.do(ctx, http.MethodGet, "/v1/accounts/"+account(userID), nil, &a)
	return a, err
}

func (c *Client) StartLogin(ctx context.Context, userID int64) (Account, error) {
	var a Account
	err := c.do(ctx, http.MethodPost, "/v1/accounts/"+account(userID)+"/login", nil, &a)
	return a, err
}

func (c *Client) Unlink(ctx context.Context, userID int64) error {
	return c.do(ctx, http.MethodDelete, "/v1/accounts/"+account(userID), nil, nil)
}

// Feed is the sidecar's normalised browse/search shape, passed through as-is.
type Feed = json.RawMessage

func (c *Client) Browse(ctx context.Context, userID int64, page, cont string) (Feed, error) {
	q := url.Values{}
	if cont != "" {
		q.Set("cont", cont)
	}
	var f Feed
	err := c.do(ctx, http.MethodGet, "/v1/accounts/"+account(userID)+"/browse/"+url.PathEscape(page), q, &f)
	return f, err
}

func (c *Client) Search(ctx context.Context, userID int64, query, cont string) (Feed, error) {
	q := url.Values{}
	if cont != "" {
		q.Set("cont", cont)
	} else {
		q.Set("q", query)
	}
	var f Feed
	err := c.do(ctx, http.MethodGet, "/v1/accounts/"+account(userID)+"/search", q, &f)
	return f, err
}

func (c *Client) Video(ctx context.Context, userID int64, videoID string) (Feed, error) {
	var f Feed
	err := c.do(ctx, http.MethodGet, "/v1/accounts/"+account(userID)+"/video/"+url.PathEscape(videoID), nil, &f)
	return f, err
}

// TrackURL is the sidecar URL ffmpeg reads one media track from.
// TrackURL is the sidecar URL ffmpeg reads one elementary track from;
// startSec > 0 makes the sidecar begin the SABR pull at that source second
// (docs/youtube.md: resume and far seeks).
func (c *Client) TrackURL(userID int64, videoID, track, quality string, startSec float64) string {
	u := c.base + "/v1/accounts/" + account(userID) + "/stream/" + url.PathEscape(videoID) + "/" + track + "?quality=" + url.QueryEscape(quality)
	if startSec > 0 {
		u += "&start=" + strconv.FormatFloat(startSec, 'f', 3, 64)
	}
	return u
}

// Probe asks the sidecar whether media for the video can be fetched right
// now (anonymous web player + PO token) — the answer ffmpeg would otherwise
// discover mid-job. Returns the sidecar's *Error (409 unplayable with the
// reason, e.g. "Sign in to confirm you're not a bot") or nil.
func (c *Client) Probe(ctx context.Context, userID int64, videoID string) error {
	return c.do(ctx, http.MethodGet, "/v1/accounts/"+account(userID)+"/stream/"+url.PathEscape(videoID)+"/probe", nil, nil)
}

// Watch reports a playback position to the account's YouTube history (the
// sidecar sends the stats pings as the signed-in TV client).
func (c *Client) Watch(ctx context.Context, userID int64, videoID string, positionSec, durationSec float64) error {
	body := map[string]float64{"position_sec": positionSec, "duration_sec": durationSec}
	return c.doBody(ctx, http.MethodPost, "/v1/accounts/"+account(userID)+"/watch/"+url.PathEscape(videoID), nil, body, nil)
}

// ---- SponsorBlock ------------------------------------------------------------

// Segment is one community-marked span to skip.
type Segment struct {
	Category string  `json:"category"`
	Start    float64 `json:"start"`
	End      float64 `json:"end"`
}

type segEntry struct {
	at   time.Time
	segs []Segment
}

const (
	sbBase     = "https://sponsor.ajay.app/api/skipSegments/"
	sbTTL      = 6 * time.Hour
	sbCacheMax = 2000
)

// Segments looks a video up by the privacy-preserving hash-prefix endpoint
// (the server never sees the exact video id) and caches the answer. Errors
// degrade to "no segments": playback must never wait on SponsorBlock.
func (c *Client) Segments(ctx context.Context, videoID string, categories []string) []Segment {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	if e, ok := c.segs[videoID]; ok && time.Since(e.at) < sbTTL {
		c.mu.Unlock()
		return e.segs
	}
	c.mu.Unlock()

	sum := sha256.Sum256([]byte(videoID))
	prefix := hex.EncodeToString(sum[:])[:4]
	cats, _ := json.Marshal(categories)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sbBase+prefix+"?categories="+url.QueryEscape(string(cats)), nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "promin/1.0 (+https://promin.club)")
	resp, err := c.sb.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var list []struct {
		VideoID  string `json:"videoID"`
		Segments []struct {
			Category string     `json:"category"`
			Segment  [2]float64 `json:"segment"`
		} `json:"segments"`
	}
	segs := []Segment{}
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&list); err == nil {
			for _, v := range list {
				if v.VideoID != videoID {
					continue
				}
				for _, s := range v.Segments {
					segs = append(segs, Segment{Category: s.Category, Start: s.Segment[0], End: s.Segment[1]})
				}
			}
		}
	} else if resp.StatusCode != http.StatusNotFound {
		return nil // transient failure: do not cache
	}
	c.mu.Lock()
	if len(c.segs) >= sbCacheMax {
		c.segs = map[string]segEntry{} // ponytail: flush instead of LRU
	}
	c.segs[videoID] = segEntry{at: time.Now(), segs: segs}
	c.mu.Unlock()
	return segs
}
