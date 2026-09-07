// Package subtitles finds external subtitle files for a title via the
// OpenSubtitles REST API (api.opensubtitles.com/api/v1) and caches the
// downloaded files on disk as WebVTT. The API key is an "API consumer" key
// (PROMIN_OPENSUBTITLES_API_KEY, k8s secret promin-secrets/opensubtitles-api-key);
// downloads use the anonymous quota, hence the cache — one download per file
// id, ever.
package subtitles

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultBaseURL = "https://api.opensubtitles.com/api/v1"
	userAgent      = "Promin v1.0"
	maxFileBytes   = 4 << 20
)

var ErrDisabled = errors.New("subtitles: no api key")
var ErrNotFound = errors.New("subtitles: not found")

// Result is one subtitle file as offered to the player.
type Result struct {
	FileID     int64   `json:"file_id"`
	Lang       string  `json:"lang"`
	Release    string  `json:"release"`
	Downloads  int     `json:"downloads"`
	HearingImp bool    `json:"hearing_impaired"`
	FPS        float64 `json:"fps,omitempty"`
	Uploader   string  `json:"uploader,omitempty"`
}

type Client struct {
	apiKey   string
	baseURL  string
	cacheDir string
	http     *http.Client
	convert  func(srt []byte) []byte // SRT → WebVTT
}

// New returns a client; convert turns SRT bytes into WebVTT (the httpapi relay
// already has that converter — injected to avoid a dependency cycle).
func New(apiKey, baseURL, cacheDir string, convert func([]byte) []byte) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{apiKey: apiKey, baseURL: strings.TrimSuffix(baseURL, "/"), cacheDir: cacheDir, http: &http.Client{Timeout: 25 * time.Second}, convert: convert}
}

func (c *Client) Enabled() bool { return c != nil && c.apiKey != "" }

type Query struct {
	IMDbID  string // "tt0903747" or "903747"
	Season  int
	Episode int
	Langs   []string // e.g. ["uk","ru","en"]
}

// Search lists subtitle files for the title, most downloaded first.
func (c *Client) Search(ctx context.Context, q Query) ([]Result, error) {
	if !c.Enabled() {
		return nil, ErrDisabled
	}
	imdb := strings.TrimPrefix(strings.TrimPrefix(q.IMDbID, "tt"), "0")
	if imdb == "" {
		return nil, fmt.Errorf("subtitles: imdb id required")
	}
	v := url.Values{}
	v.Set("imdb_id", imdb)
	if len(q.Langs) > 0 {
		v.Set("languages", strings.Join(q.Langs, ","))
	}
	if q.Season > 0 {
		v.Set("season_number", strconv.Itoa(q.Season))
		v.Set("episode_number", strconv.Itoa(q.Episode))
	}
	v.Set("order_by", "download_count")
	v.Set("order_direction", "desc")
	body, err := c.get(ctx, c.baseURL+"/subtitles?"+v.Encode())
	if err != nil {
		return nil, err
	}
	return ParseSearch(body)
}

type searchResp struct {
	Data []struct {
		Attributes struct {
			Language        string  `json:"language"`
			Release         string  `json:"release"`
			DownloadCount   int     `json:"download_count"`
			HearingImpaired bool    `json:"hearing_impaired"`
			FPS             float64 `json:"fps"`
			Uploader        struct {
				Name string `json:"name"`
			} `json:"uploader"`
			Files []struct {
				FileID int64 `json:"file_id"`
			} `json:"files"`
		} `json:"attributes"`
	} `json:"data"`
}

func ParseSearch(body []byte) ([]Result, error) {
	var r searchResp
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("subtitles: search json: %w", err)
	}
	out := make([]Result, 0, len(r.Data))
	for _, d := range r.Data {
		if len(d.Attributes.Files) == 0 {
			continue
		}
		out = append(out, Result{
			FileID: d.Attributes.Files[0].FileID, Lang: d.Attributes.Language, Release: d.Attributes.Release,
			Downloads: d.Attributes.DownloadCount, HearingImp: d.Attributes.HearingImpaired, FPS: d.Attributes.FPS,
			Uploader: d.Attributes.Uploader.Name,
		})
	}
	return out, nil
}

// VTT returns the subtitle file as WebVTT, from the disk cache when present.
func (c *Client) VTT(ctx context.Context, fileID int64) ([]byte, error) {
	if !c.Enabled() {
		return nil, ErrDisabled
	}
	path := filepath.Join(c.cacheDir, strconv.FormatInt(fileID, 10)+".vtt")
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		return b, nil
	}
	// One POST /download per file id (anonymous daily quota), then the link.
	payload, _ := json.Marshal(map[string]any{"file_id": fileID, "sub_format": "srt"})
	body, err := c.do(ctx, http.MethodPost, c.baseURL+"/download", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	var dl struct {
		Link      string `json:"link"`
		Remaining int    `json:"remaining"`
		Message   string `json:"message"`
	}
	if err := json.Unmarshal(body, &dl); err != nil || dl.Link == "" {
		return nil, fmt.Errorf("subtitles: download link: %s", strings.TrimSpace(dl.Message))
	}
	raw, err := c.get(ctx, dl.Link)
	if err != nil {
		return nil, err
	}
	vtt := raw
	if !bytes.HasPrefix(bytes.TrimLeft(raw, "\xEF\xBB\xBF"), []byte("WEBVTT")) && c.convert != nil {
		vtt = c.convert(raw)
	}
	if c.cacheDir != "" {
		_ = os.MkdirAll(c.cacheDir, 0o755)
		_ = os.WriteFile(path, vtt, 0o644)
	}
	return vtt, nil
}

func (c *Client) get(ctx context.Context, u string) ([]byte, error) {
	return c.do(ctx, http.MethodGet, u, nil)
}

func (c *Client) do(ctx context.Context, method, u string, body io.Reader) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	if strings.HasPrefix(u, c.baseURL) {
		req.Header.Set("Api-Key", c.apiKey)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxFileBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("subtitles: %s → %d %s", method, resp.StatusCode, strings.TrimSpace(string(b[:min(len(b), 120)])))
	}
	return b, nil
}
