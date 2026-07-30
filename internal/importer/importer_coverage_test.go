// importer_coverage_test.go — additional Ginkgo specs to lift importer
// coverage from ~66% to >= 90% (gaka-d6x). Each block pins a named invariant
// and, wherever possible, favors adversarial payloads / cross-key /
// no-oracle style over trivial roundtrips.
//
// The specs cluster into four buckets:
//
//  1. Pure helpers with no external I/O (jsonType predicates, name helpers,
//     driftCollector.checkObject/knownKeys, convertForDB fallbacks).
//  2. getRawJSON HTTP contract (401 → sentinel, non-200 → verbatim body,
//     malformed URL → err, request-context cancellation, non-JSON body).
//  3. Worker lifecycle end-to-end against a mock wakatime server (Postgres
//     required): RecoverInterrupted, StartJob happy-path + cancel-during-run,
//     run's 401-on-lookups branch, importDay's schema-drift-skips-day branch,
//     applyKeyOutcome's typed-token-absent-refresh branch,
//     FetchAllTimeRange happy-path + 401.
//  4. finishCancelled + withBackgroundTimeout invariants that are hard to
//     hit via `run` alone.
package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
)

// ---------------------------------------------------------------------------
// bucket 1 — pure helpers (no DB, no network)
// ---------------------------------------------------------------------------

