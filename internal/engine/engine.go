package engine

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"regexp"
	"time"

	"github.com/whitedns/vless-tester/internal/checks"
	"github.com/whitedns/vless-tester/internal/convert"
	"github.com/whitedns/vless-tester/internal/ingest"
	"github.com/whitedns/vless-tester/internal/model"
	"github.com/whitedns/vless-tester/internal/naming"
	"github.com/whitedns/vless-tester/internal/notify"
	"github.com/whitedns/vless-tester/internal/output"
	"github.com/whitedns/vless-tester/internal/store"
)

// Instance is a running proxy with a local SOCKS endpoint.
type Instance interface {
	SocksAddress() string
	Close() error
}

// Prober starts a proxy for a server. The core package provides the sing-box
// implementation; tests provide a stub.
type Prober interface {
	Start(ctx context.Context, srv model.Server) (Instance, error)
}

// ClientFactory builds an http.Client that routes through a SOCKS address.
type ClientFactory func(socksAddr string) (*http.Client, error)

// Approval is the corroboration gate. A server is published when at least
// RequiredWorkers distinct workers each measured it within the latency/speed
// bounds. AllowPartial relaxes the count to 1 when the fleet is smaller than N,
// so a small deployment still publishes (DESIGN 5).
type Approval struct {
	MaxLatencyMs    int
	MinDlMBps       float64
	RequiredWorkers int
	AllowPartial    bool
}

// required is the effective distinct-worker count the gate demands.
func (a Approval) required() int {
	if a.AllowPartial {
		return 1
	}
	if a.RequiredWorkers < 1 {
		return 1
	}
	return a.RequiredWorkers
}

// Engine orchestrates the test pipeline. It serves two modes from the same
// pieces: an in-process single-node run (RunOnce, used by cmd/tester) and the
// distributed cycle (DispatchCycle enqueues work for remote workers; Reconcile
// requeues dead-worker jobs and publishes once a batch drains). Both publish via
// the same history-driven, corroboration-aware gate, so re-gating never retests.
type Engine struct {
	Store     *store.Store
	Prober    Prober
	NewClient ClientFactory
	Latency   checks.LatencyCheck
	Speed     checks.SpeedCheck
	Resolver  naming.CountryResolver // optional; nil means "unknown country"
	Seq       naming.Allocator
	Publisher output.Publisher     // optional; nil skips publishing
	Notifier  notify.Notifier      // optional; nil skips end-of-cycle notifications
	Logf      func(string, ...any) // optional logger; nil discards
	Brand     string
	WorkerID  string
	Approval  Approval

	// Distributed-cycle knobs (unused by RunOnce).
	Fanout      int           // distinct workers per config (>=1)
	LeaseTTL    time.Duration // a claim older than this is considered dead
	MaxAttempts int           // 0 = unlimited retries before a job is failed
	AliveWindow time.Duration // a worker seen within this counts as alive

	// Live, when set, supplies the dynamic knobs above (approval gate, fan-out,
	// lease/attempts, notify) from settings at use-time, so admin edits take
	// effect without a coordinator restart. nil falls back to the static fields
	// (cmd/tester's env-configured single-node run).
	Live LiveSettings
}

// LiveSettings resolves the dynamic engine knobs from settings at use-time.
type LiveSettings interface {
	Approval(ctx context.Context) Approval
	Fanout(ctx context.Context) int
	LeaseTTL(ctx context.Context) time.Duration
	MaxAttempts(ctx context.Context) int
	NotifyURLs(ctx context.Context) (enabled bool, urls []string)
}

// approval returns the gate, live when configured.
func (e *Engine) approval(ctx context.Context) Approval {
	if e.Live != nil {
		return e.Live.Approval(ctx)
	}
	return e.Approval
}

// maxAttempts returns the requeue cap, live when configured.
func (e *Engine) maxAttempts(ctx context.Context) int {
	if e.Live != nil {
		return e.Live.MaxAttempts(ctx)
	}
	return e.MaxAttempts
}

// ErrCycleInProgress is returned by DispatchCycle when a batch is still draining.
var ErrCycleInProgress = errors.New("engine: a test cycle is already in progress")

// Summary reports the outcome of a run.
type Summary struct {
	Tested    int
	Approved  int
	Artifacts map[string][]byte
	ByCountry map[string]int // approved-server count per country (for notifications)
}

// logf logs via the optional engine logger.
func (e *Engine) logf(format string, args ...any) {
	if e.Logf != nil {
		e.Logf(format, args...)
	}
}

