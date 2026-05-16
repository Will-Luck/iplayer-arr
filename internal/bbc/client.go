package bbc

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	http      *http.Client
	userAgent string
	maxRetry  int
}

func NewClient() *Client {
	return &Client{
		http: &http.Client{
			Timeout: 20 * time.Second,
		},
		userAgent: RandomUserAgent(),
		maxRetry:  3,
	}
}

// Get is the legacy non-context HTTP helper. It now delegates to GetCtx
// with a background context so the request and retry loop share the
// single implementation in doWithRetryCtx. Callers that need cancellation
// or a deadline should call GetCtx (or GetWithTimeout) directly. Audit
// item 12.
func (c *Client) Get(url string) ([]byte, error) {
	return c.doWithRetryCtx(context.Background(), url, c.maxRetry)
}

func (c *Client) GetWithTimeout(url string, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return c.doWithRetryCtx(ctx, url, c.maxRetry)
}

// GetCtx is the context-honouring variant of Get. It bounds the HTTP
// request + retry loop by ctx rather than by any hardcoded timeout,
// so callers can set their own deadline via context.WithTimeout and
// have it actually propagate into the in-flight HTTP call. Used by
// the search-time quality prober to enforce the per-probe 20s budget.
func (c *Client) GetCtx(ctx context.Context, url string) ([]byte, error) {
	return c.doWithRetryCtx(ctx, url, c.maxRetry)
}

func (c *Client) doWithRetryCtx(ctx context.Context, url string, maxAttempts int) ([]byte, error) {
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("User-Agent", c.userAgent)

		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, url)
		}

		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, url)
			continue
		}

		return body, nil
	}
	return nil, fmt.Errorf("after %d attempts: %w", maxAttempts, lastErr)
}

// Head is the legacy non-context HEAD helper. It delegates to HeadCtx
// with a background context. Callers that need cancellation should use
// HeadCtx directly. Audit item 12.
func (c *Client) Head(url string) (int, error) {
	return c.HeadCtx(context.Background(), url)
}

// HeadCtx issues a HEAD request honouring ctx for cancellation and any
// caller-set deadline. No retry: HEAD is used as a cheap reachability
// probe, and a failure here is interpreted as "not available" by the
// caller rather than a transient error worth retrying.
func (c *Client) HeadCtx(ctx context.Context, url string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, "HEAD", url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}
