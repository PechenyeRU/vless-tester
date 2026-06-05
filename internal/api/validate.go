package api

import "github.com/whitedns/vless-tester/internal/model"

// Default plausibility ceilings for worker-reported numbers. A worker is
// untrusted (DESIGN 3.2), so the coordinator never persists a measurement it
// cannot believe: out-of-range values are dropped rather than stored, so a
// malicious worker can neither approve a server with a fabricated speed nor
// poison the history with garbage.
const (
	defaultMaxLatencyMs = 60_000  // 60s; anything slower is a failure, not a number
	defaultMaxMBps      = 100_000 // 100 GB/s: an absurd ceiling, only catches lies
)

// Bounds caps the values the control plane will accept from a worker. A zero
// field falls back to its default.
type Bounds struct {
	MaxLatencyMs int
	MaxMBps      float64
}

func (b Bounds) maxLatency() int {
	if b.MaxLatencyMs > 0 {
		return b.MaxLatencyMs
	}
	return defaultMaxLatencyMs
}

func (b Bounds) maxMBps() float64 {
	if b.MaxMBps > 0 {
		return b.MaxMBps
	}
	return defaultMaxMBps
}

// sanitize turns an untrusted result item into a storable TestRun, dropping
// implausible values. A latency outside [0, max] is discarded; an "ok" report
// without a usable latency is downgraded to an error (a pass must be measured).
// Throughput outside [0, max] is dropped (nil), so it cannot clear the speed
// gate. Negative numbers and NaN-shaped reports are treated as out of range.
func (b Bounds) sanitize(item resultItem) model.TestRun {
	run := model.TestRun{
		Status:    normalizeStatus(item.Status),
		LatencyMs: item.LatencyMs,
		DlMbps:    item.DlMbps,
		UlMbps:    item.UlMbps,
		Error:     item.Error,
		Checks:    sanitizeChecks(item.Checks),
	}

	if run.LatencyMs != nil && (*run.LatencyMs < 0 || *run.LatencyMs > b.maxLatency()) {
		run.LatencyMs = nil
	}
	run.DlMbps = clampMBps(run.DlMbps, b.maxMBps())
	run.UlMbps = clampMBps(run.UlMbps, b.maxMBps())

	// An "ok" result must carry a believable latency; otherwise it is not a pass.
	if run.Status == model.StatusOK && run.LatencyMs == nil {
		run.Status = model.StatusError
		if run.Error == "" {
			run.Error = "implausible or missing latency"
		}
	}
	return run
}

// sanitizeChecks bounds the untrusted per-platform outcomes: it caps the count
// and trims overlong names/details so a malicious worker cannot bloat the
// checks table.
func sanitizeChecks(in []model.CheckOutcome) []model.CheckOutcome {
	const (
		maxChecks = 32
		maxName   = 32
		maxDetail = 64
	)
	if len(in) == 0 {
		return nil
	}
	if len(in) > maxChecks {
		in = in[:maxChecks]
	}
	out := make([]model.CheckOutcome, 0, len(in))
	for _, c := range in {
		if c.Name == "" {
			continue
		}
		out = append(out, model.CheckOutcome{
			Name:   truncate(c.Name, maxName),
			Passed: c.Passed,
			Detail: truncate(c.Detail, maxDetail),
			Metric: clampMetric(c.Metric),
		})
	}
	return out
}

// clampMetric bounds an untrusted numeric check metric (e.g. a 0-100 risk score)
// to [0, 100], dropping NaN/negative values to 0.
func clampMetric(m *float64) *float64 {
	if m == nil {
		return nil
	}
	v := *m
	if v != v || v < 0 { // NaN or negative
		v = 0
	}
	if v > 100 {
		v = 100
	}
	return &v
}

func truncate(s string, max int) string {
	if len(s) > max {
		return s[:max]
	}
	return s
}

// clampMBps drops a throughput value that is negative or above max.
func clampMBps(v *float64, max float64) *float64 {
	if v == nil {
		return nil
	}
	if *v < 0 || *v > max || *v != *v { // last test rejects NaN
		return nil
	}
	return v
}
