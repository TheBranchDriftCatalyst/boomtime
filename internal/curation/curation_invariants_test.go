// curation_invariants_test.go — invariants and security gaps flagged during
// the critique review for the curation / spaces / labels handler cluster
// (gaka-d6x.handler). Each spec here traces to a specific critique item —
// the top-of-file comment naming the gap it closes, and inline comments
// explaining WHY the assertion isn't tautological (i.e. what regression it
// would catch that no other spec would).
//
// The specs are grouped by the endpoint they defend:
//
//   - Curation: cache-invalidation, oversize-body cap (413), empty-body,
//     cross-user re-insert leak, SQL-injection-in-matchValue smoke,
//     404-body-indistinguishable check.
//   - Spaces:   cache-invalidation, GetSpace shape TYPE assertions,
//     whitespace/casing/oversize matchValue guards.
//   - Labels:   AdminUpdateLabelGenConfig round-trip via seed.sql dump,
//     sqlStr escaping in every string-typed catalog column, LabelsCatalog
//     no-header-leak invariant.
package curation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

// -----------------------------------------------------------------------
// Curation: cache invalidation (missing invariant — handler comments
// explicitly claim invalidateOwnerCache runs; no spec verified the effect).
// -----------------------------------------------------------------------

var _ = Describe("Curation writes invalidate the owner cache (gaka-d6x.handler)", func() {
	It("CreateCuration drops the owner's cached aggregation prefix", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("cur_cache_create")

		// Prime the cache with an entry keyed by "<owner>|..." — the exact
		// key shape invalidateOwnerCache uses (see handler.go
		// invalidateOwnerCache: c.Cache.InvalidatePrefix(owner + "|")).
		primeKey := user + "|stats|synthetic"
		hz.H.Cache.Set(primeKey, []byte(`{"synthetic":true}`))
		blob, ok := hz.H.Cache.Get(primeKey)
		Expect(ok).To(BeTrue(), "cache seed failed — precondition")
		Expect(blob).NotTo(BeEmpty())

		// A curation POST must invalidate the owner's cache prefix.
		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
			"axis": "language", "action": "hide", "matchType": "exact", "matchValue": "Elm",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())

		_, ok = hz.H.Cache.Get(primeKey)
		Expect(ok).To(BeFalse(),
			"CreateCuration MUST invalidate <owner>| cache prefix — a stale-cache regression would silently leave dashboards showing pre-rule aggregations")
	})

	It("ApplyRename drops the owner's cached aggregation prefix", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("cur_cache_apply")
		seedRenameableHeartbeats(hz, user)
		newVal := "python"
		id := createRule(e, token, map[string]any{
			"axis": "language", "action": "rename", "matchType": "exact",
			"matchValue": "Python", "newValue": newVal,
		})

		// Re-seed the cache AFTER create (create itself invalidates) so we
		// isolate the apply-path invalidation.
		primeKey := user + "|stats|apply-test"
		hz.H.Cache.Set(primeKey, []byte(`x`))
		_, ok := hz.H.Cache.Get(primeKey)
		Expect(ok).To(BeTrue())

		rec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/curation/"+strconv.Itoa(id)+"/apply", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())

		_, ok = hz.H.Cache.Get(primeKey)
		Expect(ok).To(BeFalse(),
			"ApplyRename MUST invalidate the owner's cache — dashboards would otherwise show pre-rewrite aggregations for 30s")
	})

	It("PurgeHidden drops the owner's cached aggregation prefix", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("cur_cache_purge")
		seedRenameableHeartbeats(hz, user)
		id := createRule(e, token, map[string]any{
			"axis": "language", "action": "hide", "matchType": "exact", "matchValue": "Python",
		})

		primeKey := user + "|stats|purge-test"
		hz.H.Cache.Set(primeKey, []byte(`x`))
		_, ok := hz.H.Cache.Get(primeKey)
		Expect(ok).To(BeTrue())

		rec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/curation/"+strconv.Itoa(id)+"/purge", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())

		_, ok = hz.H.Cache.Get(primeKey)
		Expect(ok).To(BeFalse(),
			"PurgeHidden MUST invalidate the owner's cache — dashboards would otherwise render the purged rows for 30s")
	})

	It("cross-owner cache is NOT dropped by another owner's curation write (prefix scoping)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		userA, tokenA := hz.MintUser("cur_cache_iso_a")
		userB, _ := hz.MintUser("cur_cache_iso_b")

		aKey := userA + "|stats|iso"
		bKey := userB + "|stats|iso"
		hz.H.Cache.Set(aKey, []byte(`a`))
		hz.H.Cache.Set(bKey, []byte(`b`))

		// User A writes a curation rule.
		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/curation", tokenA, map[string]any{
			"axis": "language", "action": "hide", "matchType": "exact", "matchValue": "Prolog",
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())

		_, aStill := hz.H.Cache.Get(aKey)
		_, bStill := hz.H.Cache.Get(bKey)
		Expect(aStill).To(BeFalse(), "user A's cache MUST be invalidated")
		Expect(bStill).To(BeTrue(),
			"user B's cache MUST NOT be invalidated by user A's write — prefix scoping violation")
	})
})

