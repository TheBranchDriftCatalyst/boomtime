// goal_widgets_test.go — Part B Stage 4: privacy-gated embeddable goal
// widgets (goal-progress / goal-ring / goal-list). Invariant classes:
//
//   - THE PRIVACY TEST: an owner with a mix of public/private/disabled
//     goals must have ONLY the enabled&&public ones surface in the public
//     SVG — never a private goal's name, never a disabled-but-public goal's
//     name. An owner with ZERO public goals renders the exact same
//     empty-state bytes as an owner with NO goals at all (no oracle: a
//     stranger probing the endpoint can't tell "has goals, none public"
//     from "has no goals").
//   - THE SCOPE GATE: goals are account-wide, not scoped to a project or
//     space — a project/space-scoped widget link must 404 for goal-* kinds
//     rather than leak the owner's account-wide public goals under a URL
//     that looks project-scoped.
//   - THE CAP+FILTER TEST: with more public goals than a kind's top-N cap,
//     AND private/disabled goals interleaved by creation time, the correct
//     "filter (SQL) then cap (Go slice)" order must be observed — a
//     "cap-then-filter" regression would silently drop legitimate public
//     goals whenever a private/disabled one lands in the naive top-N window.
//   - THE DECOUPLING TEST: goal-* kinds have no legacy render.go renderer at
//     all (Part B Stage 5: renderSpec is now the only render path for every
//     kind, so this is really just a sanity check that goal-* kinds render
//     fine like everything else — see widget.IsGoalKind).
package widgets_test

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

// trivialHitSpec is a goal spec that always evaluates hit=true, progress=1
// WITHOUT needing any seeded heartbeat data: target_seconds<=0 short-
// circuits compareOp(">=", current, 0) to (true, 1) before the current
// count even matters. Keeps this file's goals deterministic and fast.
var trivialHitSpec = map[string]any{
	"kind": "time", "axis": "language", "op": ">=",
	"target_seconds": 0, "window": "day",
}

// createGoalG creates a goal via the authenticated API and returns its id.
func createGoalG(e http.Handler, token, name string, public bool) string {
	GinkgoHelper()
	rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/goals", token,
		map[string]any{"name": name, "spec": trivialHitSpec, "public": public})
	Expect(rec).To(testutil.HaveStatus(http.StatusOK), "create goal %q: body=%s", name, rec.Body.String())
	var out struct {
		Goal struct {
			ID string `json:"id"`
		} `json:"goal"`
	}
	Expect(decodeJSONBody(rec.Body.Bytes(), &out)).To(Succeed())
	Expect(out.Goal.ID).NotTo(BeEmpty())
	return out.Goal.ID
}

// setGoalEnabledG flips a goal's enabled flag via the toggle endpoint.
func setGoalEnabledG(e http.Handler, token, id string, enabled bool) {
	GinkgoHelper()
	rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/goals/"+id+"/toggle", token,
		map[string]any{"enabled": enabled})
	Expect(rec).To(testutil.HaveStatus(http.StatusOK), "toggle goal %s: body=%s", id, rec.Body.String())
}

// mintScopedLinkG mints a widget link for an arbitrary scope and returns its
// id. Local to this package — internal/testutil's mintLinkG (same shape)
// isn't reachable from here (different _test package).
func mintScopedLinkG(e http.Handler, token, scopeType, scopeRef string) string {
	GinkgoHelper()
	rec := doJSONReqG(e, http.MethodGet,
		fmt.Sprintf("/api/v1/users/current/widgets/link?scopeType=%s&scopeRef=%s", scopeType, scopeRef),
		token, nil)
	Expect(rec).To(testutil.HaveStatus(http.StatusOK), "mint %s-scoped link: body=%s", scopeType, rec.Body.String())
	var out struct {
		LinkID string `json:"linkId"`
	}
	Expect(decodeJSONBody(rec.Body.Bytes(), &out)).To(Succeed())
	return out.LinkID
}

