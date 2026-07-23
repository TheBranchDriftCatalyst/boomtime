// observability.go wires transparent DB observability onto the pgx pool via a
// pgx v5 QueryTracer at pool-config time — the single choke-point through which
// every d.Pool.Query/QueryRow/Exec (and tx.Exec) already flows, so there are no
// call-site changes. Three tracers are composed with pgx/v5/multitracer:
//
//  1. tracelog.TraceLog — structured per-query slog logging (env BOOM_DB_LOG_QUERIES),
//     with mandatory redaction of sensitive args (BOOM_DB_LOG_ARGS gates args at all).
//  2. n1Tracer — request-scoped query counter that flags N+1 patterns (WARN).
//  3. planTracer — dev-only auto-EXPLAIN of slow SELECTs on a fresh conn.
//
// tracelog/multitracer ship inside pgx v5; no new dependencies are added.
package db

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/multitracer"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/tracelog"
)

// Options controls the optional DB observability tracers. The zero value is a
// no-op (plain pool, identical to New). cmd/boomtime/main.go builds this from
// env-driven config; tests keep the plain path via New.
type Options struct {
	LogQueries  bool          // enable tracelog structured query logging
	LogArgs     bool          // include (redacted) args in query logs; requires LogQueries
	N1Threshold int           // queries/request to WARN (0 disables the count check)
	N1DupThresh int           // identical normalized statements/request to WARN (0 disables)
	ExplainSlow time.Duration // dev-only: auto-EXPLAIN reads slower than this (0=off)
	Dev         bool          // gates the EXPLAIN tracer (never run in prod)
}

// enabled reports whether any tracer would be installed. When false, New's plain
// pgxpool.New path is used verbatim.
func (o Options) enabled() bool {
	return o.LogQueries || o.N1Threshold > 0 || o.N1DupThresh > 0 || (o.Dev && o.ExplainSlow > 0)
}

// NewWithObservability opens a pool like New but attaches the configured tracers
// onto cfg.ConnConfig.Tracer via ParseConfig + NewWithConfig. When opts is a
// no-op it delegates to New (the plain pgxpool.New path) so tests and CLI paths
// are unaffected.
func NewWithObservability(ctx context.Context, url string, opts Options) (*DB, error) {
	if !opts.enabled() {
		return New(ctx, url)
	}

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}

	var tracers []pgx.QueryTracer
	if opts.LogQueries {
		tracers = append(tracers, &tracelog.TraceLog{
			Logger:   newSlogTraceLogger(opts.LogArgs),
			LogLevel: tracelog.LogLevelInfo,
		})
	}
	if opts.N1Threshold > 0 || opts.N1DupThresh > 0 {
		tracers = append(tracers, &n1Tracer{})
	}
	if opts.Dev && opts.ExplainSlow > 0 {
		tracers = append(tracers, &planTracer{slow: opts.ExplainSlow})
	}

	if len(tracers) == 1 {
		cfg.ConnConfig.Tracer = tracers[0]
	} else {
		cfg.ConnConfig.Tracer = multitracer.New(tracers...)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}

	d := &DB{Pool: pool}
	// The plan tracer runs EXPLAIN on a fresh conn from the very same pool.
	for _, t := range tracers {
		if pt, ok := t.(*planTracer); ok {
			pt.pool = pool
		}
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return d, nil
}

// ---------------------------------------------------------------------------
// Phase 1: structured query logging via slog, with mandatory arg redaction.
// ---------------------------------------------------------------------------

// ctxUserKey is the private context-value key under which the request-scope
// resolved username is stashed by an upstream middleware. The pgx tracer reads
// it in newSlogTraceLogger below and, when present, emits it as the `"user"`
// slog attribute so LogHub's FilterForUser (internal/logging) can gate the
// resulting DEBUG SQL records to the acting user only — the fix for gaka-ar7
// (LogHub SQL-tracer records leaking cross-tenant activity narration).
//
// Server-scope queries (migrations, healthz, N+1 counter probes, background
// workers) run with no user in ctx and therefore emit no `"user"` attr — those
// records stay visible to every authenticated Logs viewer, which matches the
// server-scope semantics FilterForUser already implements.
type ctxUserKey struct{}

// WithUser stashes username in ctx under the tracer's private key. It is called
// by the server-layer user-injection middleware right after the request's
// bearer token is resolved to an owner. An empty username is a no-op (returns
// ctx unchanged) so upstream code doesn't have to branch on the auth outcome.
func WithUser(ctx context.Context, username string) context.Context {
	if username == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxUserKey{}, username)
}

