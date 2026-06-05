// Command tester runs a full single-node pipeline locally (ingest -> test ->
// output) against a file of share links. It is the Phase 0 entrypoint and the
// composition root wiring the real sing-box core and SOCKS client.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/whitedns/vless-tester/internal/checks"
	"github.com/whitedns/vless-tester/internal/core"
	"github.com/whitedns/vless-tester/internal/engine"
	"github.com/whitedns/vless-tester/internal/ingest"
	"github.com/whitedns/vless-tester/internal/model"
	"github.com/whitedns/vless-tester/internal/naming"
	"github.com/whitedns/vless-tester/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatalf("usage: %s <links-file>", os.Args[0])
	}
	if err := run(os.Args[1]); err != nil {
		log.Fatal(err)
	}
}

func run(linksFile string) error {
	ctx := context.Background()

	raw, err := os.ReadFile(linksFile)
	if err != nil {
		return fmt.Errorf("read links: %w", err)
	}
	servers, failed := ingest.ParseSubscription(string(raw))
	servers, dropped := ingest.Dedup(servers)
	log.Printf("parsed %d servers (%d unparseable lines, %d duplicates removed)", len(servers), len(failed), dropped)

	// Optional cap for sampling a large subscription during real test runs.
	if limit := envInt("LIMIT", 0); limit > 0 && limit < len(servers) {
		servers = servers[:limit]
		log.Printf("limited to %d servers", limit)
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	st, err := store.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return err
	}

	var resolver naming.CountryResolver
	if dbPath := os.Getenv("GEOIP_DB"); dbPath != "" {
		mm, err := naming.OpenMaxMind(dbPath)
		if err != nil {
			return err
		}
		defer mm.Close()
		resolver = mm
	}

	eng := &engine.Engine{
		Store:     st,
		Prober:    coreProber{opts: core.Options{StartTimeout: 8 * time.Second}},
		NewClient: engine.SOCKS5Client,
		Latency:   checks.LatencyCheck{Timeout: 5 * time.Second},
		Speed:     checks.SpeedCheck{Config: checks.SpeedConfig{DownloadURL: downloadURL(), UploadURL: uploadURL(), Adaptive: true}},
		Resolver:  resolver,
		Seq:       naming.Allocator{Backend: st.NewSeqBackend()},
		Brand:     "@WhiteDNS",
		WorkerID:  workerID(),
		Approval:  engine.Approval{MaxLatencyMs: envInt("APPROVE_MAX_LATENCY_MS", 2000), MinDlMBps: envFloat("APPROVE_MIN_MBPS", 0.5)},
	}

	sum, err := eng.RunOnce(ctx, servers)
	if err != nil {
		return err
	}
	log.Printf("tested %d, approved %d", sum.Tested, sum.Approved)
	return nil
}

// coreProber adapts the sing-box core to the engine.Prober interface.
type coreProber struct {
	opts core.Options
}

func (p coreProber) Start(ctx context.Context, srv model.Server) (engine.Instance, error) {
	return core.Start(ctx, srv, p.opts)
}

func downloadURL() string {
	if v := os.Getenv("SPEED_DOWNLOAD_URL"); v != "" {
		return v
	}
	return "https://speed.cloudflare.com/__down"
}

func uploadURL() string {
	if v := os.Getenv("SPEED_UPLOAD_URL"); v != "" {
		return v
	}
	return "https://speed.cloudflare.com/__up"
}

func workerID() string {
	if v := os.Getenv("WORKER_ID"); v != "" {
		return v
	}
	return "local"
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}