// -----------------------------------------------------------------------
// Curation: BindJSONWithLimit oversize-body cap (413) — missing invariant.
// The edge test file only covers malformed JSON; the size cap itself was
// never asserted. gaka-bi2 wired this specifically as a DoS guard.
// -----------------------------------------------------------------------

var _ = Describe("Curation body-size caps (gaka-bi2)", func() {
	It("CreateCuration returns 413 when the body exceeds BodyLimitMedium (64 KiB)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("cur_oversize_create")

		// Build a payload whose serialized size is > 64 KiB. The pattern
		// itself is a regex we know will validate (any-char). The bulk of
		// the payload is padding on the matchValue field.
		giant := strings.Repeat("A", 128*1024)
		body := map[string]any{
			"axis": "language", "action": "hide",
			"matchType": "exact", "matchValue": giant,
		}
		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/curation", token, body)
		Expect(rec).To(testutil.HaveStatus(http.StatusRequestEntityTooLarge),
			"oversize CreateCuration body must be exactly 413 (BindJSONWithLimit cap), got %d body=%s",
			rec.Code, rec.Body.String())
	})

	It("ToggleCuration returns 413 when the body exceeds BodyLimitSmall (4 KiB)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("cur_oversize_toggle")
		id := createRule(e, token, map[string]any{
			"axis": "language", "action": "hide", "matchType": "exact", "matchValue": "Zig",
		})
		giant := strings.Repeat("x", 8*1024)
		body := map[string]any{"enabled": true, "pad": giant}
		rec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/curation/"+strconv.Itoa(id)+"/toggle", token, body)
		Expect(rec).To(testutil.HaveStatus(http.StatusRequestEntityTooLarge),
			"oversize toggle body must be exactly 413 (BindJSONWithLimit cap), got %d", rec.Code)
	})
})

// -----------------------------------------------------------------------
// Curation: empty-body POST behavior (missing invariant — asserts the
// field-required guard fires on {} rather than silently succeeding).
// -----------------------------------------------------------------------

var _ = Describe("CreateCuration empty-body POST", func() {
	It("returns 400 on an empty JSON object (matchValue required guard fires)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("cur_empty_post")
		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/curation", token,
			map[string]any{})
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"empty POST body must 400 via the axis/action/matchValue guards — a change to BindJSONWithLimit that swallowed EOF silently would reach the DB with an empty struct")
	})
})

// -----------------------------------------------------------------------
// CreateCuration cross-user re-insert leak (missing invariant — the ON
// CONFLICT branch is scoped by (sender,...), but the handler-layer
// invariant should be pinned).
// -----------------------------------------------------------------------

