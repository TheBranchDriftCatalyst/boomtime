// labels_http_test.go — end-to-end HTTP coverage for the labels cluster
// (gaka-d6x.handler). Covers every handler in labels.go:
//
//	LabelsCatalog (public), AdminCreateLabel, AdminUpdateLabel,
//	AdminDeleteLabel, AdminUpdateLabelGenConfig, AdminLabelsSeedSQL,
//	applyLabelBody, sqlStr, sqlStrOrNull (indirectly via seed.sql).
//
// Named invariants:
//
//   - "public GET /catalog is unauthenticated"     → LabelsCatalog
//   - "public GET includes Cache-Control 60s"      → LabelsCatalog
//   - "admin endpoints 403 for non-admin"          → security
//   - "admin endpoints 401 without token"          → security
//   - "AdminCreateLabel requires id/kind/label/cond"→ AdminCreateLabel
//   - "AdminCreateLabel refuses to overwrite id"   → AdminCreateLabel (409-shaped 400)
//   - "AdminUpdateLabel is partial (nil-preserves)"→ AdminUpdateLabel
//   - "AdminUpdateLabel never renames id"          → AdminUpdateLabel security
//   - "AdminUpdateLabel 404 on missing"            → AdminUpdateLabel
//   - "AdminDeleteLabel is idempotent"             → AdminDeleteLabel
//   - "AdminUpdateLabelGenConfig requires field"   → AdminUpdateLabelGenConfig
//   - "AdminLabelsSeedSQL is text/plain + Content-Disposition download"
package curation_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/testutil"
)

// grantAdmin adds `user` to the harness's AdminUsers set so requireAdmin
// passes. Called before each admin-endpoint spec.
func grantAdmin(hz *testutil.Harness, user string) {
	if hz.Cfg.AdminUsers == nil {
		hz.Cfg.AdminUsers = map[string]struct{}{}
	}
	hz.Cfg.AdminUsers[user] = struct{}{}
}

// mkLabelBody returns a valid create-body carrying all required fields.
// `id` is used verbatim so tests can craft unique ids and avoid catalog
// collisions between specs.
func mkLabelBody(id string) map[string]any {
	return map[string]any{
		"id":              id,
		"kind":            "tier",
		"label":           "Test Label " + id,
		"glyph":           "T",
		"description":     "test",
		"optimizedPrompt": "",
		"rank":            10,
		"tier":            "novice",
		"condition":       json.RawMessage(`{"kind":"axis-time","axis":"languages","value":"go","op":">=","hours":1}`),
	}
}

// cleanupLabel best-effort removes a catalog row after a spec finishes so
// specs don't collide on the global catalog.
func cleanupLabel(hz *testutil.Harness, id string) {
	ctx := context.Background()
	hz.T.Cleanup(func() {
		_ = hz.DB.DeleteLabel(ctx, id)
	})
}

var _ = Describe("LabelsCatalog (public)", func() {
	It("returns 200 without auth + Cache-Control: public, max-age=60", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		rec := doJSONReqG(e, http.MethodGet, "/api/v1/labels/catalog", "", nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())
		Expect(rec.Header().Get("Cache-Control")).To(Equal("public, max-age=60"),
			"public catalog must carry the short-TTL cache header FE hooks rely on")

		var got map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &got)).To(Succeed())
		Expect(got).To(HaveKey("systemPrompt"), "wire shape must carry systemPrompt")
		Expect(got).To(HaveKey("labels"), "wire shape must carry labels array")
	})
})