// notifyCycle sends the best-effort end-of-cycle notification. Failures are
// logged, never propagated, so a flaky notifier never blocks a publish.
func (e *Engine) notifyCycle(ctx context.Context, sum Summary) {
	n := e.Notifier
	if e.Live != nil {
		// Live config: build a sender from the current settings each cycle, so
		// notification URLs edited in the UI apply without a restart.
		enabled, urls := e.Live.NotifyURLs(ctx)
		if !enabled {
			return
		}
		sn, err := notify.NewShoutrrr(urls)
		if err != nil {
			e.logf("engine: notify: %v", err)
			return
		}
		if sn == nil {
			return // no URLs configured
		}
		n = sn
	}
	if n == nil {
		return
	}
	msg := notify.CycleMessage(e.Brand, sum.Approved, sum.ByCountry)
	if err := n.Notify(ctx, msg); err != nil {
		e.logf("engine: notify: %v", err)
	}
}

// RunOnce processes a batch of already-parsed servers end to end, in-process.
// It is the single-node path (cmd/tester): this process is the only worker.
func (e *Engine) RunOnce(ctx context.Context, servers []model.Server) (Summary, error) {
	var sum Summary

	if err := e.Store.UpsertWorker(ctx, model.Worker{
		ID: e.WorkerID, Status: "running", Capacity: model.Capacity{},
	}); err != nil {
		return sum, fmt.Errorf("engine: register worker: %w", err)
	}

	batchID, err := e.Store.CreateBatch(ctx, "scheduled")
	if err != nil {
		return sum, fmt.Errorf("engine: create batch: %w", err)
	}

	for _, srv := range servers {
		id, err := e.Store.UpsertServer(ctx, srv)
		if err != nil {
			return sum, fmt.Errorf("engine: upsert server: %w", err)
		}
		sum.Tested++

		lat, sp := e.probe(ctx, srv)
		if err := e.recordFunnel(ctx, id, batchID, lat, sp); err != nil {
			return sum, err
		}
		if !lat.Passed {
			continue
		}
		// Assign a stable name during the run so the single-node path keeps its
		// deterministic, server-order naming. The distributed path names at
		// publish time instead (see ensureGeo).
		if err := e.assignGeo(ctx, id, srv.Fingerprint, srv.Host); err != nil {
			return sum, err
		}
	}

	if err := e.Store.FinishBatch(ctx, batchID); err != nil {
		return sum, fmt.Errorf("engine: finish batch: %w", err)
	}

	pub, err := e.PublishFromHistory(ctx)
	if err != nil {
		return sum, err
	}
	sum.Approved = pub.Approved
	sum.Artifacts = pub.Artifacts
	sum.ByCountry = pub.ByCountry
	e.notifyCycle(ctx, pub)
	return sum, nil
}

// DispatchCycle starts a distributed cycle: it opens a batch and enqueues a
// fan-out of funnel jobs for the remote fleet to claim. dispatched is false
// (with no error) when a previous cycle is still draining, so the scheduler can
// poll harmlessly. Fan-out is capped to the live fleet size so the batch can
// always drain even when fewer than N workers are online.
func (e *Engine) DispatchCycle(ctx context.Context, servers []model.Server) (batchID int64, dispatched bool, err error) {
	if _, active, err := e.Store.LatestUnfinishedBatch(ctx); err != nil {
		return 0, false, fmt.Errorf("engine: check active batch: %w", err)
	} else if active {
		return 0, false, nil
	}

	batchID, err = e.Store.CreateBatch(ctx, "scheduled")
	if err != nil {
		return 0, false, fmt.Errorf("engine: create batch: %w", err)
	}
	// Globally disabled protocols are skipped entirely: no jobs are enqueued, so
	// they never get checked (re-enabling includes them on the next cycle).
	enabled, err := e.Store.EnabledProtocols(ctx)
	if err != nil {
		return 0, false, fmt.Errorf("engine: enabled protocols: %w", err)
	}
	allow := protocolAllowed(enabled)
	candidates := make([]model.Server, 0, len(servers))
	for _, srv := range servers {
		if allow(string(srv.Protocol)) {
			candidates = append(candidates, srv)
		}
	}

	// Persist the full catalog so the dashboard reflects every ingested server,
	// independent of how many we test this cycle. Done in bulk (set-based) rather
	// than a round-trip per server, so a 200k-server catalog stays cheap.
	if err := e.Store.BulkUpsertServers(ctx, candidates); err != nil {
		return 0, false, fmt.Errorf("engine: persist catalog: %w", err)
	}

	// Optional shuffle + cap. By default (max_probes 0) the whole catalog is
	// enqueued: enqueuing is bulk and capacity-aware claiming bounds what each
	// worker pulls, so a large queue is fine. A cap, when set, samples a subset
	// per cycle (shuffle rotates which subset across cycles).
	shuffle, maxProbes, err := e.Store.DispatchSettings(ctx)
	if err != nil {
		return 0, false, fmt.Errorf("engine: dispatch settings: %w", err)
	}
	if shuffle {
		rand.Shuffle(len(candidates), func(i, j int) { candidates[i], candidates[j] = candidates[j], candidates[i] })
	}
	sample := candidates
	if maxProbes > 0 && len(sample) > maxProbes {
		sample = sample[:maxProbes]
	}

	fingerprints := make([]string, len(sample))
	for i, srv := range sample {
		fingerprints[i] = srv.Fingerprint
	}
	n := e.fanout(ctx)
	enqueued, err := e.Store.BulkEnqueueFanout(ctx, batchID, fingerprints, model.PhaseFunnel, n)
	if err != nil {
		return 0, false, fmt.Errorf("engine: enqueue fanout: %w", err)
	}
	e.logf("engine: dispatch enqueued %d jobs for %d of %d servers (batch %d, fanout %d)", enqueued, len(sample), len(candidates), batchID, n)
	return batchID, true, nil
}