// createSpaceIDG creates a space via HTTP and returns its id. Local
// equivalent of internal/testutil's createSpaceG (different _test package).
func createSpaceIDG(e http.Handler, token, name string) string {
	GinkgoHelper()
	rec := doJSONReqG(e, http.MethodPost, "/api/v1/users/current/spaces", token,
		map[string]any{"name": name})
	Expect(rec).To(testutil.HaveStatus(http.StatusOK), "create space: body=%s", rec.Body.String())
	var out struct {
		Space struct {
			ID int `json:"id"`
		} `json:"space"`
	}
	Expect(decodeJSONBody(rec.Body.Bytes(), &out)).To(Succeed())
	Expect(out.Space.ID).NotTo(BeZero())
	return strconv.Itoa(out.Space.ID)
}

var _ = Describe("Embeddable goal widgets (Part B Stage 4)", func() {
	It("PRIVACY GATE: only enabled&&public goals appear in the SVG — private and disabled-public goals never leak", func() {
		hz := testutil.NewHarness(GinkgoTB())
		e := hz.Router()
		_, token := hz.MintUser("goalw_privacy")

		createGoalG(e, token, "Public Enabled Goal", true)   // should appear
		createGoalG(e, token, "Private Enabled Goal", false) // must NOT appear
		disabledID := createGoalG(e, token, "Public Disabled Goal", true)
		setGoalEnabledG(e, token, disabledID, false) // public, but disabled — must NOT appear

		link := mintWidgetLinkSE(e, token)

		for _, kind := range []string{"goal-list", "goal-progress", "goal-ring"} {
			rec := getSVG(e, "/widget/svg/"+link+"/"+kind+"?days=30&theme=dark")
			Expect(rec).To(testutil.HaveStatus(http.StatusOK), "kind=%s: body=%s", kind, rec.Body.String())
			body := rec.Body.String()
			Expect(body).To(ContainSubstring("Public Enabled Goal"),
				"kind=%s: the enabled&&public goal must render", kind)
			Expect(body).NotTo(ContainSubstring("Private Enabled Goal"),
				"PRIVACY LEAK kind=%s: a private goal's name surfaced in the public SVG", kind)
			Expect(body).NotTo(ContainSubstring("Public Disabled Goal"),
				"PRIVACY LEAK kind=%s: a disabled (but public) goal's name surfaced in the public SVG", kind)
		}
	})

	It("NO-ORACLE: zero public goals renders the IDENTICAL empty-state bytes whether the owner has no goals at all, or only private/disabled ones", func() {
		hz := testutil.NewHarness(GinkgoTB())
		e := hz.Router()

		// Owner A: no goals whatsoever.
		_, tokenA := hz.MintUser("goalw_noneA")
		linkA := mintWidgetLinkSE(e, tokenA)

		// Owner B: HAS goals, but none public&&enabled — one private, one
		// public-but-disabled. A stranger must not be able to distinguish
		// B (has goals, none public) from A (has no goals at all).
		_, tokenB := hz.MintUser("goalw_noneB")
		createGoalG(e, tokenB, "Secret Goal", false)
		disabledID := createGoalG(e, tokenB, "Was Public Goal", true)
		setGoalEnabledG(e, tokenB, disabledID, false)
		linkB := mintWidgetLinkSE(e, tokenB)

		for _, kind := range []string{"goal-list", "goal-progress", "goal-ring"} {
			recA := getSVG(e, "/widget/svg/"+linkA+"/"+kind+"?days=30&theme=dark")
			Expect(recA).To(testutil.HaveStatus(http.StatusOK))
			recB := getSVG(e, "/widget/svg/"+linkB+"/"+kind+"?days=30&theme=dark")
			Expect(recB).To(testutil.HaveStatus(http.StatusOK))

			Expect(recA.Body.String()).To(ContainSubstring("No goals yet"), "kind=%s owner A", kind)
			Expect(recB.Body.String()).To(ContainSubstring("No goals yet"), "kind=%s owner B", kind)
			Expect(recB.Body.String()).NotTo(ContainSubstring("Secret Goal"),
				"PRIVACY LEAK kind=%s: private goal name leaked into the empty-state render", kind)
			Expect(recB.Body.String()).NotTo(ContainSubstring("Was Public Goal"),
				"PRIVACY LEAK kind=%s: disabled-public goal name leaked into the empty-state render", kind)

			// The strong form of no-oracle: byte-identical output. Both
			// owners resolve to the exact same nil/empty Data.Goals, and
			// the goal-* specs carry no other per-owner content (title,
			// subtitle, and dimensions come from the kind + URL params,
			// not from the owner's data) — so a prober diffing two goal
			// widget embeds cannot tell "no goals" from "goals, none public"
			// apart even byte-for-byte.
			Expect(recB.Body.Bytes()).To(Equal(recA.Body.Bytes()),
				"kind=%s: empty-goals SVG bytes differ between an owner with no goals and one with only private/disabled goals — a diffable oracle", kind)
		}
	})

	It("SCOPE GATE: a project- or space-scoped widget link 404s for goal-* kinds — goals are account-wide, not project/space-scoped", func() {
		hz := testutil.NewHarness(GinkgoTB())
		e := hz.Router()
		user, token := hz.MintUser("goalw_scope")
		hz.Seeder(user).Projects("proj-x")
		createGoalG(e, token, "Account Wide Goal", true)

		projLinkID := mintScopedLinkG(e, token, "project", "proj-x")
		spaceID := createSpaceIDG(e, token, "Work")
		spaceLinkID := mintScopedLinkG(e, token, "space", spaceID)

		for _, link := range []string{projLinkID, spaceLinkID} {
			for _, kind := range []string{"goal-list", "goal-progress", "goal-ring"} {
				rec := getSVG(e, "/widget/svg/"+link+"/"+kind+"?days=30")
				Expect(rec).To(testutil.HaveStatus(http.StatusNotFound),
					"SCOPE LEAK: link=%s kind=%s must 404 (goals aren't project/space-scoped): body=%s",
					link, kind, rec.Body.String())
			}
			// Positive control: the SAME non-user-scoped link still renders an
			// ordinary (correctly scoped) kind fine — the gate is goal-kind-
			// specific, not a blanket break of project/space widgets.
			rec := getSVG(e, "/widget/svg/"+link+"/top-projects?days=30")
			Expect(rec).To(testutil.HaveStatus(http.StatusOK), "positive control (top-projects) for link=%s", link)
		}

		// Positive control: a USER-scoped link for the same owner still
		// renders the goal fine — the gate keys off scope, not the kind.
		userLink := mintWidgetLinkSE(e, token)
		rec := getSVG(e, "/widget/svg/"+userLink+"/goal-list?days=30")
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring("Account Wide Goal"))
	})

	It("CAP+FILTER: top-N caps apply to the FILTERED public set, not the raw creation-order rows — a public goal must not be bumped out by a private/disabled one", func() {
		hz := testutil.NewHarness(GinkgoTB())
		e := hz.Router()
		_, token := hz.MintUser("goalw_capfilter")

		// Creation order (oldest -> newest), deliberately interleaving
		// private/disabled-public goals among the public ones so a
		// "cap-then-filter" bug (LIMIT N on the raw rows, filter after)
		// would silently lose public goals to private/disabled slots in
		// the naive top-N window. Sleep between creates so created_at
		// strictly increases (mirrors internal/goals/handler_test.go's
		// existing use of a small sleep for the same reason).
		names := []string{"Alpha", "Bravo", "Charlie", "PrivateX", "Delta", "Echo", "PrivateY", "Foxtrot", "DisabledZulu", "Golf"}
		public := map[string]bool{
			"Alpha": true, "Bravo": true, "Charlie": true, "PrivateX": false,
			"Delta": true, "Echo": true, "PrivateY": false, "Foxtrot": true,
			"DisabledZulu": true, "Golf": true,
		}
		ids := map[string]string{}
		for _, n := range names {
			ids[n] = createGoalG(e, token, n, public[n])
			time.Sleep(2 * time.Millisecond)
		}
		setGoalEnabledG(e, token, ids["DisabledZulu"], false)

		// Filtered (enabled&&public), newest-first order: Golf, Foxtrot,
		// Echo, Delta, Charlie, Bravo, Alpha — 7 public goals total.
		// goal-list caps at 6 -> Alpha (the 7th / oldest public goal) is
		// correctly capped away; goal-ring caps at 3 -> Golf/Foxtrot/Echo
		// only; goal-progress caps at 1 -> Golf only. NONE of PrivateX,
		// PrivateY, or DisabledZulu may appear at any cap.
		link := mintWidgetLinkSE(e, token)

		listRec := getSVG(e, "/widget/svg/"+link+"/goal-list?days=30")
		Expect(listRec).To(testutil.HaveStatus(http.StatusOK))
		listBody := listRec.Body.String()
		for _, want := range []string{"Golf", "Foxtrot", "Echo", "Delta", "Charlie", "Bravo"} {
			Expect(listBody).To(ContainSubstring(want),
				"goal-list: expected top-6 public goal %q — a cap-then-filter regression would drop it", want)
		}
		Expect(listBody).NotTo(ContainSubstring("Alpha"),
			"goal-list: Alpha is the 7th (oldest) public goal and must be correctly capped away, not shown")
		for _, leak := range []string{"PrivateX", "PrivateY", "DisabledZulu"} {
			Expect(listBody).NotTo(ContainSubstring(leak),
				"PRIVACY LEAK goal-list: %q must never appear regardless of cap position", leak)
		}

		ringRec := getSVG(e, "/widget/svg/"+link+"/goal-ring?days=30")
		Expect(ringRec).To(testutil.HaveStatus(http.StatusOK))
		ringBody := ringRec.Body.String()
		for _, want := range []string{"Golf", "Foxtrot", "Echo"} {
			Expect(ringBody).To(ContainSubstring(want), "goal-ring: expected top-3 public goal %q", want)
		}
		for _, notWant := range []string{"Delta", "Charlie", "Bravo", "Alpha", "PrivateX", "PrivateY", "DisabledZulu"} {
			Expect(ringBody).NotTo(ContainSubstring(notWant), "goal-ring: %q must not appear past the top-3 cap", notWant)
		}

		progRec := getSVG(e, "/widget/svg/"+link+"/goal-progress?days=30")
		Expect(progRec).To(testutil.HaveStatus(http.StatusOK))
		progBody := progRec.Body.String()
		Expect(progBody).To(ContainSubstring("Golf"), "goal-progress: expected the single newest public goal")
		for _, notWant := range []string{"Foxtrot", "Echo", "Delta", "Charlie", "Bravo", "Alpha", "PrivateX", "PrivateY", "DisabledZulu"} {
			Expect(progBody).NotTo(ContainSubstring(notWant), "goal-progress: %q must not appear past the top-1 cap", notWant)
		}
	})

	It("DECOUPLING: goal-* kinds render via the spec engine (no legacy fallback exists)", func() {
		hz := testutil.NewHarness(GinkgoTB())
		e := hz.Router()
		_, token := hz.MintUser("goalw_decouple")
		createGoalG(e, token, "Flagless Goal", true)
		link := mintWidgetLinkSE(e, token)

		for _, kind := range []string{"goal-list", "goal-progress", "goal-ring"} {
			rec := getSVG(e, "/widget/svg/"+link+"/"+kind+"?days=30")
			Expect(rec).To(testutil.HaveStatus(http.StatusOK),
				"kind=%s: body=%s", kind, rec.Body.String())
			Expect(rec.Header().Get("Content-Type")).To(HavePrefix("image/svg+xml"))
			Expect(rec.Body.String()).To(ContainSubstring("Flagless Goal"),
				"kind=%s: goal should render", kind)
		}
	})
})
