// Package db provides the pgx connection pool, embedded migrations, and typed
// query wrappers (ports Db/Statements.hs + Db/Sessions.hs).
package db

import (
	"context"
	"embed"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed queries/*.sql
var queryFS embed.FS

//go:embed migrations/*.sql
var migrationFS embed.FS

// mustQuery loads an embedded SQL query file or panics (queries are verbatim
// from hakatime and are expected to exist at build time).
func mustQuery(name string) string {
	b, err := queryFS.ReadFile("queries/" + name)
	if err != nil {
		panic("missing embedded query " + name + ": " + err.Error())
	}
	return string(b)
}

// Preloaded query strings, embedded byte-for-byte from hakatime's sql/ files.
var (
	qInsertHeartbeat     = mustQuery("insert_heartbeat.sql")
	qGetUserActivity     = mustQuery("get_user_activity.sql")
	qGetUserActivityRoll = mustQuery("get_user_activity_rollup.sql")
	qGetUserActivityTag  = mustQuery("get_user_activity_by_tags.sql")
	qGetProjectsStats    = mustQuery("get_projects_stats.sql")
	qGetProjDailyExtras  = mustQuery("get_project_daily_extras.sql")
	qGetProjBranchDaily  = mustQuery("get_project_branch_daily.sql")
	qGetTagStats         = mustQuery("get_tag_stats.sql")
	qGetTimeline         = mustQuery("get_timeline.sql")
	qGetLeaderboards     = mustQuery("get_leaderboards.sql")
	qGetTimeBetween      = mustQuery("get_time_between.sql")
	qGetTimeToday        = mustQuery("get_time_today.sql")
	qGetTotalProject     = mustQuery("get_total_project_time.sql")
)

// DB wraps a pgx pool.
type DB struct {
	Pool *pgxpool.Pool
}

// New opens a connection pool.
func New(ctx context.Context, url string) (*DB, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &DB{Pool: pool}, nil
}

// Close releases the pool.
func (d *DB) Close() { d.Pool.Close() }
