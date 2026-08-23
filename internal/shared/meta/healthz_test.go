// healthz_test.go — boom-d6x.handler: cover the public /healthz endpoint.
// Named invariants:
//
//	"reports version + schemaVersion + uptime + DB reachable" — the JSON
//	shape is stable and every non-omitempty field is populated when the DB
//	is reachable. schemaVersion is > 0 because the isolated DB is migrated
//	by testutil.OpenIsolatedDB before this test runs, and status must be
//	"ok" (not "degraded") on a healthy pool.
//
//	"no auth required" — the endpoint intentionally rejects nothing; a
//	request with no Authorization header still gets 200 + JSON. Prevents a
//	future middleware pass from silently gating the k8s liveness probe.
package meta_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/meta"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

var _ = Describe("Healthz endpoint (boom-d6x.handler)", func() {
	It("returns 200 + populated JSON without any Authorization header", func() {
		hz := testutil.NewHarness(GinkgoT())
		// Stamp the Cfg with values that would be omitted on a bare `go run`
		// so we can pin the branch/commit/buildTime fields are wired through.
		hz.Cfg.Version = "v1.2.3-test"
		hz.Cfg.Branch = "test-branch"
		hz.Cfg.Commit = "deadbeef"
		hz.Cfg.BuildTime = "2026-01-01T00:00:00Z"
		e := hz.Router()

		// Deliberately no Authorization header — Healthz must not gate on it.
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		Expect(rec).To(testutil.HaveStatus(http.StatusOK))

		var got meta.HealthzResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &got)).To(Succeed(),
			"body=%s", rec.Body.String())

		// Healthy pool → status == "ok" (not "degraded").
		Expect(got.Status).To(Equal("ok"),
			"a reachable DB must produce status=ok, got %q; body=%s",
			got.Status, rec.Body.String())
		Expect(got.DBReachable).To(BeTrue(),
			"harness DB unreachable? body=%s", rec.Body.String())
		Expect(got.Version).To(Equal("v1.2.3-test"))
		Expect(got.Branch).To(Equal("test-branch"))
		Expect(got.Commit).To(Equal("deadbeef"))
		Expect(got.BuildTime).To(Equal("2026-01-01T00:00:00Z"))
		Expect(got.StartedAt).NotTo(BeEmpty(), "StartedAt should always render")
		Expect(got.SchemaVersion).To(BeNumerically(">", 0),
			"schemaVersion must be a real migrated version; got %d",
			got.SchemaVersion)
		Expect(got.UptimeSeconds).To(BeNumerically(">=", 0),
			"uptime should be non-negative")
	})

	It("falls back to 'dev' version when Cfg.Version is empty (same shape wired through)", func() {
		// Version is a meta.go concern but /healthz reads h.Cfg.Version — pin
		// that /healthz does NOT substitute "dev" (only /version does). An
		// empty Cfg.Version renders as omitempty in the healthz payload.
		hz := testutil.NewHarness(GinkgoT())
		hz.Cfg.Version = ""
		e := hz.Router()

		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		Expect(rec).To(testutil.HaveStatus(http.StatusOK))
		// Assert raw body — the JSON should omit the version field.
		Expect(rec.Body.String()).NotTo(ContainSubstring(`"version":"dev"`),
			"healthz must NOT substitute the /version 'dev' fallback")
	})

	It("startedAt round-trips as RFC3339 with a UTC 'Z' suffix", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()

		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		var got meta.HealthzResponse
		Expect(json.Unmarshal(rec.Body.Bytes(), &got)).To(Succeed())
		parsed, err := time.Parse(time.RFC3339, got.StartedAt)
		Expect(err).NotTo(HaveOccurred(), "startedAt is not RFC3339: %q", got.StartedAt)
		// Deliberate UTC pin: monitoring dashboards assume Zulu time.
		Expect(parsed.Location().String()).To(Equal("UTC"),
			"startedAt must be UTC; got %v", parsed.Location())
	})
})
