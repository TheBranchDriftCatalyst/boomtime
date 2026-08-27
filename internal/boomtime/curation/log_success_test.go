// log_success_test.go — asserts the success-path narration log line added by
// the logging audit (gaka): ApplyRename emits "curation rename applied" with
// the rule id AND the affected-row COUNT (the whole point of the audit line —
// a bulk mutation must say how much it touched). Uses a capturing slog handler
// (mirrors internal/ingest sampler_test.go's capHandler) so we assert the
// exact message + attrs, and prove the count is present and non-zero.
//
// Plain stdlib test (not ginkgo) so it can drive a curation.Handler wired to a
// capturing logger without tripping the gomega-outside-ginkgo fail handler.
package curation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/curation"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apiroute"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

type capRec struct {
	msg   string
	attrs map[string]any
}

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

func doReq(e http.Handler, method, target, token string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, target, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Basic "+token)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestApplyRename_LogsSuccessWithRuleIDAndRowCount(t *testing.T) {
	hz := testutil.NewHarness(t)
	user, token := hz.MintUser("cur_log")
	seedRenameableHeartbeats(hz, user) // seeds >0 rows on language="Python"

	logger, cap := newCapLog()
	ch := curation.New(hz.DB, hz.Cfg, logger, nil)
	e := echo.New()
	// Both handlers now live on the typed apiroute seam, so this hand-rolled
	// mini-router registers through it too rather than through plain e.POST.
	apiroute.POST(e, "/api/v1/users/current/curation", ch.CreateCuration)
	apiroute.POSTNoBody(e, "/api/v1/users/current/curation/:id/apply", ch.ApplyRename)

	// Create a rename rule Python → python.
	crRec := doReq(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
		"axis": "language", "action": "rename", "matchType": "exact",
		"matchValue": "Python", "newValue": "python",
	})
	if crRec.Code != http.StatusOK {
		t.Fatalf("create rule: expected 200, got %d — body=%s", crRec.Code, crRec.Body.String())
	}
	var cr struct {
		Rule struct {
			ID int `json:"id"`
		} `json:"rule"`
	}
	if err := json.Unmarshal(crRec.Body.Bytes(), &cr); err != nil || cr.Rule.ID == 0 {
		t.Fatalf("decode rule id: err=%v body=%s", err, crRec.Body.String())
	}

	applyRec := doReq(e, http.MethodPost,
		"/api/v1/users/current/curation/"+strconv.Itoa(cr.Rule.ID)+"/apply", token, nil)
	if applyRec.Code != http.StatusOK {
		t.Fatalf("apply: expected 200, got %d — body=%s", applyRec.Code, applyRec.Body.String())
	}

	r, ok := cap.find("curation rename applied")
	if !ok {
		t.Fatalf(`expected a "curation rename applied" success log; got none`)
	}
	// ruleId attr must match the applied rule.
	if gotID := toInt(r.attrs["ruleId"]); gotID != cr.Rule.ID {
		t.Errorf(`"curation rename applied" ruleId = %v, want %d`, r.attrs["ruleId"], cr.Rule.ID)
	}
	// rows attr must be present AND non-zero (the seeded Python rows were rewritten).
	rows, present := r.attrs["rows"]
	if !present {
		t.Fatalf(`"curation rename applied" MUST carry the affected-row count in a "rows" attr; got attrs=%v`, r.attrs)
	}
	if toInt(rows) <= 0 {
		t.Errorf(`"rows" count should be > 0 on seeded fixture, got %v`, rows)
	}
}

// toInt normalizes whatever numeric type slog stored (int / int64) to int.
func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return -1
	}
}
