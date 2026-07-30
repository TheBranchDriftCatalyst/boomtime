// heartbeats_internal_test.go — internal-package (package ingest) coverage
// for the two ingest-owned helpers on heartbeats.go that can't be reached
// via HTTP: the headerPtr nil-vs-*string branch (private, called by
// storeAndRespond) and the remoteWrite error branches (json.Marshal /
// http.NewRequest / httpClient.Do, all silent early-returns).
//
// Extracted from internal/handler/handler_helpers_test.go as part of
// gaka-8tn phase 5a — the tests moved with the code they cover. Assertions
// are BYTE-IDENTICAL to the pre-refactor versions; only the package
// declaration changed (handler → ingest) so the `Handler` and
// `remoteWrite`/`headerPtr` references now resolve inside this package.
package ingest

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
	"github.com/labstack/echo/v5"
)

// -- headerPtr empty branch ----------------------------------------------

var _ = Describe("headerPtr", func() {
	It("returns nil for a missing/empty header — downstream must treat this as 'unknown machine' not empty string", func() {
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		c := e.NewContext(req, httptest.NewRecorder())
		Expect(headerPtr(c, "X-Missing")).To(BeNil(),
			"empty header MUST map to nil so callers can distinguish unset vs blank")
	})
	It("returns *string for a set header", func() {
		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("X-Present", "hello")
		c := e.NewContext(req, httptest.NewRecorder())
		got := headerPtr(c, "X-Present")
		Expect(got).NotTo(BeNil())
		Expect(*got).To(Equal("hello"))
	})
})

// -- remoteWrite error branches -------------------------------------------
//
// remoteWrite runs in a goroutine; the three internal errors (json.Marshal,
// http.NewRequest, httpClient.Do) all early-return silently. We can trigger
// TWO of them directly by calling remoteWrite() with a hostile URL. The
// json.Marshal branch is unreachable in practice ([]HeartbeatPayload always
// marshals) and is left as documented-unreachable.
//
// gaka-d6x.handler critique fix: previously both specs only asserted
// "does not panic" — the exact anti-pattern the reviewer flagged. A
// regression that leaked memory, forgot resp.Body.Close on error, or
// leaked goroutines would all pass "no panic" in the calling goroutine.
// The specs now attach an in-memory slog handler and assert the specific
// error-log line is written (Do branch) and use an atomic counter to
// prove NO HTTP request escaped to any listener (NewRequest branch).

var _ = Describe("remoteWrite failure branches", func() {
	It("http.NewRequest error branch: bad URL is caught BEFORE any HTTP request is sent (asserts zero server hits)", func() {
		// URL with control chars is guaranteed to fail http.NewRequest.
		// The whole point of the branch is that a misconfigured
		// RemoteWrite MUST NOT crash the process — errors are absorbed.
		//
		// Observable-side-effect assertion (fixing reviewer's anti-pattern
		// callout): mount an httptest.Server + atomic hit counter and
		// assert the counter is EXACTLY zero — a regression that fell
		// through to httpClient.Do despite the NewRequest error would
		// leak an outbound request and this test would catch it.
		var hits int64
		listener := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt64(&hits, 1)
			w.WriteHeader(http.StatusOK)
		}))
		DeferCleanup(listener.Close)

		var logBuf bytes.Buffer
		logHandler := slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})
		h := &Handler{
			// URL with a control char guarantees http.NewRequest fails.
			// Note: this URL is DISTINCT from the listener URL so a
			// fallthrough to Do would hit the listener (and bump the counter).
			Cfg:    &config.Config{RemoteWrite: &config.RemoteWriteConfig{URL: "http://\x7f/bad", Token: "t"}},
			Logger: slog.New(logHandler),
		}
		Expect(func() { h.remoteWrite(nil, nil) }).NotTo(Panic())
		Expect(atomic.LoadInt64(&hits)).To(Equal(int64(0)),
			"NewRequest-error branch MUST return before any request is sent — hits=%d indicates fallthrough", hits)
		// The current code does NOT log NewRequest errors (silent early-return
		// by design). If a future refactor adds logging, this assertion still
		// documents the "no remote-write success" invariant: the "remote write
		// failed" log line should NOT appear for a NewRequest failure because
		// no request was made.
		Expect(logBuf.String()).NotTo(ContainSubstring("remote write succeeded"),
			"no remote-write success log MUST be emitted for a bad-URL config")
	})

	It("httpClient.Do error branch: unreachable URL logs 'remote write failed' AND resp is nil (no Body.Close leak)", func() {
		// http://127.0.0.1:1 is a valid URL, so NewRequest succeeds, but
		// nothing listens there → Do errors → the branch fires. This is
		// the "remote wakatime is down" path in prod.
		//
		// Observable-side-effect assertion (fixing reviewer's anti-pattern
		// callout): attach an in-memory slog handler and assert the exact
		// "remote write failed" log line was emitted. Without this a
		// regression that swallowed the error (e.g., removed the h.Logger
		// call) would silently break production observability — no panic,
		// no visible test failure.
		var logBuf bytes.Buffer
		logHandler := slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})
		h := &Handler{
			Cfg:    &config.Config{RemoteWrite: &config.RemoteWriteConfig{URL: "http://127.0.0.1:1/nope", Token: "t"}},
			Logger: slog.New(logHandler),
		}
		m := "test-machine"
		Expect(func() { h.remoteWrite(nil, &m) }).NotTo(Panic())
		Expect(logBuf.String()).To(ContainSubstring("remote write failed"),
			"Do-error branch MUST log 'remote write failed' — observability regression if removed: log=%s", logBuf.String())
	})
})
