// log_success_test.go — asserts the success-path narration log lines added by
// the logging audit (gaka): CreateGoal emits "goal created" with the owner +
// goal id. Uses a capturing slog handler (mirrors internal/ingest
// sampler_test.go's capHandler) so we assert the exact message + attrs rather
// than just that "something logged".
//
// Plain stdlib tests (not ginkgo) so we can drive a fresh goals.Handler wired
// to a capturing logger against the shared isolated test DB.
package goals_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/goals"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apiroute"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

// capRec is one captured slog record: its message plus a flattened attr map
// (base attrs from .With(...) merged with the record's own inline attrs).
type capRec struct {
	msg   string
	attrs map[string]any
}

// capLog is a slog.Handler that records every record for later assertion.
type capLog struct {
	mu   *sync.Mutex
	recs *[]capRec
	base []slog.Attr
}

func newCapLog() (*slog.Logger, *capLog) {
	h := &capLog{mu: &sync.Mutex{}, recs: &[]capRec{}}
	return slog.New(h), h
}

func (h *capLog) Enabled(context.Context, slog.Level) bool { return true }
func (h *capLog) Handle(_ context.Context, r slog.Record) error {
	m := map[string]any{}
	for _, a := range h.base {
		m[a.Key] = a.Value.Any()
	}
	r.Attrs(func(a slog.Attr) bool { m[a.Key] = a.Value.Any(); return true })
	h.mu.Lock()
	defer h.mu.Unlock()
	*h.recs = append(*h.recs, capRec{msg: r.Message, attrs: m})
	return nil
}
func (h *capLog) WithAttrs(as []slog.Attr) slog.Handler {
	nb := append(append([]slog.Attr{}, h.base...), as...)
	return &capLog{mu: h.mu, recs: h.recs, base: nb}
}
func (h *capLog) WithGroup(string) slog.Handler { return h }
func (h *capLog) find(msg string) (capRec, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range *h.recs {
		if r.msg == msg {
			return r, true
		}
	}
	return capRec{}, false
}

func TestCreateGoal_LogsSuccessWithOwnerAndGoalID(t *testing.T) {
	hz := testutil.NewHarness(t)
	owner, token := hz.MintUser("goal_log")

	logger, cap := newCapLog()
	gh := &goals.Handler{DB: hz.DB, Logger: logger}
	e := echo.New()
	// CreateGoal now lives on the typed apiroute seam (its request/response
	// types are captured for the OpenAPI spec), so it registers through
	// apiroute.POST rather than e.POST. The types are unexported inside
	// package goals — inference resolves them here without naming them.
	apiroute.POST(e, "/api/v1/users/current/goals", gh.CreateGoal)

	body, _ := json.Marshal(map[string]any{
		"name": "log-goal",
		"spec": json.RawMessage(`{"kind":"time","axis":"language","value":"Go","op":">=","target_seconds":3600,"window":"week"}`),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/current/goals", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("create goal: expected 200, got %d — body=%s", rec.Code, rec.Body.String())
	}

	r, ok := cap.find("goal created")
	if !ok {
		t.Fatalf(`expected a "goal created" success log; got none`)
	}
	if r.attrs["user"] != owner {
		t.Errorf(`"goal created" user attr = %v, want %q`, r.attrs["user"], owner)
	}
	gid, _ := r.attrs["goal"].(string)
	if gid == "" {
		t.Errorf(`"goal created" goal attr should be a non-empty id, got %v`, r.attrs["goal"])
	}
}
