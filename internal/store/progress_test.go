package store_test

import (
	"context"
	"testing"
)

func TestCycleProgress(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// Idle when there is no unfinished batch.
	if cp, err := st.CycleProgress(ctx); err != nil || cp.Active {
		t.Fatalf("idle: active=%v err=%v", cp.Active, err)
	}

	batch, err := st.CreateBatch(ctx, "scheduled")
	if err != nil {
		t.Fatalf("create batch: %v", err)
	}
	srv, err := st.UpsertServer(ctx, sampleServer(1))
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Insert jobs in mixed states (distinct slots to satisfy the open-job index).
	for i, state := range []string{"done", "done", "failed", "queued", "queued"} {
		if _, err := st.Pool().Exec(ctx,
			`INSERT INTO jobs (server_id, phase, state, slot, batch_id) VALUES ($1, 'funnel', $2, $3, $4)`,
			srv, state, i, batch,
		); err != nil {
			t.Fatalf("insert job: %v", err)
		}
	}

	cp, err := st.CycleProgress(ctx)
	if err != nil {
		t.Fatalf("progress: %v", err)
	}
	if !cp.Active || cp.BatchID != batch {
		t.Fatalf("active=%v batch=%d, want active batch %d", cp.Active, cp.BatchID, batch)
	}
	if cp.Total != 5 || cp.Done != 2 || cp.Failed != 1 || cp.Open != 2 {
		t.Fatalf("counts: total=%d done=%d failed=%d open=%d", cp.Total, cp.Done, cp.Failed, cp.Open)
	}
	if cp.StartedAt.IsZero() {
		t.Error("started_at not set")
	}

	// Finishing the batch returns to idle.
	if err := st.FinishBatch(ctx, batch); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if cp, _ := st.CycleProgress(ctx); cp.Active {
		t.Fatalf("after finish: still active")
	}
}