var _ = Describe("AdminCreateLabel guardrails", func() {
	It("401s a plain non-authenticated request", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		id := "test-noauth-" + time.Now().Format("150405.000000000")
		rec := doJSONReqG(e, http.MethodPost, "/api/v1/admin/labels", "", mkLabelBody(id))
		Expect(rec.Code).To(BeNumerically(">=", 400), "unauth must be 4xx, got %d", rec.Code)
	})

	It("403s a non-admin authenticated user", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("lb_nonadmin")
		id := "test-nonadmin-" + time.Now().Format("150405.000000000")
		rec := doJSONReqG(e, http.MethodPost, "/api/v1/admin/labels", token, mkLabelBody(id))
		Expect(rec).To(testutil.HaveStatus(http.StatusForbidden),
			"non-admin POST /admin/labels must 403, got %d body=%s", rec.Code, rec.Body.String())
	})

	It("400s a body missing `id`", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("lb_no_id")
		grantAdmin(hz, user)
		body := mkLabelBody("temp")
		delete(body, "id")
		rec := doJSONReqG(e, http.MethodPost, "/api/v1/admin/labels", token, body)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})

	It("400s a body missing `kind`", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("lb_no_kind")
		grantAdmin(hz, user)
		id := "test-nokind-" + time.Now().Format("150405.000000000")
		body := mkLabelBody(id)
		delete(body, "kind")
		rec := doJSONReqG(e, http.MethodPost, "/api/v1/admin/labels", token, body)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})

	It("400s a body missing `label`", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("lb_no_label")
		grantAdmin(hz, user)
		id := "test-nolabel-" + time.Now().Format("150405.000000000")
		body := mkLabelBody(id)
		delete(body, "label")
		rec := doJSONReqG(e, http.MethodPost, "/api/v1/admin/labels", token, body)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})

	It("400s a body missing `condition`", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("lb_no_cond")
		grantAdmin(hz, user)
		id := "test-nocond-" + time.Now().Format("150405.000000000")
		body := mkLabelBody(id)
		delete(body, "condition")
		rec := doJSONReqG(e, http.MethodPost, "/api/v1/admin/labels", token, body)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest))
	})

	It("201s a valid create + fresh label appears in the catalog", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("lb_create_ok")
		grantAdmin(hz, user)
		id := "test-ok-" + time.Now().Format("150405.000000000")
		cleanupLabel(hz, id)

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/admin/labels", token, mkLabelBody(id))
		Expect(rec).To(testutil.HaveStatus(http.StatusCreated), "body=%s", rec.Body.String())

		// Now visible in the public catalog.
		catRec := doJSONReqG(e, http.MethodGet, "/api/v1/labels/catalog", "", nil)
		Expect(catRec).To(testutil.HaveStatus(http.StatusOK))
		var cat struct {
			Labels []db.Label `json:"labels"`
		}
		Expect(json.Unmarshal(catRec.Body.Bytes(), &cat)).To(Succeed())
		found := false
		for _, l := range cat.Labels {
			if l.ID == id {
				found = true
			}
		}
		Expect(found).To(BeTrue(), "created label id=%s absent from public catalog", id)
	})

	It("refuses to POST-overwrite an existing id (409-shaped 400)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("lb_dup")
		grantAdmin(hz, user)
		id := "test-dup-" + time.Now().Format("150405.000000000")
		cleanupLabel(hz, id)

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/admin/labels", token, mkLabelBody(id))
		Expect(rec).To(testutil.HaveStatus(http.StatusCreated))

		rec2 := doJSONReqG(e, http.MethodPost, "/api/v1/admin/labels", token, mkLabelBody(id))
		Expect(rec2).To(testutil.HaveStatus(http.StatusBadRequest),
			"POST-overwrite must NOT silently upsert; admin should use PATCH instead")
		Expect(rec2.Body.String()).To(ContainSubstring("already exists"))
	})
})