// UserFrom returns the username previously stashed via WithUser, or the empty
// string if none is present (nil ctx included — safe to call in every tracer
// path). The pgx tracer treats "" as "server-scope" and emits no `"user"` attr.
func UserFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	u, _ := ctx.Value(ctxUserKey{}).(string)
	return u
}

// newSlogTraceLogger adapts our log/slog default logger to tracelog.Logger. When
// logArgs is false, args are dropped entirely (only sql/duration/commandTag are
// logged). When true, args are still redacted for statements touching sensitive
// tables/columns before they reach the log.
//
// Every emitted record additionally carries the request-scope username (when
// present in ctx via WithUser) under the `"user"` attribute — the tee handler
// flattens that into LogEntry.Attrs["user"], and logging.FilterForUser (gaka-
// awh.2) drops the record for any other tenant's Logs viewer. Without this
// hook the DEBUG SQL narration ("UPDATE users SET encrypted_wakatime_key = NULL
// WHERE username = $1") leaks activity metadata cross-tenant even though the
// bind args are redacted (gaka-ar7).
func newSlogTraceLogger(logArgs bool) tracelog.Logger {
	return tracelog.LoggerFunc(func(ctx context.Context, level tracelog.LogLevel, msg string, data map[string]any) {
		attrs := make([]any, 0, len(data)*2+2)
		sql, _ := data["sql"].(string)
		for k, v := range data {
			if k == "args" {
				if !logArgs {
					continue
				}
				v = redactArgs(sql, v)
			}
			attrs = append(attrs, k, v)
		}
		if u := UserFrom(ctx); u != "" {
			attrs = append(attrs, "user", u)
		}
		slog.Default().Log(ctx, mapLevel(level), msg, attrs...)
	})
}

// mapLevel converts a tracelog level to an slog level. pgx emits successful
// per-query logs at Info; we map that to Debug so the query firehose doesn't
// clutter stdout/INFO — it's still captured by the LogHub (the Logs tab keeps
// DEBUG) and filterable there. pgx warnings/errors keep their real levels.
func mapLevel(l tracelog.LogLevel) slog.Level {
	switch l {
	case tracelog.LogLevelError:
		return slog.LevelError
	case tracelog.LogLevelWarn:
		return slog.LevelWarn
	case tracelog.LogLevelInfo:
		return slog.LevelDebug // per-query traces -> Debug (see comment above)
	default: // Debug, Trace
		return slog.LevelDebug
	}
}

// sensitiveSQL matches statements that carry secrets as positional args: the
// auth_tokens/refresh_tokens/users tables, or token/password/salt columns. When
// it matches, every arg for that statement is masked (we cannot reliably map a
// specific $N to a column without parsing SQL, so we redact conservatively).
var sensitiveSQL = regexp.MustCompile(`(?i)\b(auth_tokens|refresh_tokens|users|token|refresh_token|hashed_password|password|salt|salt_used)\b`)

// redactArgs returns args with sensitive values masked. If the SQL touches a
// sensitive table/column, all positional args are replaced with "***"; the arg
// count is preserved so the shape of the query is still visible in logs.
func redactArgs(sql string, args any) any {
	list, ok := args.([]any)
	if !ok || !sensitiveSQL.MatchString(sql) {
		return args
	}
	masked := make([]any, len(list))
	for i := range list {
		masked[i] = "***"
	}
	return masked
}

// ---------------------------------------------------------------------------
// Phase 2: per-request N+1 detection.
// ---------------------------------------------------------------------------

type reqStatsKey struct{}

// reqStats accumulates per-request query counts. It is stashed in the request
// context by the echo middleware (internal/server) and read here. Queries made
// outside an HTTP request simply have no reqStats in ctx, so the tracer no-ops.
type reqStats struct {
	mu     sync.Mutex
	count  int
	byNorm map[string]int
}

func (s *reqStats) record(sql string) {
	norm := normalizeSQL(sql)
	s.mu.Lock()
	s.count++
	if s.byNorm == nil {
		s.byNorm = make(map[string]int)
	}
	s.byNorm[norm]++
	s.mu.Unlock()
}

// WithReqStats returns a context carrying a fresh per-request query counter.
// The N+1 middleware calls this at request start; ReqStatsSummary reads the
// result at request end.
func WithReqStats(ctx context.Context) context.Context {
	return context.WithValue(ctx, reqStatsKey{}, &reqStats{})
}

