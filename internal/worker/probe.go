package worker

import (
	"context"
	"net/http"
	"time"

	"github.com/whitedns/vless-tester/internal/checks"
	"github.com/whitedns/vless-tester/internal/ingest"
	"github.com/whitedns/vless-tester/internal/mcore"
	"github.com/whitedns/vless-tester/internal/model"
)

// ProbeRunner is the production Runner: it parses the share link, starts an
// in-process mihomo proxy for the server, and runs the funnel appropriate to the
// job's phase. It reports only raw measurements; the coordinator decides what
// they mean.
type ProbeRunner struct {
	// HandshakeTimeout bounds the TLS handshake on each proxied connection.
	HandshakeTimeout time.Duration
	Latency          checks.LatencyCheck
	Speed            checks.SpeedCheck
	// SpeedGate bounds how many speed legs run at once across all concurrent
	// funnel jobs, so latency probes fan out wide while bandwidth-sensitive speed
	// tests stay limited (DESIGN 4). nil means no extra gating.
	SpeedGate *checks.Semaphore
	// MediaTimeout bounds each media-unlock probe. Zero uses the check default.
	MediaTimeout time.Duration
	// IPRiskURL overrides the IP-risk reputation provider; empty uses the default.
	IPRiskURL string
}

// Run measures one job. Latency always runs (it is the cheap gate); speed runs
// only for speed/checks phases and only if latency passed.
func (p ProbeRunner) Run(ctx context.Context, job Job) Result {
	srv, err := ingest.Parse(job.RawURI)
	if err != nil {
		return fail(err)
	}

	inst, err := mcore.Start(ctx, srv, p.HandshakeTimeout)
	if err != nil {
		return fail(err)
	}
	defer func() { _ = inst.Close() }()

	client := inst.Client()

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

	// DNS-leak is informational and outside the configurable pipeline: run it once
	// the proxy is up, when the coordinator asked for it.
	if job.DNSLeak {
		if c, ok := p.runDNSLeak(ctx, client); ok {
			res.Checks = append(res.Checks, c)
		}
	}

	// Run the configurable funnel pipeline in order. Latency already ran above as
	// the connectivity gate; the remaining stages (media, ip_risk, speed) and
	// their gates come from the coordinator (default order when unset). A gated
	// stage that does not pass stops the funnel for this node.
	stages := job.Stages
	if len(stages) == 0 {
		stages = defaultStages
	}
	for _, st := range stages {
		switch st.Check {
		case "media":
			res.Checks = append(res.Checks, p.runMedia(ctx, client, job.Checks)...)
			if st.Gate && !passesRequire(res.Checks, job.Require) {
				res.Error = "skipped: media gate not passed"
				return res
			}
		case "ip_risk":
			if !job.IPRisk {
				continue
			}
			c, ok := p.runIPRisk(ctx, client, job.IPRiskURL)
			if !ok {
				continue
			}
			res.Checks = append(res.Checks, c)
			if st.Gate && !c.Passed {
				res.Error = "skipped: ip_risk gate not passed"
				return res
			}
		case "navigation":
			if !job.Navigation {
				continue
			}
			c := p.runNavigation(ctx, client, job.NavigationURL)
			res.Checks = append(res.Checks, c)
			if st.Gate && !c.Passed {
				res.Error = "skipped: navigation gate not passed"
				return res
			}
		case "speed":
			sp, ran := p.runSpeed(ctx, client, &res, job.Speed)
			if !ran {
				return res // ctx canceled while waiting for the speed slot
			}
			if st.Gate && !sp.Passed {
				res.Error = "skipped: speed gate not passed"
				return res
			}
		}
	}
	return res
}

// defaultStages is the built-in funnel order used when the coordinator does not
// push a pipeline (older coordinator / unset setting). media and navigation gate
// (navigation is the real-browse gate, on by default and honoring job.Navigation
// as its kill-switch); ip_risk and speed do not.
var defaultStages = []model.FunnelStage{
	{Check: "media", Gate: true},
	{Check: "navigation", Gate: true},
	{Check: "ip_risk", Gate: false},
	{Check: "speed", Gate: false},
}

