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
	"sync"
	"syscall"
	"time"

	"github.com/whitedns/vless-tester/internal/api"
	"github.com/whitedns/vless-tester/internal/engine"
	"github.com/whitedns/vless-tester/internal/ingest"
	"github.com/whitedns/vless-tester/internal/logbuf"
	"github.com/whitedns/vless-tester/internal/model"
	"github.com/whitedns/vless-tester/internal/naming"
	"github.com/whitedns/vless-tester/internal/output"
	"github.com/whitedns/vless-tester/internal/scheduler"
	"github.com/whitedns/vless-tester/internal/store"
	webui "github.com/whitedns/vless-tester/web"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Tee the coordinator log into an in-memory ring buffer so the admin
	// dashboard can poll recent lines (GET /api/v1/logs).
	logs := logbuf.New(1000)
	log.SetOutput(io.MultiWriter(os.Stderr, logs))

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

	eng := buildEngine(ctx, st)

	sched := scheduler.New(func(name string, err error) {
		log.Printf("job %q error: %v", name, err)
	})

	// Dispatch: ingest sources and enqueue a fan-out of jobs for the fleet. It is
	// a no-op while a previous cycle is still draining.
	sched.Add(scheduler.Job{
		Name:       "dispatch",
		IntervalFn: func() time.Duration { return intervalSetting(ctx, st, "dispatch.interval", 12*time.Hour) },
		RunOnStart: true,
		Run: func(ctx context.Context) error {
			servers, err := loadServers(ctx, st)
			if err != nil {
				return err
			}
			batch, dispatched, err := eng.DispatchCycle(ctx, servers)
			if err != nil {
				return err
			}
			if dispatched {
				// The engine logs the actual enqueued count (after the per-cycle
				// cap); here we report how many were loaded from sources.
				log.Printf("dispatch: batch %d started (%d servers loaded)", batch, len(servers))
			} else {
				log.Printf("dispatch: skipped, a cycle is still in progress")
			}
			return nil
		},
	})

	// Reconcile: requeue dead-worker jobs and publish once the batch drains.
	sched.Add(scheduler.Job{
		Name:       "reconcile",
		IntervalFn: func() time.Duration { return intervalSetting(ctx, st, "reconcile.interval", 10*time.Second) },
		RunOnStart: true,
		Run: func(ctx context.Context) error {
			res, err := eng.Reconcile(ctx)
			if err != nil {
				return err
			}
			if res.Requeued > 0 || res.Failed > 0 {
				log.Printf("reconcile: requeued %d, failed %d", res.Requeued, res.Failed)
			}
			if res.Published {
				log.Printf("reconcile: batch complete, approved %d", res.Approved)
			}
			return nil
		},
	})

	// Fleet metrics: periodic snapshot of fleet and queue health.
	sched.Add(scheduler.Job{
		Name:       "metrics",
		Interval:   60 * time.Second,
		RunOnStart: false,
		Run: func(ctx context.Context) error {
			fs, err := st.Fleet(ctx, 90*time.Second)
			if err != nil {
				return err
			}
			log.Printf("fleet: workers=%d alive=%d | jobs queued=%d claimed=%d done=%d failed=%d",
				fs.Workers, fs.Alive, fs.Queued, fs.Claimed, fs.Done, fs.Failed)
			return nil
		},
	})

	// Republish: re-evaluate the approval gate against stored history and push,
	// without re-testing. Manual-only (Interval 0, no run-on-start); the Phase 2
	// UI triggers it after a gate change.
	sched.Add(scheduler.Job{
		Name: "republish",
		Run: func(ctx context.Context) error {
			sum, err := eng.PublishFromHistory(ctx)
			if err != nil {
				return err
			}
			log.Printf("republish: approved %d (from history, no retest)", sum.Approved)
			return nil
		},
	})

	// GeoIP refresh (only when credentials are configured).
	if acc, key := os.Getenv("MAXMIND_ACCOUNT_ID"), os.Getenv("MAXMIND_LICENSE_KEY"); acc != "" && key != "" {
		dl := &naming.MaxMindDownloader{AccountID: acc, LicenseKey: key}
		sched.Add(scheduler.Job{
			Name:       "geoip-refresh",
			IntervalFn: func() time.Duration { return intervalSetting(ctx, st, "geoip.refresh", 336*time.Hour) },
			Run: func(ctx context.Context) error {
				return dl.EnsureDatabase(ctx, geoipPath(), 14*24*time.Hour)
			},
		})
	}

	// HTTP surface: the untrusted worker control plane (per-worker tokens) and
	// the admin/read plane for the dashboard (ADMIN_TOKEN), two distinct trust
	// domains served on one listener.
	srv := &http.Server{
		Addr:              apiAddr(),
		Handler:           buildHTTP(st, sched, logs),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		log.Printf("coordinator: control plane listening on %s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("control plane: %v", err)
			stop()
		}
	}()

	log.Printf("coordinator started; scheduler running")
	sched.Start(ctx)
	<-ctx.Done()
	log.Printf("coordinator shutting down")

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Printf("control plane shutdown: %v", err)
	}
	return nil
}

