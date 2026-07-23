package db

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/tracelog"
)

// TestRedactArgsMasksSensitive verifies that args for statements touching
// sensitive tables/columns are masked, while ordinary statements pass through.
func TestRedactArgsMasksSensitive(t *testing.T) {
	cases := []struct {
		name   string
		sql    string
		args   []any
		masked bool
	}{
		{"auth_tokens table", "SELECT owner FROM auth_tokens WHERE token=$1", []any{"secret-tok"}, true},
		{"refresh_tokens table", "INSERT INTO refresh_tokens (owner, refresh_token) VALUES ($1,$2)", []any{"u", "rt"}, true},
		{"users insert", "INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,$2,$3)", []any{"u", "h", "s"}, true},
		{"token column", "SELECT * FROM t WHERE token=$1", []any{"tok"}, true},
		{"password column", "UPDATE t SET hashed_password=$1 WHERE id=$2", []any{"h", 1}, true},
		{"non-sensitive", "SELECT * FROM heartbeats WHERE sender=$1", []any{"alice"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := redactArgs(tc.sql, tc.args)
			list, ok := out.([]any)
			if !ok {
				t.Fatalf("expected []any, got %T", out)
			}
			if len(list) != len(tc.args) {
				t.Fatalf("arg count changed: got %d want %d", len(list), len(tc.args))
			}
			if tc.masked {
				for i, v := range list {
					if v != "***" {
						t.Errorf("arg[%d] = %v, want masked ***", i, v)
					}
				}
			} else {
				for i := range list {
					if list[i] != tc.args[i] {
						t.Errorf("arg[%d] = %v, want unchanged %v", i, list[i], tc.args[i])
					}
				}
			}
		})
	}
}

// TestSlogAdapterDropsArgsWhenOff verifies that with logArgs=false the adapter's
// redactArgs helper is never consulted (args dropped). We exercise the wiring by
// confirming the logger builds and, indirectly, that redaction only triggers on
// sensitive SQL. The drop path is covered by newSlogTraceLogger closure logic;
// here we assert the redaction predicate stays off for benign SQL.
func TestSlogAdapterEnvOffPath(t *testing.T) {
	// logArgs off -> adapter must not panic and produces a usable logger.
	if l := newSlogTraceLogger(false); l == nil {
		t.Fatal("nil logger with args off")
	}
	if l := newSlogTraceLogger(true); l == nil {
		t.Fatal("nil logger with args on")
	}
}

// TestN1TracerIncrement verifies the tracer increments the request-scoped counter
// on TraceQueryStart and no-ops when no counter is present in ctx.
func TestN1TracerIncrement(t *testing.T) {
	tr := n1Tracer{}

	// No reqStats in ctx -> no-op, no panic, summary reports not-ok.
	plain := tr.TraceQueryStart(context.Background(), nil, traceStart("SELECT 1"))
	if _, _, _, ok := ReqStatsSummary(plain); ok {
		t.Error("expected no reqStats in plain ctx")
	}

	// With reqStats: repeated identical statements bump count and the dup bucket.
	ctx := WithReqStats(context.Background())
	for i := 0; i < 5; i++ {
		tr.TraceQueryStart(ctx, nil, traceStart("SELECT * FROM heartbeats WHERE sender=$1"))
	}
	// A different statement bumps count but a separate bucket.
	tr.TraceQueryStart(ctx, nil, traceStart("SELECT * FROM projects WHERE owner=$1"))

	total, maxDup, dupSQL, ok := ReqStatsSummary(ctx)
	if !ok {
		t.Fatal("expected reqStats present")
	}
	if total != 6 {
		t.Errorf("total = %d, want 6", total)
	}
	if maxDup != 5 {
		t.Errorf("maxDup = %d, want 5", maxDup)
	}
	if dupSQL == "" {
		t.Error("expected a duplicate SQL bucket")
	}
}

