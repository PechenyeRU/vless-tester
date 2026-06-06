// Command worker is a probe: it pulls jobs from the coordinator, runs the test
// battery through a local sing-box instance, and reports raw measurements. It
// holds no approval/naming/publish logic. All configuration is via env var so
// the same binary runs bare-metal or in a container. See DESIGN.md 3.2.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/whitedns/vless-tester/internal/checks"
	"github.com/whitedns/vless-tester/internal/core"
	"github.com/whitedns/vless-tester/internal/engine"
	"github.com/whitedns/vless-tester/internal/worker"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	base := os.Getenv("COORDINATOR_URL")
	if base == "" {
		return fmt.Errorf("COORDINATOR_URL is required")
	}

	// Control channel: optionally tunneled through a SOCKS5 (COORDINATOR_PROXY),
	// independent of the local sing-box proxy used to test servers.
	httpClient, err := worker.ProxyClient(os.Getenv("COORDINATOR_PROXY"), 30*time.Second)
	if err != nil {
		return err
	}
	coord := &worker.Client{
		BaseURL: base,
		Token:   os.Getenv("WORKER_TOKEN"),
		HTTP:    httpClient,
	}

	id := worker.ResolveID(os.Getenv("WORKER_ID"))

	speedCfg := checks.SpeedConfig{
		DownloadURL: "https://speed.cloudflare.com/__down",
		UploadURL:   "https://speed.cloudflare.com/__up",
		Adaptive:    true,
	}

	// Baseline self-test: measure direct (un-proxied) download bandwidth so the
	// coordinator can size this worker. Skipped when WORKER_BW_MBPS is set.
	measure := func(ctx context.Context) (float64, error) {
		res, err := (checks.SpeedCheck{Config: speedCfg}).Run(ctx, &http.Client{Timeout: 30 * time.Second})
		if err != nil {
			return 0, err
		}
		if res.DlMbps == nil {
			return 0, nil
		}
		return *res.DlMbps, nil
	}
	capCfg := worker.CapacityConfig{
		Latency: envInt("WORKER_CAP_LATENCY"),
		Speed:   envInt("WORKER_CAP_SPEED"),
		BwMbps:  envFloat("WORKER_BW_MBPS"),
	}
	baselineCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	capacity := worker.Capacity(baselineCtx, capCfg, measure)
	cancel()

	// Speed tests saturate the link, so only Capacity.Speed run at once across
	// all concurrent funnel jobs; latency probes are cheap and fan out wide.
	speedGate := checks.NewSemaphore(capacity.Speed)

	batchMax := envInt("WORKER_BATCH")
	if batchMax <= 0 {
		batchMax = capacity.Latency
	}

	// Tunable probe timeouts: most servers in the catalog are dead, so the
	// latency timeout dominates per-probe time; lowering it raises throughput at
	// the cost of cutting slow-but-alive servers. Configurable without a rebuild.
	latencyTimeout := envDuration("WORKER_LATENCY_TIMEOUT", 5*time.Second)
	startTimeout := envDuration("WORKER_START_TIMEOUT", 8*time.Second)

	w := &worker.Worker{
		ID:       id,
		Capacity: capacity,
		Coord:    coord,
		Runner: worker.ProbeRunner{
			Options:   core.Options{StartTimeout: startTimeout},
			Latency:   checks.LatencyCheck{Timeout: latencyTimeout},
			Speed:     checks.SpeedCheck{Config: speedCfg},
			SpeedGate: speedGate,
			NewClient: engine.SOCKS5Client,
		},
		BatchMax:    batchMax,
		Concurrency: capacity.Latency,
		Idle:        5 * time.Second,
		Logf:        log.Printf,
	}

	log.Printf("worker starting: id=%s coordinator=%s", id, base)
	if err := w.Run(ctx); err != nil && ctx.Err() == nil {
		return err
	}
	log.Printf("worker %s shutting down", w.ID)
	return nil
}

func envInt(key string) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return 0
}

// envDuration reads a Go duration (e.g. "3s", "2500ms") from key, falling back to
// def when unset or unparseable.
func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return def
}

func envFloat(key string) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return 0
}
