package store_test

import (
	"context"
	"testing"

	"github.com/whitedns/vless-tester/internal/model"
)

func TestEnabledProtocolsSetting(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	// Settings persist across tests; leave "all" so other suites stay clean.
	t.Cleanup(func() { _ = st.SetSetting(ctx, "protocols.enabled", []string{}) })

	// Empty/all -> nil (no restriction).
	if err := st.SetSetting(ctx, "protocols.enabled", []string{}); err != nil {
		t.Fatal(err)
	}
	if got, err := st.EnabledProtocols(ctx); err != nil || got != nil {
		t.Fatalf("empty: got %v err %v, want nil", got, err)
	}

	if err := st.SetSetting(ctx, "protocols.enabled", []string{"vless", "trojan"}); err != nil {
		t.Fatal(err)
	}
	got, err := st.EnabledProtocols(ctx)
	if err != nil || len(got) != 2 || got[0] != "vless" {
		t.Fatalf("set: got %v err %v", got, err)
	}
}

func TestClaimJobsProtocolFilter(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	mustWorker(t, st, "w-tcp")
	mustWorker(t, st, "w-any")

	vlessID, _ := st.UpsertServer(ctx, sampleServer(1)) // vless by default
	trojan := sampleServer(2)
	trojan.Protocol = model.ProtocolTrojan
	trojanID, _ := st.UpsertServer(ctx, trojan)
	if _, err := st.EnqueueJob(ctx, vlessID, model.PhaseFunnel); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnqueueJob(ctx, trojanID, model.PhaseFunnel); err != nil {
		t.Fatal(err)
	}

	// A worker allowed only vless claims just the vless job, never the trojan one.
	claimed, err := st.ClaimJobs(ctx, "w-tcp", model.PhaseFunnel, 10, []string{"vless"})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 1 || claimed[0].Protocol != model.ProtocolVLESS {
		t.Fatalf("restricted claim = %+v, want one vless job", claimed)
	}

	// An unrestricted worker can still pick up the remaining trojan job.
	rest, err := st.ClaimJobs(ctx, "w-any", model.PhaseFunnel, 10, nil)
	if err != nil {
		t.Fatalf("claim rest: %v", err)
	}
	if len(rest) != 1 || rest[0].Protocol != model.ProtocolTrojan {
		t.Fatalf("unrestricted claim = %+v, want the trojan job", rest)
	}
}
