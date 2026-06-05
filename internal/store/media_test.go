package store_test

import (
	"context"
	"testing"

	"github.com/whitedns/vless-tester/internal/model"
)

func TestMediaChecksSetting(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// Disabled -> nil (settings persist across tests, so set it explicitly).
	if err := st.SetSetting(ctx, "media.enabled", false); err != nil {
		t.Fatal(err)
	}
	got, err := st.MediaChecks(ctx)
	if err != nil {
		t.Fatalf("disabled: %v", err)
	}
	if got != nil {
		t.Fatalf("disabled media platforms = %v, want nil", got)
	}

	// Enabling returns the configured list.
	if err := st.SetSetting(ctx, "media.enabled", true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, "media.platforms", []string{"openai", "spotify"}); err != nil {
		t.Fatal(err)
	}
	got, err = st.MediaChecks(ctx)
	if err != nil {
		t.Fatalf("enabled: %v", err)
	}
	if len(got) != 2 || got[0] != "openai" || got[1] != "spotify" {
		t.Fatalf("platforms = %v, want [openai spotify]", got)
	}
}

func TestMediaRequireSetting(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// Missing/empty -> nil (no gating). The seed default is [].
	if got, err := st.MediaRequire(ctx); err != nil || got != nil {
		t.Fatalf("default: got %v err %v, want nil", got, err)
	}
	if err := st.SetSetting(ctx, "media.require", []string{"openai"}); err != nil {
		t.Fatal(err)
	}
	got, err := st.MediaRequire(ctx)
	if err != nil || len(got) != 1 || got[0] != "openai" {
		t.Fatalf("set: got %v err %v", got, err)
	}
}

func TestIPRiskEnabledSetting(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// Seed default is false (also reset by newTestStore).
	if got, err := st.IPRiskEnabled(ctx); err != nil || got {
		t.Fatalf("default: got %v err %v, want false", got, err)
	}
	if err := st.SetSetting(ctx, "iprisk.enabled", true); err != nil {
		t.Fatal(err)
	}
	if got, err := st.IPRiskEnabled(ctx); err != nil || !got {
		t.Fatalf("enabled: got %v err %v, want true", got, err)
	}
}

func TestRecordResultStoresCheckMetric(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	mustWorker(t, st, "w1")
	srvID, _ := st.UpsertServer(ctx, sampleServer(1))
	if _, err := st.EnqueueJob(ctx, srvID, model.PhaseChecks); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	claimed, err := st.ClaimJobs(ctx, "w1", model.PhaseChecks, 1, nil)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim got %d err=%v", len(claimed), err)
	}

	lat := 30
	score := 75.0
	ok, err := st.RecordResult(ctx, "w1", claimed[0].JobID, model.TestRun{
		Status:    model.StatusOK,
		LatencyMs: &lat,
		Checks: []model.CheckOutcome{
			{Name: "ip_risk", Passed: false, Detail: "proxy,hosting (NL)", Metric: &score},
		},
	})
	if err != nil || !ok {
		t.Fatalf("record: ok=%v err=%v", ok, err)
	}

	// ServerChecks surfaces the metric so the UI/filters can read the risk score.
	checks, err := st.ServerChecks(ctx, srvID)
	if err != nil {
		t.Fatalf("server checks: %v", err)
	}
	if len(checks) != 1 {
		t.Fatalf("checks = %d, want 1", len(checks))
	}
	c := checks[0]
	if c.Name != "ip_risk" || c.Metric == nil || *c.Metric != 75 || c.Detail != "proxy,hosting (NL)" {
		t.Fatalf("ip_risk check = %+v (metric %v)", c, c.Metric)
	}
}

func TestRecordResultStoresChecks(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	mustWorker(t, st, "w1")
	srvID, _ := st.UpsertServer(ctx, sampleServer(1))
	if _, err := st.EnqueueJob(ctx, srvID, model.PhaseChecks); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	claimed, err := st.ClaimJobs(ctx, "w1", model.PhaseChecks, 1, nil)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim got %d err=%v", len(claimed), err)
	}

	lat := 30
	ok, err := st.RecordResult(ctx, "w1", claimed[0].JobID, model.TestRun{
		Status:    model.StatusOK,
		LatencyMs: &lat,
		Checks: []model.CheckOutcome{
			{Name: "openai", Passed: true, Detail: "US"},
			{Name: "netflix", Passed: false, Detail: "blocked"},
		},
	})
	if err != nil || !ok {
		t.Fatalf("record: ok=%v err=%v", ok, err)
	}

	// Both checks are persisted and linked to the run's server.
	var n int
	if err := st.Pool().QueryRow(ctx,
		`SELECT count(*) FROM checks WHERE server_id = $1`, srvID).Scan(&n); err != nil {
		t.Fatalf("count checks: %v", err)
	}
	if n != 2 {
		t.Fatalf("stored %d checks, want 2", n)
	}
	var passed bool
	var detail string
	if err := st.Pool().QueryRow(ctx,
		`SELECT passed, detail FROM checks WHERE server_id = $1 AND name = 'openai'`, srvID).
		Scan(&passed, &detail); err != nil {
		t.Fatalf("read openai check: %v", err)
	}
	if !passed || detail != "US" {
		t.Fatalf("openai check = {%v %q}, want {true US}", passed, detail)
	}
}
