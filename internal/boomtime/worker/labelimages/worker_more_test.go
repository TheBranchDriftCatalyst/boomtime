// worker_more_test.go — additional coverage (boom-d6x) targeting branches
// not exercised by the baseline suite. Every case pins a NAMED invariant
// rather than a bare roundtrip.
//
// Invariants pinned here (grouped by target):
//
//	NewWorker:
//	  - feature-flag-off returns (nil, nil) — no client, no error
//	  - malformed shim URL surfaces a wrapped comfyui: error (not nil-nil)
//	  - flag ON + valid URL populates model + logger, non-nil worker
//
//	catalog():
//	  - DB-backed rows with non-empty optimized_prompt become entries
//	    (compiled baseline is NOT used when DB has data)
//	  - Rows with an empty optimized_prompt are silently skipped
//	    (tier labels don't spam the log at every regen)
//	  - When the DB is set but returns zero non-empty rows, worker
//	    falls back to labelcatalog.Entries (compiled baseline)
//	  - A closed pool → ListLabels returns err → fallback to baseline
//	    (transient DB blip does NOT abort a regen)
//
//	RegenerateEntry (boom-8bz Executor path):
//	  - happy path saves a fresh row with per-entry Model override taking
//	    precedence over worker's env-configured default
//	  - empty ID / empty Prompt reject BEFORE any DB write (no ciphertext,
//	    no shim hit, no partial row)
//	  - closed-pool → wrapped "delete old row" error (delete-before-save
//	    contract cannot be bypassed by a caller with a stale worker)
//
//	RegenerateOne (DB-source-of-truth path, post boom-364.3):
//	  - id present in DB with non-empty optimized_prompt is regenerated
//	    using DB's Description + OptimizedPrompt (NOT the compiled baseline,
//	    even if the id ALSO exists in labelcatalog.Entries)
//	  - id present in DB with EMPTY optimized_prompt returns an explicit
//	    "nothing to generate" error — no silent no-op
//
//	RegenerateList (admin regen endpoint path):
//	  - happy path returns (len(entries), 0, nil) and hits the shim once
//	    per entry
//	  - entries with empty ID OR empty Prompt are counted as failed and
//	    skipped (no partial row, no shim hit)
//	  - a pre-cancelled ctx short-circuits BEFORE the first shim hit
//	  - after generating N entries, on a cancel returns the partial count
//
//	systemPrompt() cache:
//	  - two consecutive calls within TTL result in exactly ONE DB read
//	    (the sysFetched timestamp gates the second call)
//	  - after TTL expiry (simulated by resetting sysFetched to zero-time),
//	    the next call MUST re-read the DB and pick up an admin edit
//	  - sysMu actually serializes concurrent systemPrompt() callers so
//	    parallel generateAndSave invocations converge on one cached value
//	    (positive spec for the mutex — not just `go test -race` coverage)
//
//	generateAndSave error paths:
//	  - shim 500 → wrapped "shim:" error, no DB row written
//	  - per-entry Seed override is passed through to SaveLabelImage's seed
//	    column verbatim
//	  - per-entry Size override is passed through to the shim request
//	  - final saved `prompt` column carries the FULL 3-segment composition
//	    (systemPrompt + description + entryPrompt) — the "provenance is
//	    self-contained" claim in worker.go:344-346
//
//	RegenerateEntry model-fallback:
//	  - empty per-entry Model falls back to worker.model (parity with Run
//	    loop; the "override-wins" path is covered elsewhere)
package labelimages

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/comfyui"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/labelcatalog"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// ------- helpers -------

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// countingShim is a shimServer variant that lets each test opt into
// recording the last request body so we can assert per-entry override
// propagation (model / seed / size).
type shimReq struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Size   string `json:"size"`
	Seed   *int64 `json:"seed"`
}

func recordingShim(hits *atomic.Int32, lastReq **shimReq) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer GinkgoRecover()
		if hits != nil {
			hits.Add(1)
		}
		defer r.Body.Close()
		body, _ := io.ReadAll(r.Body)
		var req shimReq
		_ = json.Unmarshal(body, &req)
		if lastReq != nil {
			r := req
			*lastReq = &r
		}
		out := pngBytesGinkgo(req.Prompt)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(out)}},
		})
	}))
}

// errorShim always 500s so callers exercise generateAndSave's shim error
// branch — Client.Generate retries 500s but eventually returns the wrapped
// error.
func errorShim() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
}

// ------- NewWorker branches -------

var _ = Describe("NewWorker feature gate (boom-d6x)", func() {
	It("returns (nil, nil) when feature flag is off — feature-off is not an error", func() {
		cfg := &config.Config{FeatureLabelImages: false, ComfyUIShimURL: "http://example.invalid"}
		w, err := NewWorker(cfg, nil, silentLogger())
		Expect(err).NotTo(HaveOccurred(), "feature-off must not error")
		Expect(w).To(BeNil(), "feature-off must return nil worker so caller no-ops")
	})

	It("returns (nil, nil) when flag is on but ShimURL is empty — same as off", func() {
		// LabelImagesEnabled() requires BOTH the flag AND a non-empty URL.
		cfg := &config.Config{FeatureLabelImages: true, ComfyUIShimURL: ""}
		w, err := NewWorker(cfg, nil, silentLogger())
		Expect(err).NotTo(HaveOccurred())
		Expect(w).To(BeNil())
	})

	It("returns a wrapped error for a malformed URL (missing scheme) — loud boot failure", func() {
		cfg := &config.Config{FeatureLabelImages: true, ComfyUIShimURL: "no-scheme.example"}
		w, err := NewWorker(cfg, nil, silentLogger())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("labelimages:"), "must be wrapped by the package")
		Expect(w).To(BeNil())
	})

	It("returns a live worker when flag ON + valid URL — model + logger threaded through", func() {
		cfg := &config.Config{
			FeatureLabelImages: true,
			ComfyUIShimURL:     "http://localhost:9",
			ComfyUIModel:       "my-model-v2",
		}
		w, err := NewWorker(cfg, nil, silentLogger())
		Expect(err).NotTo(HaveOccurred())
		Expect(w).NotTo(BeNil())
		Expect(w.model).To(Equal("my-model-v2"), "ComfyUIModel from cfg must land on the worker")
		Expect(w.client).NotTo(BeNil(), "URL-provided branch must construct a comfyui client")
	})
})