// buildHTTP composes the worker control plane and the admin/read plane onto one
// mux. The two planes have separate bearer tokens, so a compromised worker
// cannot reach the mutating admin endpoints. Admin actions map to scheduler
// triggers, the single source of out-of-band runs.
func buildHTTP(st *store.Store, sched *scheduler.Scheduler, logs *logbuf.Hub) http.Handler {
	worker := (&api.Server{Store: st, Tokens: st, Logf: log.Printf}).Handler()
	adminUser := os.Getenv("ADMIN_USER")
	if adminUser == "" {
		adminUser = "admin"
	}
	admin := (&api.AdminServer{
		Logs:     logs,
		Store:    st,
		Username: adminUser,
		Password: os.Getenv("ADMIN_PASSWORD"),
		Logf:     log.Printf,
		Action: func(name string) error {
			switch name {
			case "refresh-sources", "retest":
				return sched.Trigger("dispatch")
			case "publish":
				return sched.Trigger("republish")
			case "refresh-geoip":
				return sched.Trigger("geoip-refresh")
			default:
				return fmt.Errorf("unknown action %q", name)
			}
		},
	}).Handler()

	mux := http.NewServeMux()
	// Worker control plane (untrusted workers).
	mux.Handle("/api/v1/workers/register", worker)
	mux.Handle("/api/v1/workers/heartbeat", worker)
	mux.Handle("/api/v1/jobs/", worker)
	// Admin/read plane (dashboard). The exact /workers path is the fleet view;
	// it does not collide with the worker plane's /workers/{register,heartbeat}.
	mux.Handle("/api/v1/servers", admin)
	mux.Handle("/api/v1/servers/", admin)
	mux.Handle("/api/v1/workers", admin)
	mux.Handle("/api/v1/stats", admin)
	mux.Handle("/api/v1/progress", admin)
	mux.Handle("/api/v1/cancel-cycle", admin)
	mux.Handle("/api/v1/logs", admin)
	mux.Handle("/api/v1/notify-test", admin)
	mux.Handle("/api/v1/sources", admin)
	mux.Handle("/api/v1/sources/import", admin)
	mux.Handle("/api/v1/settings", admin)
	mux.Handle("/api/v1/actions/", admin)
	mux.Handle("/api/v1/worker-tokens", admin)
	mux.Handle("/api/v1/worker-tokens/", admin)
	mux.Handle("/api/v1/login", admin)
	mux.Handle("/api/v1/logout", admin)
	// Public subscription distribution endpoint (no auth): clients fetch the
	// working list here in their preferred format. Serves public data only.
	sub := (&api.SubServer{Store: st, Logf: log.Printf}).Handler()
	mux.Handle("/sub", sub)
	mux.Handle("/sub/", sub) // obfuscated-path form /sub/{token}
	// Embedded SvelteKit dashboard at the root, below the API planes.
	mux.Handle("/", webui.Handler())
	return mux
}

func apiAddr() string {
	if v := os.Getenv("API_ADDR"); v != "" {
		return v
	}
	return ":8080"
}

// buildEngine wires the coordinator-side engine. It does no in-process probing
// (remote workers test); it dispatches jobs, reconciles, and publishes. The gate
// and queue knobs come from settings so they are tunable from the admin UI.
func buildEngine(_ context.Context, st *store.Store) *engine.Engine {
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

	// The approval gate, fan-out, lease/attempts and notifications are read live
	// from settings (via liveSettings), so admin edits apply on the next cycle
	// without a coordinator restart.
	return &engine.Engine{
		Store:       st,
		Resolver:    resolver,
		Seq:         naming.Allocator{Backend: st.NewSeqBackend()},
		Publisher:   publisher,
		Logf:        log.Printf,
		Brand:       "@WhiteDNS",
		AliveWindow: 90 * time.Second,
		Live:        liveSettings{st: st},
	}
}

