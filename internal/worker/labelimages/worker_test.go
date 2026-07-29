// worker_ginkgo_test.go — ginkgo mirror of worker_test.go.
// 1:1 case map (6 stdlib TestXxx):
//
//	TestWorker_Run_GeneratesMissing              → Worker.Run > "generates missing rows and records provenance"
//	TestWorker_Run_SkipsExisting                 → Worker.Run > "skips labels that already have a row on a second Run"
//	TestWorker_RegenerateAll_TruncatesAndReplaces → Worker.RegenerateAll > "truncates existing rows and re-generates every label"
//	TestWorker_RegenerateOne_UnknownID           → Worker.RegenerateOne > "returns error for unknown id"
//	TestBuildFinalPrompt_JoinsThreeSegments (6 subtests) → DescribeTable("buildFinalPrompt joins three segments") with 6 Entries
//	TestWorker_NilReceiver_NoOp                  → Worker (nil receiver) > 3 Its (Run no-op, RegenerateOne err, RegenerateAll err)
package labelimages

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/comfyui"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/labelcatalog"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// openTestDBGinkgo mirrors openTestDB but Skips inside a ginkgo spec when
// Postgres is unreachable — uses the same URL-resolution rules.
func openTestDBGinkgo() *db.DB {
	url := testDatabaseURL
	if v := os.Getenv("BOOM_TEST_DATABASE_URL"); v != "" {
		url = v
	}
	ctx := context.Background()
	d, err := db.New(ctx, url)
	if err != nil {
		Skip("labelimages worker test: no test DB (" + url + "): " + err.Error())
	}
	return d
}

// pngBytesGinkgo mirrors pngBytes from the stdlib file.
func pngBytesGinkgo(tag string) []byte {
	return append([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, []byte(tag)...)
}

// shimServerGinkgo mirrors shimServer — uses GinkgoRecover so Expect panics
// inside the handler goroutine bubble as spec failures.
func shimServerGinkgo(hits *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer GinkgoRecover()
		if hits != nil {
			hits.Add(1)
		}
		if r.URL.Path != "/v1/images/generations" {
			http.Error(w, "unexpected path", 404)
			return
		}
		defer r.Body.Close()
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		prompt, _ := req["prompt"].(string)
		bytes := pngBytesGinkgo(prompt)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(bytes)}},
		})
	}))
}

func fixtureEntriesGinkgo() []labelcatalog.Entry {
	return []labelcatalog.Entry{
		{ID: "test-w-a", Prompt: "prompt A"},
		{ID: "test-w-b", Prompt: "prompt B"},
	}
}

func cleanupTestRowsGinkgo(d *db.DB, ids ...string) {
	for _, id := range ids {
		_ = d.DeleteLabelImage(context.Background(), id)
	}
}

var _ = Describe("Worker.Run", func() {
	It("generates missing rows and records provenance", func() {
		d := openTestDBGinkgo()
		DeferCleanup(func() { d.Close() })
		cleanupTestRowsGinkgo(d, "test-w-a", "test-w-b")
		DeferCleanup(func() { cleanupTestRowsGinkgo(d, "test-w-a", "test-w-b") })

		var hits atomic.Int32
		srv := shimServerGinkgo(&hits)
		DeferCleanup(func() { srv.Close() })

		client, err := comfyui.NewClient(srv.URL)
		Expect(err).NotTo(HaveOccurred())
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		w := newWorkerForTest(d, client, "test-model", logger, fixtureEntriesGinkgo())

		w.Run(context.Background())

		Expect(hits.Load()).To(BeEquivalentTo(2), "one hit per label")

		got, ok, err := d.GetLabelImage(context.Background(), "test-w-a")
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())

		// Match the worker's final-prompt construction: systemPrompt from DB
		// prefixed to the per-label prompt. Description is empty in the
		// fixture, so buildFinalPrompt drops it.
		sysPrompt, _ := d.GetGenConfig(context.Background())
		expected := buildFinalPrompt(sysPrompt, "", "prompt A")

		Expect(string(got.ImageBytes)).To(Equal(string(pngBytesGinkgo(expected))))
		Expect(got.Model).To(Equal("test-model"))
		Expect(got.Prompt).To(Equal(expected))
	})

	It("skips labels that already have a row on a second Run", func() {
		d := openTestDBGinkgo()
		DeferCleanup(func() { d.Close() })
		cleanupTestRowsGinkgo(d, "test-w-a", "test-w-b")
		DeferCleanup(func() { cleanupTestRowsGinkgo(d, "test-w-a", "test-w-b") })

		var hits atomic.Int32
		srv := shimServerGinkgo(&hits)
		DeferCleanup(func() { srv.Close() })

		client, _ := comfyui.NewClient(srv.URL)
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		w := newWorkerForTest(d, client, "test-model", logger, fixtureEntriesGinkgo())

		w.Run(context.Background())
		first := hits.Load()
		w.Run(context.Background()) // second run — should be a no-op
		second := hits.Load()

		Expect(second).To(Equal(first), "shim must not be called on second run")
	})
})