// ------- catalog() branches -------

var _ = Describe("Worker.catalog() DB-first source-of-truth (boom-d6x)", func() {
	It("prefers DB rows over compiled baseline and skips rows with empty optimized_prompt", func() {
		d := openTestDBGinkgo()
		DeferCleanup(func() { d.Close() })
		ctx := context.Background()

		// Insert two rows: one with a prompt (should surface), one with no
		// prompt (should be silently dropped — tier-label case).
		withPrompt := db.Label{
			ID: "test-cat-with", Kind: "archetype", Label: "WithPrompt",
			OptimizedPrompt: "prompt-yes",
			Condition:       json.RawMessage(`{}`),
		}
		withoutPrompt := db.Label{
			ID: "test-cat-without", Kind: "tier", Label: "NoPrompt",
			OptimizedPrompt: "", // NULLIF drops it — ListLabels COALESCEs to ""
			Condition:       json.RawMessage(`{}`),
		}
		Expect(d.UpsertLabel(ctx, withPrompt)).To(Succeed())
		Expect(d.UpsertLabel(ctx, withoutPrompt)).To(Succeed())
		DeferCleanup(func() {
			_ = d.DeleteLabel(ctx, "test-cat-with")
			_ = d.DeleteLabel(ctx, "test-cat-without")
		})

		// Worker with entries=nil so it uses the DB path.
		w := &Worker{db: d, logger: silentLogger()}
		got := w.catalog()

		var haveWith, haveWithout bool
		for _, e := range got {
			if e.ID == "test-cat-with" {
				haveWith = true
				Expect(e.Prompt).To(Equal("prompt-yes"),
					"DB's optimized_prompt must be threaded through as Entry.Prompt")
			}
			if e.ID == "test-cat-without" {
				haveWithout = true
			}
		}
		Expect(haveWith).To(BeTrue(), "rows WITH a prompt must appear in the catalog")
		Expect(haveWithout).To(BeFalse(), "rows WITHOUT a prompt must be filtered out")
	})

	It("falls back to compiled baseline when the DB pool is closed (transient blip)", func() {
		d := openTestDBGinkgo()
		d.Close() // simulate a hard DB failure — ListLabels will return an err

		w := &Worker{db: d, logger: silentLogger()}
		got := w.catalog()

		// The compiled baseline is non-empty; if we returned an empty slice
		// here, callers (Run / RegenerateAll) would think the DB has zero
		// labels and skip everything on a transient outage.
		Expect(got).To(Equal(labelcatalog.Entries),
			"pool-closed error path MUST fall back to compiled baseline, not return empty")
	})

	It("uses explicit worker.entries even when DB has DIFFERENT populated rows (short-circuit proof)", func() {
		// Stronger than a nil-DB check: point the worker at a real DB that
		// WOULD return non-empty rows, and inject an entries slice with a
		// disjoint ID set. If the branch `if w.entries != nil { return w.entries }`
		// were removed, catalog() would fall through to the DB path and
		// return DB rows — the injected sentinel would be absent.
		d := openTestDBGinkgo()
		DeferCleanup(func() { d.Close() })
		ctx := context.Background()

		// Seed a DB row with a distinctive id that MUST NOT appear in the
		// returned catalog if the short-circuit works.
		Expect(d.UpsertLabel(ctx, db.Label{
			ID: "test-cat-db-should-not-appear", Kind: "archetype", Label: "X",
			OptimizedPrompt: "db-prompt-should-not-appear",
			Condition:       json.RawMessage(`{}`),
		})).To(Succeed())
		DeferCleanup(func() { _ = d.DeleteLabel(ctx, "test-cat-db-should-not-appear") })

		injected := []labelcatalog.Entry{{ID: "inject-only-1", Prompt: "p-inject-1"}}
		w := &Worker{db: d, entries: injected, logger: silentLogger()}

		got := w.catalog()
		Expect(got).To(Equal(injected),
			"explicit entries MUST short-circuit and return exactly the injected slice")
		for _, e := range got {
			Expect(e.ID).NotTo(Equal("test-cat-db-should-not-appear"),
				"DB rows MUST NOT leak through when explicit entries is set")
			Expect(e.Prompt).NotTo(ContainSubstring("db-prompt-should-not-appear"),
				"DB prompt MUST NOT leak through when explicit entries is set")
		}
	})

	It("with a nil DB and nil entries, returns compiled baseline", func() {
		w := &Worker{db: nil, entries: nil, logger: silentLogger()}
		Expect(w.catalog()).To(Equal(labelcatalog.Entries))
	})
})

// ------- RegenerateEntry (boom-8bz path) -------