var _ = Describe("checkJSONType predicate matrix (gaka-d6x)", func() {
	// Named invariant: EVERY jsonType constant either accepts or rejects a
	// given raw fragment deterministically — no partial coverage of the
	// switch means no silent behavior drift when the enum grows.
	type row struct {
		name  string
		raw   string
		wants map[jsonType]bool
	}
	cases := []row{
		{
			name: "JSON string",
			raw:  `"hi"`,
			wants: map[jsonType]bool{
				jtAny: true, jtString: true, jtStringOrNumber: true, jtStringOrNull: true,
				jtNumber: false, jtNumberOrNull: false, jtBool: false, jtBoolOrNull: false,
				jtArray: false, jtObject: false,
			},
		},
		{
			name: "JSON positive number",
			raw:  `42`,
			wants: map[jsonType]bool{
				jtAny: true, jtNumber: true, jtNumberOrNull: true, jtStringOrNumber: true,
				jtString: false, jtBool: false, jtArray: false, jtObject: false,
			},
		},
		{
			name: "JSON negative number",
			raw:  `-3.14`,
			wants: map[jsonType]bool{
				jtNumber: true, jtNumberOrNull: true, jtStringOrNumber: true,
				jtString: false,
			},
		},
		{
			name: "JSON true",
			raw:  `true`,
			wants: map[jsonType]bool{
				jtBool: true, jtBoolOrNull: true,
				jtString: false, jtNumber: false, jtArray: false,
			},
		},
		{
			name: "JSON false",
			raw:  `false`,
			wants: map[jsonType]bool{jtBool: true, jtBoolOrNull: true, jtString: false},
		},
		{
			name: "JSON null",
			raw:  `null`,
			wants: map[jsonType]bool{
				jtStringOrNull: true, jtNumberOrNull: true, jtBoolOrNull: true,
				jtArrayOrNull: true, jtObjectOrNull: true,
				jtString: false, jtNumber: false, jtBool: false, jtArray: false, jtObject: false,
			},
		},
		{
			name: "JSON array",
			raw:  `[]`,
			wants: map[jsonType]bool{jtArray: true, jtArrayOrNull: true, jtObject: false},
		},
		{
			name: "JSON object",
			raw:  `{}`,
			wants: map[jsonType]bool{jtObject: true, jtObjectOrNull: true, jtArray: false},
		},
		{
			name: "whitespace-padded object",
			raw:  "   \n\t {}\r  ",
			wants: map[jsonType]bool{jtObject: true, jtObjectOrNull: true},
		},
		{
			name: "empty fragment always rejects (defensive)",
			raw:  ``,
			wants: map[jsonType]bool{
				jtString: false, jtNumber: false, jtBool: false, jtArray: false, jtObject: false,
				jtStringOrNull: false, jtNumberOrNull: false, jtBoolOrNull: false,
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		It(tc.name, func() {
			for want, expect := range tc.wants {
				got := checkJSONType(json.RawMessage(tc.raw), want)
				Expect(got).To(Equal(expect),
					"checkJSONType(%q, %s) = %v, want %v", tc.raw, typeName(want), got, expect)
			}
		})
	}

	It("jtAny always passes even for empty fragment", func() {
		Expect(checkJSONType(json.RawMessage(""), jtAny)).To(BeTrue())
	})
})

var _ = Describe("typeName / rawTypeName exhaustive labels (gaka-d6x)", func() {
	// Named invariant: every jsonType has a stable human label — silently
	// returning "any" for a real enum entry would corrupt drift detail
	// messages the FE shows to users.
	It("typeName covers every named jsonType constant", func() {
		labels := map[jsonType]string{
			jtString:         "string",
			jtNumber:         "number",
			jtBool:           "boolean",
			jtArray:          "array",
			jtObject:         "object",
			jtStringOrNumber: "string|number",
			jtStringOrNull:   "string|null",
			jtNumberOrNull:   "number|null",
			jtBoolOrNull:     "boolean|null",
			jtArrayOrNull:    "array|null",
			jtObjectOrNull:   "object|null",
			jtAny:            "any",
		}
		for k, want := range labels {
			Expect(typeName(k)).To(Equal(want), "typeName(%d)", int(k))
		}
	})

	It("typeName falls back to 'any' for unknown numeric values (defensive default)", func() {
		Expect(typeName(jsonType(9999))).To(Equal("any"))
	})

	It("rawTypeName distinguishes all six JSON leaf shapes + empty", func() {
		Expect(rawTypeName(json.RawMessage(`"x"`))).To(Equal("string"))
		Expect(rawTypeName(json.RawMessage(`{}`))).To(Equal("object"))
		Expect(rawTypeName(json.RawMessage(`[]`))).To(Equal("array"))
		Expect(rawTypeName(json.RawMessage(`true`))).To(Equal("boolean"))
		Expect(rawTypeName(json.RawMessage(`false`))).To(Equal("boolean"))
		Expect(rawTypeName(json.RawMessage(`null`))).To(Equal("null"))
		Expect(rawTypeName(json.RawMessage(`0`))).To(Equal("number"))
		Expect(rawTypeName(json.RawMessage(`-1.5`))).To(Equal("number"))
		Expect(rawTypeName(json.RawMessage(``))).To(Equal("empty"))
		Expect(rawTypeName(json.RawMessage("  \t\n "))).To(Equal("empty"))
	})
})

var _ = Describe("driftCollector.checkObject + knownKeys (gaka-d6x)", func() {
	// Named invariant: checkObject applied to a stripped, malformed all_time
	// data object surfaces both `missing_required` and `type_changed` for
	// the same call — one payload, multiple flavors, deterministic order.
	It("checkObject flags missing_required + type_changed on the same object", func() {
		// range is required (missing here), and total_seconds is wrong type
		// (string instead of number). Both must appear as findings.
		obj := json.RawMessage(`{"total_seconds":"lots","text":"hi"}`)
		c := newDriftCollector()
		c.checkObject("all_time_since_today", obj, allTimeSpec)

		kinds := map[string]bool{}
		fields := map[string]bool{}
		for _, f := range c.findings() {
			kinds[f.Kind] = true
			fields[f.Field] = true
		}
		Expect(kinds).To(HaveKey(driftKindTypeChanged))
		Expect(kinds).To(HaveKey(driftKindMissingRequired))
		Expect(fields).To(HaveKey("total_seconds"))
		Expect(fields).To(HaveKey("range"))
	})

	It("knownKeys returns sorted slice matching schemaSpec.known cardinality", func() {
		got := heartbeatSpec.knownKeys()
		Expect(got).To(HaveLen(len(heartbeatSpec.known)))
		// Sorted invariant — critical for stable debug output.
		for i := 1; i < len(got); i++ {
			Expect(got[i-1] < got[i]).To(BeTrue(),
				"knownKeys not sorted at index %d: %q >= %q", i, got[i-1], got[i])
		}
		// And every key in the map appears exactly once in the slice.
		seen := map[string]int{}
		for _, k := range got {
			seen[k]++
		}
		for k := range heartbeatSpec.known {
			Expect(seen[k]).To(Equal(1), "known key %q missing/duplicated", k)
		}
	})
})

var _ = Describe("driftCollector.checkList envelope-defense (gaka-d6x)", func() {
	// Named invariant: checkList's inner json.Unmarshal is a belt-and-
	// suspenders guard; if a caller feeds it a non-array raw fragment
	// (bypassing checkEnvelope), it MUST still emit an error-severity
	// envelope_changed finding rather than panic or silently do nothing.
	It("non-array data raw → error-severity envelope_changed finding", func() {
		c := newDriftCollector()
		c.checkList("heartbeats", "2025-01-01", json.RawMessage(`"nope"`), heartbeatSpec, 5)
		f := c.findings()
		Expect(f).To(HaveLen(1))
		Expect(f[0].Kind).To(Equal(driftKindEnvelopeChanged))
		Expect(f[0].Severity).To(Equal(driftSeverityError))
		Expect(f[0].FirstSeenDay).To(Equal("2025-01-01"))
	})

	It("sample=-1 (unlimited) still bounded by len(items)", func() {
		body := `[{"id":"1","value":"v"},{"id":"2","value":"v"}]`
		c := newDriftCollector()
		c.checkList("user_agents", "", json.RawMessage(body), lookupSpec, -1)
		Expect(c.findings()).To(BeNil(), "clean baseline should produce no findings")
	})

	It("non-object list item → error-severity envelope_changed with detail", func() {
		body := `[42]` // number inside array where the schema expects an object
		c := newDriftCollector()
		c.checkList("user_agents", "d", json.RawMessage(body), lookupSpec, -1)
		f := c.findings()
		Expect(f).ToNot(BeEmpty())
		Expect(f[0].Kind).To(Equal(driftKindEnvelopeChanged))
		Expect(f[0].Severity).To(Equal(driftSeverityError))
		Expect(f[0].Detail).To(ContainSubstring("not a JSON object"))
	})
})

var _ = Describe("checkEnvelope non-object raw (gaka-d6x)", func() {
	// Named invariant: a top-level JSON scalar (array/string/number/null)
	// as the wakatime response is envelope_changed at error severity —
	// otherwise a wakatime.com regression that returned a raw list would
	// silently 500 the importer.
	It("raw array (no {data} wrapper) → error envelope_changed", func() {
		c := newDriftCollector()
		_, ok := c.checkEnvelope("heartbeats", []byte(`[]`), jtArray)
		Expect(ok).To(BeFalse())
		Expect(c.hasError()).To(BeTrue())
		Expect(c.findings()[0].Kind).To(Equal(driftKindEnvelopeChanged))
	})
})

var _ = Describe("convertForDB machine + ua fallbacks (gaka-d6x)", func() {
	// Named invariant: a heartbeat with unknown machine_name_id / user_agent_id
	// must not blank out those fields — machine falls back to the literal
	// "wakatime-import" sentinel (so operators can tell an unresolved row
	// from a real machine), and UserAgent stays the raw wakatime id lookup
	// (empty string), rather than "" without any indicator.
	It("unresolved machine id → 'wakatime-import' sentinel; nil machine id → 'wakatime-import'", func() {
		mid := "unknown-machine"
		hbs := []importHeartbeat{
			{UserAgentID: "known-ua", MachineNameID: &mid, Entity: "/x", Type: "file", Time: 1},
			{UserAgentID: "known-ua", MachineNameID: nil, Entity: "/y", Type: "file", Time: 2},
		}
		out := convertForDB("alice", map[string]string{}, map[string]string{"known-ua": "vscode/1"}, hbs)
		Expect(out).To(HaveLen(2))
		Expect(out[0].Machine).ToNot(BeNil())
		Expect(*out[0].Machine).To(Equal("wakatime-import"))
		Expect(*out[1].Machine).To(Equal("wakatime-import"))
	})

	It("machine id present + resolved → uses resolved name (not sentinel)", func() {
		mid := "mn-1"
		hbs := []importHeartbeat{{UserAgentID: "u", MachineNameID: &mid, Entity: "/x", Type: "file", Time: 3}}
		out := convertForDB("bob", map[string]string{"mn-1": "the-real-mac"}, map[string]string{"u": "vim/9"}, hbs)
		Expect(out).To(HaveLen(1))
		Expect(*out[0].Machine).To(Equal("the-real-mac"))
	})

	It("empty input → empty output (no nils, no panic)", func() {
		out := convertForDB("alice", nil, nil, nil)
		Expect(out).To(HaveLen(0))
		Expect(out).ToNot(BeNil()) // allocated slice, not nil
	})

	It("AI-assistance fields (gaka-1l9) pass through byte-for-byte", func() {
		inTok, outTok := int64(100), int64(200)
		sess := "sess-abc"
		hbs := []importHeartbeat{{
			UserAgentID: "u", Entity: "/x", Type: "file", Time: 1,
			AIInputTokens: &inTok, AIOutputTokens: &outTok, AISession: &sess,
		}}
		out := convertForDB("alice", nil, map[string]string{"u": "cursor/1"}, hbs)
		Expect(out).To(HaveLen(1))
		Expect(*out[0].AIInputTokens).To(Equal(int64(100)))
		Expect(*out[0].AIOutputTokens).To(Equal(int64(200)))
		Expect(*out[0].AISession).To(Equal("sess-abc"))
	})
})

// ---------------------------------------------------------------------------
// bucket 2 — getRawJSON HTTP contract
// ---------------------------------------------------------------------------

var _ = Describe("getRawJSON HTTP contract (gaka-d6x)", func() {
	// Named invariant: 401 from wakatime.com must produce an error that
	// wraps ErrWakatimeUnauthorized — the worker distinguishes bad-key from
	// network-failure precisely via errors.Is on this sentinel for
	// save-on-success and key_status.
	It("401 response → error wraps ErrWakatimeUnauthorized AND preserves upstream body", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"bad api key"}`)
		}))
		defer srv.Close()

		_, err := getRawJSON(context.Background(), srv.URL, "Basic zzz", nil)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrWakatimeUnauthorized)).To(BeTrue(),
			"401 must wrap ErrWakatimeUnauthorized (save-on-success key gaka-6jm.8)")
		Expect(err.Error()).To(ContainSubstring("bad api key"),
			"upstream body must be preserved for operator debugging")
	})

	It("non-200, non-401 (e.g. 429) → error mentions status code + body, does NOT wrap 401 sentinel", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `rate limited, please back off`)
		}))
		defer srv.Close()

		_, err := getRawJSON(context.Background(), srv.URL, "auth", nil)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrWakatimeUnauthorized)).To(BeFalse(),
			"429 must NOT wrap 401 sentinel — that would misclassify a rate-limit as a bad key")
		Expect(err.Error()).To(ContainSubstring("429"))
		Expect(err.Error()).To(ContainSubstring("rate limited"))
	})

	It("query values are urlencoded onto the endpoint", func() {
		var gotQuery string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.RawQuery
			_, _ = io.WriteString(w, `{"data":[]}`)
		}))
		defer srv.Close()

		q := url.Values{"date": {"2025-01-02"}, "extra": {"a b"}}
		body, err := getRawJSON(context.Background(), srv.URL, "auth", q)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(Equal(`{"data":[]}`))
		Expect(gotQuery).To(ContainSubstring("date=2025-01-02"))
		Expect(gotQuery).To(ContainSubstring("extra=a+b"))
	})

	It("malformed URL → error without doing any network I/O (never leaks a request)", func() {
		// Space in scheme host: http.NewRequestWithContext will reject.
		_, err := getRawJSON(context.Background(), "http://bad host/x", "auth", nil)
		Expect(err).To(HaveOccurred())
	})

	It("cancelled request context → error, no oracle for the caller other than ctx.Err()", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Block until client goes away.
			<-r.Context().Done()
		}))
		defer srv.Close()

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(20 * time.Millisecond)
			cancel()
		}()

		_, err := getRawJSON(ctx, srv.URL, "auth", nil)
		Expect(err).To(HaveOccurred())
	})
})

// ---------------------------------------------------------------------------
// bucket 3 — Worker lifecycle end-to-end
// ---------------------------------------------------------------------------

// silentLoggerCov drops every log record. Kept separate from silentLoggerGinkgo
// so this file doesn't have to poke at the other suite's helpers.
func silentLoggerCov() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// insertUserCov inserts a bare users row (no wakatime key) with DeferCleanup.
// Kept independent of the other test files' helpers.
func insertUserCov(database *db.DB, username string) {
	ctx := context.Background()
	_, err := database.Pool.Exec(ctx,
		`INSERT INTO users (username, hashed_password, salt_used) VALUES ($1, '\x00', '\x00') ON CONFLICT DO NOTHING`,
		username)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		bg := context.Background()
		_, _ = database.Pool.Exec(bg, `DELETE FROM heartbeats WHERE sender=$1`, username)
		_, _ = database.Pool.Exec(bg, `DELETE FROM projects WHERE owner=$1`, username)
		_, _ = database.Pool.Exec(bg, `DELETE FROM import_job_logs WHERE job_id IN (SELECT id FROM import_jobs WHERE owner=$1)`, username)
		_, _ = database.Pool.Exec(bg, `DELETE FROM import_jobs WHERE owner=$1`, username)
		_, _ = database.Pool.Exec(bg, `DELETE FROM users WHERE username=$1`, username)
	})
}

// buildRunWorker spins up a mock wakatime server with configurable handlers
// and returns an httptest.Server + a fresh Worker pointed at it.
type wakaHandler struct {
	uaBody, mnBody string
	uaStatus       int // 0 → 200
	mnStatus       int
	// hbHandler is called per /heartbeats hit. If nil, default 200 + empty data.
	hbHandler func(w http.ResponseWriter, r *http.Request)
}

func startWaka(h wakaHandler) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/users/current/user_agents", func(w http.ResponseWriter, _ *http.Request) {
		if h.uaStatus != 0 {
			w.WriteHeader(h.uaStatus)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, h.uaBody)
	})
	mux.HandleFunc("/api/v1/users/current/machine_names", func(w http.ResponseWriter, _ *http.Request) {
		if h.mnStatus != 0 {
			w.WriteHeader(h.mnStatus)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, h.mnBody)
	})
	if h.hbHandler != nil {
		mux.HandleFunc("/api/v1/users/current/heartbeats", h.hbHandler)
	} else {
		mux.HandleFunc("/api/v1/users/current/heartbeats", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"data":[]}`)
		})
	}
	return httptest.NewServer(mux)
}