var _ = Describe("AdminCreateLabel + PATCH: server-side condition validation (gaka-6uf)", func() {
	It("POST 400s a syntactically-decodable but semantically-invalid condition with a JSON pointer path", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("lb_val_post")
		grantAdmin(hz, user)
		id := "test-val-" + time.Now().Format("150405.000000000")
		cleanupLabel(hz, id)

		// Bad op (=== instead of >= / <=). Pre-refactor: silently accepted +
		// evaluator always-false. Post gaka-6uf: rejected at write time.
		body := mkLabelBody(id)
		body["condition"] = json.RawMessage(`{"kind":"axis-time","axis":"languages","value":"go","op":"===","hours":5}`)

		rec := doJSONReqG(e, http.MethodPost, "/api/v1/admin/labels", token, body)
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest), "body=%s", rec.Body.String())
		Expect(rec.Body.String()).To(ContainSubstring("/op"),
			"error must carry the JSON pointer to the offending field: got %s", rec.Body.String())

		// The row MUST NOT exist post-rejection.
		got, err := hz.DB.GetLabel(context.Background(), id)
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(BeNil(), "rejected create must not persist a row")
	})

	It("PATCH 400s an invalid condition and leaves the existing row untouched", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("lb_val_patch")
		grantAdmin(hz, user)
		id := "test-valp-" + time.Now().Format("150405.000000000")
		cleanupLabel(hz, id)

		// Seed a valid label first.
		cRec := doJSONReqG(e, http.MethodPost, "/api/v1/admin/labels", token, mkLabelBody(id))
		Expect(cRec).To(testutil.HaveStatus(http.StatusCreated))

		// PATCH with pct=50 (author mistake: DSL uses 0..1, they meant 0.5).
		pRec := doJSONReqG(e, http.MethodPatch, "/api/v1/admin/labels/"+id, token, map[string]any{
			"condition": json.RawMessage(`{"kind":"axis-pct","axis":"languages","value":"go","op":">=","pct":50}`),
		})
		Expect(pRec).To(testutil.HaveStatus(http.StatusBadRequest), "body=%s", pRec.Body.String())
		Expect(pRec.Body.String()).To(ContainSubstring("/pct"))

		// Row still has the original axis-time condition.
		got, err := hz.DB.GetLabel(context.Background(), id)
		Expect(err).ToNot(HaveOccurred())
		Expect(got).ToNot(BeNil())
		Expect(string(got.Condition)).To(ContainSubstring(`"axis-time"`),
			"rejected PATCH must not overwrite the persisted condition")
	})

	It("PATCH without a `condition` field still allows partial updates to other fields", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("lb_val_partial")
		grantAdmin(hz, user)
		id := "test-valpt-" + time.Now().Format("150405.000000000")
		cleanupLabel(hz, id)

		cRec := doJSONReqG(e, http.MethodPost, "/api/v1/admin/labels", token, mkLabelBody(id))
		Expect(cRec).To(testutil.HaveStatus(http.StatusCreated))

		// PATCH label only — validator MUST NOT trip on the absent condition.
		pRec := doJSONReqG(e, http.MethodPatch, "/api/v1/admin/labels/"+id, token, map[string]any{
			"label": "Renamed-No-Cond",
		})
		Expect(pRec).To(testutil.HaveStatus(http.StatusOK), "body=%s", pRec.Body.String())
	})
})

