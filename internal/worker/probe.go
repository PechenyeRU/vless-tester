package worker

import (
	"context"
	"net/http"
	"time"

	"github.com/whitedns/vless-tester/internal/checks"
	"github.com/whitedns/vless-tester/internal/core"
	"github.com/whitedns/vless-tester/internal/ingest"
	"github.com/whitedns/vless-tester/internal/model"
)

// ProbeRunner is the production Runner: it parses the share link, starts a local
// sing-box for the server, and runs the funnel appropriate to the job's phase.
// It reports only raw measurements; the coordinator decides what they mean.
type ProbeRunner struct {
	Options core.Options
	Latency checks.LatencyCheck
	Speed   checks.SpeedCheck
	// SpeedGate bounds how many speed legs run at once across all concurrent
	// funnel jobs, so latency probes fan out wide while bandwidth-sensitive speed
	// tests stay limited (DESIGN 4). nil means no extra gating.
	SpeedGate *checks.Semaphore
	// MediaTimeout bounds each media-unlock probe. Zero uses the check default.
	MediaTimeout time.Duration
	NewClient    func(socksAddr string) (*http.Client, error)
}

// Run measures one job. Latency always runs (it is the cheap gate); speed runs
// only for speed/checks phases and only if latency passed.
func (p ProbeRunner) Run(ctx context.Context, job Job) Result {
	srv, err := ingest.Parse(job.RawURI)
	if err != nil {
		return fail(err)
	}

	inst, err := core.Start(ctx, srv, p.Options)
	if err != nil {
		return fail(err)
	}
	defer inst.Close()

	client, err := p.NewClient(inst.SocksAddress())
	if err != nil {
		return fail(err)
	}

	lat, err := p.Latency.Run(ctx, client)
	if err != nil {
		return fail(err)
	}
	res := Result{Status: string(model.StatusOK), LatencyMs: lat.LatencyMs}
	if !lat.Passed {
		res.Status = string(model.StatusTimeout)
		res.Error = lat.Detail
		return res
	}

	if model.JobPhase(job.Phase) == model.PhaseLatency {
		return res
	}

	// Media-unlock probes run through the same proxy once the server is alive,
	// before the expensive speed test so a node that fails the media filter never
	// reaches it (latency -> media -> speed).
	res.Checks = p.runMedia(ctx, client, job.Checks)

	// Media gate: if the coordinator requires certain unlocks and this node does
	// not provide them, skip the speed test to save the bandwidth-heavy leg.
	if !passesRequire(res.Checks, job.Require) {
		res.Error = "skipped speed: required media not unlocked"
		return res
	}

	// Bandwidth-sensitive: only a bounded number of speed legs run at once.
	if p.SpeedGate != nil {
		if err := p.SpeedGate.Acquire(ctx); err != nil {
			return res // ctx cancelled; keep what we have so far
		}
		defer p.SpeedGate.Release()
	}

	sp, err := p.Speed.Run(ctx, client)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.DlMbps = sp.DlMbps
	res.UlMbps = sp.UlMbps
	if !sp.Passed && res.Error == "" {
		res.Error = sp.Detail
	}
	return res
}

// passesRequire reports whether every required platform unlocked. An empty
// requirement always passes (no media gating).
func passesRequire(checks []model.CheckOutcome, require []string) bool {
	if len(require) == 0 {
		return true
	}
	unlocked := make(map[string]bool, len(checks))
	for _, c := range checks {
		if c.Passed {
			unlocked[c.Name] = true
		}
	}
	for _, name := range require {
		if !unlocked[name] {
			return false
		}
	}
	return true
}

// runMedia probes each requested platform through the proxy and returns the
// outcomes. Probe errors are non-fatal: a failing probe is reported as not
// unlocked rather than failing the whole job.
func (p ProbeRunner) runMedia(ctx context.Context, client *http.Client, platforms []string) []model.CheckOutcome {
	if len(platforms) == 0 {
		return nil
	}
	media := checks.NewMediaChecks(platforms, p.MediaTimeout)
	out := make([]model.CheckOutcome, 0, len(media))
	for _, m := range media {
		r, err := m.Run(ctx, client)
		detail := r.Detail
		if err != nil {
			detail = err.Error()
		}
		out = append(out, model.CheckOutcome{Name: m.Platform, Passed: r.Passed, Detail: detail})
	}
	return out
}

func fail(err error) Result {
	return Result{Status: string(model.StatusError), Error: err.Error()}
}
