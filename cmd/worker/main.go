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

	// Control channel: optionally tunnelled through a SOCKS5 (COORDINATOR_PROXY),
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

	w := &worker.Worker{
		ID:       id,
		Capacity: capacity,
		Coord:    coord,
		Runner: worker.ProbeRunner{
			Options:   core.Options{StartTimeout: 8 * time.Second},
			Latency:   checks.LatencyCheck{Timeout: 5 * time.Second},
			Speed:     checks.SpeedCheck{Config: speedCfg},
			NewClient: engine.SOCKS5Client,
		},
		BatchMax: envInt("WORKER_BATCH"),
		Idle:     5 * time.Second,
		Logf:     log.Printf,
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

func envFloat(key string) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return 0
}
