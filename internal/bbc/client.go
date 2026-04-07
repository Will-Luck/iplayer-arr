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

func (c *Client) Get(url string) ([]byte, error) {
	return c.doWithRetry(url, c.maxRetry)
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

func (c *Client) doWithRetry(url string, maxAttempts int) ([]byte, error) {
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("User-Agent", c.userAgent)

		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
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

func (c *Client) Head(url string) (int, error) {
	req, err := http.NewRequest("HEAD", url, nil)
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