var _ = Describe("CreateCuration re-insert cross-user scoping (gaka-dfd)", func() {
	It("user A re-POSTing the SAME rule as user B's disabled rule does NOT flip user B's enabled=true", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, tokenA := hz.MintUser("cur_reins_iso_a")
		_, tokenB := hz.MintUser("cur_reins_iso_b")

		// User B creates + disables a rule.
		bID := createRule(e, tokenB, map[string]any{
			"axis": "language", "action": "hide", "matchType": "exact", "matchValue": "COBOL",
		})
		tog := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/curation/"+strconv.Itoa(bID)+"/toggle", tokenB,
			map[string]any{"enabled": false})
		Expect(tog).To(testutil.HaveStatus(http.StatusOK))

		// User A creates the SAME rule shape (axis+action+matchValue+matchType).
		aID := createRule(e, tokenA, map[string]any{
			"axis": "language", "action": "hide", "matchType": "exact", "matchValue": "COBOL",
		})
		// Ids MUST differ — dedup is (sender,...)-scoped, not global.
		Expect(aID).NotTo(Equal(bID),
			"per-owner uniqueness: user A's dedup MUST NOT reuse user B's rule id")

		// And user B's rule stays disabled (A's create did NOT touch it).
		listB := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/curation", tokenB, nil)
		var outB struct {
			Rules []db.CurationRule `json:"rules"`
		}
		Expect(json.Unmarshal(listB.Body.Bytes(), &outB)).To(Succeed())
		var bRule *db.CurationRule
		for i := range outB.Rules {
			if outB.Rules[i].ID == bID {
				bRule = &outB.Rules[i]
			}
		}
		Expect(bRule).NotTo(BeNil(), "user B's own rule vanished")
		Expect(bRule.Enabled).To(BeFalse(),
			"user A's identical-shape POST MUST NOT flip user B's rule enabled=true — cross-user ON CONFLICT leak")
	})
})

// -----------------------------------------------------------------------
// SECURITY: SQL-injection smoke on matchValue (defensive test). The DB
// layer parameterizes correctly, but a defensive spec pins that no future
// SQL-string concatenation regression breaks the boundary — especially
// warranted because the seed.sql dumper builds SQL by string concat.
// -----------------------------------------------------------------------

