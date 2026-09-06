package stats

import (
	"context"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/labstack/echo/v5"
)

// dashLoad selects which curation sets a dashboard handler needs (space
// membership is always resolved by load()). Not every handler applies every
// set — e.g. Timeline applies neither hide nor rename — so each handler
// declares exactly what it used to load.
type dashLoad int

const (
	loadNone    dashLoad = 0
	loadHidden  dashLoad = 1 << 0
	loadRenames dashLoad = 1 << 1
)

// dashboardScope carries the cheap, eager per-request parts shared by the
// dashboard read handlers: the resolved owner, the start/end range, the
// timeLimit param, and the raw ?space= param (used verbatim in cache keys).
// The expensive curation/space lookups live in load(), invoked lazily inside
// the cachedJSON compute closure so a cache hit skips them entirely.
type dashboardScope struct {
	h          *Handler
	ctx        context.Context
	owner      string
	t0, t1     time.Time
	limit      int64
	spaceParam string
	// tz (boom-dg7) is the resolved IANA name for the owner (never ""): a
	// SINGLE lookup at scope-construction time then threaded through every
	// SQL that extracts dow/hour/date from time_sent. If a handler talks to
	// multiple TZ-sensitive queries in one request (Stats -> activity +
	// categories, for example), all of them see the same resolved zone so
	// their day series line up.
	tz string
}

// maxDashboardSpan bounds how far back a single dashboard read may reach.
//
// The dashboard payload builders gap-fill ONE ENTRY PER CALENDAR DAY in the
// requested range (genDates → ToStatsPayload / ToProjectStatistics /
// ToSessionsPayload). ?start and ?end are unvalidated client input parsed by
// the "2006-01-02" layout, so `?start=0001-01-01&end=9999-12-31` asked for
// ~3.65 MILLION days: an ~88 MB []time.Time, a same-order SessionDaily slice
// (ToSessionsPayload has no clampStartToData to save it), and a JSON body to
// match — all of it then stored in the in-process response cache. One
// authenticated request per pod was enough to matter.
//
// Clamping the SPAN (rather than pinning either endpoint) is what keeps the
// legitimate "All time" case working: it preserves `end`, so the window that
// survives is the most recent one, which is where every user's data actually
// is. 25 years is far beyond any real coding history while capping the day
// series at ~9k entries. StartDate in the response reflects the clamp, so the
// FE renders a truthful axis rather than a silently truncated one.
const maxDashboardSpan = 25 * 365 * 24 * time.Hour

// dashboardScope resolves the requesting user and the common dashboard query
// params. days picks the default range window (7 = week, 30 = month).
func (h *Handler) dashboardScope(c *echo.Context, days int) (*dashboardScope, *apierr.Error) {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return nil, aerr
	}
	t0, t1 := apihelpers.DefaultRange(c, days)
	if t1.Sub(t0) > maxDashboardSpan {
		t0 = t1.Add(-maxDashboardSpan)
	}
	ctx := c.Request().Context()
	return &dashboardScope{
		h:          h,
		ctx:        ctx,
		owner:      owner,
		t0:         t0,
		t1:         t1,
		limit:      apihelpers.TimeLimit(c),
		spaceParam: c.QueryParam("space"),
		// boom-dg7: single lookup, one place per request. resolveUserTZ never
		// returns "" so all downstream $tz bindings are safe.
		tz: apihelpers.ResolveUserTZ(h.DB, h.Logger, ctx, owner, h.Cfg.DefaultTimezoneValue()),
	}, nil
}

// cacheKey builds the handler's cache key from the given middle parts, always
// terminated with the "space:<param>" and "tz:<name>" components. The key
// format (same parts, same order) is behavior — keep it stable.
//
// boom-dg7: tz is part of the key so a TZ change flips buckets to a distinct
// cache slot instead of serving pre-change UTC buckets under a hot key. The
// PATCH endpoint also fires invalidateOwnerCache, so this is defense-in-depth.
func (s *dashboardScope) cacheKey(name string, parts ...any) string {
	return apihelpers.CacheKey(s.owner, name, append(parts, "space:"+s.spaceParam, "tz:"+s.tz)...)
}

// dashSets is the lazily loaded query-time scoping data: hide exclusions and
// rename remaps (both reversible; audit views stay unfiltered/un-remapped),
// plus the optional ?space= membership scope.
type dashSets struct {
	hidden         db.HiddenSets
	renames        db.RenameSets
	members        db.MemberSets
	spaceRequested bool
}

// load fetches the requested curation sets (in the fixed hidden→renames→space
// order) plus the space scope. Call it INSIDE the cachedJSON compute closure
// so cache hits skip the queries.
func (s *dashboardScope) load(sets dashLoad) (dashSets, error) {
	var out dashSets
	var err error
	if sets&loadHidden != 0 {
		if out.hidden, err = s.h.DB.LoadHiddenSets(s.ctx, s.owner); err != nil {
			return out, err
		}
	}
	if sets&loadRenames != 0 {
		if out.renames, err = s.h.DB.LoadRenameSets(s.ctx, s.owner); err != nil {
			return out, err
		}
	}
	// s.owner is threaded through so LoadSpace can reject another user's space
	// id (audit 2026-09-06: ?space= was applied with no ownership check).
	if out.members, out.spaceRequested, err = apihelpers.LoadSpace(s.h.DB, s.ctx, s.owner, s.spaceParam); err != nil {
		return out, err
	}
	return out, nil
}
