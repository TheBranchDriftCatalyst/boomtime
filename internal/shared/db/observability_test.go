// observability_ginkgo_test.go — ginkgo mirror of observability_test.go (boom-0vp.13).
// 1:1 case map (15 stdlib TestXxx incl 6 subtests → 12 Its + 1 DescribeTable(6)):
//
//	TestRedactArgsMasksSensitive                          → DescribeTable "redactArgs" 6 entries
//	TestSlogAdapterEnvOffPath                             → It "newSlogTraceLogger builds with args on/off"
//	TestN1TracerIncrement                                 → It "n1Tracer.TraceQueryStart increments bucket"
//	TestNormalizeSQLBuckets                               → It "normalizeSQL collapses literals/params/whitespace/IN-lists"
//	TestIsReadQuery                                       → It "isReadQuery: SELECT/WITH yes; INSERT/UPDATE/DELETE/TRUNCATE no"
//	TestOptionsEnabled                                    → It "Options.enabled() gates zero/LogQueries/N1/ExplainSlow+Dev"
//	TestNewWithObservabilityPlainPath                     → It "NewWithObservability zero Options → plain pool"
//	TestNewWithObservabilityTracersAttached               → It "tracers attached: counted per-request"
//	TestWithUser_UserFrom_RoundTrip                       → It "WithUser + UserFrom roundtrip"
//	TestUserFrom_EmptyCtx                                 → It "UserFrom on bare ctx → \"\""
//	TestUserFrom_NilCtx                                   → It "UserFrom on nil ctx does not panic"
//	TestWithUser_EmptyIsNoOp                              → It "WithUser(ctx, \"\") preserves prior value"
//	TestSensitiveSQLRegex_UnchangedByChanges              → DescribeTable "sensitiveSQL regex hits" (6 entries — inlined in an It)
//	TestPgxTracer_TagsUserOnPerUserQueries_GakaAr7Regression → It "boom-ar7: tracer tags per-user query with owner"
//	TestPgxTracer_SkipsUserWhenLogArgsOff                 → It "boom-ar7: owner-tag survives logArgs=off"
package db

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/tracelog"
	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("observability", func() {
	ginkgo.DescribeTable("redactArgs masks args for sensitive statements and passes through benign ones",
		func(sql string, args []any, masked bool) {
			out := redactArgs(sql, args)
			list, ok := out.([]any)
			Expect(ok).To(BeTrue(), "expected []any, got %T", out)
			Expect(list).To(HaveLen(len(args)))
			if masked {
				for i, v := range list {
					Expect(v).To(Equal("***"), "arg[%d]", i)
				}
			} else {
				for i := range list {
					Expect(list[i]).To(Equal(args[i]), "arg[%d]", i)
				}
			}
		},
		ginkgo.Entry("auth_tokens table", "SELECT owner FROM auth_tokens WHERE token=$1", []any{"secret-tok"}, true),
		ginkgo.Entry("refresh_tokens table", "INSERT INTO refresh_tokens (owner, refresh_token) VALUES ($1,$2)", []any{"u", "rt"}, true),
		ginkgo.Entry("users insert", "INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,$2,$3)", []any{"u", "h", "s"}, true),
		ginkgo.Entry("token column", "SELECT * FROM t WHERE token=$1", []any{"tok"}, true),
		ginkgo.Entry("password column", "UPDATE t SET hashed_password=$1 WHERE id=$2", []any{"h", 1}, true),
		ginkgo.Entry("non-sensitive", "SELECT * FROM heartbeats WHERE sender=$1", []any{"alice"}, false),
	)

	ginkgo.It("newSlogTraceLogger builds a usable logger with args on and off", func() {
		Expect(newSlogTraceLogger(false)).NotTo(BeNil())
		Expect(newSlogTraceLogger(true)).NotTo(BeNil())
	})

	ginkgo.It("n1Tracer.TraceQueryStart is a no-op without reqStats and increments dup bucket with reqStats", func() {
		tr := n1Tracer{}
		plain := tr.TraceQueryStart(context.Background(), nil, traceStart("SELECT 1"))
		_, _, _, ok := ReqStatsSummary(plain)
		Expect(ok).To(BeFalse())

		ctx := WithReqStats(context.Background())
		for i := 0; i < 5; i++ {
			tr.TraceQueryStart(ctx, nil, traceStart("SELECT * FROM heartbeats WHERE sender=$1"))
		}
		tr.TraceQueryStart(ctx, nil, traceStart("SELECT * FROM projects WHERE owner=$1"))

		total, maxDup, dupSQL, ok := ReqStatsSummary(ctx)
		Expect(ok).To(BeTrue())
		Expect(total).To(Equal(6))
		Expect(maxDup).To(Equal(5))
		Expect(dupSQL).NotTo(BeEmpty())
	})

	ginkgo.It("record() excludes transaction-control + SET statements so aggQuery's per-read begin/set-local/rollback don't read as N+1", func() {
		ctx := WithReqStats(context.Background())
		tr := n1Tracer{}
		// Three aggQuery reads, each: Begin → SET LOCAL work_mem → query → Rollback.
		for i := 0; i < 3; i++ {
			tr.TraceQueryStart(ctx, nil, traceStart("begin"))
			tr.TraceQueryStart(ctx, nil, traceStart("SET LOCAL work_mem = '256MB'"))
			tr.TraceQueryStart(ctx, nil, traceStart("SELECT * FROM heartbeats WHERE sender=$1"))
			tr.TraceQueryStart(ctx, nil, traceStart("rollback"))
		}
		total, maxDup, dupSQL, ok := ReqStatsSummary(ctx)
		Expect(ok).To(BeTrue())
		// Only the 3 real SELECTs count — not the 9 begin/set/rollback bookkeeping
		// statements that previously produced a false "duplicate_sql=rollback".
		Expect(total).To(Equal(3))
		Expect(maxDup).To(Equal(3))
		Expect(dupSQL).To(ContainSubstring("heartbeats"))
	})

	ginkgo.It("normalizeSQL collapses literals/params/whitespace/IN-list variants to the same bucket", func() {
		a := normalizeSQL("SELECT * FROM t WHERE id = $1 AND name = 'bob'")
		b := normalizeSQL("select  *  from t where id = $2 and name = 'alice'")
		Expect(a).To(Equal(b))

		c := normalizeSQL("SELECT * FROM t WHERE id IN (1,2,3)")
		d := normalizeSQL("SELECT * FROM t WHERE id IN (9,8)")
		Expect(c).To(Equal(d))

		e := normalizeSQL("SELECT * FROM heartbeats")
		Expect(e).NotTo(Equal(a))
	})

	ginkgo.It("isReadQuery accepts SELECT/WITH and rejects INSERT/UPDATE/DELETE/TRUNCATE", func() {
		reads := []string{"SELECT 1", "  select * from t", "WITH x AS (SELECT 1) SELECT * FROM x", "-- c\nSELECT 1"}
		writes := []string{"INSERT INTO t VALUES (1)", "UPDATE t SET a=1", "DELETE FROM t", "TRUNCATE t"}
		for _, q := range reads {
			Expect(isReadQuery(q)).To(BeTrue(), "%q", q)
		}
		for _, q := range writes {
			Expect(isReadQuery(q)).To(BeFalse(), "%q", q)
		}
	})

	ginkgo.It("Options.enabled(): zero=off; LogQueries|N1Threshold|(Dev+ExplainSlow)=on; ExplainSlow alone=off", func() {
		Expect((Options{}).enabled()).To(BeFalse())
		Expect((Options{LogQueries: true}).enabled()).To(BeTrue())
		Expect((Options{N1Threshold: 20}).enabled()).To(BeTrue())
		Expect((Options{ExplainSlow: time.Second}).enabled()).To(BeFalse())
		Expect((Options{Dev: true, ExplainSlow: time.Second}).enabled()).To(BeTrue())
	})

	ginkgo.It("NewWithObservability with zero Options opens the plain pool (env-off path)", func() {
		if !dbReady {
			ginkgo.Skip("isolated test database unavailable: " + dbSkipMsg)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		d, err := NewWithObservability(ctx, testDatabaseURL(), Options{})
		Expect(err).NotTo(HaveOccurred())
		ginkgo.DeferCleanup(func() { d.Close() })
		var one int
		Expect(d.Pool.QueryRow(ctx, "SELECT 1").Scan(&one)).To(Succeed())
		Expect(one).To(Equal(1))
	})

	ginkgo.It("NewWithObservability with tracers counts real queries under a reqStats-bearing ctx", func() {
		if !dbReady {
			ginkgo.Skip("isolated test database unavailable: " + dbSkipMsg)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		d, err := NewWithObservability(ctx, testDatabaseURL(), Options{
			LogQueries:  true,
			N1Threshold: 1,
			N1DupThresh: 1,
		})
		Expect(err).NotTo(HaveOccurred())
		ginkgo.DeferCleanup(func() { d.Close() })

		rctx := WithReqStats(ctx)
		for i := 0; i < 3; i++ {
			var one int
			Expect(d.Pool.QueryRow(rctx, "SELECT 1").Scan(&one)).To(Succeed())
		}
		total, maxDup, _, ok := ReqStatsSummary(rctx)
		Expect(ok).To(BeTrue())
		Expect(total).To(BeNumerically(">=", 3))
		Expect(maxDup).To(BeNumerically(">=", 3))
	})

	ginkgo.It("WithUser + UserFrom round-trip", func() {
		ctx := WithUser(context.Background(), "alice")
		Expect(UserFrom(ctx)).To(Equal("alice"))
	})

	ginkgo.It("UserFrom on a bare ctx yields \"\"", func() {
		Expect(UserFrom(context.Background())).To(Equal(""))
	})

	ginkgo.It("UserFrom on a nil ctx does not panic and yields \"\"", func() {
		//nolint:staticcheck // intentionally exercising nil-ctx safety
		Expect(UserFrom(nil)).To(Equal(""))
	})

	ginkgo.It("WithUser(ctx, \"\") is a no-op that preserves the prior value", func() {
		base := context.WithValue(context.Background(), ctxUserKey{}, "prior")
		got := WithUser(base, "")
		Expect(UserFrom(got)).To(Equal("prior"))
	})

	ginkgo.It("sensitiveSQL regex hits the load-bearing statements and misses the benign ones", func() {
		cases := map[string]bool{
			"UPDATE users SET encrypted_wakatime_key = NULL WHERE username = $1": true,
			"DELETE FROM refresh_tokens WHERE owner = $1":                        true,
			"INSERT INTO auth_tokens (owner, token) VALUES ($1, $2)":             true,
			"SELECT hashed_password FROM users WHERE username = $1":              true,
			"SELECT * FROM heartbeats WHERE sender = $1":                         false,
			"SELECT project FROM projects WHERE owner = $1":                      false,
		}
		for sql, want := range cases {
			Expect(sensitiveSQL.MatchString(sql)).To(Equal(want), "sensitiveSQL.MatchString(%q)", sql)
		}
	})

	ginkgo.It("boom-ar7: per-user tracer event carries owner attr and masks sensitive args; server-scope has no user attr", func() {
		cap := &captureHandler{}
		prev := slog.Default()
		slog.SetDefault(slog.New(cap))
		ginkgo.DeferCleanup(func() { slog.SetDefault(prev) })

		logger := newSlogTraceLogger(true)

		ctx := WithUser(context.Background(), "victim")
		logger.Log(ctx, tracelog.LogLevelInfo, "Query", map[string]any{
			"sql":  "UPDATE users SET encrypted_wakatime_key = NULL WHERE username = $1",
			"args": []any{"victim"},
		})

		logger.Log(context.Background(), tracelog.LogLevelInfo, "Query", map[string]any{
			"sql":  "SELECT 1",
			"args": []any{},
		})

		records := cap.snapshot()
		Expect(records).To(HaveLen(2))

		perUser := records[0]
		Expect(perUser["user"]).To(Equal("victim"), "boom-ar7 REGRESSION: per-user tracer event missing owner attr")
		args, _ := perUser["args"].([]any)
		Expect(args).To(HaveLen(1))
		Expect(args[0]).To(Equal("***"), "sensitive arg redaction regressed")

		serverScope := records[1]
		_, ok := serverScope["user"]
		Expect(ok).To(BeFalse(), "server-scope tracer event unexpectedly carries user attr")
	})

	ginkgo.It("boom-ar7: owner-tagging survives logArgs=false (args dropped, user attr kept)", func() {
		cap := &captureHandler{}
		prev := slog.Default()
		slog.SetDefault(slog.New(cap))
		ginkgo.DeferCleanup(func() { slog.SetDefault(prev) })

		logger := newSlogTraceLogger(false)
		ctx := WithUser(context.Background(), "alice")
		logger.Log(ctx, tracelog.LogLevelInfo, "Query", map[string]any{
			"sql":  "UPDATE users SET encrypted_wakatime_key = NULL WHERE username = $1",
			"args": []any{"alice"},
		})

		records := cap.snapshot()
		Expect(records).To(HaveLen(1))
		rec := records[0]
		_, ok := rec["args"]
		Expect(ok).To(BeFalse(), "logArgs=false but args leaked")
		Expect(rec["user"]).To(Equal("alice"), "owner-tagging must survive logArgs=off")
	})
})

// -- helpers restored from stdlib partner (boom-0vp.17) --
func traceStart(sql string) pgx.TraceQueryStartData {
	return pgx.TraceQueryStartData{SQL: sql}
}

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

func (c *captureHandler) WithGroup(string) slog.Handler { return c }

func (c *captureHandler) snapshot() []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]map[string]any, len(c.records))
	copy(out, c.records)
	return out
}
