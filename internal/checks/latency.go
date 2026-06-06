package checks

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/whitedns/vless-tester/internal/model"
)

// DefaultLatencyURL is a tiny, globally reachable 204 endpoint.
const DefaultLatencyURL = "https://cp.cloudflare.com/generate_204"

// LatencyCheck measures round-trip time to a known 204 endpoint through the
// proxy. It is the cheap first stage of the funnel.
type LatencyCheck struct {
	URL     string
	Timeout time.Duration
}

// Name returns the check's identifier.
func (c LatencyCheck) Name() string { return "latency" }

// Phase reports which pipeline phase the check runs in.
func (c LatencyCheck) Phase() model.JobPhase { return model.PhaseLatency }

// Run executes the check through the proxied client and returns its result.
func (c LatencyCheck) Run(ctx context.Context, client *http.Client) (Result, error) {
	url := c.URL
	if url == "" {
		url = DefaultLatencyURL
	}
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Result{}, fmt.Errorf("latency: build request: %w", err)
	}

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return Result{Passed: false, Detail: err.Error()}, nil
	}
	defer func() { _ = resp.Body.Close() }()
	elapsed := time.Since(start)

	if resp.StatusCode >= 400 {
		return Result{Passed: false, Detail: fmt.Sprintf("status %d", resp.StatusCode)}, nil
	}
	ms := int(elapsed.Milliseconds())
	return Result{Passed: true, LatencyMs: ptrInt(ms)}, nil
}