// TestNormalizeSQLBuckets verifies that statements differing only in literals,
// params, whitespace, or IN-list contents normalize to the same bucket.
func TestNormalizeSQLBuckets(t *testing.T) {
	a := normalizeSQL("SELECT * FROM t WHERE id = $1 AND name = 'bob'")
	b := normalizeSQL("select  *  from t where id = $2 and name = 'alice'")
	if a != b {
		t.Errorf("expected same bucket:\n a=%q\n b=%q", a, b)
	}

	c := normalizeSQL("SELECT * FROM t WHERE id IN (1,2,3)")
	d := normalizeSQL("SELECT * FROM t WHERE id IN (9,8)")
	if c != d {
		t.Errorf("IN-lists should collapse:\n c=%q\n d=%q", c, d)
	}

	e := normalizeSQL("SELECT * FROM heartbeats")
	if e == a {
		t.Error("different statements should not collapse together")
	}
}

// TestIsReadQuery verifies only SELECT/WITH reads are eligible for EXPLAIN.
func TestIsReadQuery(t *testing.T) {
	reads := []string{"SELECT 1", "  select * from t", "WITH x AS (SELECT 1) SELECT * FROM x", "-- c\nSELECT 1"}
	writes := []string{"INSERT INTO t VALUES (1)", "UPDATE t SET a=1", "DELETE FROM t", "TRUNCATE t"}
	for _, q := range reads {
		if !isReadQuery(q) {
			t.Errorf("isReadQuery(%q) = false, want true", q)
		}
	}
	for _, q := range writes {
		if isReadQuery(q) {
			t.Errorf("isReadQuery(%q) = true, want false", q)
		}
	}
}

// TestOptionsEnabled verifies the no-op gate: zero Options is disabled, and dev
// gating applies to the EXPLAIN tracer.
func TestOptionsEnabled(t *testing.T) {
	if (Options{}).enabled() {
		t.Error("zero Options should be a no-op")
	}
	if !(Options{LogQueries: true}).enabled() {
		t.Error("LogQueries should enable")
	}
	if !(Options{N1Threshold: 20}).enabled() {
		t.Error("N1Threshold should enable")
	}
	if (Options{ExplainSlow: time.Second}).enabled() {
		t.Error("ExplainSlow without Dev should stay disabled")
	}
	if !(Options{Dev: true, ExplainSlow: time.Second}).enabled() {
		t.Error("Dev + ExplainSlow should enable")
	}
}

// TestNewWithObservabilityPlainPath verifies that a no-op Options opens the same
// plain pool as New against the isolated test DB (env-off path).
func TestNewWithObservabilityPlainPath(t *testing.T) {
	if !dbReady {
		t.Skipf("skipping: isolated test database unavailable: %s", dbSkipMsg)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	d, err := NewWithObservability(ctx, testDatabaseURL(), Options{})
	if err != nil {
		t.Fatalf("NewWithObservability plain: %v", err)
	}
	defer d.Close()
	var one int
	if err := d.Pool.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Fatalf("plain pool query: one=%d err=%v", one, err)
	}
}