var _ = Describe("Worker.RecoverInterrupted (gaka-d6x)", func() {
	// Named invariant: any queued/running rows left over from a crash MUST
	// be flipped to failed with the given reason — otherwise a restart
	// silently orphans a zombie job that never runs.
	It("marks queued + running leftover jobs failed with the given reason", func() {
		database := openImportOutcomeDBGinkgo()
		ctx := context.Background()

		owner := fmt.Sprintf("recov_%d", time.Now().UnixNano())
		insertUserCov(database, owner)

		start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		queued, err := database.CreateImportJob(ctx, owner, []byte(`{}`), start, start, 1)
		Expect(err).NotTo(HaveOccurred())
		running, err := database.CreateImportJob(ctx, owner, []byte(`{}`), start, start, 1)
		Expect(err).NotTo(HaveOccurred())
		_, err = database.MarkJobRunning(ctx, running.ID)
		Expect(err).NotTo(HaveOccurred())

		w := &Worker{db: database, logger: silentLoggerCov(), hub: NewHub()}
		w.RecoverInterrupted(ctx)

		q, err := database.GetJobByID(ctx, queued.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(q.State).To(Equal(db.JobStateFailed))
		Expect(q.Error).ToNot(BeNil())
		Expect(*q.Error).To(Equal("interrupted by restart"))

		r, err := database.GetJobByID(ctx, running.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.State).To(Equal(db.JobStateFailed))
		Expect(*r.Error).To(Equal("interrupted by restart"))
	})
})

var _ = Describe("Worker.StartJob happy-path (gaka-d6x)", func() {
	// Named invariant: StartJob spawns a goroutine that drives the job to
	// completed AND removes itself from the running registry after the
	// terminal DB write lands. Cancel on a since-completed job returns a
	// pre-closed done channel with running=false — the deregister step
	// runs after the terminal DB write, so the DB check downstream is safe.
	It("StartJob → completed; running registry drained after finished_at is set", func() {
		database := openImportOutcomeDBGinkgo()
		ctx := context.Background()

		owner := fmt.Sprintf("startjob_%d", time.Now().UnixNano())
		insertUserCov(database, owner)

		srv := startWaka(wakaHandler{
			uaBody: `{"data":[{"id":"ua-1","value":"vscode/1.0 (mac) my-editor/1.0"}]}`,
			mnBody: `{"data":[{"id":"mn-1","value":"mac"}]}`,
			hbHandler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, `{"data":[]}`)
			},
		})
		defer srv.Close()

		hub := NewHub()
		w := NewWorker(context.Background(), database, silentLoggerCov(), hub)
		w.BaseURL = srv.URL

		start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		payload := model.ImportRequestPayload{APIToken: "tok", StartDate: start, EndDate: start}
		item := QueueItem{Requester: owner, ReqPayload: payload}
		raw, _ := json.Marshal(item)
		job, err := database.CreateImportJob(ctx, owner, raw, start, start, TotalDays(start, start))
		Expect(err).NotTo(HaveOccurred())

		w.StartJob(job, item)

		// Wait for the job to drain from the running registry — that
		// happens AFTER the terminal DB write via the goroutine's defer
		// stack in StartJob (delete → cancel → close(done)).
		Eventually(func() bool {
			w.mu.Lock()
			defer w.mu.Unlock()
			_, ok := w.running[job.ID]
			return ok
		}, 3*time.Second, 10*time.Millisecond).Should(BeFalse(),
			"StartJob's goroutine never drained from running registry")

		final, err := database.GetJobByID(ctx, job.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(final.State).To(Equal(db.JobStateCompleted),
			"expected StartJob's happy-path to reach completed state")
		Expect(final.FinishedAt).ToNot(BeNil(),
			"finished_at must be set on terminal state before registry drains")

		// Cancel on a done job returns pre-closed done + running=false.
		done, running := w.Cancel(job.ID)
		Expect(running).To(BeFalse())
		select {
		case <-done:
		case <-time.After(50 * time.Millisecond):
			Fail("Cancel on already-terminal job should return pre-closed done")
		}
	})
})

