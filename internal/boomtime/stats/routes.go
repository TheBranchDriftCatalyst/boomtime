// routes.go — Echo route registrations for the stats domain
// (boom-8tn phase 6). Extracted from internal/server/server.go's
// registerStatsRoutes and the projects/active_files subset that lived
// in registerMiscRoutes / registerStatsRoutes.
//
// URL patterns are byte-identical to the pre-refactor set — this is a
// pure package move, not a route rename. The tests already assert
// specific 404s / 400s / status-code invariants against these strings;
// changing any of them is out of scope for phase 6.
//
// The DB backup routes (/db/export, /db/import) and the source-health
// route stay in internal/handler/ — they're admin/observability endpoints
// that migrate to internal/admin/ in phase 7 alongside sources.go +
// backup.go. Same for the widget/label-image routes that are in
// registerMiscRoutes but owned by widgets/curation/admin domains.
package stats

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apiroute"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
)

// Register wires the stats-domain endpoints onto e. Handler must be
// non-nil. Registration order preserves the pre-refactor sequence inside
// registerStatsRoutes + the stats-owned subset of registerMiscRoutes so
// any test that hit these routes previously still hits them in the same
// order — Echo picks the first registered matcher for overlapping
// patterns, so preserving order preserves matching. In particular
// /projects/:project must stay in the same registration slot so the
// FE's /projects call still resolves without shadowing.
//
// Route inventory (six clusters — derived health + core stats + big-bet
// aggregations + files + projects + leaderboards + commits):
//
//	GET    /api/v1/users/current/derived/status         (h.DerivedStatus)
//	POST   /api/v1/users/current/derived/resync         (h.DerivedResync)
//	GET    /api/v1/users/current/stats                  (h.Stats)
//	GET    /api/v1/users/current/timeline               (h.Timeline)
//	GET    /api/v1/users/current/statusbar/today        (h.StatusbarToday)
//	GET    /api/v1/users/current/stats/punchcard        (h.Punchcard)
//	GET    /api/v1/users/current/stats/sessions         (h.Sessions)
//	GET    /api/v1/users/current/stats/momentum         (h.Momentum)
//	GET    /api/v1/users/current/stats/ai               (h.AIActivity)
//	GET    /api/v1/users/current/stats/loc              (h.Loc)
//	GET    /api/v1/users/current/stats/health           (h.HealthActivity)
//	GET    /api/v1/users/current/workouts               (h.WorkoutList)
//	GET    /api/v1/users/current/files                  (h.ActiveFiles)
//	GET    /api/v1/users/current/projects/:project      (h.ProjectStats)
//	GET    /api/v1/projects                             (h.ProjectList)
//	GET    /api/v1/leaderboards                         (h.Leaderboards)
//	GET    /api/v1/commits/:project/report              (h.Commits)
//
// EVERY route registers through internal/shared/apiroute so the OpenAPI
// schema is generated from Go types and the prose lives at the call site.
// The dozen aggregation reads answer through apihelpers.CachedJSON, which
// serves PRE-MARSHALLED bytes on a cache hit — that is the whole point of
// it, these are the heaviest queries in the app — so they register with
// apiroute.WritesJSON: the handler keeps ownership of the write and the
// payload type is DECLARED rather than encoded by the seam. The declared
// type is always the concrete type the CachedJSON compute closure returns.
func Register(e *echo.Echo, h *Handler) {
	// Derived-data health (gap_seconds + rollup status / resync).
	// Typed seam (internal/shared/apiroute): the response TYPE is captured at
	// registration so the OpenAPI schema is generated from Go rather than
	// hand-written. Resync takes no request body — its inputs are the caller's
	// identity alone — hence POSTNoBody.
	apiroute.GET(e, "/api/v1/users/current/derived/status", h.DerivedStatus).
		Doc("Derived-data health",
			"Row counts and on-disk sizes for the precomputed gap_seconds column and the "+
				"hb_rollup_daily rollup belonging to the requesting user: how many heartbeats "+
				"have a gap populated versus missing, the rollup's second total against the raw "+
				"total, whether the two agree (inSync), the byte size of the heartbeats table, "+
				"the rollup and the whole database, and a per-index size inventory of the "+
				"heartbeats table (largest first) so the storage cost of each perf index is "+
				"visible. Read-only — it never rebuilds anything.")
	apiroute.POSTNoBody(e, "/api/v1/users/current/derived/resync", h.DerivedResync).
		Doc("Derived-data rebuild",
			"Recomputes gap_seconds and hb_rollup_daily from the requesting user's raw "+
				"heartbeats, then drops every cached dashboard aggregate for that owner so the "+
				"next read reflects the rebuilt data, and returns the refreshed status (same "+
				"shape as GET derived/status). Takes NO request body — the caller's identity is "+
				"the only input. Expensive on a large account: it rewrites derived rows across "+
				"the user's entire history.")

	// Stats
	// Stats + Timeline answer through apihelpers.CachedJSON (pre-marshalled
	// bytes on a hit), so they register via WritesJSON — the handler owns the
	// write, the payload type is declared here. StatusbarToday answers with a
	// direct c.JSON, so it takes the ordinary typed GET.
	apiroute.WritesJSON[model.StatsPayload](e, http.MethodGet, "/api/v1/users/current/stats", h.Stats).
		Doc("Dashboard rollup",
			"Attributed coding time over ?start..?end (default: the last 7 days) with totals, "+
				"a daily series, and top-N breakdowns by project, language, editor, platform, "+
				"machine and category — each capped to the busiest entries plus one aggregated "+
				"\"Other\" bucket, with the true distinct counts reported alongside. When the "+
				"owner has a cached GitHub contribution grid an OPTIONAL githubDailyTotal series "+
				"is included, aligned index-for-index to dailyTotal; the key is absent otherwise "+
				"and never blocks the response. Applies the caller's hide exclusions and rename "+
				"remaps and the optional ?space scope. ?timeLimit is the gap cutoff in minutes "+
				"(default 15); at the default the pre-aggregated daily rollup serves the query "+
				"instead of a raw heartbeat scan. Cached per owner/range/timeLimit/space/timezone "+
				"for 30s by default (BOOM_STATS_CACHE_TTL).")
	apiroute.WritesJSON[model.TimelinePayload](e, http.MethodGet, "/api/v1/users/current/timeline", h.Timeline).
		Doc("Language timeline spans",
			"Coding spans over ?start..?end (default: the last 7 days) grouped by language "+
				"under `timelineLangs`, each span carrying its project name and start/end "+
				"instants. Spans shorter than 60 seconds are dropped. Unlike the other dashboard "+
				"feeds this one applies NEITHER hide nor rename curation — only the optional "+
				"?space scope. The span keys are the legacy hakatime names (tName, tRangeStart, "+
				"tRangeEnd), kept verbatim so existing dashboards keep working. Cached 30s per "+
				"owner/range/timeLimit/space/timezone.")
	apiroute.GET(e, "/api/v1/users/current/statusbar/today", h.StatusbarToday).
		Doc("Today's grand total",
			"Wakatime-compatible status-bar payload: one humanized grand-total string (e.g. "+
				"\"2 hrs 5 mins\") for the caller's activity so far today, plus an always-empty "+
				"categories array kept for wire compatibility. \"Today\" is bounded by the user's "+
				"LOCAL midnight — their configured timezone, falling back to the server default — "+
				"not UTC midnight, so a late-evening refresh does not roll into the next "+
				"(empty) UTC day. Hidden axis values are excluded. Not cached; takes no query "+
				"parameters.")

	// Stats — big-bet aggregations (council visualizations)
	apiroute.WritesJSON[model.PunchcardPayload](e, http.MethodGet, "/api/v1/users/current/stats/punchcard", h.Punchcard).
		Doc("Punchcard intensity grid",
			"Day-of-week x hour-of-day activity cells (dow 0-6, hour 0-23) over ?start..?end "+
				"(default: the last 7 days), bucketed in the caller's LOCAL timezone rather than "+
				"UTC, together with the maximum and total seconds so the heatmap can normalize "+
				"without a second pass. Only cells with recorded activity are returned. Excludes "+
				"hidden axis values and honours the optional ?space scope; rename remaps are not "+
				"applied (the grid has no name axis). Cached 30s per "+
				"owner/range/timeLimit/space/timezone.")
	apiroute.WritesJSON[model.SessionsPayload](e, http.MethodGet, "/api/v1/users/current/stats/sessions", h.Sessions).
		Doc("Session aggregates",
			"Sessionized activity over ?start..?end (default: the last 7 days): a summary "+
				"(session count plus total / average / maximum / median duration in seconds), a "+
				"per-day series gap-filled across every day of the range, and a duration "+
				"histogram over five fixed buckets — 0–15m, 15–30m, 30–60m, 1–2h and an "+
				"open-ended 2h+. Individual sessions are NEVER returned, only aggregates. "+
				"Excludes hidden axis values and honours ?space. Cached 30s per "+
				"owner/range/timeLimit/space/timezone.")
	apiroute.WritesJSON[model.MomentumPayload](e, http.MethodGet, "/api/v1/users/current/stats/momentum", h.Momentum).
		Doc("Weekly project momentum",
			"The top ?top projects (default 8; values below 1 fall back to 8) as weekly time "+
				"series over ?start..?end (default: the last 7 days), every series aligned to one "+
				"shared `weeks` axis of ascending ISO Monday week-starts so they stack directly. "+
				"Applies hide exclusions, rename remaps and the optional ?space scope; at the "+
				"default ?timeLimit=15 the pre-aggregated daily rollup serves it. Cached 30s per "+
				"owner/range/timeLimit/top/space/timezone.")

	// boom-1l9: wakatime.com AI-assistance metrics (heartbeats.ai_*).
	apiroute.WritesJSON[db.AIActivitySummary](e, http.MethodGet, "/api/v1/users/current/stats/ai", h.AIActivity).
		Doc("AI-assistance metrics",
			"Per-day AI-assistance metrics read straight off the heartbeat columns the "+
				"wakatime plugin populates (ai_input_tokens, ai_output_tokens, ai_line_changes, "+
				"human_line_changes, ai_session): a daily series, range totals for prompt tokens "+
				"and the AI-versus-human line-change split, how many heartbeats carried any AI "+
				"signal, and the most recent subscription plan observed. Default range is the "+
				"last 30 days. When the range holds no AI-tagged heartbeats the payload comes "+
				"back with hasData=false and everything else zeroed, so the FE card can skip "+
				"render without null-checking each field. Deliberately unaffected by hide/rename "+
				"curation and by ?space — AI usage is an audit-first, cross-cutting per-user "+
				"signal. Cached 30s per owner/range/space/timezone.")

	// boom-yfg: lines-of-code (total + per-project + over-time) from file_lines.
	apiroute.WritesJSON[model.LocPayload](e, http.MethodGet, "/api/v1/users/current/stats/loc", h.Loc).
		Doc("Lines-of-code snapshot",
			"Current total lines of code plus a per-project breakdown and a bounded "+
				"total-LOC-over-time growth curve, all derived from heartbeats.file_lines with "+
				"generated and vendored files filtered out in SQL. There is NO GitHub dependency "+
				"— this is entirely local heartbeat data. Default range is the last 7 days. "+
				"Hidden axis values are excluded and the optional ?space scope applies; rename "+
				"remaps are NOT applied, because LOC groups on the raw project name. An owner "+
				"with no file-lines data gets totalLoc=0 with empty (never null) perProject and "+
				"overTime arrays rather than an error, so the FE renders a gentle empty state. "+
				"Cached 30s per owner/range/space/timezone.")

	// HealthKit metrics feed (Wellness card + Wellness page).
	apiroute.WritesJSON[model.HealthActivityPayload](e, http.MethodGet, "/api/v1/users/current/stats/health", h.HealthActivity).
		Doc("Wellness metrics feed",
			"Per-day HealthKit aggregates powering the Wellness card and page: workout count "+
				"and minutes, active kilocalories, steps, average and resting heart rate, sleep "+
				"minutes, HRV (SDNN) and mindful minutes — plus a `totals` block of the same "+
				"shape covering the whole range. Default range is the last 30 days. An empty "+
				"range answers hasData=false so the card can skip render. Not affected by "+
				"hide/rename curation or by ?space scoping; health metrics are cross-cutting "+
				"personal signals, same as the AI feed. Cached 30s per "+
				"owner/range/space/timezone.")

	// Per-workout event list + per-label breakdown (Wellness events breakdown).
	apiroute.WritesJSON[model.WorkoutListPayload](e, http.MethodGet, "/api/v1/users/current/workouts", h.WorkoutList).
		Doc("Workout events and label breakdown",
			"RAW per-workout event rows — heartbeat id, the underlying HealthKit activity "+
				"kind, the user-chosen label (falling back to the kind when unset), start instant "+
				"as unix seconds, duration, and optional kilocalories / average heart rate / "+
				"distance — alongside a per-label aggregate (workout count, total minutes, total "+
				"kilocalories, and an average-of-averages heart rate). Rows, not aggregates, is "+
				"why this is not under /stats/. Default range is the last 30 days; an empty "+
				"range answers hasData=false. This is the READ side only — POST to the same path "+
				"ingests workouts. Cached 30s per owner/range/space/timezone.")

	// Cross-project active files (shared lynchpins spanning multiple projects)
	apiroute.WritesJSON[model.ActiveFilesPayload](e, http.MethodGet, "/api/v1/users/current/files", h.ActiveFiles).
		Doc("Cross-project active files",
			"The busiest FILES across ALL of the owner's projects, each with its attributed "+
				"seconds and the number of DISTINCT projects that touch it — a file with "+
				"projects > 1 is a shared interface or lynchpin. Rows arrive ordered "+
				"lynchpins-first (project count descending, then seconds descending). ?limit "+
				"defaults to 20 and is clamped into 1..100; `truncated` reports whether the cap "+
				"cut rows off. Hide exclusions and rename remaps are applied BEFORE the "+
				"distinct-project count so the tally matches the dashboards, and ?space applies. "+
				"Default range is the last 7 days. Cached 30s per "+
				"owner/range/timeLimit/limit/space/timezone.")

	// Projects
	apiroute.WritesJSON[model.ProjectStatistics](e, http.MethodGet, "/api/v1/users/current/projects/:project", h.ProjectStats).
		Doc("Per-project statistics",
			"Everything the project detail page needs for one project over ?start..?end "+
				"(default: the last 7 days): total and per-day seconds, language / file / weekday "+
				"/ hour segments, a per-day-per-language matrix aligned to the same day axis as "+
				"the daily total, the authoring-versus-reading second split, a branch breakdown "+
				"and a per-day distinct-entity breadth series. Segment lists are capped to a "+
				"top-N plus one aggregated \"Other (N more)\" bucket, with the true distinct "+
				"counts reported separately. The :project path segment is a DISPLAY name: rename "+
				"rules resolve first, so a merged or regex-derived name aggregates all its source "+
				"projects; a name the caller does not own answers 404. The language segment "+
				"excludes rows whose heartbeat had no language and the file segment counts only "+
				"real files (ty='file' with an entity), while the totals still sum every row so "+
				"tracked time stays honest. Applies hide exclusions and ?space. Cached 30s per "+
				"owner/project/range/timeLimit/space/timezone.")
	apiroute.GET(e, "/api/v1/projects", h.ProjectList).
		Doc("Project name list",
			"Flat list of the owner's project DISPLAY names that have activity in "+
				"?start..?end (default: the last 30 days) — the picker feed, not a statistics "+
				"endpoint. Hidden values are excluded, so a project appears only if it has "+
				"non-hidden heartbeats in the window, and rename remaps are applied, so merged "+
				"projects collapse to a single name. The optional ?space scope applies. NOT "+
				"cached: the curation sets load eagerly on every call.")

	// Leaderboards
	apiroute.WritesJSON[model.LeaderboardsPayload](e, http.MethodGet, "/api/v1/leaderboards", h.Leaderboards).
		Doc("Cross-user leaderboards",
			"Total attributed seconds per user across EVERY user on the instance for "+
				"?start..?end (default: the last 30 days) — one global ranking plus a ranking per "+
				"language. The gap cutoff is fixed at 15 minutes here; ?timeLimit is ignored and "+
				"is not part of the cache key. The requester's own hide exclusions, rename remaps "+
				"and ?space scope apply to THEIR OWN rows only, other users' rows pass through "+
				"untouched — which makes the response per-requester, so it is cached per "+
				"owner/range/space/timezone for 30s.")

	// Commits
	apiroute.GET(e, "/api/v1/commits/:project/report", h.Commits).
		Doc("Commit report with attributed time",
			"Recent commits for a GitHub repository, annotated with the coding time boomtime "+
				"attributed to :project in the window between each pair of consecutive commits. "+
				"?repoName, ?repoOwner and ?user are all REQUIRED — a missing one is a 400. "+
				"?limit caps the returned commits (default 40); one extra commit is fetched "+
				"internally because the oldest commit in a page has no predecessor to measure "+
				"from. Only the named user's non-merge commits take part in the attribution, but "+
				"every fetched commit is returned, those without a computed window simply "+
				"carrying no totalSeconds. Calls api.github.com INLINE on every request (15s "+
				"timeout, no caching) using the server's configured GitHub token — a missing "+
				"token or a failed upstream call is reported as a 500.")
}
