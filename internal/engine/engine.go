package engine

import (
	"context"
	"fmt"
	"net/http"

	"github.com/whitedns/vless-tester/internal/checks"
	"github.com/whitedns/vless-tester/internal/model"
	"github.com/whitedns/vless-tester/internal/naming"
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

// Approval decides whether a measured server is published.
type Approval struct {
	MaxLatencyMs int
	MinDlMBps    float64
}

func (a Approval) approved(latencyMs *int, dlMBps *float64) bool {
	if latencyMs == nil || *latencyMs > a.MaxLatencyMs {
		return false
	}
	if dlMBps == nil || *dlMBps < a.MinDlMBps {
		return false
	}
	return true
}

// Engine orchestrates the single-node pipeline: for each server it starts a
// proxy, runs the funnel (latency then speed), records results, assigns a stable
// name, and finally publishes the approved set. It depends only on interfaces,
// so the composition root wires the real core/SOCKS implementations while tests
// inject stubs.
type Engine struct {
	Store     *store.Store
	Prober    Prober
	NewClient ClientFactory
	Latency   checks.LatencyCheck
	Speed     checks.SpeedCheck
	Resolver  naming.CountryResolver // optional; nil means "unknown country"
	Seq       naming.Allocator
	Publisher output.Publisher // optional; nil skips publishing
	Brand     string
	WorkerID  string
	Approval  Approval
}

// Summary reports the outcome of a run.
type Summary struct {
	Tested    int
	Approved  int
	Artifacts map[string][]byte
}

// RunOnce processes a batch of already-parsed servers end to end.
func (e *Engine) RunOnce(ctx context.Context, servers []model.Server) (Summary, error) {
	var sum Summary

	if err := e.Store.UpsertWorker(ctx, model.Worker{
		ID: e.WorkerID, Status: "running", Capacity: model.Capacity{},
	}); err != nil {
		return sum, fmt.Errorf("engine: register worker: %w", err)
	}

	var approved []output.PublicServer
	for _, srv := range servers {
		id, err := e.Store.UpsertServer(ctx, srv)
		if err != nil {
			return sum, fmt.Errorf("engine: upsert server: %w", err)
		}
		sum.Tested++

		lat, sp := e.probe(ctx, srv)
		if err := e.recordRun(ctx, id, model.PhaseLatency, lat); err != nil {
			return sum, err
		}
		if !lat.Passed {
			continue
		}
		if err := e.recordRun(ctx, id, model.PhaseSpeed, sp); err != nil {
			return sum, err
		}

		country := e.country(ctx, srv.Host)
		// Unknown country (e.g. CDN/anycast IPs) still gets a stable name under
		// the "XX" bucket instead of a bare number.
		seqCountry := country
		if seqCountry == "" {
			seqCountry = "XX"
		}
		seqName, err := e.Seq.Assign(ctx, srv.Fingerprint, seqCountry)
		if err != nil {
			return sum, fmt.Errorf("engine: assign seq: %w", err)
		}
		if err := e.Store.SetServerGeo(ctx, id, country, seqName); err != nil {
			return sum, fmt.Errorf("engine: set geo: %w", err)
		}

		if e.Approval.approved(lat.LatencyMs, sp.DlMbps) {
			approved = append(approved, output.PublicServer{
				RawURI:    srv.RawURI,
				Country:   country,
				SeqName:   seqName,
				SpeedMBps: deref(sp.DlMbps),
			})
			sum.Approved++
		}
	}

	files, err := output.BuildArtifacts(approved, output.Options{Brand: e.Brand})
	if err != nil {
		return sum, err
	}
	sum.Artifacts = files

	if e.Publisher != nil {
		msg := fmt.Sprintf("publish: %d working servers", len(approved))
		if err := e.Publisher.Publish(ctx, files, msg); err != nil {
			return sum, fmt.Errorf("engine: publish: %w", err)
		}
	}
	return sum, nil
}

// probe starts the proxy and runs latency, then speed only if latency passed.
// Failures are folded into the returned Results so the caller can record them.
func (e *Engine) probe(ctx context.Context, srv model.Server) (lat, sp checks.Result) {
	inst, err := e.Prober.Start(ctx, srv)
	if err != nil {
		return checks.Result{Passed: false, Detail: err.Error()}, checks.Result{}
	}
	defer inst.Close()

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

func (e *Engine) recordRun(ctx context.Context, serverID int64, phase model.JobPhase, res checks.Result) error {
	status := model.StatusError
	if res.Passed {
		status = model.StatusOK
	}
	_, err := e.Store.InsertTestRun(ctx, model.TestRun{
		ServerID:  serverID,
		WorkerID:  e.WorkerID,
		Phase:     phase,
		LatencyMs: res.LatencyMs,
		DlMbps:    res.DlMbps,
		UlMbps:    res.UlMbps,
		Status:    status,
		Error:     res.Detail,
	})
	if err != nil {
		return fmt.Errorf("engine: record %s run: %w", phase, err)
	}
	return nil
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

func deref(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}