var _ = Describe("Worker.run 401-on-lookups (gaka-d6x)", func() {
	// Named invariant: when the very first upstream call (user_agents)
	// returns 401, the run terminates as failed AND (given a previously
	// saved key) applyKeyOutcome flips wakatime_key_status to 'invalid'.
	// The typed token in the queue item MUST NOT overwrite the prior blob.
	It("401 on user_agents → job failed, key_status=invalid, typed token does NOT overwrite prior blob", func() {
		database := openImportOutcomeDBGinkgo()
		withEncryptionKeyGinkgo()
		ctx := context.Background()

		owner := fmt.Sprintf("run401_%d", time.Now().UnixNano())
		// Seed a prior saved key so UpdateWakatimeKeyStatus has a row to
		// touch (its guard requires encrypted_wakatime_key IS NOT NULL).
		priorCT := seedUserWithKeyGinkgo(database, owner, "prior_saved_key", db.WakatimeKeyStatusValid)

		srv := startWaka(wakaHandler{
			uaStatus: http.StatusUnauthorized,
			uaBody:   `{"error":"bad key"}`,
			mnBody:   `{"data":[]}`,
		})
		defer srv.Close()

		hub := NewHub()
		w := NewWorker(context.Background(), database, silentLoggerCov(), hub)
		w.BaseURL = srv.URL

		start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		payload := model.ImportRequestPayload{APIToken: "tok", StartDate: start, EndDate: start}
		item := QueueItem{Requester: owner, ReqPayload: payload, TypedToken: "never-persist-me"}
		raw, _ := json.Marshal(item)
		job, err := database.CreateImportJob(ctx, owner, raw, start, start, TotalDays(start, start))
		Expect(err).NotTo(HaveOccurred())

		w.run(ctx, job.ID, item)

		final, err := database.GetJobByID(ctx, job.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(final.State).To(Equal(db.JobStateFailed))

		info, err := database.GetWakatimeKeyInfo(ctx, owner)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.HasSavedKey).To(BeTrue(), "prior saved key must remain")
		// Byte-for-byte: the typed token MUST NOT overwrite the prior blob.
		Expect(len(info.Blob)).To(Equal(len(priorCT)))
		for i := range priorCT {
			Expect(info.Blob[i]).To(Equal(priorCT[i]),
				"typed-token ciphertext overwrote prior blob at byte %d — save-on-success (gaka-6jm.8) violated on 401", i)
		}
		Expect(info.Status).ToNot(BeNil())
		Expect(*info.Status).To(Equal(string(db.WakatimeKeyStatusInvalid)))
	})
})