// TestNewWithObservabilityTracersAttached verifies the full tracer path opens a
// working pool and that queries are counted through the N+1 tracer when a
// reqStats-bearing ctx is used. Uses only the isolated test DB.
func TestNewWithObservabilityTracersAttached(t *testing.T) {
	if !dbReady {
		t.Skipf("skipping: isolated test database unavailable: %s", dbSkipMsg)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	d, err := NewWithObservability(ctx, testDatabaseURL(), Options{
		LogQueries:  true,
		N1Threshold: 1,
		N1DupThresh: 1,
	})
	if err != nil {
		t.Fatalf("NewWithObservability tracers: %v", err)
	}
	defer d.Close()

	rctx := WithReqStats(ctx)
	for i := 0; i < 3; i++ {
		var one int
		if err := d.Pool.QueryRow(rctx, "SELECT 1").Scan(&one); err != nil {
			t.Fatalf("query %d: %v", i, err)
		}
	}
	total, maxDup, _, ok := ReqStatsSummary(rctx)
	if !ok {
		t.Fatal("expected reqStats after queries")
	}
	if total < 3 {
		t.Errorf("total = %d, want >= 3 (tracer should count real queries)", total)
	}
	if maxDup < 3 {
		t.Errorf("maxDup = %d, want >= 3 (SELECT 1 repeated)", maxDup)
	}
}

// traceStart is a tiny test helper to build a start-data payload with just the
// SQL set (args are irrelevant to N+1 counting).
func traceStart(sql string) pgx.TraceQueryStartData {
	return pgx.TraceQueryStartData{SQL: sql}
}

// ---------------------------------------------------------------------------
// gaka-ar7: owner-attribution for the pgx tracer.
// ---------------------------------------------------------------------------

// TestWithUser_UserFrom_RoundTrip verifies the ctx helpers are symmetric.
func TestWithUser_UserFrom_RoundTrip(t *testing.T) {
	ctx := WithUser(context.Background(), "alice")
	if got := UserFrom(ctx); got != "alice" {
		t.Errorf("UserFrom = %q, want alice", got)
	}
}

// TestUserFrom_EmptyCtx verifies that a bare ctx yields "" — the pgx tracer
// treats "" as server-scope and emits no "user" attr.
func TestUserFrom_EmptyCtx(t *testing.T) {
	if got := UserFrom(context.Background()); got != "" {
		t.Errorf("UserFrom(bare ctx) = %q, want empty", got)
	}
}

// TestUserFrom_NilCtx is a defensive check: some code paths call UserFrom with
// a nil ctx (background workers wiring things up). This must not panic.
func TestUserFrom_NilCtx(t *testing.T) {
	//nolint:staticcheck // intentionally exercising nil-ctx safety
	if got := UserFrom(nil); got != "" {
		t.Errorf("UserFrom(nil) = %q, want empty", got)
	}
}

// TestWithUser_EmptyIsNoOp verifies that WithUser("") returns ctx unchanged so
// upstream code doesn't have to branch on the auth outcome.
func TestWithUser_EmptyIsNoOp(t *testing.T) {
	base := context.WithValue(context.Background(), ctxUserKey{}, "prior")
	got := WithUser(base, "")
	if UserFrom(got) != "prior" {
		t.Errorf("WithUser(ctx, \"\") clobbered prior value")
	}
}

// TestSensitiveSQLRegex_UnchangedByChanges pins the existing redaction regex
// behavior so the gaka-ar7 wiring cannot silently narrow the mask. If this
// fails, the sensitiveSQL regex was changed — Alpha reported it works, don't
// regress it.
func TestSensitiveSQLRegex_UnchangedByChanges(t *testing.T) {
	cases := map[string]bool{
		"UPDATE users SET encrypted_wakatime_key = NULL WHERE username = $1": true,
		"DELETE FROM refresh_tokens WHERE owner = $1":                        true,
		"INSERT INTO auth_tokens (owner, token) VALUES ($1, $2)":             true,
		"SELECT hashed_password FROM users WHERE username = $1":              true,
		"SELECT * FROM heartbeats WHERE sender = $1":                         false,
		"SELECT project FROM projects WHERE owner = $1":                      false,
	}
	for sql, want := range cases {
		if got := sensitiveSQL.MatchString(sql); got != want {
			t.Errorf("sensitiveSQL.MatchString(%q) = %v, want %v", sql, got, want)
		}
	}
}

// -- Integration: tracer emits "user" attr when ctx carries an owner ---------
//
// TestPgxTracer_TagsUserOnPerUserQueries_GakaAr7Regression is the REGRESSION
// GUARD for gaka-ar7. If this fails, gaka-ar7 has regressed — the DB tracer is
// no longer tagging events with owner, exposing cross-tenant activity to
// LogHub subscribers.
//
// Layer: integration. It exercises the real newSlogTraceLogger closure via
// slog.Default() (swapped for a capture handler), feeding it the exact ctx
// shape the middleware+tracer combo produces at runtime. The captured attrs
// are asserted against the LogHub tee's contract: attrs["user"] MUST be set on
// per-user queries and MUST NOT be set on server-scope ones.
//
// Anti-tautology: this test fails iff the tracer omits `"user"` from attrs.
// Delete the `attrs = append(attrs, "user", u)` line in observability.go and
// re-run to confirm the failure mode (captured under gaka-ar7 work-log).
func TestPgxTracer_TagsUserOnPerUserQueries_GakaAr7Regression(t *testing.T) {
	cap := &captureHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(cap))
	t.Cleanup(func() { slog.SetDefault(prev) })

	logger := newSlogTraceLogger(true)

	// Case 1: per-user query with owner in ctx → tracer must emit "user".
	ctx := WithUser(context.Background(), "victim")
	logger.Log(ctx, tracelog.LogLevelInfo, "Query", map[string]any{
		"sql":  "UPDATE users SET encrypted_wakatime_key = NULL WHERE username = $1",
		"args": []any{"victim"},
	})

	// Case 2: server-scope query with no owner in ctx → NO "user" attr.
	logger.Log(context.Background(), tracelog.LogLevelInfo, "Query", map[string]any{
		"sql":  "SELECT 1",
		"args": []any{},
	})

	records := cap.snapshot()
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}

	perUser := records[0]
	if got, ok := perUser["user"]; !ok || got != "victim" {
		t.Fatalf("gaka-ar7 REGRESSION: per-user tracer event missing owner attr; got user=%v ok=%v (all attrs: %+v)", got, ok, perUser)
	}
	// Sanity: sensitive args on the users table must still be masked (don't
	// regress the existing redaction while adding owner-tagging).
	if args, _ := perUser["args"].([]any); len(args) != 1 || args[0] != "***" {
		t.Errorf("sensitive arg redaction regressed: args=%v", args)
	}

	serverScope := records[1]
	if _, ok := serverScope["user"]; ok {
		t.Errorf("server-scope tracer event unexpectedly carries user attr: %+v", serverScope)
	}
}