var _ = Describe("AdminUpdateLabel (partial PATCH)", func() {
	It("403s a non-admin authenticated user", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("lb_pat_nonadmin")
		rec := doJSONReqG(e, http.MethodPatch, "/api/v1/admin/labels/anything", token,
			map[string]any{"label": "x"})
		Expect(rec).To(testutil.HaveStatus(http.StatusForbidden))
	})

	It("404s when the id does not exist", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("lb_pat_404")
		grantAdmin(hz, user)
		rec := doJSONReqG(e, http.MethodPatch, "/api/v1/admin/labels/no-such-id-9999", token,
			map[string]any{"label": "won't stick"})
		Expect(rec).To(testutil.HaveStatus(http.StatusNotFound))
	})

	It("partial PATCH only overwrites the fields sent (label change; glyph preserved)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("lb_pat_partial")
		grantAdmin(hz, user)
		id := "test-part-" + time.Now().Format("150405.000000000")
		cleanupLabel(hz, id)

		// Seed.
		cRec := doJSONReqG(e, http.MethodPost, "/api/v1/admin/labels", token, mkLabelBody(id))
		Expect(cRec).To(testutil.HaveStatus(http.StatusCreated))

		// PATCH only "label".
		newLabel := "Renamed Label"
		pRec := doJSONReqG(e, http.MethodPatch, "/api/v1/admin/labels/"+id, token,
			map[string]any{"label": newLabel})
		Expect(pRec).To(testutil.HaveStatus(http.StatusOK), "body=%s", pRec.Body.String())

		var got db.Label
		Expect(json.Unmarshal(pRec.Body.Bytes(), &got)).To(Succeed())
		Expect(got.Label).To(Equal(newLabel))
		Expect(got.Glyph).To(Equal("T"), "glyph must be preserved by a partial PATCH (nil-pointer = don't touch)")
	})

	It("PATCH ignores an incoming `id` field (never renames)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("lb_pat_noid")
		grantAdmin(hz, user)
		id := "test-noid-" + time.Now().Format("150405.000000000")
		cleanupLabel(hz, id)

		cRec := doJSONReqG(e, http.MethodPost, "/api/v1/admin/labels", token, mkLabelBody(id))
		Expect(cRec).To(testutil.HaveStatus(http.StatusCreated))

		pRec := doJSONReqG(e, http.MethodPatch, "/api/v1/admin/labels/"+id, token, map[string]any{
			"id":    "hijacked-id",
			"label": "x",
		})
		Expect(pRec).To(testutil.HaveStatus(http.StatusOK))
		var got db.Label
		Expect(json.Unmarshal(pRec.Body.Bytes(), &got)).To(Succeed())
		Expect(got.ID).To(Equal(id),
			"PATCH must never rename id — that would break label_images FKs + persisted award history")

		// The `hijacked-id` was NOT created either.
		hijack, err := hz.DB.GetLabel(context.Background(), "hijacked-id")
		Expect(err).NotTo(HaveOccurred())
		Expect(hijack).To(BeNil(), "renaming via PATCH must not create the target id")
	})
})

var _ = Describe("AdminDeleteLabel", func() {
	It("403s a non-admin user", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("lb_del_nonadmin")
		rec := doJSONReqG(e, http.MethodDelete, "/api/v1/admin/labels/x", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusForbidden))
	})

	It("204s on a non-existent id (idempotent)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("lb_del_idem")
		grantAdmin(hz, user)
		rec := doJSONReqG(e, http.MethodDelete, "/api/v1/admin/labels/nothing-here-9999", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusNoContent),
			"DELETE must be idempotent — same 204 whether or not the row existed")
	})

	It("204s + removes the row on a real target", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("lb_del_ok")
		grantAdmin(hz, user)
		id := "test-del-" + time.Now().Format("150405.000000000")
		cleanupLabel(hz, id)

		cRec := doJSONReqG(e, http.MethodPost, "/api/v1/admin/labels", token, mkLabelBody(id))
		Expect(cRec).To(testutil.HaveStatus(http.StatusCreated))

		dRec := doJSONReqG(e, http.MethodDelete, "/api/v1/admin/labels/"+id, token, nil)
		Expect(dRec).To(testutil.HaveStatus(http.StatusNoContent))

		got, err := hz.DB.GetLabel(context.Background(), id)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeNil(), "label row still present after DELETE")
	})
})

