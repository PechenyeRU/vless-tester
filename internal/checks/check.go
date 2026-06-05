package checks

import (
	"context"
	"net/http"

	"github.com/whitedns/vless-tester/internal/model"
)

// Result is the outcome of a single check. Throughput values are expressed in
// megabytes per second (MB/s) to match the unit published in node names.
type Result struct {
	Passed    bool
	LatencyMs *int
	DlMbps    *float64 // download throughput, MB/s
	UlMbps    *float64 // upload throughput, MB/s
	Detail    string
}

// Check is one test in the funnel pipeline. Run receives an http.Client whose
// transport routes through the proxy under test (via the worker's local SOCKS),
// so a Check only needs to issue ordinary HTTP requests. New approval checks
// (reachability, geo-match, dns-leak) implement this interface.
type Check interface {
	Name() string
	Phase() model.JobPhase
	Run(ctx context.Context, client *http.Client) (Result, error)
}

// ptrInt and ptrFloat are small helpers for building optional Result fields.
func ptrInt(v int) *int           { return &v }
func ptrFloat(v float64) *float64 { return &v }