var _ = Describe("Worker.run schema-drift-skips-day (gaka-d6x)", func() {
	// Named invariant: a NEW error-severity finding on a day's heartbeats
	// (missing required field) causes THAT day's insert to skip while the
	// job continues running — resiliency contract. Also proves drift
	// findings are persisted to the terminal job row.
	It("required field missing on heartbeats → day skipped, job STILL completes with drift persisted", func() {
		database := openImportOutcomeDBGinkgo()
		ctx := context.Background()

		owner := fmt.Sprintf("driftskip_%d", time.Now().UnixNano())
		insertUserCov(database, owner)

		// The importer processes start..end+1 days. Only the FIRST day
		// gets the malformed payload; subsequent days must return empty
		// so the drift-hasError latch doesn't cause them to insert
		// silently corrupted rows (the drift guard is `!before &&
		// hasError()`).
		var hbHits int32
		srv := startWaka(wakaHandler{
			uaBody: `{"data":[{"id":"ua-1","value":"vscode/1.0 (mac) my-editor/1.0"}]}`,
			mnBody: `{"data":[{"id":"mn-1","value":"mac"}]}`,
			hbHandler: func(w http.ResponseWriter, r *http.Request) {
				n := atomic.AddInt32(&hbHits, 1)
				if n == 1 {
					// heartbeats missing `entity` — required + error severity.
					_, _ = io.WriteString(w, `{"data":[{"user_agent_id":"ua-1","type":"file","time":1735689600.0}]}`)
					return
				}
				_, _ = io.WriteString(w, `{"data":[]}`)
			},
		})
		defer srv.Close()

		hub := NewHub()
		w := NewWorker(context.Background(), database, silentLoggerCov(), hub)
		w.BaseURL = srv.URL

		start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		payload := model.ImportRequestPayload{APIToken: "tok", StartDate: start, EndDate: start}
		item := QueueItem{Requester: owner, ReqPayload: payload}
		raw, _ := json.Marshal(item)
		job, err := database.CreateImportJob(ctx, owner, raw, start, start, TotalDays(start, start))
		Expect(err).NotTo(HaveOccurred())

		w.run(ctx, job.ID, item)

		final, err := database.GetJobByID(ctx, job.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(final.State).To(Equal(db.JobStateCompleted),
			"job must complete even after a day is skipped for missing required fields")
		Expect(final.ImportedCount).To(Equal(int64(0)),
			"the malformed day is skipped, the empty day inserts 0 → total 0")
		Expect(len(final.Drift)).To(BeNumerically(">", 0), "drift must be persisted")

		// The persisted drift contains the missing_required for entity.
		var findings []DriftFinding
		Expect(json.Unmarshal(final.Drift, &findings)).To(Succeed())
		sawMissingEntity := false
		for _, f := range findings {
			if f.Kind == driftKindMissingRequired && f.Field == "entity" && f.Severity == driftSeverityError {
				sawMissingEntity = true
				break
			}
		}
		Expect(sawMissingEntity).To(BeTrue(),
			"missing_required.entity finding must appear in persisted drift, got %+v", findings)
	})
})

var _ = Describe("Worker.applyKeyOutcome typed-token-absent-refresh (gaka-d6x)", func() {
	// Named invariant: a completed run with NO typed token still refreshes
	// wakatime_key_status → 'valid' on the users row (a clean run just
	// re-proved a previously-saved key works). Complements the .8 typed
	// token save case tested elsewhere.
	It("completed + no 401 + empty typed token → previously-saved key blob untouched, status='valid'", func() {
		database := openImportOutcomeDBGinkgo()
		withEncryptionKeyGinkgo()
		ctx := context.Background()

		user := fmt.Sprintf("refresh_%d", time.Now().UnixNano())
		priorCT := seedUserWithKeyGinkgo(database, user, "waka_prior", db.WakatimeKeyStatusInvalid)

		w := &Worker{db: database, logger: silentLoggerCov(), hub: NewHub()}
		item := QueueItem{Requester: user, TypedToken: ""} // deliberately empty
		w.applyKeyOutcome(item, db.JobStateCompleted, false)

		info, err := database.GetWakatimeKeyInfo(ctx, user)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.HasSavedKey).To(BeTrue())
		// Raw-bytes comparison: prior ciphertext must be byte-identical.
		Expect(len(info.Blob)).To(Equal(len(priorCT)))
		for i := range priorCT {
			Expect(info.Blob[i]).To(Equal(priorCT[i]),
				"blob mutated at byte %d — status-only refresh must not touch ciphertext", i)
		}
		Expect(info.Status).ToNot(BeNil())
		Expect(*info.Status).To(Equal(string(db.WakatimeKeyStatusValid)),
			"invalid → valid refresh should happen on clean run")
	})

	It("applyKeyOutcome default branch (failed + no 401 + empty typed token) is a no-op", func() {
		database := openImportOutcomeDBGinkgo()
		withEncryptionKeyGinkgo()
		ctx := context.Background()

		user := fmt.Sprintf("noop_%d", time.Now().UnixNano())
		seedUserNoKeyGinkgo(database, user)

		w := &Worker{db: database, logger: silentLoggerCov(), hub: NewHub()}
		item := QueueItem{Requester: user, TypedToken: ""}
		w.applyKeyOutcome(item, db.JobStateFailed, false)

		info, err := database.GetWakatimeKeyInfo(ctx, user)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.HasSavedKey).To(BeFalse(),
			"failed+no401+no typed token → no writes to users row")
	})
})

