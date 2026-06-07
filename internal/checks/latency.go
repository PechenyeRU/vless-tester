package checks

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/whitedns/vless-tester/internal/model"
)

// DefaultLatencyURLs are the connectivity endpoints the gate probes when no
// explicit URL is configured: one Cloudflare, one Google. Requiring both to
// answer guards against nodes that can reach a single provider's anycast (and so
// pass a one-endpoint test) but cannot actually carry general traffic.
var DefaultLatencyURLs = []string{
	"https://cp.cloudflare.com/generate_204",
	"https://www.gstatic.com/generate_204",
}

// DefaultLatencyURL is the first default endpoint, kept for callers/tests that
// reference a single URL.
const DefaultLatencyURL = "https://cp.cloudflare.com/generate_204"

// LatencyCheck measures round-trip time to one or more known 204 endpoints
// through the proxy. It is the cheap first stage of the funnel and passes only
// if every probed endpoint answers. Endpoint selection, in precedence order:
// URLs (when non-empty), then URL (single, legacy), then DefaultLatencyURLs.
type LatencyCheck struct {
	URL     string
	URLs    []string
	Timeout time.Duration
}

// targets resolves the endpoints to probe, honoring the field precedence.
func (c LatencyCheck) targets() []string {
	if len(c.URLs) > 0 {
		return c.URLs
	}
	if c.URL != "" {
		return []string{c.URL}
	}
	return DefaultLatencyURLs
}

// Name returns the check's identifier.
func (c LatencyCheck) Name() string { return "latency" }

// Phase reports which pipeline phase the check runs in.
func (c LatencyCheck) Phase() model.JobPhase { return model.PhaseLatency }

// Run executes the check through the proxied client and returns its result. All
// configured endpoints must answer; the recorded latency is the slowest of them,
// so the gate reflects worst-case reachability rather than the best provider.
func (c LatencyCheck) Run(ctx context.Context, client *http.Client) (Result, error) {
	urls := c.targets()
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	worst := 0
	for _, url := range urls {
		ms, detail, ok := c.probe(ctx, client, url, timeout)
		if !ok {
			return Result{Passed: false, Detail: detail}, nil
		}
		if ms > worst {
			worst = ms
		}
	}
	return Result{Passed: true, LatencyMs: ptrInt(worst)}, nil
}

// probe times a single endpoint, returning the round-trip in ms and ok=true on a
// sub-400 response; on failure it returns a detail string and ok=false.
func (c LatencyCheck) probe(ctx context.Context, client *http.Client, url string, timeout time.Duration) (int, string, bool) {
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Sprintf("build request: %v", err), false
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, err.Error(), false
	}
	defer func() { _ = resp.Body.Close() }()
	elapsed := time.Since(start)
	if resp.StatusCode >= 400 {
		return 0, fmt.Sprintf("%s: status %d", url, resp.StatusCode), false
	}
	return int(elapsed.Milliseconds()), "", true
}