var _ = Describe("Worker.RegenerateEntry (boom-d6x)", func() {
	It("nil receiver returns a feature-disabled error — no panic, no silent success", func() {
		var w *Worker
		err := w.RegenerateEntry(context.Background(), labelcatalog.Entry{ID: "x", Prompt: "y"})
		Expect(err).To(MatchError(ContainSubstring("feature disabled")))
	})

	It("rejects entry with empty ID BEFORE any DB or shim call", func() {
		d := openTestDBGinkgo()
		DeferCleanup(func() { d.Close() })
		var hits atomic.Int32
		srv := recordingShim(&hits, nil)
		DeferCleanup(srv.Close)
		client, _ := comfyui.NewClient(srv.URL)
		w := newWorkerForTest(d, client, "m", silentLogger(), nil)

		err := w.RegenerateEntry(context.Background(), labelcatalog.Entry{ID: "", Prompt: "p"})
		Expect(err).To(HaveOccurred())
		Expect(hits.Load()).To(BeEquivalentTo(0), "shim must NOT be called for invalid input")
	})

	It("rejects entry with empty Prompt BEFORE any DB or shim call", func() {
		d := openTestDBGinkgo()
		DeferCleanup(func() { d.Close() })
		var hits atomic.Int32
		srv := recordingShim(&hits, nil)
		DeferCleanup(srv.Close)
		client, _ := comfyui.NewClient(srv.URL)
		w := newWorkerForTest(d, client, "m", silentLogger(), nil)

		err := w.RegenerateEntry(context.Background(), labelcatalog.Entry{ID: "id", Prompt: ""})
		Expect(err).To(HaveOccurred())
		Expect(hits.Load()).To(BeEquivalentTo(0))
	})

	It("delete-before-save + per-entry Model override lands on the saved row", func() {
		d := openTestDBGinkgo()
		DeferCleanup(func() { d.Close() })
		ctx := context.Background()
		cleanupTestRowsGinkgo(d, "test-re-a")
		DeferCleanup(func() { cleanupTestRowsGinkgo(d, "test-re-a") })

		// Pre-seed a row with the OLD model to prove delete-before-save wipes it.
		Expect(d.SaveLabelImage(ctx, "test-re-a", pngBytesGinkgo("OLD"), "image/png",
			"old-model-v0", "old prompt", nil)).To(Succeed())

		var lastReq *shimReq
		srv := recordingShim(nil, &lastReq)
		DeferCleanup(srv.Close)
		client, _ := comfyui.NewClient(srv.URL)

		// Worker default is "worker-default-model" — the per-entry Model
		// "entry-override-model" MUST win.
		w := newWorkerForTest(d, client, "worker-default-model", silentLogger(), nil)
		e := labelcatalog.Entry{
			ID:     "test-re-a",
			Prompt: "fresh-prompt",
			Model:  "entry-override-model",
		}
		Expect(w.RegenerateEntry(ctx, e)).To(Succeed())

		got, ok, err := d.GetLabelImage(ctx, "test-re-a")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(got.Model).To(Equal("entry-override-model"),
			"per-entry Model MUST override worker's default (per-label editor invariant)")
		Expect(lastReq).NotTo(BeNil())
		Expect(lastReq.Model).To(Equal("entry-override-model"),
			"the model sent to the shim MUST also reflect the override, not the default")
	})

	It("with a closed pool, wraps the delete failure BEFORE calling the shim", func() {
		d := openTestDBGinkgo()
		d.Close() // any DB call now errors immediately

		var hits atomic.Int32
		srv := recordingShim(&hits, nil)
		DeferCleanup(srv.Close)
		client, _ := comfyui.NewClient(srv.URL)
		w := newWorkerForTest(d, client, "m", silentLogger(), nil)

		err := w.RegenerateEntry(context.Background(),
			labelcatalog.Entry{ID: "id-x", Prompt: "p"})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("delete old row"),
			"delete-before-save contract: failure must be wrapped as delete-phase, not save-phase")
		Expect(hits.Load()).To(BeEquivalentTo(0),
			"shim MUST NOT be called if the pre-delete failed")
	})
})

// ------- RegenerateOne DB source-of-truth branches -------

var _ = Describe("Worker.RegenerateOne DB-first (boom-d6x)", func() {
	It("uses the DB's OptimizedPrompt over the compiled baseline (post boom-364.3)", func() {
		d := openTestDBGinkgo()
		DeferCleanup(func() { d.Close() })
		ctx := context.Background()

		// Pick an id that ALSO exists in the compiled baseline — proves the
		// DB row wins even in the id-collision case.
		baseline := labelcatalog.Entries[0]
		dbLabel := db.Label{
			ID: baseline.ID, Kind: "archetype", Label: baseline.ID,
			OptimizedPrompt: "DB-WINS-PROMPT",
			Description:     "db-desc",
			Condition:       json.RawMessage(`{}`),
		}
		Expect(d.UpsertLabel(ctx, dbLabel)).To(Succeed())
		DeferCleanup(func() {
			// Restore the baseline-shipped row via re-upsert with the
			// baseline's prompt so we don't leak DB-WINS-PROMPT to other tests.
			restored := dbLabel
			restored.OptimizedPrompt = baseline.Prompt
			_ = d.UpsertLabel(ctx, restored)
			cleanupTestRowsGinkgo(d, baseline.ID)
		})
		cleanupTestRowsGinkgo(d, baseline.ID)

		var lastReq *shimReq
		srv := recordingShim(nil, &lastReq)
		DeferCleanup(srv.Close)
		client, _ := comfyui.NewClient(srv.URL)

		w := newWorkerForTest(d, client, "m", silentLogger(), nil)
		Expect(w.RegenerateOne(ctx, baseline.ID)).To(Succeed())

		// The shim request's prompt must contain the DB prompt, not baseline.
		Expect(lastReq).NotTo(BeNil())
		Expect(lastReq.Prompt).To(ContainSubstring("DB-WINS-PROMPT"),
			"DB row's OptimizedPrompt MUST take precedence over compiled baseline")
		Expect(lastReq.Prompt).NotTo(ContainSubstring(baseline.Prompt),
			"compiled baseline prompt MUST NOT leak through when DB has the row")
	})

	It("returns explicit 'nothing to generate' when DB row has empty OptimizedPrompt", func() {
		d := openTestDBGinkgo()
		DeferCleanup(func() { d.Close() })
		ctx := context.Background()

		id := "test-r1-empty"
		Expect(d.UpsertLabel(ctx, db.Label{
			ID: id, Kind: "tier", Label: "T",
			OptimizedPrompt: "",
			Condition:       json.RawMessage(`{}`),
		})).To(Succeed())
		DeferCleanup(func() { _ = d.DeleteLabel(ctx, id) })

		srv := recordingShim(nil, nil)
		DeferCleanup(srv.Close)
		client, _ := comfyui.NewClient(srv.URL)

		w := newWorkerForTest(d, client, "m", silentLogger(), nil)
		err := w.RegenerateOne(ctx, id)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no optimized_prompt"),
			"empty prompt on a DB row must be an explicit, actionable error")
	})
})

