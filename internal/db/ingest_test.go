package db

import (
	"context"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
)

// TestSaveHeartbeatsAtomicity is the acceptance test for gaka-4sq: an ingest
// batch that fails partway through must leave NO trace behind. Before the
// transactional refactor the earlier rows in the batch were already committed by
// the time the failing row surfaced, and gap_seconds + hb_rollup_daily either
// never ran or ran against a half-populated table.
func TestSaveHeartbeatsAtomicity(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	f := newSender(t, d, "atomic")
	sender := f.Sender()

	// Baseline: pre-existing rows we can diff against.
	baselineHBs := scalarCount(t, d, ctx, `SELECT count(*) FROM heartbeats WHERE sender=$1`, sender)
	baselineProjects := scalarCount(t, d, ctx, `SELECT count(*) FROM projects WHERE owner=$1`, sender)
	baselineRollup := scalarCount(t, d, ctx, `SELECT count(*) FROM hb_rollup_daily WHERE sender=$1`, sender)

	// A valid heartbeat + one that will fail FK (unknown sender = no users row).
	base := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	proj := "atomic_proj_new"
	good := model.HeartbeatPayload{
		Sender: &sender, Project: &proj,
		Entity: "a.go", Type: model.FileType, TimeSent: float64(base.Unix()),
	}
	unknownSender := "no_such_user_" + sender
	bad := good
	bad.Sender = &unknownSender
	bad.TimeSent = float64(base.Add(time.Minute).Unix())

	if _, err := d.SaveHeartbeats(ctx, []model.HeartbeatPayload{good, bad}); err == nil {
		t.Fatal("expected FK error on batch with unknown sender, got nil")
	}

	// Every table the ingest touches must look exactly like it did before.
	if got := scalarCount(t, d, ctx, `SELECT count(*) FROM heartbeats WHERE sender=$1`, sender); got != baselineHBs {
		t.Fatalf("heartbeats leaked from failed batch: baseline=%d after=%d", baselineHBs, got)
	}
	if got := scalarCount(t, d, ctx, `SELECT count(*) FROM projects WHERE owner=$1`, sender); got != baselineProjects {
		t.Fatalf("projects leaked from failed batch: baseline=%d after=%d", baselineProjects, got)
	}
	if got := scalarCount(t, d, ctx, `SELECT count(*) FROM hb_rollup_daily WHERE sender=$1`, sender); got != baselineRollup {
		t.Fatalf("rollup leaked from failed batch: baseline=%d after=%d", baselineRollup, got)
	}

	// A follow-up clean batch must still succeed against the same fixture — proves
	// the failed tx didn't leave the pool in a bad state.
	ids, err := d.SaveHeartbeats(ctx, []model.HeartbeatPayload{good})
	if err != nil {
		t.Fatalf("follow-up SaveHeartbeats: %v", err)
	}
	if len(ids) != 1 || ids[0] == 0 {
		t.Fatalf("expected 1 non-zero id from follow-up batch, got %v", ids)
	}
}

// TestSaveHeartbeatsBatchOrder proves that ids are returned in input order (the
// heartbeat handler builds its response envelope positionally). pgx.Batch
// consumes results in enqueue order, so this is a regression gate for that
// contract.
func TestSaveHeartbeatsBatchOrder(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	ctx := context.Background()

	f := newSender(t, d, "order")
	sender := f.Sender()

	base := time.Now().UTC().Truncate(time.Second).Add(-2 * time.Hour)
	const n = 25
	batch := make([]model.HeartbeatPayload, n)
	for i := 0; i < n; i++ {
		p := "order_p_" + string(rune('a'+i%20))
		entity := "f" + string(rune('a'+i%20)) + ".go"
		batch[i] = model.HeartbeatPayload{
			Sender: &sender, Project: &p,
			Entity: entity, Type: model.FileType,
			TimeSent: float64(base.Add(time.Duration(i) * time.Minute).Unix()),
		}
	}

	ids, err := d.SaveHeartbeats(ctx, batch)
	if err != nil {
		t.Fatalf("SaveHeartbeats: %v", err)
	}
	if len(ids) != n {
		t.Fatalf("want %d ids, got %d", n, len(ids))
	}

	// Cross-check: the ids must map back to the same input order when we look up
	// each row's time_sent in the DB.
	for i, id := range ids {
		var ts time.Time
		if err := d.Pool.QueryRow(ctx, `SELECT time_sent FROM heartbeats WHERE id=$1`, id).Scan(&ts); err != nil {
			t.Fatalf("lookup id %d: %v", id, err)
		}
		want := base.Add(time.Duration(i) * time.Minute)
		if !ts.Equal(want) {
			t.Fatalf("id[%d]=%d has time_sent=%s, want %s (batch order broken)", i, id, ts, want)
		}
	}
}
