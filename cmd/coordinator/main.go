// Command coordinator is the control plane: it owns the internal scheduler that
// periodically ingests sources, drives the test engine, refreshes the GeoIP
// database, and publishes the working list. See DESIGN.md.
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/whitedns/vless-tester/internal/checks"
	"github.com/whitedns/vless-tester/internal/core"
	"github.com/whitedns/vless-tester/internal/engine"
	"github.com/whitedns/vless-tester/internal/ingest"
	"github.com/whitedns/vless-tester/internal/model"
	"github.com/whitedns/vless-tester/internal/naming"
	"github.com/whitedns/vless-tester/internal/output"
	"github.com/whitedns/vless-tester/internal/scheduler"
	"github.com/whitedns/vless-tester/internal/store"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

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

	eng, err := buildEngine(ctx, st)
	if err != nil {
		return err
	}

	sched := scheduler.New(func(name string, err error) {
		log.Printf("job %q error: %v", name, err)
	})

	// Test cycle: ingest sources, run the funnel, publish.
	sched.Add(scheduler.Job{
		Name:       "cycle",
		Interval:   intervalSetting(ctx, st, "publish.interval", 12*time.Hour),
		RunOnStart: true,
		Run: func(ctx context.Context) error {
			servers, err := loadServers(ctx, st)
			if err != nil {
				return err
			}
			log.Printf("cycle: testing %d servers", len(servers))
			sum, err := eng.RunOnce(ctx, servers)
			if err != nil {
				return err
			}
			log.Printf("cycle: tested %d, approved %d", sum.Tested, sum.Approved)
			return nil
		},
	})

	// GeoIP refresh (only when credentials are configured).
	if acc, key := os.Getenv("MAXMIND_ACCOUNT_ID"), os.Getenv("MAXMIND_LICENSE_KEY"); acc != "" && key != "" {
		dl := &naming.MaxMindDownloader{AccountID: acc, LicenseKey: key}
		sched.Add(scheduler.Job{
			Name:     "geoip-refresh",
			Interval: intervalSetting(ctx, st, "geoip.refresh", 336*time.Hour),
			Run: func(ctx context.Context) error {
				return dl.EnsureDatabase(ctx, geoipPath(), 14*24*time.Hour)
			},
		})
	}

	log.Printf("coordinator started; scheduler running")
	sched.Start(ctx)
	<-ctx.Done()
	log.Printf("coordinator shutting down")
	return nil
}

// buildEngine wires the engine with the real sing-box core and SOCKS client.
func buildEngine(_ context.Context, st *store.Store) (*engine.Engine, error) {
	var resolver naming.CountryResolver
	if path := geoipPath(); fileExists(path) {
		if mm, err := naming.OpenMaxMind(path); err == nil {
			resolver = mm
		}
	}
	var publisher output.Publisher
	if repo := os.Getenv("GITHUB_PUBLISH_REPO"); repo != "" {
		publisher = &output.GitPublisher{RepoURL: repo, Branch: "main"}
	}

	return &engine.Engine{
		Store:     st,
		Prober:    coreProber{opts: core.Options{StartTimeout: 8 * time.Second}},
		NewClient: engine.SOCKS5Client,
		Latency:   checks.LatencyCheck{Timeout: 5 * time.Second},
		Speed: checks.SpeedCheck{Config: checks.SpeedConfig{
			DownloadURL: "https://speed.cloudflare.com/__down",
			UploadURL:   "https://speed.cloudflare.com/__up",
			Adaptive:    true,
		}},
		Resolver:  resolver,
		Seq:       naming.Allocator{Backend: st.NewSeqBackend()},
		Publisher: publisher,
		Brand:     "@WhiteDNS",
		WorkerID:  "coordinator",
		Approval:  engine.Approval{MaxLatencyMs: 2000, MinDlMBps: 0.5},
	}, nil
}

// loadServers fetches and parses all enabled ingest sources.
func loadServers(ctx context.Context, st *store.Store) ([]model.Server, error) {
	sources, err := st.ListSources(ctx)
	if err != nil {
		return nil, err
	}
	var all []model.Server
	for _, src := range sources {
		body, err := readSource(ctx, src)
		if err != nil {
			log.Printf("source %s: %v", src.Location, err)
			continue
		}
		servers, _ := ingest.ParseSubscription(body)
		all = append(all, servers...)
		_ = st.TouchSource(ctx, src.ID)
	}
	unique, _ := ingest.Dedup(all)
	return unique, nil
}

func readSource(ctx context.Context, src model.Source) (string, error) {
	switch src.Kind {
	case model.SourceRawFile:
		b, err := os.ReadFile(src.Location)
		return string(b), err
	case model.SourceSubscriptionURL:
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.Location, nil)
		if err != nil {
			return "", err
		}
		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		return string(b), err
	default:
		return "", fmt.Errorf("unknown source kind %q", src.Kind)
	}
}

// intervalSetting reads a duration setting, falling back to def.
func intervalSetting(ctx context.Context, st *store.Store, key string, def time.Duration) time.Duration {
	var raw string
	if err := st.GetSetting(ctx, key, &raw); err != nil {
		return def
	}
	return scheduler.ParseInterval(raw, def)
}

func geoipPath() string {
	if v := os.Getenv("GEOIP_DB"); v != "" {
		return v
	}
	return "geoip/GeoLite2-Country.mmdb"
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// coreProber adapts the sing-box core to the engine.Prober interface.
type coreProber struct {
	opts core.Options
}

func (p coreProber) Start(ctx context.Context, srv model.Server) (engine.Instance, error) {
	return core.Start(ctx, srv, p.opts)
}