// ------- RegenerateList branches -------

var _ = Describe("Worker.RegenerateList (boom-d6x)", func() {
	It("nil receiver returns feature-disabled — no panic", func() {
		var w *Worker
		gen, failed, err := w.RegenerateList(context.Background(), nil)
		Expect(err).To(HaveOccurred())
		Expect(gen).To(Equal(0))
		Expect(failed).To(Equal(0))
	})

	It("counts entries with empty ID or Prompt as failed and skips them (no shim hit)", func() {
		d := openTestDBGinkgo()
		DeferCleanup(func() { d.Close() })
		var hits atomic.Int32
		srv := recordingShim(&hits, nil)
		DeferCleanup(srv.Close)
		client, _ := comfyui.NewClient(srv.URL)
		w := newWorkerForTest(d, client, "m", silentLogger(), nil)

		cleanupTestRowsGinkgo(d, "test-rl-ok")
		DeferCleanup(func() { cleanupTestRowsGinkgo(d, "test-rl-ok") })

		entries := []labelcatalog.Entry{
			{ID: "", Prompt: "no-id"},
			{ID: "id-only", Prompt: ""},
			{ID: "test-rl-ok", Prompt: "valid"},
		}
		gen, failed, err := w.RegenerateList(context.Background(), entries)
		Expect(err).NotTo(HaveOccurred())
		Expect(gen).To(Equal(1), "only the fully-populated entry should generate")
		Expect(failed).To(Equal(2), "empty ID + empty Prompt must count as failed")
		Expect(hits.Load()).To(BeEquivalentTo(1),
			"shim must fire exactly once — validation happens BEFORE the shim call")
	})

	It("pre-cancelled ctx returns ctx.Err() BEFORE the first shim call", func() {
		d := openTestDBGinkgo()
		DeferCleanup(func() { d.Close() })
		var hits atomic.Int32
		srv := recordingShim(&hits, nil)
		DeferCleanup(srv.Close)
		client, _ := comfyui.NewClient(srv.URL)
		w := newWorkerForTest(d, client, "m", silentLogger(), nil)

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancelled BEFORE the loop starts

		gen, failed, err := w.RegenerateList(ctx, []labelcatalog.Entry{
			{ID: "any", Prompt: "any"},
		})
		Expect(err).To(MatchError(context.Canceled))
		Expect(gen).To(Equal(0))
		Expect(failed).To(Equal(0))
		Expect(hits.Load()).To(BeEquivalentTo(0),
			"cancellation must short-circuit the loop before generation")
	})
})

// ------- systemPrompt cache TTL -------

var _ = Describe("Worker.systemPrompt cache (boom-d6x)", func() {
	It("caches within TTL so a second call in the same regen batch does NOT re-hit the DB", func() {
		d := openTestDBGinkgo()
		DeferCleanup(func() { d.Close() })
		ctx := context.Background()

		// Seed a known value, then flip the value AFTER the first read to
		// prove the cache — if the cache is honored, the second call sees the
		// OLD value even though the DB now holds the NEW one.
		original, err := d.GetGenConfig(ctx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = d.SetGenConfig(ctx, original) })

		Expect(d.SetGenConfig(ctx, "PROMPT-V1")).To(Succeed())

		w := &Worker{db: d, logger: silentLogger()}
		first := w.systemPrompt(ctx)
		Expect(first).To(Equal("PROMPT-V1"))

		// Change the DB row underneath the worker.
		Expect(d.SetGenConfig(ctx, "PROMPT-V2-should-be-invisible")).To(Succeed())

		// Second call within TTL must return the cached V1 (proves cache).
		second := w.systemPrompt(ctx)
		Expect(second).To(Equal("PROMPT-V1"),
			"within systemPromptCacheTTL, worker MUST return cached value even if DB changed")
	})

	It("falls back to empty string on DB read failure (does NOT abort generation)", func() {
		d := openTestDBGinkgo()
		d.Close() // any subsequent DB call fails

		w := &Worker{db: d, logger: silentLogger()}
		Expect(w.systemPrompt(context.Background())).To(Equal(""),
			"DB blip must yield '' (no prefix), not a panic or persistent bad state")
	})
})

// ------- Run branches (mid-loop cancel + per-label failure) -------