// protocolAllowed returns a membership predicate for an allow-list; a nil/empty
// list allows everything.
func protocolAllowed(enabled []string) func(string) bool {
	if len(enabled) == 0 {
		return func(string) bool { return true }
	}
	set := make(map[string]bool, len(enabled))
	for _, p := range enabled {
		set[p] = true
	}
	return func(p string) bool { return set[p] }
}

// ReconcileResult reports what one reconcile pass did.
type ReconcileResult struct {
	Requeued  int64
	Failed    int64
	Published bool
	Approved  int
	OpenJobs  int
}

// Reconcile advances the active distributed cycle: it requeues dead-worker jobs
// (and fails ones past MaxAttempts), and when the batch has drained it finishes
// the batch and publishes. It is safe to call on a timer; it is a no-op when no
// cycle is active or the batch is still in flight.
func (e *Engine) Reconcile(ctx context.Context) (ReconcileResult, error) {
	var res ReconcileResult

	requeued, failed, err := e.Store.RequeueExpired(ctx, e.leaseTTL(ctx), e.maxAttempts(ctx))
	if err != nil {
		return res, fmt.Errorf("engine: requeue: %w", err)
	}
	res.Requeued, res.Failed = requeued, failed

	id, active, err := e.Store.LatestUnfinishedBatch(ctx)
	if err != nil {
		return res, fmt.Errorf("engine: active batch: %w", err)
	}
	if !active {
		return res, nil
	}

	open, err := e.Store.OpenJobCount(ctx, &id)
	if err != nil {
		return res, fmt.Errorf("engine: open jobs: %w", err)
	}
	res.OpenJobs = open
	if open > 0 {
		return res, nil // still draining
	}

	if err := e.Store.FinishBatch(ctx, id); err != nil {
		return res, fmt.Errorf("engine: finish batch: %w", err)
	}
	pub, err := e.PublishFromHistory(ctx)
	if err != nil {
		return res, err
	}
	res.Published = true
	res.Approved = pub.Approved
	e.notifyCycle(ctx, pub)
	return res, nil
}

