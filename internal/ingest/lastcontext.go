// lastcontext.go: ingest-time substitution of Wakatime `<<LAST_PROJECT>>` /
// `<<LAST_BRANCH>>` / `<<LAST_LANGUAGE>>` template tokens.
//
// WakaTime editors/apps (here: macos-wakatime tracking browser activity) send
// these tokens in the project/branch/language fields for activity with no code
// context, expecting the SERVER to substitute each with the user's LAST-KNOWN
// real value for that axis. wakatime.com does this server-side; without it the
// literal token gets stored and pollutes every aggregation. We resolve it at
// ingest so the literal never lands in the DB (the `backfill last-context`
// command fixes rows written before this shipped).
//
// Semantics, per axis independently: a placeholder is replaced by the most
// recent real (non-null, non-placeholder) value of THAT axis for the SAME
// sender at a time strictly before this heartbeat. No prior real value => the
// placeholder is dropped to nil (never stored verbatim).
package ingest

import (
	"sort"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/wakatime"
)

// batchHasLastPlaceholder reports whether ANY heartbeat in the batch carries a
// placeholder in project/branch/language. The substitution pass is skipped
// entirely when this is false, so a normal (placeholder-free) ingest is a
// byte-for-byte no-op — no DB seed query, no sorting, no field writes.
func batchHasLastPlaceholder(hbs []model.HeartbeatPayload) bool {
	for i := range hbs {
		if ptrIsLastPlaceholder(hbs[i].Project) ||
			ptrIsLastPlaceholder(hbs[i].Branch) ||
			ptrIsLastPlaceholder(hbs[i].Language) {
			return true
		}
	}
	return false
}

func ptrIsLastPlaceholder(s *string) bool {
	return s != nil && wakatime.IsLastPlaceholder(*s)
}

// substituteLastContext resolves every `<<LAST_*>>` placeholder in `enriched`
// in place. The batch is a single sender's heartbeats; seedProject/seedLanguage/
// seedBranch are that sender's last-known real values from the DB (nil if the
// sender has none) — see db.GetLastKnownContext.
//
// It walks the batch in time_sent ASC order (a sorted index view — the
// `enriched` slice itself is NOT reordered, so returned ids stay in input
// order), forward-filling a running last-known per axis seeded from the DB
// values. For each heartbeat: a placeholder axis becomes the running last-known
// (which reflects the most recent real value strictly before it — the running
// value is read BEFORE this heartbeat can advance it); a real, non-empty axis
// advances the running last-known. A placeholder with no prior real value
// resolves to nil.
func substituteLastContext(enriched []model.HeartbeatPayload, seedProject, seedLanguage, seedBranch *string) {
	order := make([]int, len(enriched))
	for i := range order {
		order[i] = i
	}
	// Stable so heartbeats sharing a time_sent keep input order — the DB seed
	// already covers the "strictly before" boundary for the batch's head.
	sort.SliceStable(order, func(a, b int) bool {
		return enriched[order[a]].TimeSent < enriched[order[b]].TimeSent
	})

	lastProject, lastLanguage, lastBranch := seedProject, seedLanguage, seedBranch
	for _, idx := range order {
		hb := &enriched[idx]
		lastProject = substituteAxis(&hb.Project, lastProject)
		lastLanguage = substituteAxis(&hb.Language, lastLanguage)
		lastBranch = substituteAxis(&hb.Branch, lastBranch)
	}
}

// substituteAxis applies one axis's rule to *field and returns the (possibly
// updated) running last-known:
//   - placeholder      => *field := running (may be nil); running unchanged
//   - nil / empty value => *field untouched; running unchanged
//   - real value        => *field untouched; running := that value
func substituteAxis(field **string, running *string) *string {
	v := *field
	if v == nil {
		return running
	}
	if wakatime.IsLastPlaceholder(*v) {
		*field = running
		return running
	}
	if *v == "" {
		return running
	}
	return v
}