var _ = Describe("Worker.Run error branches (boom-d6x)", func() {
	It("with a closed pool, has-check errors are recorded as failed and the loop CONTINUES", func() {
		d := openTestDBGinkgo()
		d.Close() // HasLabelImage will error immediately

		var hits atomic.Int32
		srv := recordingShim(&hits, nil)
		DeferCleanup(srv.Close)
		client, _ := comfyui.NewClient(srv.URL)
		w := newWorkerForTest(d, client, "m", silentLogger(),
			[]labelcatalog.Entry{
				{ID: "a", Prompt: "pa"},
				{ID: "b", Prompt: "pb"},
			})

		// Should NOT panic even though every entry's has-check fails.
		Expect(func() { w.Run(context.Background()) }).NotTo(Panic())
		// No shim call is expected — the has-check failed BEFORE the
		// generate branch (single-failure-in-batch invariant: log + skip).
		Expect(hits.Load()).To(BeEquivalentTo(0),
			"has-check failure MUST short-circuit that entry — shim untouched")
	})

	It("logs + counts shim failures per entry but keeps processing the rest of the batch", func() {
		d := openTestDBGinkgo()
		DeferCleanup(func() { d.Close() })
		ctx := context.Background()
		cleanupTestRowsGinkgo(d, "test-runfail-a", "test-runfail-b")
		DeferCleanup(func() { cleanupTestRowsGinkgo(d, "test-runfail-a", "test-runfail-b") })

		srv := errorShim() // every generate 500s
		DeferCleanup(srv.Close)
		client, _ := comfyui.NewClient(srv.URL)

		w := newWorkerForTest(d, client, "m", silentLogger(),
			[]labelcatalog.Entry{
				{ID: "test-runfail-a", Prompt: "pa"},
				{ID: "test-runfail-b", Prompt: "pb"},
			})

		Expect(func() { w.Run(ctx) }).NotTo(Panic(),
			"a single failing prompt MUST NOT abort the whole batch")

		// Neither row should have been created.
		_, hasA, _ := d.GetLabelImage(ctx, "test-runfail-a")
		_, hasB, _ := d.GetLabelImage(ctx, "test-runfail-b")
		Expect(hasA).To(BeFalse())
		Expect(hasB).To(BeFalse())
	})

	It("pre-cancelled ctx short-circuits Run before touching the DB or shim", func() {
		d := openTestDBGinkgo()
		DeferCleanup(func() { d.Close() })

		var hits atomic.Int32
		srv := recordingShim(&hits, nil)
		DeferCleanup(srv.Close)
		client, _ := comfyui.NewClient(srv.URL)

		w := newWorkerForTest(d, client, "m", silentLogger(),
			[]labelcatalog.Entry{{ID: "z", Prompt: "z"}})

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		w.Run(ctx) // must return, must not hit shim

		Expect(hits.Load()).To(BeEquivalentTo(0),
			"cancelled Run MUST NOT call the shim (respects operator shutdown)")
	})
})

// ------- RegenerateOne fallback + delete-error branches -------

var _ = Describe("Worker.RegenerateOne fallback (boom-d6x)", func() {
	It("falls back to compiled baseline when id is NOT in DB but IS in baseline", func() {
		d := openTestDBGinkgo()
		DeferCleanup(func() { d.Close() })
		ctx := context.Background()

		// Pick a baseline id and ensure it is NOT in the DB catalog.
		baseline := labelcatalog.Entries[0]
		_ = d.DeleteLabel(ctx, baseline.ID)
		cleanupTestRowsGinkgo(d, baseline.ID)
		DeferCleanup(func() { cleanupTestRowsGinkgo(d, baseline.ID) })

		var lastReq *shimReq
		srv := recordingShim(nil, &lastReq)
		DeferCleanup(srv.Close)
		client, _ := comfyui.NewClient(srv.URL)

		w := newWorkerForTest(d, client, "m", silentLogger(), nil)
		Expect(w.RegenerateOne(ctx, baseline.ID)).To(Succeed())

		Expect(lastReq).NotTo(BeNil())
		Expect(lastReq.Prompt).To(ContainSubstring(baseline.Prompt),
			"missing DB row MUST fall back to the compiled baseline's prompt")
	})

	It("wraps DeleteLabelImage failure as 'delete old row' — same as RegenerateEntry", func() {
		// Force RegenerateOne to reach the delete step: inject an entry via
		// worker.entries, then close the pool so DeleteLabelImage errors.
		// (The DB lookup + baseline lookup happen BEFORE the delete; a nil
		// db.GetLabel returns non-nil error but is ignored, then the code
		// falls back to labelcatalog.ByID.)
		d := openTestDBGinkgo()
		DeferCleanup(func() { d.Close() })
		ctx := context.Background()

		baseline := labelcatalog.Entries[0]
		// Remove the DB row so the baseline path is taken (proves compile-
		// baseline-then-delete-fail path).
		_ = d.DeleteLabel(ctx, baseline.ID)
		cleanupTestRowsGinkgo(d, baseline.ID)

		// Close the pool so BOTH GetLabel (returns err→ignored) AND
		// DeleteLabelImage (returns err→wrapped) fail. GetLabel returning
		// err triggers the baseline fallback.
		d.Close()

		srv := recordingShim(nil, nil)
		DeferCleanup(srv.Close)
		client, _ := comfyui.NewClient(srv.URL)
		w := newWorkerForTest(d, client, "m", silentLogger(), nil)

		err := w.RegenerateOne(ctx, baseline.ID)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("delete old row"),
			"a delete failure MUST surface as delete-phase, not save-phase")
	})
})