// ReqStatsSummary returns the total query count and the highest single
// normalized-statement repetition seen during the request (classic N+1 signal),
// plus that statement. It returns ok=false when no counter is attached.
func ReqStatsSummary(ctx context.Context) (total, maxDup int, dupSQL string, ok bool) {
	s, ok := ctx.Value(reqStatsKey{}).(*reqStats)
	if !ok {
		return 0, 0, "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for norm, n := range s.byNorm {
		if n > maxDup {
			maxDup, dupSQL = n, norm
		}
	}
	return s.count, maxDup, dupSQL, true
}

// n1Tracer increments the request-scoped counter on every query start. It is a
// no-op when no reqStats is in ctx (non-HTTP paths), and does nothing on end.
type n1Tracer struct{}

func (n1Tracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if s, ok := ctx.Value(reqStatsKey{}).(*reqStats); ok {
		s.record(data.SQL)
	}
	return ctx
}

func (n1Tracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

var (
	reWhitespace = regexp.MustCompile(`\s+`)
	reNumLit     = regexp.MustCompile(`\b\d+\b`)
	reStrLit     = regexp.MustCompile(`'[^']*'`)
	reParams     = regexp.MustCompile(`\$\d+`)
	reInList     = regexp.MustCompile(`(?i)\bin\s*\([^)]*\)`)
)

// normalizeSQL collapses a statement so semantically-identical queries that
// differ only in literals/params/whitespace bucket together (for N+1 dup
// detection). It strips string/number literals, params, collapses IN-lists, and
// normalizes whitespace/case.
func normalizeSQL(sql string) string {
	s := strings.ToLower(strings.TrimSpace(sql))
	s = reStrLit.ReplaceAllString(s, "?")
	s = reInList.ReplaceAllString(s, "in (?)")
	s = reParams.ReplaceAllString(s, "?")
	s = reNumLit.ReplaceAllString(s, "?")
	s = reWhitespace.ReplaceAllString(s, " ")
	return s
}

// ---------------------------------------------------------------------------
// Phase 3: dev-only auto-EXPLAIN of slow reads.
// ---------------------------------------------------------------------------

type explainSkipKey struct{}
type planStartKey struct{}

type planStartData struct {
	sql   string
	args  []any
	start time.Time
}

// planTracer runs a plain EXPLAIN (never ANALYZE; reads only) on a fresh pool
// conn when a SELECT/WITH read exceeds the slow threshold. It guards against
// recursion by tagging its own EXPLAIN ctx with a skip flag.
type planTracer struct {
	slow time.Duration
	pool *pgxpool.Pool
}

func (p *planTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if _, skip := ctx.Value(explainSkipKey{}).(struct{}); skip {
		return ctx
	}
	return context.WithValue(ctx, planStartKey{}, &planStartData{
		sql: data.SQL, args: data.Args, start: time.Now(),
	})
}

func (p *planTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	st, ok := ctx.Value(planStartKey{}).(*planStartData)
	if !ok || p.pool == nil || data.Err != nil {
		return
	}
	if time.Since(st.start) < p.slow || !isReadQuery(st.sql) {
		return
	}
	p.explain(ctx, st)
}

// explain runs `EXPLAIN <sql>` on a fresh conn, tagging the ctx with a skip flag
// so the tracer does not recurse on its own EXPLAIN. Never uses ANALYZE (which
// would execute the statement), and only reaches here for read queries.
func (p *planTracer) explain(ctx context.Context, st *planStartData) {
	explainCtx := context.WithValue(ctx, explainSkipKey{}, struct{}{})
	explainCtx, cancel := context.WithTimeout(explainCtx, 5*time.Second)
	defer cancel()

	rows, err := p.pool.Query(explainCtx, "EXPLAIN "+st.sql, st.args...)
	if err != nil {
		slog.Default().Warn("db slow-query explain failed", "err", err, "sql", st.sql)
		return
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			break
		}
		plan = append(plan, line)
	}
	slog.Default().Warn("db slow-query plan",
		"dur_ms", time.Since(st.start).Milliseconds(),
		"sql", st.sql,
		"plan", strings.Join(plan, "\n"),
	)
}

// isReadQuery reports whether sql is a plain read (SELECT/WITH) that is safe to
// EXPLAIN without side effects. Writes are never explained.
func isReadQuery(sql string) bool {
	s := strings.ToLower(strings.TrimSpace(sql))
	// Strip a leading line comment if present.
	for strings.HasPrefix(s, "--") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = strings.TrimSpace(s[i+1:])
		} else {
			return false
		}
	}
	return strings.HasPrefix(s, "select") || strings.HasPrefix(s, "with")
}
