// helpers.go: shared package-internal helpers for the awards HTTP
// endpoints. The public-profile window constants + timezone resolver
// now live in shared homes (internal/identity for the profile-shape
// constants — canonical because /p/:slug is the endpoint that
// RENDERS this window; internal/apihelpers for ResolveUserTZ) so a
// drift here vs. there can never happen (gaka-hc6.3 / gaka-dg7).
//
// The local unexported aliases below keep eval.go / backfill.go reading
// as it did pre-collapse — a call site that says
// `publicProfilePayloadDays` still resolves to the SAME 60 used by the
// identity endpoint.
package awards

import (
	"github.com/TheBranchDriftCatalyst/boomtime/internal/identity"
)

// publicProfilePayloadDays / publicProfileTimeLimit alias the exported
// constants that live on internal/identity. Identity is the CANONICAL
// owner because it renders the same window at GET /p/:slug; awards
// evaluates the same payload for the streaks mirror and the label
// evaluator, so both must read one number.
const (
	publicProfilePayloadDays       = identity.PublicProfilePayloadDays
	publicProfileTimeLimit   int64 = identity.PublicProfileTimeLimit
)