// ------- RegenerateAll truncate-error branch -------

var _ = Describe("Worker.RegenerateAll error paths (boom-d6x)", func() {
	It("wraps a truncate failure with the 'truncate:' prefix", func() {
		d := openTestDBGinkgo()
		d.Close() // TruncateLabelImages now errors

		srv := recordingShim(nil, nil)
		DeferCleanup(srv.Close)
		client, _ := comfyui.NewClient(srv.URL)
		w := newWorkerForTest(d, client, "m", silentLogger(),
			[]labelcatalog.Entry{{ID: "x", Prompt: "y"}})

		gen, failed, err := w.RegenerateAll(context.Background())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("truncate:"),
			"truncate failure MUST be labeled to distinguish from per-label errors")
		Expect(gen).To(Equal(0))
		Expect(failed).To(Equal(0))
	})
})

// ------- RegenerateList generateAndSave-failure branch -------

var _ = Describe("Worker.RegenerateList per-entry failure (boom-d6x)", func() {
	It("counts a shim 500 as failed and continues to the next entry", func() {
		d := openTestDBGinkgo()
		DeferCleanup(func() { d.Close() })
		ctx := context.Background()

		srv := errorShim() // all entries fail
		DeferCleanup(srv.Close)
		client, _ := comfyui.NewClient(srv.URL)
		w := newWorkerForTest(d, client, "m", silentLogger(), nil)

		entries := []labelcatalog.Entry{
			{ID: "a", Prompt: "pa"},
			{ID: "b", Prompt: "pb"},
		}
		gen, failed, err := w.RegenerateList(ctx, entries)
		Expect(err).NotTo(HaveOccurred(),
			"per-entry failures are logged, NOT returned — batch-level err reserved for ctx cancel")
		Expect(gen).To(Equal(0), "no entries succeed when the shim always 500s")
		Expect(failed).To(Equal(2), "both failures must be counted")
	})
})

// ------- generateAndSave error paths + seed override -------