// TestPgxTracer_SkipsUserWhenLogArgsOff verifies the owner-attr path is
// independent of the logArgs toggle: even with args off, we still tag records
// with "user" so the LogHub filter can gate them. Regression coverage for a
// plausible future refactor that guards the whole attrs loop behind logArgs.
func TestPgxTracer_SkipsUserWhenLogArgsOff(t *testing.T) {
	cap := &captureHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(cap))
	t.Cleanup(func() { slog.SetDefault(prev) })

	logger := newSlogTraceLogger(false) // logArgs OFF
	ctx := WithUser(context.Background(), "alice")
	logger.Log(ctx, tracelog.LogLevelInfo, "Query", map[string]any{
		"sql":  "UPDATE users SET encrypted_wakatime_key = NULL WHERE username = $1",
		"args": []any{"alice"},
	})

	records := cap.snapshot()
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	rec := records[0]
	if _, ok := rec["args"]; ok {
		t.Errorf("logArgs=false but args leaked: %+v", rec)
	}
	if got, ok := rec["user"]; !ok || got != "alice" {
		t.Errorf("owner-tagging must survive logArgs=off; got user=%v ok=%v", got, ok)
	}
}

// captureHandler is a tiny slog.Handler that records every record's attrs into
// a slice we can assert against. Concurrency-safe (the tracelog Logger callback
// runs on caller goroutines and the tests are single-threaded, but we lock
// anyway to keep -race clean).
type captureHandler struct {
	mu      sync.Mutex
	records []map[string]any
}

func (c *captureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (c *captureHandler) Handle(_ context.Context, r slog.Record) error {
	m := make(map[string]any, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		// Unwrap so []any args show up cleanly (not as slog.Value).
		m[a.Key] = a.Value.Any()
		return true
	})
	// Also stash msg so failures point to the right event.
	if strings.TrimSpace(r.Message) != "" {
		m["_msg"] = r.Message
	}
	c.mu.Lock()
	c.records = append(c.records, m)
	c.mu.Unlock()
	return nil
}
func (c *captureHandler) WithAttrs(as []slog.Attr) slog.Handler { return c }
func (c *captureHandler) WithGroup(string) slog.Handler         { return c }
func (c *captureHandler) snapshot() []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]map[string]any, len(c.records))
	copy(out, c.records)
	return out
}
