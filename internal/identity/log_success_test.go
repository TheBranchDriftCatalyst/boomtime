// log_success_test.go — asserts the success-path narration log line added by
// the logging audit (gaka): Login emits "login" with the resolved username and,
// critically, NEVER the password (HARD RULE: log the fact + safe identifiers
// only, never a secret/plaintext credential). Uses a capturing slog handler
// (mirrors internal/ingest sampler_test.go's capHandler).
//
// Plain stdlib test (not ginkgo) so it can drive an identity.Handler wired to a
// capturing logger without tripping the gomega-outside-ginkgo fail handler.
package identity_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apiroute"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/identity"
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

func TestLogin_LogsSuccessWithUsernameNeverPassword(t *testing.T) {
	hz := testutil.NewHarness(t)
	// MintUser seeds a users row whose password is "pw-"+username (see
	// testutil.MintUser) so we can drive a real successful password login.
	username, _ := hz.MintUser("login_log")
	password := "pw-" + username

	logger, cap := newCapLog()
	h := identity.New(hz.DB, hz.Cfg, logger, nil)
	e := echo.New()
	apiroute.POSTNoBody(e, "/auth/login", h.Login)

	body, _ := json.Marshal(map[string]any{"username": username, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("login: expected 200, got %d — body=%s", rec.Code, rec.Body.String())
	}

	r, ok := cap.find("login")
	if !ok {
		t.Fatalf(`expected a "login" success log; got none`)
	}
	if r.attrs["user"] != username {
		t.Errorf(`"login" user attr = %v, want %q`, r.attrs["user"], username)
	}
	// HARD RULE: the password must NEVER appear — not as a key, not as a value.
	if _, bad := r.attrs["password"]; bad {
		t.Errorf(`"login" log leaked a "password" attr key: %v`, r.attrs)
	}
	for k, v := range r.attrs {
		if fmt.Sprint(v) == password {
			t.Errorf(`"login" log leaked the plaintext password in attr %q`, k)
		}
	}
}
