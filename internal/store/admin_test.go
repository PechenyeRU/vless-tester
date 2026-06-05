package store_test

import (
	"context"
	"testing"

	"github.com/whitedns/vless-tester/internal/model"
	"github.com/whitedns/vless-tester/internal/store"
)

// seedRun inserts one ok measurement, wiring the worker, server geo, and run in
// one call so the admin queries have something to aggregate.
func seedRun(t *testing.T, st *store.Store, worker string, srvID int64, lat int, dl float64) {
	t.Helper()
	ctx := context.Background()
	if _, err := st.InsertTestRun(ctx, model.TestRun{
		ServerID: srvID, WorkerID: worker, Phase: model.PhaseFunnel,
		LatencyMs: &lat, DlMbps: &dl, Status: model.StatusOK,
	}); err != nil {
		t.Fatalf("insert run: %v", err)
	}
}

func TestListServersFilterAndLatest(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	mustWorker(t, st, "w1")
	mustWorker(t, st, "w2")

	frID, _ := st.UpsertServer(ctx, sampleServer(1))
	chID, _ := st.UpsertServer(ctx, sampleServer(2))
	_ = st.SetServerGeo(ctx, frID, "FR", "FR1")
	_ = st.SetServerGeo(ctx, chID, "CH", "CH1")

	// Two runs for the FR server; the later (faster) one must win as "latest".
	seedRun(t, st, "w1", frID, 100, 5.0)
	seedRun(t, st, "w1", frID, 90, 20.0)
	seedRun(t, st, "w2", chID, 50, 2.0)

	all, err := st.ListServers(ctx, store.ServerFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("want 2 servers, got %d", len(all))
	}
	// Ordered fastest-first: FR (20) before CH (2).
	if all[0].ID != frID || all[0].DlMbps == nil || *all[0].DlMbps != 20.0 {
		t.Fatalf("latest/ordering wrong: %+v", all[0])
	}

	// Country filter.
	ch, _ := st.ListServers(ctx, store.ServerFilter{Country: "CH"})
	if len(ch) != 1 || ch[0].ID != chID {
		t.Fatalf("country filter: %+v", ch)
	}

	// Min-speed filter drops the slow CH server.
	fast, _ := st.ListServers(ctx, store.ServerFilter{MinSpeed: 10})
	if len(fast) != 1 || fast[0].ID != frID {
		t.Fatalf("min-speed filter: %+v", fast)
	}

	// Worker filter scopes "latest" to that vantage; w2 never tested FR.
	byW2, _ := st.ListServers(ctx, store.ServerFilter{Worker: "w2"})
	for _, s := range byW2 {
		if s.ID == frID && s.DlMbps != nil {
			t.Fatalf("w2 should have no FR measurement: %+v", s)
		}
	}
}

func TestServerHistoryNewestFirst(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	mustWorker(t, st, "w1")
	srvID, _ := st.UpsertServer(ctx, sampleServer(1))

	seedRun(t, st, "w1", srvID, 100, 5.0)
	seedRun(t, st, "w1", srvID, 80, 15.0)

	hist, err := st.ServerHistory(ctx, srvID, 0)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(hist) != 2 {
		t.Fatalf("want 2 runs, got %d", len(hist))
	}
	if hist[0].DlMbps == nil || *hist[0].DlMbps != 15.0 {
		t.Fatalf("newest-first wrong: %+v", hist[0])
	}
}

func TestStatsAggregates(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	mustWorker(t, st, "w1")

	frID, _ := st.UpsertServer(ctx, sampleServer(1))
	chID, _ := st.UpsertServer(ctx, sampleServer(2))
	_ = st.SetServerGeo(ctx, frID, "FR", "FR1")
	_ = st.SetServerGeo(ctx, chID, "CH", "CH1")
	seedRun(t, st, "w1", frID, 90, 20.0)
	// CH stays untested.

	stats, err := st.Stats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Servers != 2 || stats.Runs != 1 {
		t.Fatalf("totals: servers=%d runs=%d", stats.Servers, stats.Runs)
	}
	var fr *store.CountryStat
	for i := range stats.ByCountry {
		if stats.ByCountry[i].Country == "FR" {
			fr = &stats.ByCountry[i]
		}
	}
	if fr == nil || fr.Tested != 1 || fr.MedianDl == nil || *fr.MedianDl != 20.0 {
		t.Fatalf("FR country stat wrong: %+v", fr)
	}
	if len(stats.ByWorker) != 1 || stats.ByWorker[0].WorkerID != "w1" || stats.ByWorker[0].OK != 1 {
		t.Fatalf("worker stats wrong: %+v", stats.ByWorker)
	}
}

func TestSourceEnableToggleAndListAll(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	if err := st.UpsertSource(ctx, model.SourceSubscriptionURL, "https://x/sub"); err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	all, _ := st.ListAllSources(ctx)
	if len(all) != 1 {
		t.Fatalf("want 1 source, got %d", len(all))
	}
	id := all[0].ID
	if err := st.SetSourceEnabled(ctx, id, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	// ListSources (ingest path) hides it; ListAllSources (admin) still shows it.
	if active, _ := st.ListSources(ctx); len(active) != 0 {
		t.Fatalf("disabled source should be hidden from ingest: %+v", active)
	}
	all, _ = st.ListAllSources(ctx)
	if len(all) != 1 || all[0].Enabled {
		t.Fatalf("admin list should show disabled source: %+v", all)
	}
}
