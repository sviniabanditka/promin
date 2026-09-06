package sources

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// requestTimeout is the per-HTTP-call budget to the torrent indexer.
const requestTimeout = 20 * time.Second

// ErrUpstreamUnavailable: the indexer answered with an error or not at all.
var ErrUpstreamUnavailable = errors.New("sources: upstream unavailable")

// Client talks to the JacRed-compatible torrent indexer (jac.red). Online
// sources are all native providers now (internal/sources/provider); the lampac
// sidecar and its RCH relay were removed in Ф5 (docs/streaming.md).
type Client struct {
	httpClient *http.Client
	jacredURL  string
	jacredKey  string
}

func NewClient(jacredURL, jacredKey string) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: requestTimeout},
		jacredURL:  jacredURL,
		jacredKey:  jacredKey,
	}
}

func (c *Client) get(ctx context.Context, base string, q url.Values) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	reqURL := base
	if q != nil {
		reqURL += "?" + q.Encode()
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, truncate(body, 200))
	}
	return body, nil
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