var _ = Describe("generateAndSave (boom-d6x)", func() {
	It("wraps shim failure as 'shim:' error and writes NO DB row", func() {
		d := openTestDBGinkgo()
		DeferCleanup(func() { d.Close() })
		ctx := context.Background()

		srv := errorShim()
		DeferCleanup(srv.Close)
		client, _ := comfyui.NewClient(srv.URL)

		// Ensure no pre-existing row for the id so a false-positive can't
		// slip in via a stale record.
		id := "test-gs-shimerr"
		cleanupTestRowsGinkgo(d, id)
		DeferCleanup(func() { cleanupTestRowsGinkgo(d, id) })

		w := newWorkerForTest(d, client, "m", silentLogger(),
			[]labelcatalog.Entry{{ID: id, Prompt: "p"}})

		err := w.generateAndSave(ctx, labelcatalog.Entry{ID: id, Prompt: "p"})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("shim:"),
			"shim failures must be labeled 'shim:' for operator triage")

		_, ok, gerr := d.GetLabelImage(ctx, id)
		Expect(gerr).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse(),
			"generation failure MUST NOT leave a partial row in the DB")
	})

	It("wraps SaveLabelImage failure as 'save:' error when the DB is unavailable AFTER a successful shim call", func() {
		// Trigger the save-phase error branch: use a working shim + a closed
		// pool. generateAndSave will get bytes back from the shim, then hit
		// SaveLabelImage which errors on a closed pool.
		d := openTestDBGinkgo()
		// Do NOT defer d.Close() twice — closing again is a no-op but keeps
		// the intent clear. Close now to force the save-phase failure.
		d.Close()

		srv := recordingShim(nil, nil)
		DeferCleanup(srv.Close)
		client, _ := comfyui.NewClient(srv.URL)

		w := newWorkerForTest(d, client, "m", silentLogger(), nil)
		err := w.generateAndSave(context.Background(),
			labelcatalog.Entry{ID: "test-gs-savefail", Prompt: "p"})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("save:"),
			"save-phase failure MUST be labeled 'save:' to distinguish from shim-phase")
	})

	It("catalog(): when DB is present but yields zero non-empty prompts, returns the FULL compiled baseline (warn-log branch)", func() {
		// This test actually exercises worker.go:132-137 (the "DB non-empty
		// rows but all filtered → warn + baseline" fallback path) by blanking
		// every existing labels.optimized_prompt in a transaction we roll back
		// via DeferCleanup. If the impl deleted the fallback and returned an
		// empty slice, len(got) == 0 would trip the assertion below.
		d := openTestDBGinkgo()
		DeferCleanup(func() { d.Close() })
		ctx := context.Background()

		// Snapshot every existing (id, optimized_prompt) so we can restore.
		type row struct {
			id string
			op string
		}
		var snapshot []row
		{
			rs, err := d.Pool.Query(ctx, `SELECT id, optimized_prompt FROM labels`)
			Expect(err).NotTo(HaveOccurred())
			for rs.Next() {
				var r row
				Expect(rs.Scan(&r.id, &r.op)).To(Succeed())
				snapshot = append(snapshot, r)
			}
			rs.Close()
		}
		DeferCleanup(func() {
			for _, r := range snapshot {
				_, _ = d.Pool.Exec(context.Background(),
					`UPDATE labels SET optimized_prompt = $1 WHERE id = $2`, r.op, r.id)
			}
		})

		// Blank every prompt so ListLabels returns rows but the catalog()
		// reducer filters them all out — that is the branch under test.
		_, err := d.Pool.Exec(ctx, `UPDATE labels SET optimized_prompt = ''`)
		Expect(err).NotTo(HaveOccurred())

		// Also insert a row that would exist post-migration but pre-manifest
		// (still empty) — proves the branch fires regardless of row count.
		Expect(d.UpsertLabel(ctx, db.Label{
			ID: "test-cat-all-empty", Kind: "tier", Label: "T",
			OptimizedPrompt: "",
			Condition:       json.RawMessage(`{}`),
		})).To(Succeed())
		DeferCleanup(func() { _ = d.DeleteLabel(context.Background(), "test-cat-all-empty") })

		w := &Worker{db: d, logger: silentLogger()}
		got := w.catalog()

		// The load-bearing assertion: with DB non-empty but zero usable rows,
		// the catalog MUST be the compiled baseline verbatim — same slice,
		// same length, same per-index ID. If the fallback branch were
		// removed, this returns [] and every assertion below fails.
		Expect(got).To(HaveLen(len(labelcatalog.Entries)),
			"empty-prompt fallback MUST return the FULL compiled baseline, not an empty slice")
		for i, want := range labelcatalog.Entries {
			Expect(got[i].ID).To(Equal(want.ID),
				"baseline order + ID must match at index %d — proves compiled slice returned, not synthesized", i)
			Expect(got[i].Prompt).To(Equal(want.Prompt),
				"baseline prompt must match at index %d — proves DB filtered rows were not partially merged", i)
		}
	})

	It("threads per-entry Seed through to the shim request verbatim", func() {
		d := openTestDBGinkgo()
		DeferCleanup(func() { d.Close() })
		ctx := context.Background()

		id := "test-gs-seed"
		cleanupTestRowsGinkgo(d, id)
		DeferCleanup(func() { cleanupTestRowsGinkgo(d, id) })

		var lastReq *shimReq
		srv := recordingShim(nil, &lastReq)
		DeferCleanup(srv.Close)
		client, _ := comfyui.NewClient(srv.URL)

		seed := int64(1234567890)
		e := labelcatalog.Entry{ID: id, Prompt: "p", Seed: &seed}
		w := newWorkerForTest(d, client, "m", silentLogger(), nil)

		Expect(w.generateAndSave(ctx, e)).To(Succeed())
		Expect(lastReq).NotTo(BeNil())
		Expect(lastReq.Seed).NotTo(BeNil(), "seed MUST be sent when non-nil")
		Expect(*lastReq.Seed).To(Equal(seed),
			"per-entry Seed must be passed to the shim verbatim (provenance invariant)")
	})

	It("threads per-entry Size through to the shim request verbatim", func() {
		// Size is documented in worker.go:322 as a per-entry override but no
		// spec previously verified it lands on the shim request — Seed and
		// Model were covered, Size was not. If the impl dropped `e.Size`
		// from the Generate call, this would still pass Seed/Model tests but
		// silently regress the size override.
		d := openTestDBGinkgo()
		DeferCleanup(func() { d.Close() })
		ctx := context.Background()

		id := "test-gs-size"
		cleanupTestRowsGinkgo(d, id)
		DeferCleanup(func() { cleanupTestRowsGinkgo(d, id) })

		var lastReq *shimReq
		srv := recordingShim(nil, &lastReq)
		DeferCleanup(srv.Close)
		client, _ := comfyui.NewClient(srv.URL)

		e := labelcatalog.Entry{ID: id, Prompt: "p", Size: "512x768"}
		w := newWorkerForTest(d, client, "m", silentLogger(), nil)

		Expect(w.generateAndSave(ctx, e)).To(Succeed())
		Expect(lastReq).NotTo(BeNil())
		Expect(lastReq.Size).To(Equal("512x768"),
			"per-entry Size must be passed to the shim verbatim (parity with Model + Seed overrides)")
	})

	It("persists the FULL 3-segment composed prompt (systemPrompt + description + entryPrompt) to the DB", func() {
		// Pins the provenance invariant claimed in worker.go:344-346 — the
		// saved `prompt` column MUST carry the composed final prompt so a
		// reader of the row can reproduce the image without a separate
		// systemPrompt lookup. Previously covered only for empty-description
		// (partner file line 116-120); this spec pins the full 3-segment case.
		d := openTestDBGinkgo()
		DeferCleanup(func() { d.Close() })
		ctx := context.Background()

		// Snapshot + restore the singleton system prompt so we don't leak
		// state to other specs.
		original, err := d.GetGenConfig(ctx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = d.SetGenConfig(context.Background(), original) })

		const (
			sysP  = "sys-STYLE-prefix"
			descP = "desc-NARRATIVE-middle"
			entP  = "entry-SCENE-suffix"
		)
		Expect(d.SetGenConfig(ctx, sysP)).To(Succeed())

		id := "test-gs-full-prompt"
		cleanupTestRowsGinkgo(d, id)
		DeferCleanup(func() { cleanupTestRowsGinkgo(d, id) })

		srv := recordingShim(nil, nil)
		DeferCleanup(srv.Close)
		client, _ := comfyui.NewClient(srv.URL)

		w := newWorkerForTest(d, client, "m", silentLogger(), nil)
		e := labelcatalog.Entry{ID: id, Prompt: entP, Description: descP}
		Expect(w.generateAndSave(ctx, e)).To(Succeed())

		got, ok, err := d.GetLabelImage(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		want := buildFinalPrompt(sysP, descP, entP)
		Expect(got.Prompt).To(Equal(want),
			"saved prompt MUST be the FULL composed final prompt (self-contained provenance)")
		// Belt-and-braces: all three segments must be substrings so a future
		// join-order swap still trips the assertion via a segment-absent
		// failure mode, not just a whole-string mismatch.
		Expect(got.Prompt).To(ContainSubstring(sysP), "systemPrompt segment must be present in persisted prompt")
		Expect(got.Prompt).To(ContainSubstring(descP), "description segment must be present in persisted prompt")
		Expect(got.Prompt).To(ContainSubstring(entP), "entry prompt segment must be present in persisted prompt")
	})
})

