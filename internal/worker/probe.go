package worker

import (
	"context"
	"net/http"

	"github.com/whitedns/vless-tester/internal/checks"
	"github.com/whitedns/vless-tester/internal/core"
	"github.com/whitedns/vless-tester/internal/ingest"
	"github.com/whitedns/vless-tester/internal/model"
)

// ProbeRunner is the production Runner: it parses the share link, starts a local
// sing-box for the server, and runs the funnel appropriate to the job's phase.
// It reports only raw measurements; the coordinator decides what they mean.
type ProbeRunner struct {
	Options   core.Options
	Latency   checks.LatencyCheck
	Speed     checks.SpeedCheck
	NewClient func(socksAddr string) (*http.Client, error)
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

func fail(err error) Result {
	return Result{Status: string(model.StatusError), Error: err.Error()}
}
