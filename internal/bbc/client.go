package bbc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type Client struct {
	http     *http.Client
	maxRetry int
}

func NewClient() *Client {
	return &Client{
		http: &http.Client{
			Timeout: 20 * time.Second,
		},
		maxRetry: 3,
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

// ErrNotJSON matches a *NotJSONError: the BBC answered a JSON endpoint
// with an HTML page or an empty body. GitHub #57.
var ErrNotJSON = errors.New("not a JSON response")

// NotJSONError reports a JSON endpoint that answered with an HTML page or
// an empty body. From 2026-10-21 every BBC /programmes URL redirected to
// /error.html ("BBC: Service Temporarily Unavailable"); handing that page
// to json.Unmarshal surfaced as "invalid character '<'", which told the
// user nothing. GitHub #57.
type NotJSONError struct {
	URL         string
	FinalURL    string // after redirects
	ContentType string
	PageTitle   string // <title> of an HTML body, if any
}

func (e *NotJSONError) Error() string {
	if e.PageTitle == "" && e.ContentType == "" {
		return fmt.Sprintf("BBC returned an empty response instead of JSON for %s", e.URL)
	}
	msg := fmt.Sprintf("BBC returned an HTML page instead of JSON for %s", e.URL)
	var detail []string
	if e.FinalURL != "" && e.FinalURL != e.URL {
		detail = append(detail, "redirected to "+e.FinalURL)
	}
	if e.PageTitle != "" {
		detail = append(detail, fmt.Sprintf("page title %q", e.PageTitle))
	}
	if len(detail) > 0 {
		msg += " (" + strings.Join(detail, ", ") + ")"
	}
	return msg
}

func (e *NotJSONError) Is(target error) bool { return target == ErrNotJSON }

// GetJSONCtx is GetCtx for endpoints that must answer with JSON. A 2xx
// whose body is HTML (by Content-Type or by a leading '<') or empty is
// returned as a *NotJSONError rather than as a body, and is not retried:
// the BBC edge caches its error page for a minute, so an immediate retry
// only fetches the same page. A body with no Content-Type still passes
// when it does not start with '<'. A UTF-8 BOM is stripped, since
// encoding/json rejects one. GitHub #57.
func (c *Client) GetJSONCtx(ctx context.Context, url string) ([]byte, error) {
	res, err := c.fetchWithRetryCtx(ctx, url, c.maxRetry)
	if err != nil {
		return nil, err
	}
	body := bytes.TrimPrefix(res.body, []byte("\xef\xbb\xbf"))
	trimmed := bytes.TrimSpace(body)
	mediaType, _, _ := mime.ParseMediaType(res.contentType)
	isHTML := mediaType == "text/html" || mediaType == "application/xhtml+xml" ||
		(len(trimmed) > 0 && trimmed[0] == '<')
	if !isHTML && len(trimmed) > 0 {
		return body, nil
	}
	nj := &NotJSONError{URL: url, FinalURL: res.finalURL}
	if isHTML {
		nj.ContentType = res.contentType
		if nj.ContentType == "" {
			nj.ContentType = "text/html"
		}
		nj.PageTitle = htmlTitle(res.body)
	}
	return nil, nj
}

var titleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// htmlTitle returns the page's <title>, whitespace collapsed and capped,
// looking only at the first 64 KiB.
func htmlTitle(body []byte) string {
	if len(body) > 64<<10 {
		body = body[:64<<10]
	}
	m := titleRe.FindSubmatch(body)
	if m == nil {
		return ""
	}
	title := strings.Join(strings.Fields(string(m[1])), " ")
	if r := []rune(title); len(r) > 120 {
		title = string(r[:120])
	}
	return title
}

func (c *Client) doWithRetryCtx(ctx context.Context, url string, maxAttempts int) ([]byte, error) {
	res, err := c.fetchWithRetryCtx(ctx, url, maxAttempts)
	if err != nil {
		return nil, err
	}
	return res.body, nil
}

type fetchResult struct {
	body        []byte
	finalURL    string
	contentType string
}

func (c *Client) fetchWithRetryCtx(ctx context.Context, url string, maxAttempts int) (*fetchResult, error) {
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}
		// Roll a fresh UA per attempt so BBC's anti-bot heuristics don't
		// see a session-long fingerprint from one client instance. Audit
		// item 36.
		req.Header.Set("User-Agent", RandomUserAgent())

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

		return &fetchResult{
			body:        body,
			finalURL:    resp.Request.URL.String(),
			contentType: resp.Header.Get("Content-Type"),
		}, nil
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
	req.Header.Set("User-Agent", RandomUserAgent())
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}