var _ = Describe("Worker.RegenerateAll", func() {
	It("truncates existing rows and re-generates every label", func() {
		d := openTestDBGinkgo()
		DeferCleanup(func() { d.Close() })
		cleanupTestRowsGinkgo(d, "test-w-a", "test-w-b")
		DeferCleanup(func() { cleanupTestRowsGinkgo(d, "test-w-a", "test-w-b") })

		var hits atomic.Int32
		srv := shimServerGinkgo(&hits)
		DeferCleanup(func() { srv.Close() })

		client, _ := comfyui.NewClient(srv.URL)
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		w := newWorkerForTest(d, client, "model-v1", logger, fixtureEntriesGinkgo())

		// Seed both labels with a stub row so we can prove Truncate wiped them.
		Expect(d.SaveLabelImage(context.Background(), "test-w-a", pngBytesGinkgo("OLD-A"), "image/png", "old-model", "old prompt", nil)).To(Succeed())
		Expect(d.SaveLabelImage(context.Background(), "test-w-b", pngBytesGinkgo("OLD-B"), "image/png", "old-model", "old prompt", nil)).To(Succeed())

		gen, failed, err := w.RegenerateAll(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(failed).To(Equal(0))
		Expect(gen).To(Equal(2))

		// Row must now carry the NEW model + NEW bytes.
		got, _, _ := d.GetLabelImage(context.Background(), "test-w-a")
		Expect(got).NotTo(BeNil())
		Expect(got.Model).To(Equal("model-v1"))
		Expect(string(got.ImageBytes)).NotTo(Equal(string(pngBytesGinkgo("OLD-A"))),
			"Truncate should have wiped old seed and worker should have re-generated")
	})
})

var _ = Describe("Worker.RegenerateOne", func() {
	It("returns error for unknown id", func() {
		d := openTestDBGinkgo()
		DeferCleanup(func() { d.Close() })

		srv := shimServerGinkgo(nil)
		DeferCleanup(func() { srv.Close() })

		client, _ := comfyui.NewClient(srv.URL)
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		w := newWorkerForTest(d, client, "test-model", logger, fixtureEntriesGinkgo())

		err := w.RegenerateOne(context.Background(), "does-not-exist-in-catalog")
		Expect(err).To(HaveOccurred())
	})
})

var _ = DescribeTable("buildFinalPrompt joins three segments (systemPrompt, description, entryPrompt)",
	func(sys, desc, prompt, want string) {
		Expect(buildFinalPrompt(sys, desc, prompt)).To(Equal(want))
	},
	Entry("all three populated",
		"cyberpunk emblem",
		"a machine-like coder who never sleeps",
		"half-android at terminal",
		"cyberpunk emblem, a machine-like coder who never sleeps, half-android at terminal",
	),
	Entry("empty description falls back to old {sys, prompt}",
		"cyberpunk emblem",
		"",
		"half-android at terminal",
		"cyberpunk emblem, half-android at terminal",
	),
	Entry("empty systemPrompt keeps {desc, prompt}",
		"",
		"a machine-like coder",
		"half-android",
		"a machine-like coder, half-android",
	),
	Entry("only prompt populated returns just the prompt",
		"",
		"",
		"half-android",
		"half-android",
	),
	Entry("whitespace-only counts as empty",
		"   \n  \t",
		"narrative",
		"scene",
		"narrative, scene",
	),
	Entry("all empty returns empty string",
		"",
		"",
		"",
		"",
	),
)

var _ = Describe("Worker (nil receiver) — feature-disabled gate", func() {
	It("Run is a graceful no-op (does not panic)", func() {
		var w *Worker
		Expect(func() { w.Run(context.Background()) }).NotTo(Panic())
	})

	It("RegenerateOne returns an error (not silent success)", func() {
		var w *Worker
		err := w.RegenerateOne(context.Background(), "late-night-coder")
		Expect(err).To(HaveOccurred())
	})

	It("RegenerateAll returns an error (not silent success)", func() {
		var w *Worker
		_, _, err := w.RegenerateAll(context.Background())
		Expect(err).To(HaveOccurred())
	})
})

// -- helpers restored from stdlib partner (gaka-0vp.17) --
const testDatabaseURL = "postgres://test:test@localhost:5432/boomtime_test?sslmode=disable"