// liveSettings reads the dynamic engine knobs from settings at use-time so the
// admin UI takes effect without restarting the coordinator.
type liveSettings struct{ st *store.Store }

func (l liveSettings) Approval(ctx context.Context) engine.Approval {
	return engine.Approval{
		MaxLatencyMs:    intSetting(ctx, l.st, "approval.max_latency_ms", 800),
		MinDlMBps:       floatSetting(ctx, l.st, "approval.min_dl_mbps", 1),
		RequiredWorkers: intSetting(ctx, l.st, "approval.required_workers", 1),
		AllowPartial:    boolSetting(ctx, l.st, "approval.allow_partial", true),
	}
}

func (l liveSettings) Fanout(ctx context.Context) int {
	return intSetting(ctx, l.st, "approval.required_workers", 1)
}

func (l liveSettings) LeaseTTL(ctx context.Context) time.Duration {
	return intervalSetting(ctx, l.st, "jobs.lease_ttl", 2*time.Minute)
}

func (l liveSettings) MaxAttempts(ctx context.Context) int {
	return intSetting(ctx, l.st, "jobs.max_attempts", 3)
}

func (l liveSettings) NotifyURLs(ctx context.Context) (bool, []string) {
	enabled, urls, err := l.st.NotifySettings(ctx)
	if err != nil {
		return false, nil
	}
	return enabled, urls
}

// maxSourceFetch caps how many subscription sources are fetched at once. The
// fetch is network-bound, so a serial loop over hundreds of sources takes
// forever; a bounded pool keeps the wall-clock low without opening hundreds of
// concurrent connections.
const maxSourceFetch = 16

// maxSubscriptionBytes caps a single subscription response body (32 MiB). Far
// above any real link list, but bounds memory against a runaway or hostile
// source.
const maxSubscriptionBytes = 32 << 20

// loadServers fetches and parses all enabled ingest sources concurrently, folding
// each parsed batch into a shared deduper as it arrives. Subscriptions overlap
// heavily, so deduping on arrival (rather than accumulating every duplicate and
// deduping once at the end) keeps peak memory proportional to the unique server
// count instead of the raw total across all sources.
func loadServers(ctx context.Context, st *store.Store) ([]model.Server, error) {
	sources, err := st.ListSources(ctx)
	if err != nil {
		return nil, err
	}

	var (
		mu    sync.Mutex
		dedup = ingest.NewDeduper()
		wg    sync.WaitGroup
		sem   = make(chan struct{}, maxSourceFetch)
	)
	for _, src := range sources {
		wg.Add(1)
		sem <- struct{}{}
		go func(src model.Source) {
			defer wg.Done()
			defer func() { <-sem }()

			body, err := readSource(ctx, src)
			if err != nil {
				log.Printf("source %s: %v", src.Location, err)
				return
			}
			servers, _ := ingest.ParseSubscription(body)
			// Drop the body before locking so only the parsed servers (and never
			// many full bodies plus the shared unique set) coexist at the peak.
			mu.Lock()
			dedup.Add(servers)
			mu.Unlock()
			_ = st.TouchSource(ctx, src.ID)
		}(src)
	}
	wg.Wait()
	return dedup.Servers(), nil
}

func readSource(ctx context.Context, src model.Source) (string, error) {
	switch src.Kind {
	case model.SourceRawInline:
		// The config text is the location itself; no fetch needed.
		return src.Location, nil
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
		defer func() { _ = resp.Body.Close() }()
		// Cap the body: a subscription is base64 text of share links (a few MB even
		// for tens of thousands of nodes). Without a limit a single huge or
		// misbehaving response would read straight into RAM and could OOM the
		// coordinator on its own.
		b, err := io.ReadAll(io.LimitReader(resp.Body, maxSubscriptionBytes))
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

// intSetting reads an integer setting, falling back to def.
func intSetting(ctx context.Context, st *store.Store, key string, def int) int {
	var v int
	if err := st.GetSetting(ctx, key, &v); err != nil {
		return def
	}
	return v
}

// floatSetting reads a float setting, falling back to def.
func floatSetting(ctx context.Context, st *store.Store, key string, def float64) float64 {
	var v float64
	if err := st.GetSetting(ctx, key, &v); err != nil {
		return def
	}
	return v
}

// boolSetting reads a boolean setting, falling back to def.
func boolSetting(ctx context.Context, st *store.Store, key string, def bool) bool {
	var v bool
	if err := st.GetSetting(ctx, key, &v); err != nil {
		return def
	}
	return v
}
