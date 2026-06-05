package api

import (
	"testing"

	"github.com/whitedns/vless-tester/internal/model"
)

func intp(v int) *int       { return &v }
func fp(v float64) *float64 { return &v }

func TestSanitizeValidPassthrough(t *testing.T) {
	run := Bounds{}.sanitize(resultItem{Status: "ok", LatencyMs: intp(42), DlMbps: fp(12.5), UlMbps: fp(3)})
	if run.Status != model.StatusOK {
		t.Fatalf("status = %q, want ok", run.Status)
	}
	if run.LatencyMs == nil || *run.LatencyMs != 42 || run.DlMbps == nil || *run.DlMbps != 12.5 {
		t.Fatalf("valid values dropped: %+v", run)
	}
}

func TestSanitizeDropsOutOfRangeLatency(t *testing.T) {
	// Latency beyond the ceiling is dropped, and an "ok" with no usable latency
	// is downgraded: a worker cannot claim a pass it did not measure.
	run := Bounds{}.sanitize(resultItem{Status: "ok", LatencyMs: intp(10_000_000), DlMbps: fp(50)})
	if run.LatencyMs != nil {
		t.Fatalf("out-of-range latency kept: %v", *run.LatencyMs)
	}
	if run.Status != model.StatusError {
		t.Fatalf("status = %q, want error (ok needs a latency)", run.Status)
	}
}

func TestSanitizeDropsNegativeLatency(t *testing.T) {
	run := Bounds{}.sanitize(resultItem{Status: "ok", LatencyMs: intp(-5), DlMbps: fp(50)})
	if run.LatencyMs != nil || run.Status != model.StatusError {
		t.Fatalf("negative latency not handled: %+v", run)
	}
}

func TestSanitizeDropsImplausibleSpeed(t *testing.T) {
	// A fabricated huge speed is dropped so it cannot clear the approval gate.
	run := Bounds{}.sanitize(resultItem{Status: "ok", LatencyMs: intp(20), DlMbps: fp(1e9), UlMbps: fp(-1)})
	if run.DlMbps != nil {
		t.Fatalf("implausible dl kept: %v", *run.DlMbps)
	}
	if run.UlMbps != nil {
		t.Fatalf("negative ul kept: %v", *run.UlMbps)
	}
	// Latency was fine, so the row stays ok (just without a speed number).
	if run.Status != model.StatusOK {
		t.Fatalf("status = %q, want ok", run.Status)
	}
}

func TestSanitizeRespectsCustomBounds(t *testing.T) {
	b := Bounds{MaxLatencyMs: 100, MaxMBps: 5}
	run := b.sanitize(resultItem{Status: "ok", LatencyMs: intp(150), DlMbps: fp(10)})
	if run.LatencyMs != nil {
		t.Fatalf("latency 150 should exceed custom max 100")
	}
	if run.DlMbps != nil {
		t.Fatalf("dl 10 should exceed custom max 5")
	}
}