var _ = Describe("CreateCuration matchValue rejects SQL-injection payload as literal (defensive)", func() {
	It("stores a matchValue containing a SQL comment/DROP as an opaque string literal, no SQL executed", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("cur_sqli_defense")

		payload := `'; DROP TABLE heartbeats; --`
		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/curation", token, map[string]any{
			"axis": "language", "action": "hide", "matchType": "exact", "matchValue": payload,
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())

		// The rule row MUST carry the literal string — no truncation, no escape leak.
		listRec := doJSONReqG(e, http.MethodGet, "/api/v1/users/current/curation", token, nil)
		Expect(listRec).To(testutil.HaveStatus(http.StatusOK))
		var out struct {
			Rules []db.CurationRule `json:"rules"`
		}
		Expect(json.Unmarshal(listRec.Body.Bytes(), &out)).To(Succeed())
		found := false
		for _, r := range out.Rules {
			if r.MatchValue == payload {
				found = true
			}
		}
		Expect(found).To(BeTrue(),
			"matchValue must round-trip as the LITERAL string %q (no SQL executed, no truncation)", payload)

		// And the heartbeats table still exists / is accessible — a
		// successful DROP would make the next query 500.
		var n int64
		err := hz.DB.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM heartbeats WHERE sender=$1`, user).Scan(&n)
		Expect(err).NotTo(HaveOccurred(),
			"heartbeats table missing after crafted matchValue — SQL injection succeeded")
	})
})

// -----------------------------------------------------------------------
// SECURITY: 404 body indistinguishable across "does not exist" vs
// "exists but not mine" for the destructive cross-user paths. A body
// text difference would let user B distinguish rule-owned-by-A from
// rule-does-not-exist, defeating the 404-instead-of-403 design.
// -----------------------------------------------------------------------

var _ = Describe("Cross-user 404 body is indistinguishable from a genuine 404-not-found", func() {
	It("cross-user apply vs ghost-id apply produce byte-identical 404 bodies", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, tokenA := hz.MintUser("cur_404body_a")
		_, tokenB := hz.MintUser("cur_404body_b")
		newVal := "python"
		id := createRule(e, tokenA, map[string]any{
			"axis": "language", "action": "rename", "matchType": "exact",
			"matchValue": "Python", "newValue": newVal,
		})

		// User B tries to apply user A's rule → 404.
		crossRec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/curation/"+strconv.Itoa(id)+"/apply", tokenB, nil)
		Expect(crossRec).To(testutil.HaveStatus(http.StatusNotFound))

		// A genuinely nonexistent id for user B → 404.
		ghostRec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/curation/8888888/apply", tokenB, nil)
		Expect(ghostRec).To(testutil.HaveStatus(http.StatusNotFound))

		Expect(crossRec.Body.String()).To(Equal(ghostRec.Body.String()),
			"cross-user 404 body MUST be byte-identical to ghost-id 404 body — otherwise user B can distinguish 'rule owned by another user' from 'rule does not exist' via body diff (defeats the 404-instead-of-403 design)")
	})

	It("cross-user preview vs ghost-id preview produce byte-identical 404 bodies", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, tokenA := hz.MintUser("cur_404body_prv_a")
		_, tokenB := hz.MintUser("cur_404body_prv_b")
		id := createRule(e, tokenA, map[string]any{
			"axis": "language", "action": "hide", "matchType": "exact", "matchValue": "Erlang",
		})
		crossRec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/curation/"+strconv.Itoa(id)+"/preview", tokenB, nil)
		Expect(crossRec).To(testutil.HaveStatus(http.StatusNotFound))
		ghostRec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/curation/8888888/preview", tokenB, nil)
		Expect(ghostRec).To(testutil.HaveStatus(http.StatusNotFound))
		Expect(crossRec.Body.String()).To(Equal(ghostRec.Body.String()),
			"cross-user preview 404 body MUST match ghost-id 404 body byte-for-byte")
	})
})

// -----------------------------------------------------------------------
// SPACES: cache invalidation on write paths (missing invariant).
// -----------------------------------------------------------------------

var _ = Describe("Spaces writes invalidate the owner cache", func() {
	It("CreateSpace drops the owner's cache prefix", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("sp_cache_create")
		primeKey := user + "|stats|sp"
		hz.H.Cache.Set(primeKey, []byte(`x`))

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/spaces", token,
			map[string]any{"name": "cache-test"})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())

		_, ok := hz.H.Cache.Get(primeKey)
		Expect(ok).To(BeFalse(),
			"CreateSpace MUST invalidate the owner's cache")
	})
})

// createSpaceH — local mirror of the helper originally colocated in
// spaces_http_test.go (moved to internal/spaces/ in gaka-8tn phase 2a).
// Kept here so the cross-domain shape check below stays in the
// handler_test package without pulling in the spaces test binary.
func createSpaceH(e http.Handler, token, name string) int {
	rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/spaces", token,
		map[string]any{"name": name})
	ExpectWithOffset(1, rec).To(testutil.HaveStatus(http.StatusOK),
		"create space %q: body=%s", name, rec.Body.String())
	var out struct {
		Space struct {
			ID int `json:"id"`
		} `json:"space"`
	}
	ExpectWithOffset(1, json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
	ExpectWithOffset(1, out.Space.ID).NotTo(BeZero())
	return out.Space.ID
}

// -----------------------------------------------------------------------
// SPACES: GetSpace shape TYPE assertions (missing invariant — was only
// checking key presence, not the type of `position` or `rules[].axis`).
// -----------------------------------------------------------------------

var _ = Describe("GetSpace shape TYPE checks (wire contract)", func() {
	It("`position` is a JSON number (not a string) and `rules[].axis` is a string", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("sp_shape_types")
		id := createSpaceH(e, token, "shape-check")
		// Add a rule so rules[] is non-empty.
		addRec := doJSONReqG(e, http.MethodPost,
			"/api/v1/users/current/spaces/"+strconv.Itoa(id)+"/rules", token,
			map[string]any{"axis": "language", "matchType": "exact", "matchValue": "Go"})
		Expect(addRec).To(testutil.HaveStatus(http.StatusOK))

		rec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/spaces/"+strconv.Itoa(id), token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		var got map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &got)).To(Succeed())

		// json.Unmarshal into any decodes numbers as float64. A regression
		// that turned position into a string would fail this cast — key
		// presence alone wouldn't catch it.
		_, posIsNumber := got["position"].(float64)
		Expect(posIsNumber).To(BeTrue(),
			"`position` must be a JSON number, got %T (value=%v)", got["position"], got["position"])

		rules, ok := got["rules"].([]any)
		Expect(ok).To(BeTrue(), "`rules` must be a JSON array")
		Expect(rules).NotTo(BeEmpty())
		firstRule, ok := rules[0].(map[string]any)
		Expect(ok).To(BeTrue(), "rules[0] must be a JSON object")
		axis, axisIsString := firstRule["axis"].(string)
		Expect(axisIsString).To(BeTrue(),
			"rules[0].axis must be a string, got %T (value=%v)", firstRule["axis"], firstRule["axis"])
		Expect(axis).To(Equal("language"),
			"rules[0].axis must round-trip the seeded value — a rename axis→field would fail this")
	})
})

// -----------------------------------------------------------------------
// SPACES: name whitespace-only / oversize / preview edge cases (missing).
// -----------------------------------------------------------------------

var _ = Describe("CreateSpace input hardening", func() {
	It("returns 413 on an oversized name (BodyLimitSmall = 4 KiB)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("sp_oversize_name")
		giantName := strings.Repeat("A", 8*1024)
		rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/spaces", token,
			map[string]any{"name": giantName})
		Expect(rec).To(testutil.HaveStatus(http.StatusRequestEntityTooLarge),
			"oversized CreateSpace body must 413 via BindJSONWithLimit — a 100 KiB name should never reach the DB")
	})
})

var _ = Describe("SpacePreview matchValue casing + whitespace edge cases", func() {
	It("rejects an unknown matchType even when it differs only in casing (`Exact` vs `exact`)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("sp_prv_casing")
		// The handler does strict-equality checks against db.MatchExact;
		// a helpful-but-wrong strings.ToLower would silently accept "Exact".
		q := "axis=project&matchType=Exact&matchValue=x"
		rec := doJSONReqG(e, http.MethodGet,
			"/api/v1/users/current/spaces/preview?"+q, token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"matchType casing must be exact-case-sensitive — a strings.ToLower regression would silently accept `Exact`")
	})
})

// -----------------------------------------------------------------------
// LABELS: gen-config round-trip via seed.sql dump (missing invariant —
// AdminUpdateLabelGenConfig round-trip only via the public catalog, not
// through the seed dumper which is the operator-edit capture path).
// -----------------------------------------------------------------------

var _ = Describe("AdminUpdateLabelGenConfig round-trips into the seed.sql dump", func() {
	It("value set via PATCH appears (properly escaped) in the AdminLabelsSeedSQL dump", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("lb_gc_seed_rt")
		grantAdmin(hz, user)
		// Include a single quote so we also assert sqlStr escaping in the
		// systemPrompt column path.
		prompt := "operator's prompt — meowify all"
		defer func() {
			_ = hz.DB.SetGenConfig(context.Background(), "")
		}()

		rec := doJSONReqG(e, http.MethodPatch, "/api/v1/admin/label-gen-config", token,
			map[string]any{"systemPrompt": prompt})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())

		dumpRec := doJSONReqG(e, http.MethodGet, "/api/v1/admin/labels/seed.sql", token, nil)
		Expect(dumpRec).To(testutil.HaveStatus(http.StatusOK))
		// Expect the escaped form (single quote → double single quote) —
		// the seed-dump promise is "operator edits get captured as
		// reviewable code" and a stale-cache regression on GetGenConfig
		// would break exactly this path even though the public-catalog
		// round-trip still worked.
		Expect(dumpRec.Body.String()).To(ContainSubstring("'operator''s prompt — meowify all'"),
			"AdminLabelsSeedSQL dump MUST reflect the freshly-PATCHed systemPrompt — a stale-cache bug on GetGenConfig would silently break the operator-edit capture promise")
	})
})

// -----------------------------------------------------------------------
// SECURITY: sqlStr escaping in EVERY string-typed catalog column
// (kind, glyph, description, optimizedPrompt, tier) — not just `label`.
// -----------------------------------------------------------------------

var _ = Describe("AdminLabelsSeedSQL escapes single quotes in EVERY string field", func() {
	It("id / label / glyph / description / optimizedPrompt / tier all get proper sqlStr escaping", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("lb_seed_all_quotes")
		grantAdmin(hz, user)
		// Note: `kind` is constrained to a fixed enum (labels_kind_check),
		// so a quote-in-kind fixture would be rejected by the check
		// constraint before ever reaching the dumper. Every OTHER string
		// column is free TEXT and IS covered here.
		id := "id's-" + time.Now().Format("150405.000000000")
		cleanupLabel(hz, id)

		body := map[string]any{
			"id":              id,
			"kind":            "tier", // must satisfy labels_kind_check
			"label":           "label's",
			"glyph":           "g's",
			"description":     "desc's",
			"optimizedPrompt": "prompt's",
			"rank":            10,
			"tier":            "tier's",
			"condition":       json.RawMessage(`{"kind":"axis-time","axis":"languages","value":"go","op":">=","hours":1}`),
		}
		cRec := doJSONReqG(e, http.MethodPost, "/api/v1/admin/labels", token, body)
		Expect(cRec).To(testutil.HaveStatus(http.StatusCreated), "body=%s", cRec.Body.String())

		rec := doJSONReqG(e, http.MethodGet, "/api/v1/admin/labels/seed.sql", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		dump := rec.Body.String()

		// Every column must be doubly-escaped ('' inside a '...' literal).
		for label, escaped := range map[string]string{
			"label":           "'label''s'",
			"glyph":           "'g''s'",
			"description":     "'desc''s'",
			"optimizedPrompt": "'prompt''s'",
			"tier":            "'tier''s'",
		} {
			Expect(dump).To(ContainSubstring(escaped),
				"column %s: dump missing well-escaped literal %s — an unescaped quote here would break the seed's SQL", label, escaped)
		}
		// The id column also carries a quote in this fixture.
		Expect(dump).To(ContainSubstring("'id''s-"),
			"column id: dump missing well-escaped literal 'id''s-... — an unescaped quote here would break the seed")

		// Structural sanity: an unescaped quote at end-of-line signals a
		// broken emission (a hanging literal), regardless of which column
		// leaked it.
		Expect(dump).NotTo(ContainSubstring(" '\n"),
			"a lone unescaped trailing single quote at end-of-line signals broken emission")
	})
})

// -----------------------------------------------------------------------
// SECURITY: LabelsCatalog does NOT leak transport secrets (Set-Cookie,
// Authorization) in the public response — the endpoint IS unauthenticated
// (by design), so any reflected credential material is a defense-in-depth
// bug.
// -----------------------------------------------------------------------

var _ = Describe("LabelsCatalog does not leak transport secrets in the response", func() {
	It("response body contains no Set-Cookie / Authorization / Bearer / api-key markers", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		// A crafted request carrying a Cookie + Authorization — the public
		// catalog handler MUST NOT reflect them.
		req := httptest.NewRequest(http.MethodGet, "/api/v1/labels/catalog", bytes.NewReader(nil))
		req.Header.Set("Cookie", "session=super-secret-cookie-value")
		req.Header.Set("Authorization", "Bearer super-secret-token-value")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))

		body := rec.Body.String()
		for _, forbidden := range []string{
			"super-secret-cookie-value",
			"super-secret-token-value",
			"Set-Cookie",
			"Bearer super-secret",
		} {
			Expect(body).NotTo(ContainSubstring(forbidden),
				"public LabelsCatalog reflected transport secret %q in response body — defense-in-depth violation", forbidden)
		}
	})
})