var _ = Describe("Worker.applyKeyOutcome encrypt-failure survives (gaka-d6x)", func() {
	// Named invariant: if auth.Encrypt fails (env-key unset), the import is
	// still considered a success from the user's perspective — no panic,
	// no write to encrypted_wakatime_key, and prior blob (if any) untouched.
	It("no BOOM_ENCRYPTION_KEY → encrypt fails, no ciphertext written, no panic", func() {
		database := openImportOutcomeDBGinkgo()
		// Deliberately do NOT install BOOM_ENCRYPTION_KEY. Reset any prior state.
		auth.ResetForTest()
		DeferCleanup(func() { auth.ResetForTest() })

		user := fmt.Sprintf("nokey_%d", time.Now().UnixNano())
		seedUserNoKeyGinkgo(database, user)

		w := &Worker{db: database, logger: silentLoggerCov(), hub: NewHub()}
		item := QueueItem{Requester: user, TypedToken: "would-be-plaintext"}

		Expect(func() {
			w.applyKeyOutcome(item, db.JobStateCompleted, false)
		}).NotTo(Panic())

		info, err := database.GetWakatimeKeyInfo(context.Background(), user)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.HasSavedKey).To(BeFalse(),
			"encrypt failure MUST NOT persist a partial/plaintext blob")
	})
})

// ---------------------------------------------------------------------------
// bucket 4 — finishCancelled + withBackgroundTimeout + StartJob cancel-race
// ---------------------------------------------------------------------------