// PublishFromHistory applies the corroboration gate to the stored history and
// publishes the working list. It runs no proxy tests, so changing the gate and
// re-publishing never retests. It names any approved server still lacking a
// stable name (the distributed path defers naming to here).
func (e *Engine) PublishFromHistory(ctx context.Context) (Summary, error) {
	var sum Summary

	// Default to the latest completed batch; nil falls back to rolling history.
	var filter *int64
	if id, ok, err := e.Store.LatestFinishedBatch(ctx); err != nil {
		return sum, fmt.Errorf("engine: latest batch: %w", err)
	} else if ok {
		filter = &id
	}

	ap := e.approval(ctx)
	approved, err := e.Store.ApprovedServers(ctx, filter, ap.MinDlMBps, ap.MaxLatencyMs, ap.required())
	if err != nil {
		return sum, fmt.Errorf("engine: approved servers: %w", err)
	}
	pubServers := make([]output.PublicServer, 0, len(approved))
	for _, a := range approved {
		country, seqName := a.Country, a.SeqName
		if seqName == "" {
			country, seqName, err = e.ensureGeo(ctx, a.ServerID, a.Fingerprint, a.Host)
			if err != nil {
				return sum, err
			}
		}
		checks, err := e.Store.ServerChecks(ctx, a.ServerID)
		if err != nil {
			return sum, fmt.Errorf("engine: server checks: %w", err)
		}
		pubServers = append(pubServers, output.PublicServer{
			RawURI:    a.RawURI,
			Country:   country,
			SeqName:   seqName,
			SpeedMBps: a.MedianDlMbps,
			Tags:      output.MediaTags(country, checks),
		})
	}

	// Output filters (node-prefix, name-regex include/exclude, success-limit) are
	// applied at publish time, so they re-shape the published list without retest.
	of, err := e.Store.OutputSettings(ctx)
	if err != nil {
		return sum, fmt.Errorf("engine: output settings: %w", err)
	}
	pubServers = e.applyOutputFilter(of, pubServers)

	byCountry := make(map[string]int)
	for _, ps := range pubServers {
		byCountry[ps.Country]++
	}
	sum.ByCountry = byCountry
	sum.Approved = len(pubServers)

	files, err := output.BuildArtifacts(pubServers, output.Options{Brand: e.Brand, Prefix: of.NodePrefix})
	if err != nil {
		return sum, err
	}
	sum.Artifacts = files

	// Render and persist the multi-format subscriptions the public /sub endpoint
	// serves. Done before the git push so a render failure surfaces without
	// publishing a stale repo.
	if err := e.persistArtifacts(ctx, pubServers, of.NodePrefix); err != nil {
		return sum, err
	}

	if e.Publisher != nil {
		msg := fmt.Sprintf("publish: %d working servers", len(pubServers))
		if err := e.Publisher.Publish(ctx, files, msg); err != nil {
			return sum, fmt.Errorf("engine: publish: %w", err)
		}
	}
	return sum, nil
}

// applyOutputFilter applies the publish-time output filters: a node-name regex
// include/exclude and a success-limit cap (the list is already sorted best-first
// by speed). An invalid regex is logged and ignored (no filtering on that side).
func (e *Engine) applyOutputFilter(of store.OutputFilter, servers []output.PublicServer) []output.PublicServer {
	inc := e.compileFilter(of.NameInclude)
	exc := e.compileFilter(of.NameExclude)
	out := make([]output.PublicServer, 0, len(servers))
	for _, ps := range servers {
		if inc != nil || exc != nil {
			name := output.NodeName(e.Brand, of.NodePrefix, ps)
			if inc != nil && !inc.MatchString(name) {
				continue
			}
			if exc != nil && exc.MatchString(name) {
				continue
			}
		}
		out = append(out, ps)
	}
	if of.SuccessLimit > 0 && len(out) > of.SuccessLimit {
		out = out[:of.SuccessLimit]
	}
	return out
}

// compileFilter compiles a name-filter pattern, returning nil for an empty or
// invalid pattern (logged), so a bad regex never drops the whole list.
func (e *Engine) compileFilter(pattern string) *regexp.Regexp {
	if pattern == "" {
		return nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		e.logf("engine: invalid name filter %q: %v", pattern, err)
		return nil
	}
	return re
}

// persistArtifacts renders every subscription format from the approved nodes and
// stores them for the public /sub endpoint. Each rendered format derives only
// from the public share URI (re-parsed) and the public node name, so the served
// output carries no inner-working. An approved URI that no longer parses is
// skipped rather than aborting the whole publish.
func (e *Engine) persistArtifacts(ctx context.Context, pubServers []output.PublicServer, prefix string) error {
	nodes := make([]convert.Node, 0, len(pubServers))
	for _, p := range pubServers {
		srv, err := ingest.Parse(p.RawURI)
		if err != nil {
			continue
		}
		nodes = append(nodes, convert.Node{Server: srv, Name: output.NodeName(e.Brand, prefix, p)})
	}
	for _, target := range convert.Targets {
		content, err := convert.Render(target, nodes)
		if err != nil {
			return fmt.Errorf("engine: render %s: %w", target, err)
		}
		if err := e.Store.SavePublishedArtifact(ctx, target, convert.ContentType(target), content, len(nodes)); err != nil {
			return fmt.Errorf("engine: persist %s: %w", target, err)
		}
	}
	return nil
}