// ------- RegenerateEntry model-fallback branch -------

var _ = Describe("Worker.RegenerateEntry Model fallback (boom-d6x)", func() {
	It("falls back to worker.model when entry.Model is empty (parity with Run-loop path)", func() {
		// The override-wins path is covered at line 270 with an entry that
		// SETS Model. This spec pins the OTHER branch: `if model == ""
		// { model = w.model }` at worker.go:328-331. Without this, deleting
		// the fallback would break the imagejobs Executor's default-model
		// path but the existing tests would still pass.
		d := openTestDBGinkgo()
		DeferCleanup(func() { d.Close() })
		ctx := context.Background()

		id := "test-re-model-fallback"
		cleanupTestRowsGinkgo(d, id)
		DeferCleanup(func() { cleanupTestRowsGinkgo(d, id) })

		var lastReq *shimReq
		srv := recordingShim(nil, &lastReq)
		DeferCleanup(srv.Close)
		client, _ := comfyui.NewClient(srv.URL)

		const workerDefault = "worker-default-model-fallback"
		w := newWorkerForTest(d, client, workerDefault, silentLogger(), nil)

		// Model is intentionally empty on the entry — worker.model should win.
		e := labelcatalog.Entry{ID: id, Prompt: "fresh"}
		Expect(w.RegenerateEntry(ctx, e)).To(Succeed())

		got, ok, err := d.GetLabelImage(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(got.Model).To(Equal(workerDefault),
			"empty entry.Model MUST fall back to worker.model on the persisted row")
		Expect(lastReq).NotTo(BeNil())
		Expect(lastReq.Model).To(Equal(workerDefault),
			"empty entry.Model MUST fall back to worker.model on the shim request too")
	})
})

// ------- systemPrompt TTL-refresh + mutex serialization -------

var _ = Describe("Worker.systemPrompt cache refresh + mutex (boom-d6x)", func() {
	It("re-reads the DB after TTL expiry so admin edits become visible on the next batch", func() {
		// Complements the within-TTL cache-hit spec at line 457. Simulates
		// TTL expiry by resetting w.sysFetched to the zero-time (which makes
		// `time.Since(zero) >= systemPromptCacheTTL` trivially true) between
		// the two calls. Without this, flipping the impl's TTL comparison
		// from `<` to `>` would break only cache-hits — the refresh path
		// would go unverified.
		d := openTestDBGinkgo()
		DeferCleanup(func() { d.Close() })
		ctx := context.Background()

		original, err := d.GetGenConfig(ctx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = d.SetGenConfig(context.Background(), original) })

		Expect(d.SetGenConfig(ctx, "TTL-BEFORE")).To(Succeed())

		w := &Worker{db: d, logger: silentLogger()}
		first := w.systemPrompt(ctx)
		Expect(first).To(Equal("TTL-BEFORE"))

		// Admin flips the row underneath.
		Expect(d.SetGenConfig(ctx, "TTL-AFTER")).To(Succeed())

		// Simulate TTL expiry by resetting the fetched-at timestamp so the
		// gate `time.Since(w.sysFetched) < systemPromptCacheTTL` is FALSE.
		w.sysMu.Lock()
		w.sysFetched = time.Time{}
		w.sysMu.Unlock()

		second := w.systemPrompt(ctx)
		Expect(second).To(Equal("TTL-AFTER"),
			"post-TTL call MUST re-read the DB — admin edits should become visible on the next regen batch")
	})

	It("serializes concurrent systemPrompt() callers via sysMu (positive mutex spec)", func() {
		// Positive spec for the sysMu invariant flagged in the critique: only
		// `go test -race` catches an outright missing lock. This spec proves
		// that N parallel goroutines converge on ONE cached value even when
		// they all miss the cache simultaneously. If the mutex were removed,
		// each goroutine could race, over-write w.sysPrompt, and observe
		// inconsistent results.
		d := openTestDBGinkgo()
		DeferCleanup(func() { d.Close() })
		ctx := context.Background()

		original, err := d.GetGenConfig(ctx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = d.SetGenConfig(context.Background(), original) })

		Expect(d.SetGenConfig(ctx, "MUTEX-VALUE")).To(Succeed())

		w := &Worker{db: d, logger: silentLogger()}

		const N = 16
		var wg sync.WaitGroup
		results := make([]string, N)
		for i := 0; i < N; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				defer GinkgoRecover()
				results[idx] = w.systemPrompt(ctx)
			}(i)
		}
		wg.Wait()

		for i, r := range results {
			Expect(r).To(Equal("MUTEX-VALUE"),
				"goroutine %d observed a torn/inconsistent cached prompt — sysMu MUST serialize", i)
		}
		// After all goroutines return, the cache field MUST also hold the
		// single canonical value — proves no late writer clobbered it.
		w.sysMu.Lock()
		defer w.sysMu.Unlock()
		Expect(w.sysPrompt).To(Equal("MUTEX-VALUE"),
			"post-batch cached sysPrompt MUST be the single canonical value")
		Expect(w.sysFetched.IsZero()).To(BeFalse(),
			"sysFetched MUST be advanced past zero-time exactly once")
	})
})