var _ = Describe("Worker.finishCancelled (gaka-d6x)", func() {
	// Named invariant: finishCancelled uses a bounded BACKGROUND context so
	// even when the caller's context is already done, the terminal DB write
	// AND the "cancelled by user" log line both persist. Otherwise a user
	// cancel would silently leave the job in 'running'.
	It("writes 'cancelled' state + 'cancelled by user' log even when caller ctx is done", func() {
		database := openImportOutcomeDBGinkgo()
		ctx := context.Background()

		owner := fmt.Sprintf("fincxl_%d", time.Now().UnixNano())
		insertUserCov(database, owner)

		start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		job, err := database.CreateImportJob(ctx, owner, []byte(`{}`), start, start, 1)
		Expect(err).NotTo(HaveOccurred())
		_, err = database.MarkJobRunning(ctx, job.ID)
		Expect(err).NotTo(HaveOccurred())

		w := &Worker{db: database, logger: silentLoggerCov(), hub: NewHub()}
		published := 0
		publishJob := func(_ string, j *db.Job) {
			if j != nil {
				published++
			}
		}
		w.finishCancelled(job.ID, publishJob)

		final, err := database.GetJobByID(ctx, job.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(final.State).To(Equal(db.JobStateCancelled))
		Expect(final.FinishedAt).ToNot(BeNil())

		logs, err := database.GetJobLogs(ctx, job.ID, 0, 100)
		Expect(err).NotTo(HaveOccurred())
		sawCancelLog := false
		for _, l := range logs {
			if l.Level == "info" && l.Message == "cancelled by user" {
				sawCancelLog = true
			}
		}
		Expect(sawCancelLog).To(BeTrue(),
			"finishCancelled must persist the human-readable 'cancelled by user' log line")
		Expect(published).To(BeNumerically(">", 0), "publishJob should be invoked with the cancelled snapshot")
	})

	It("finishCancelled on already-terminal job is a no-op (CancelJob returns nil)", func() {
		database := openImportOutcomeDBGinkgo()
		ctx := context.Background()

		owner := fmt.Sprintf("finc2_%d", time.Now().UnixNano())
		insertUserCov(database, owner)

		start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		job, err := database.CreateImportJob(ctx, owner, []byte(`{}`), start, start, 1)
		Expect(err).NotTo(HaveOccurred())
		// Push to terminal state before finishCancelled sees it.
		_, err = database.FinishImportJob(ctx, job.ID, db.JobStateCompleted, nil)
		Expect(err).NotTo(HaveOccurred())

		w := &Worker{db: database, logger: silentLoggerCov(), hub: NewHub()}
		Expect(func() {
			w.finishCancelled(job.ID, func(_ string, _ *db.Job) {})
		}).NotTo(Panic())

		final, err := database.GetJobByID(ctx, job.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(final.State).To(Equal(db.JobStateCompleted),
			"finishCancelled must not overwrite a completed job's state")
	})
})

var _ = Describe("withBackgroundTimeout (gaka-d6x)", func() {
	// Named invariant: fn always receives a NON-nil context with a deadline
	// and the cancel is always called — no leaked contexts.
	It("passes a context with a deadline and calls cancel exactly once", func() {
		var got context.Context
		withBackgroundTimeout(50*time.Millisecond, func(ctx context.Context) {
			got = ctx
		})
		Expect(got).ToNot(BeNil())
		_, hasDeadline := got.Deadline()
		Expect(hasDeadline).To(BeTrue())
		// After the outer call returns, the ctx should be Done() (cancel invoked).
		select {
		case <-got.Done():
		case <-time.After(200 * time.Millisecond):
			Fail("withBackgroundTimeout did not cancel its child context")
		}
	})
})

// ---------------------------------------------------------------------------
// bucket 5 — FetchAllTimeRange (uses hardcoded wakatimeAPI → swap httpClient
// transport to intercept)
// ---------------------------------------------------------------------------

// fakeRoundTripper returns a canned response for any request. Used to
// intercept FetchAllTimeRange without touching the real wakatime.com.
type fakeRoundTripper struct {
	status int
	body   string
	saw    string // last URL seen (for assertions)
	err    error
}

func (f *fakeRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	f.saw = req.URL.String()
	if f.err != nil {
		return nil, f.err
	}
	return &http.Response{
		StatusCode: f.status,
		Body:       io.NopCloser(strings.NewReader(f.body)),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

var _ = Describe("FetchAllTimeRange (gaka-d6x)", func() {
	// Named invariant: the transport-level Authorization header must be
	// Basic <base64(apiToken)> — the caller provides a RAW apiToken and
	// FetchAllTimeRange does the single base64 wrap. Double-base64 would
	// 401 per gaka-f2l.
	It("happy-path: parses total_seconds + text + range + HasData=true", func() {
		prev := httpClient
		fake := &fakeRoundTripper{
			status: http.StatusOK,
			body:   `{"data":{"total_seconds":3600.5,"text":"1 hr","range":{"start_date":"2025-01-01","end_date":"2025-01-31"}}}`,
		}
		httpClient = &http.Client{Transport: fake, Timeout: 5 * time.Second}
		defer func() { httpClient = prev }()

		got, err := FetchAllTimeRange(context.Background(), "my-token")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.TotalSeconds).To(Equal(3600.5))
		Expect(got.Text).To(Equal("1 hr"))
		Expect(got.StartDate).To(Equal("2025-01-01"))
		Expect(got.EndDate).To(Equal("2025-01-31"))
		Expect(got.HasData).To(BeTrue())
		Expect(fake.saw).To(ContainSubstring("/api/v1/users/current/all_time_since_today"))
	})

	It("empty startDate → HasData=false (user with no wakatime history)", func() {
		prev := httpClient
		fake := &fakeRoundTripper{
			status: http.StatusOK,
			body:   `{"data":{"total_seconds":0,"text":"","range":{"start_date":"","end_date":""}}}`,
		}
		httpClient = &http.Client{Transport: fake, Timeout: 5 * time.Second}
		defer func() { httpClient = prev }()

		got, err := FetchAllTimeRange(context.Background(), "tok")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.HasData).To(BeFalse(),
			"empty start_date is the signal for 'no wakatime data' — HasData must be false")
	})

	It("401 → error wraps ErrWakatimeUnauthorized (same sentinel as import worker)", func() {
		prev := httpClient
		fake := &fakeRoundTripper{status: http.StatusUnauthorized, body: `{"error":"nope"}`}
		httpClient = &http.Client{Transport: fake, Timeout: 5 * time.Second}
		defer func() { httpClient = prev }()

		_, err := FetchAllTimeRange(context.Background(), "bad")
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrWakatimeUnauthorized)).To(BeTrue())
	})

	It("malformed JSON body → decode error (not silent zero-value success)", func() {
		prev := httpClient
		fake := &fakeRoundTripper{status: http.StatusOK, body: `{"data":not-json`}
		httpClient = &http.Client{Transport: fake, Timeout: 5 * time.Second}
		defer func() { httpClient = prev }()

		_, err := FetchAllTimeRange(context.Background(), "tok")
		Expect(err).To(HaveOccurred(),
			"caller MUST see decode errors — silently returning HasData=false would look identical to a legitimate empty-history user")
	})

	It("transport error (dead network) → error surfaces intact", func() {
		prev := httpClient
		fake := &fakeRoundTripper{err: &net.OpError{Op: "dial", Err: errors.New("network unreachable")}}
		httpClient = &http.Client{Transport: fake, Timeout: 5 * time.Second}
		defer func() { httpClient = prev }()

		_, err := FetchAllTimeRange(context.Background(), "tok")
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrWakatimeUnauthorized)).To(BeFalse())
	})
})

