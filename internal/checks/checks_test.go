package checks_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/whitedns/vless-tester/internal/checks"
)

// zeroSource feeds zero bytes to the test download handler.
type zeroSource struct{}

func (zeroSource) Read(p []byte) (int, error) { return len(p), nil }

// newTestServer serves /204, /down (honoring ?bytes=) and /up. When served is
// non-nil it accumulates the total download bytes requested, letting adaptive
// tests assert exactly how much was transferred.
func newTestServer(served *atomic.Int64) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/204", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/down", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("bytes"))
		if served != nil {
			served.Add(int64(n))
		}
		io.CopyN(w, zeroSource{}, int64(n))
	})
	mux.HandleFunc("/up", func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	})
	return httptest.NewServer(mux)
}

func TestLatencyCheck(t *testing.T) {
	srv := newTestServer(nil)
	defer srv.Close()

	c := checks.LatencyCheck{URL: srv.URL + "/204"}
	res, err := c.Run(context.Background(), srv.Client())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Passed {
		t.Fatalf("expected passed, detail=%q", res.Detail)
	}
	if res.LatencyMs == nil || *res.LatencyMs < 0 {
		t.Fatalf("latency = %v, want >= 0", res.LatencyMs)
	}
}

func TestLatencyCheckFailsOnError(t *testing.T) {
	c := checks.LatencyCheck{URL: "http://127.0.0.1:1/nope"} // refused
	res, err := c.Run(context.Background(), http.DefaultClient)
	if err != nil {
		t.Fatalf("Run should not hard-error on unreachable host: %v", err)
	}
	if res.Passed {
		t.Fatal("expected not passed for unreachable host")
	}
}

func TestSpeedCheckBothDirections(t *testing.T) {
	srv := newTestServer(nil)
	defer srv.Close()

	c := checks.SpeedCheck{Config: checks.SpeedConfig{
		DownloadURL: srv.URL + "/down",
		UploadURL:   srv.URL + "/up",
		Streams:     4,
		Bytes:       400_000,
	}}
	res, err := c.Run(context.Background(), srv.Client())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Passed {
		t.Fatalf("expected passed, detail=%q", res.Detail)
	}
	if res.DlMbps == nil || *res.DlMbps <= 0 {
		t.Fatalf("dl = %v, want > 0", res.DlMbps)
	}
	if res.UlMbps == nil || *res.UlMbps <= 0 {
		t.Fatalf("ul = %v, want > 0", res.UlMbps)
	}
}

func TestSpeedCheckNonAdaptiveTransfersFullBytes(t *testing.T) {
	var served atomic.Int64
	srv := newTestServer(&served)
	defer srv.Close()

	c := checks.SpeedCheck{Config: checks.SpeedConfig{
		DownloadURL: srv.URL + "/down",
		Streams:     4,
		Bytes:       1_000_000,
		Adaptive:    false,
	}}
	if _, err := c.Run(context.Background(), srv.Client()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := served.Load(); got != 1_000_000 {
		t.Fatalf("downloaded %d bytes, want 1000000 (full transfer only)", got)
	}
}

func TestSpeedCheckAdaptiveSkipsFullWhenSlow(t *testing.T) {
	var served atomic.Int64
	srv := newTestServer(&served)
	defer srv.Close()

	// An impossibly high threshold makes the probe look "too slow", so the full
	// transfer is skipped and only the probe bytes are moved.
	c := checks.SpeedCheck{Config: checks.SpeedConfig{
		DownloadURL: srv.URL + "/down",
		Streams:     4,
		Bytes:       1_000_000,
		Adaptive:    true,
		ProbeBytes:  100_000,
		ProbeMinMBs: 1e9,
	}}
	res, err := c.Run(context.Background(), srv.Client())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := served.Load(); got != 100_000 {
		t.Fatalf("downloaded %d bytes, want 100000 (probe only)", got)
	}
	if res.DlMbps == nil || *res.DlMbps <= 0 {
		t.Fatalf("probe should still report a measurement, got %v", res.DlMbps)
	}
}

func TestSpeedCheckAdaptiveRunsFullWhenPromising(t *testing.T) {
	var served atomic.Int64
	srv := newTestServer(&served)
	defer srv.Close()

	c := checks.SpeedCheck{Config: checks.SpeedConfig{
		DownloadURL: srv.URL + "/down",
		Streams:     4,
		Bytes:       1_000_000,
		Adaptive:    true,
		ProbeBytes:  100_000,
		ProbeMinMBs: 0, // any speed is promising
	}}
	if _, err := c.Run(context.Background(), srv.Client()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Probe (100k) + full (1M).
	if got := served.Load(); got != 1_100_000 {
		t.Fatalf("downloaded %d bytes, want 1100000 (probe + full)", got)
	}
}