// runSpeed runs the bandwidth-bounded speed leg, recording dl/ul on res. ran is
// false only when the context was canceled while waiting for a speed slot (so
// the caller keeps prior results); otherwise it ran (sp.Passed reports outcome).
func (p ProbeRunner) runSpeed(ctx context.Context, client *http.Client, res *Result, spec *model.SpeedSpec) (checks.Result, bool) {
	if p.SpeedGate != nil {
		if err := p.SpeedGate.Acquire(ctx); err != nil {
			return checks.Result{}, false
		}
		defer p.SpeedGate.Release()
	}
	// Merge the coordinator-pushed speed config over the worker default, and
	// bound the leg by its timeout when set.
	check := p.Speed
	if spec != nil {
		cfg := p.Speed.Config
		if spec.DownloadURL != "" {
			cfg.DownloadURL = spec.DownloadURL
		}
		if spec.UploadURL != "" {
			cfg.UploadURL = spec.UploadURL
		}
		if spec.Streams > 0 {
			cfg.Streams = spec.Streams
		}
		if spec.Bytes > 0 {
			cfg.Bytes = spec.Bytes
		}
		if spec.Adaptive != nil {
			cfg.Adaptive = *spec.Adaptive
		}
		check = checks.SpeedCheck{Config: cfg}
		if spec.TimeoutMs > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, time.Duration(spec.TimeoutMs)*time.Millisecond)
			defer cancel()
		}
	}
	sp, err := check.Run(ctx, client)
	if err != nil {
		res.Error = err.Error()
		return checks.Result{}, true
	}
	res.DlMbps = sp.DlMbps
	res.UlMbps = sp.UlMbps
	if !sp.Passed && res.Error == "" {
		res.Error = sp.Detail
	}
	return sp, true
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

// runIPRisk scores the exit IP's reputation through the proxy. It returns a
// CheckOutcome (name "ip_risk", passed = low risk, metric = 0-100 score) only
// when the lookup succeeded; a failed lookup is dropped so it never records a
// misleading clean score.
func (p ProbeRunner) runIPRisk(ctx context.Context, client *http.Client, url string) (model.CheckOutcome, bool) {
	if url == "" {
		url = p.IPRiskURL
	}
	rr, err := checks.IPRiskCheck{URL: url, Timeout: p.MediaTimeout}.Run(ctx, client)
	if err != nil || !rr.OK {
		return model.CheckOutcome{}, false
	}
	score := rr.Score
	return model.CheckOutcome{
		Name:   "ip_risk",
		Passed: !rr.Risky,
		Detail: rr.Detail,
		Metric: &score,
	}, true
}

// runNavigation fetches a real web page through the proxy and reports whether
// the node can actually browse (2xx + non-trivial body) — the gate that catches
// exits which pass the cheap 204 latency check but cannot carry general traffic.
func (p ProbeRunner) runNavigation(ctx context.Context, client *http.Client, url string) model.CheckOutcome {
	r, err := checks.NavigationCheck{URL: url, Timeout: p.MediaTimeout}.Run(ctx, client)
	detail := r.Detail
	if err != nil {
		detail = err.Error()
	}
	return model.CheckOutcome{Name: "navigation", Passed: r.Passed, Detail: detail}
}

// runDNSLeak checks whether DNS escapes the tunnel (resolver country != exit
// country). Informational: recorded only when the lookup completed.
func (p ProbeRunner) runDNSLeak(ctx context.Context, client *http.Client) (model.CheckOutcome, bool) {
	r, err := checks.DNSLeakCheck{Timeout: p.MediaTimeout}.Run(ctx, client)
	if err != nil || !r.OK {
		return model.CheckOutcome{}, false
	}
	return model.CheckOutcome{Name: "dns_leak", Passed: !r.Leak, Detail: r.Detail}, true
}

func fail(err error) Result {
	return Result{Status: string(model.StatusError), Error: err.Error()}
}