// ---------------------------------------------------------------------------
// bucket 6 — fetchLookups error branches (decode failure)
// ---------------------------------------------------------------------------

var _ = Describe("Worker.fetchLookups decode failure (gaka-d6x)", func() {
	// Named invariant: a wakatime response that is 200 OK but not valid
	// JSON causes fetchLookups to return an error mentioning "decode
	// user_agents" — the drift subsystem cannot mask this because the
	// typed decode ran BEFORE any drift check.
	It("200 OK with non-JSON body → 'decode user_agents' error", func() {
		srv := startWaka(wakaHandler{
			uaBody: `<html>oops</html>`, // not JSON
			mnBody: `{"data":[]}`,
		})
		defer srv.Close()

		w := &Worker{logger: silentLoggerCov(), hub: NewHub(), BaseURL: srv.URL}
		drift := newDriftCollector()
		_, _, err := w.fetchLookups(context.Background(), "Basic zzz", drift)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("decode user_agents"))
	})

	It("valid user_agents + non-JSON machine_names → 'decode machine_names' error", func() {
		srv := startWaka(wakaHandler{
			uaBody: `{"data":[{"id":"u","value":"v"}]}`,
			mnBody: `not-json-at-all`,
		})
		defer srv.Close()

		w := &Worker{logger: silentLoggerCov(), hub: NewHub(), BaseURL: srv.URL}
		drift := newDriftCollector()
		_, _, err := w.fetchLookups(context.Background(), "Basic zzz", drift)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("decode machine_names"))
	})

	It("machine_names 401 → error wraps ErrWakatimeUnauthorized (drives key_status=invalid)", func() {
		srv := startWaka(wakaHandler{
			uaBody:   `{"data":[]}`,
			mnStatus: http.StatusUnauthorized,
			mnBody:   `{"error":"nope"}`,
		})
		defer srv.Close()

		w := &Worker{logger: silentLoggerCov(), hub: NewHub(), BaseURL: srv.URL}
		drift := newDriftCollector()
		_, _, err := w.fetchLookups(context.Background(), "Basic zzz", drift)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrWakatimeUnauthorized)).To(BeTrue(),
			"401 on machine_names MUST wrap sentinel so applyKeyOutcome flips status=invalid")
	})
})

// ---------------------------------------------------------------------------
// bucket 7 — Worker.baseURL default branch
// ---------------------------------------------------------------------------

var _ = Describe("Worker.run cancellation-mid-import (gaka-d6x)", func() {
	// Named invariant: cancellation during a day's HTTP call causes the run
	// to reach the ctx-Err branch of the day loop → finishCancelled path.
	// The persisted terminal state is 'cancelled' (not 'failed'), even
	// though the underlying HTTP error was a plain "context cancelled".
	It("ctx cancelled while importDay is fetching → job ends as 'cancelled', not 'failed'", func() {
		database := openImportOutcomeDBGinkgo()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		owner := fmt.Sprintf("cancelmid_%d", time.Now().UnixNano())
		insertUserCov(database, owner)

		// heartbeats handler blocks until ctx.Done — gives us a stable
		// window to trigger cancel while `importDay` is mid-fetch.
		srv := startWaka(wakaHandler{
			uaBody: `{"data":[{"id":"ua-1","value":"vscode/1.0 (mac) my-editor/1.0"}]}`,
			mnBody: `{"data":[{"id":"mn-1","value":"mac"}]}`,
			hbHandler: func(w http.ResponseWriter, r *http.Request) {
				<-r.Context().Done()
			},
		})
		defer srv.Close()

		w := NewWorker(context.Background(), database, silentLoggerCov(), NewHub())
		w.BaseURL = srv.URL

		start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		payload := model.ImportRequestPayload{APIToken: "tok", StartDate: start, EndDate: start}
		item := QueueItem{Requester: owner, ReqPayload: payload}
		raw, _ := json.Marshal(item)
		job, err := database.CreateImportJob(context.Background(), owner, raw, start, start, TotalDays(start, start))
		Expect(err).NotTo(HaveOccurred())

		// Cancel shortly after run starts — importDay will be stuck in
		// the blocking hbHandler.
		go func() {
			time.Sleep(80 * time.Millisecond)
			cancel()
		}()
		w.run(ctx, job.ID, item)

		final, err := database.GetJobByID(context.Background(), job.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(final.State).To(Equal(db.JobStateCancelled),
			"ctx cancel during importDay must surface as 'cancelled', not 'failed'")
		Expect(final.FinishedAt).ToNot(BeNil())
	})
})

var _ = Describe("Worker.baseURL default (gaka-d6x)", func() {
	// Named invariant: an unset BaseURL falls back to the wakatime.com
	// constant — otherwise production callers would silently hit the
	// empty string.
	It("empty BaseURL → wakatimeAPI constant", func() {
		w := &Worker{}
		Expect(w.baseURL()).To(Equal(wakatimeAPI))
	})

	It("explicit BaseURL is preserved verbatim", func() {
		w := &Worker{BaseURL: "http://my.test.local:1234"}
		Expect(w.baseURL()).To(Equal("http://my.test.local:1234"))
	})
})