// probe starts the proxy and runs latency, then speed only if latency passed.
func (e *Engine) probe(ctx context.Context, srv model.Server) (lat, sp checks.Result) {
	inst, err := e.Prober.Start(ctx, srv)
	if err != nil {
		return checks.Result{Passed: false, Detail: err.Error()}, checks.Result{}
	}
	defer func() { _ = inst.Close() }()

	client, err := e.NewClient(inst.SocksAddress())
	if err != nil {
		return checks.Result{Passed: false, Detail: err.Error()}, checks.Result{}
	}

	lat, err = e.Latency.Run(ctx, client)
	if err != nil {
		return checks.Result{Passed: false, Detail: err.Error()}, checks.Result{}
	}
	if !lat.Passed {
		return lat, checks.Result{}
	}
	sp, err = e.Speed.Run(ctx, client)
	if err != nil {
		sp = checks.Result{Passed: false, Detail: err.Error()}
	}
	return lat, sp
}

// recordFunnel writes one combined measurement row (latency + speed) per server,
// the same shape a remote worker reports, so the corroboration gate treats both
// paths identically.
func (e *Engine) recordFunnel(ctx context.Context, serverID, batchID int64, lat, sp checks.Result) error {
	run := model.TestRun{
		ServerID: serverID,
		WorkerID: e.WorkerID,
		BatchID:  &batchID,
		Phase:    model.PhaseFunnel,
		Status:   model.StatusError,
	}
	if lat.Passed {
		run.Status = model.StatusOK
		run.LatencyMs = lat.LatencyMs
		run.DlMbps = sp.DlMbps
		run.UlMbps = sp.UlMbps
	} else {
		run.Status = model.StatusTimeout
		run.LatencyMs = lat.LatencyMs
		run.Error = lat.Detail
	}
	if _, err := e.Store.InsertTestRun(ctx, run); err != nil {
		return fmt.Errorf("engine: record funnel run: %w", err)
	}
	return nil
}

// assignGeo resolves the country, assigns a stable sequence name, and persists
// both for a server.
func (e *Engine) assignGeo(ctx context.Context, serverID int64, fingerprint, host string) error {
	_, _, err := e.ensureGeo(ctx, serverID, fingerprint, host)
	return err
}

// ensureGeo resolves+assigns+persists the country and sequence name and returns
// them. Unknown countries fall under the "XX" bucket so every node still gets a
// stable name rather than a bare number.
func (e *Engine) ensureGeo(ctx context.Context, serverID int64, fingerprint, host string) (country, seqName string, err error) {
	country = e.country(ctx, host)
	seqCountry := country
	if seqCountry == "" {
		seqCountry = "OT" // unknown-country bucket (matches WhiteDNS "OT" + ❓ flag)
	}
	seqName, err = e.Seq.Assign(ctx, fingerprint, seqCountry)
	if err != nil {
		return "", "", fmt.Errorf("engine: assign seq: %w", err)
	}
	if err := e.Store.SetServerGeo(ctx, serverID, country, seqName); err != nil {
		return "", "", fmt.Errorf("engine: set geo: %w", err)
	}
	return country, seqName, nil
}

// country resolves the server's country, returning "" when unknown.
func (e *Engine) country(ctx context.Context, host string) string {
	if e.Resolver == nil {
		return ""
	}
	c, err := naming.ResolveCountry(ctx, e.Resolver, host)
	if err != nil {
		return ""
	}
	return c
}

// fanout is the per-config worker count, capped to the live fleet so the batch
// can always drain. With no workers yet it stays at 1 (the first worker drains).
func (e *Engine) fanout(ctx context.Context) int {
	n := e.Fanout
	if e.Live != nil {
		n = e.Live.Fanout(ctx)
	}
	if n < 1 {
		n = 1
	}
	alive, err := e.Store.AliveWorkers(ctx, e.aliveWindow())
	if err != nil || alive < 1 {
		alive = 1
	}
	if alive < n {
		n = alive
	}
	return n
}

func (e *Engine) leaseTTL(ctx context.Context) time.Duration {
	d := e.LeaseTTL
	if e.Live != nil {
		d = e.Live.LeaseTTL(ctx)
	}
	if d <= 0 {
		return 2 * time.Minute
	}
	return d
}

func (e *Engine) aliveWindow() time.Duration {
	if e.AliveWindow <= 0 {
		return 60 * time.Second
	}
	return e.AliveWindow
}