var _ = Describe("AdminUpdateLabelGenConfig", func() {
	It("403s a non-admin user", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("lb_gc_nonadmin")
		rec := doJSONReqG(e, http.MethodPatch, "/api/v1/admin/label-gen-config", token,
			map[string]any{"systemPrompt": "new prompt"})
		Expect(rec).To(testutil.HaveStatus(http.StatusForbidden))
	})

	It("400s when `systemPrompt` is absent (nil pointer)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("lb_gc_missing")
		grantAdmin(hz, user)
		rec := doJSONReqG(e, http.MethodPatch, "/api/v1/admin/label-gen-config", token,
			map[string]any{}) // no systemPrompt
		Expect(rec).To(testutil.HaveStatus(http.StatusBadRequest),
			"nil systemPrompt must be rejected — a PATCH omitting the field is ambiguous, not a clear intent")
	})

	It("accepts an empty string (explicit clear) — worker treats \"\" as no prefix", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("lb_gc_clear")
		grantAdmin(hz, user)
		rec := doJSONReqG(e, http.MethodPatch, "/api/v1/admin/label-gen-config", token,
			map[string]any{"systemPrompt": ""})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())
		var out struct {
			SystemPrompt string `json:"systemPrompt"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out.SystemPrompt).To(Equal(""))
	})

	It("round-trips a non-empty systemPrompt through the public catalog", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("lb_gc_rt")
		grantAdmin(hz, user)
		prompt := "You are a friendly cat, meowify all label descriptions."
		defer func() {
			// Best-effort restore so leftover state doesn't leak between specs.
			_ = hz.DB.SetGenConfig(context.Background(), "")
		}()
		rec := doJSONReqG(e, http.MethodPatch, "/api/v1/admin/label-gen-config", token,
			map[string]any{"systemPrompt": prompt})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))

		catRec := doJSONReqG(e, http.MethodGet, "/api/v1/labels/catalog", "", nil)
		Expect(catRec).To(testutil.HaveStatus(http.StatusOK))
		var cat struct {
			SystemPrompt string `json:"systemPrompt"`
		}
		Expect(json.Unmarshal(catRec.Body.Bytes(), &cat)).To(Succeed())
		Expect(cat.SystemPrompt).To(Equal(prompt),
			"public catalog must reflect the admin-set systemPrompt (round-trip)")
	})
})

var _ = Describe("AdminLabelsSeedSQL", func() {
	It("403s a non-admin user", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		_, token := hz.MintUser("lb_seed_nonadmin")
		rec := doJSONReqG(e, http.MethodGet, "/api/v1/admin/labels/seed.sql", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusForbidden))
	})

	It("returns text/plain SQL blob with attachment Content-Disposition", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("lb_seed_ok")
		grantAdmin(hz, user)
		rec := doJSONReqG(e, http.MethodGet, "/api/v1/admin/labels/seed.sql", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))

		ct := rec.Header().Get("Content-Type")
		Expect(strings.HasPrefix(ct, "text/plain")).To(BeTrue(),
			"seed dump must be text/plain so browsers can save it, got %q", ct)

		cd := rec.Header().Get("Content-Disposition")
		Expect(cd).To(ContainSubstring(`attachment`),
			"Content-Disposition must be attachment so hitting the URL prompts a download")
		Expect(cd).To(ContainSubstring(`labels_seed.sql`))

		body := rec.Body.String()
		// Body must be an executable goose migration section.
		Expect(body).To(ContainSubstring("-- +goose Up"),
			"seed dump missing goose directive")
		Expect(body).To(ContainSubstring("INSERT INTO labels"))
		Expect(body).To(ContainSubstring("ON CONFLICT (id) DO UPDATE SET"),
			"seed must be re-runnable (idempotent upsert)")
		Expect(body).To(ContainSubstring("UPDATE label_gen_config"),
			"seed must also carry the singleton gen-config write")
	})

	It("escapes single quotes in label ids/labels (sqlStr defense)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		user, token := hz.MintUser("lb_seed_quote")
		grantAdmin(hz, user)
		id := "test-quote-" + time.Now().Format("150405.000000000")
		cleanupLabel(hz, id)

		body := mkLabelBody(id)
		// Craft a label containing a single quote to force sqlStr escaping.
		body["label"] = "quote's inside"
		cRec := doJSONReqG(e, http.MethodPost, "/api/v1/admin/labels", token, body)
		Expect(cRec).To(testutil.HaveStatus(http.StatusCreated))

		rec := doJSONReqG(e, http.MethodGet, "/api/v1/admin/labels/seed.sql", token, nil)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		Expect(rec.Body.String()).To(ContainSubstring("'quote''s inside'"),
			"sqlStr must double single quotes so the dump remains valid SQL")
	})
})
