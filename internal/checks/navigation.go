package checks

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DefaultNavigationURL is the real-site page the navigation gate fetches: a node
// that passes the cheap 204 latency gate but cannot actually carry general web
// traffic (CDN-fronted/domain-locked exits, blackholed routes) fails here.
const DefaultNavigationURL = "https://www.google.com/"

// DefaultNavigationMinBytes is the smallest response body accepted as a real
// page. A captive/blackhole exit often returns an empty or stub body with a 200,
// which this floor rejects.
const DefaultNavigationMinBytes = 1024

// NavigationCheck fetches a real web page through the proxy and requires a 2xx
// response carrying a non-trivial body. It is the funnel's "does it actually
// browse" gate, distinct from the latency gate (which only confirms a 204
// endpoint answers).
type NavigationCheck struct {
	URL      string
	MinBytes int64
	Timeout  time.Duration
}

// Name returns the check's identifier.
func (c NavigationCheck) Name() string { return "navigation" }

// Run fetches the page through the proxied client and reports whether it looks
// like a real navigation: a sub-400 status and at least MinBytes of body.
func (c NavigationCheck) Run(ctx context.Context, client *http.Client) (Result, error) {
	url := c.URL
	if url == "" {
		url = DefaultNavigationURL
	}
	minBytes := c.MinBytes
	if minBytes <= 0 {
		minBytes = DefaultNavigationMinBytes
	}
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Result{}, fmt.Errorf("navigation: build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return Result{Passed: false, Detail: err.Error()}, nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return Result{Passed: false, Detail: fmt.Sprintf("%s: status %d", url, resp.StatusCode)}, nil
	}
	// Read up to minBytes+1 to decide whether the body clears the floor without
	// pulling the whole page.
	n, err := io.Copy(io.Discard, io.LimitReader(resp.Body, minBytes+1))
	if err != nil {
		return Result{Passed: false, Detail: fmt.Sprintf("%s: read body: %v", url, err)}, nil
	}
	if n < minBytes {
		return Result{Passed: false, Detail: fmt.Sprintf("%s: body too small (%d bytes)", url, n)}, nil
	}
	return Result{Passed: true}, nil
}
