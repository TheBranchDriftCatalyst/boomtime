// helpers.go: shared package-internal helpers for the awards HTTP
// endpoints. The constants + timezone resolver here mirror the same
// helpers on internal/handler.Handler (see internal/handler/timezone.go
// and internal/handler/profile.go) — they live locally in the awards
// package so this domain doesn't depend on the god-type handler while
// the phase-4a identity extraction is still in flight. Once identity
// lands, resolveUserTZ + these constants may be centralized (candidate
// for internal/apihelpers/ or a shared payload package).
package awards

import (
	"context"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
)

// publicProfilePayloadDays is the default window for the public dashboard
// payload the awards evaluator reads. Mirror of the constant in
// internal/handler/profile.go — MUST stay in sync so a label seen on
// /p/:slug and a label seen on /awards are computed from the same data
// (gaka-hc6.3 invariant).
const publicProfilePayloadDays = 60

// publicProfileTimeLimit locks the aggregation to the app default (15-min
// gap). Mirror of the constant in internal/handler/profile.go. The public
// payload does not accept a timeLimit override — it would fragment the
// (currently uncached) response space and expose a knob a public dashboard
// doesn't need.
const publicProfileTimeLimit int64 = 15

// resolveUserTZ returns the effective IANA name for a user's dow/hour/date
// buckets. NEVER returns "" — safe to thread into an AT TIME ZONE bind
// param without further guarding. Mirror of the method on
// internal/handler.Handler (gaka-dg7): the 3-level resolution chain
// (user > env default > UTC) is enforced in one place so it can't be
// half-applied as new endpoints get added.
func (h *Handler) resolveUserTZ(ctx context.Context, owner string) string {
	userTZ, err := h.DB.GetUserTimezone(ctx, owner)
	if err != nil {
		h.Logger.Warn("resolveUserTZ: users.timezone lookup failed; falling back to defaults",
			"user", owner, "err", err)
		userTZ = ""
	}
	return db.ResolveTimezone(userTZ, h.Cfg.DefaultTimezone)
}

// removeDays subtracts n days from t, snapped to UTC midnight. Mirror of
// the shared handler-package helper (see internal/handler/handler.go).
// Local copy avoids importing the parent god-type just for one function.
func removeDays(t time.Time, n int) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -n)
}
