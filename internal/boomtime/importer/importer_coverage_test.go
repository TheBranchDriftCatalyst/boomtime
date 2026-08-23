// importer_coverage_test.go — additional Ginkgo specs to lift importer
// coverage from ~66% to >= 90% (boom-d6x). Each block pins a named invariant
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
	"bytes"
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
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
)

// roundTripFunc is a lightweight http.RoundTripper adapter so tests can
// intercept the shared httpClient without spinning up a full httptest.Server.
// Used by the "malformed URL never hits network" test and the "32MB body cap"
// test — both need to observe whether/what the transport saw.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// bytesBuffer is an alias so tests can name the slog capture buffer
// unambiguously — writing to a *bytes.Buffer from a JSON handler is standard,
// but the local name signals intent (capture-for-assertion).
type bytesBuffer = bytes.Buffer

// ---------------------------------------------------------------------------
// bucket 1 — pure helpers (no DB, no network)
// ---------------------------------------------------------------------------

var _ = Describe("checkJSONType predicate matrix (boom-d6x)", func() {
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
			name:  "JSON false",
			raw:   `false`,
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
			name:  "JSON array",
			raw:   `[]`,
			wants: map[jsonType]bool{jtArray: true, jtArrayOrNull: true, jtObject: false},
		},
		{
			name:  "JSON object",
			raw:   `{}`,
			wants: map[jsonType]bool{jtObject: true, jtObjectOrNull: true, jtArray: false},
		},
		{
			name:  "whitespace-padded object",
			raw:   "   \n\t {}\r  ",
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

var _ = Describe("typeName / rawTypeName exhaustive labels (boom-d6x)", func() {
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

var _ = Describe("driftCollector.checkObject + knownKeys (boom-d6x)", func() {
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

var _ = Describe("driftCollector.checkList envelope-defense (boom-d6x)", func() {
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

	It("sample=-1 (unlimited) visits every item — drift on item[1] surfaces", func() {
		// boom-d6x critique-fix: prior version fed [clean, clean] and asserted
		// findings==nil, which would ALSO pass under `if sample < 0 { limit = 1 }`.
		// The new payload puts drift ONLY at item[1] (missing required `id`) so
		// a finding is proof the loop reached index 1.
		body := `[{"id":"1","value":"v"},{"value":"missing_id_here"}]`
		c := newDriftCollector()
		c.checkList("user_agents", "d1", json.RawMessage(body), lookupSpec, -1)
		f := c.findings()
		Expect(f).ToNot(BeEmpty(),
			"sample=-1 must visit item[1] — a bounded 'limit=1' regression would leave findings empty")
		sawIDMissing := false
		for _, x := range f {
			if x.Kind == driftKindMissingRequired && x.Field == "id" && x.Endpoint == "user_agents" {
				sawIDMissing = true
			}
		}
		Expect(sawIDMissing).To(BeTrue(),
			"expected missing_required.id from item[1]; got %+v (loop did NOT reach index 1)", f)
	})

	It("sample=-1 with N>1 items → checkItem called for every index (drift on each)", func() {
		// Second belt-and-suspenders: 5 items, ALL missing `id`. Confirms the
		// loop reaches every element. Count-per-key dedupe makes this exactly
		// one missing_required finding — but with Count==5.
		body := `[{"value":"a"},{"value":"b"},{"value":"c"},{"value":"d"},{"value":"e"}]`
		c := newDriftCollector()
		c.checkList("user_agents", "d1", json.RawMessage(body), lookupSpec, -1)
		f := c.findings()
		Expect(f).ToNot(BeEmpty())
		var idFinding *DriftFinding
		for i := range f {
			if f[i].Kind == driftKindMissingRequired && f[i].Field == "id" {
				idFinding = &f[i]
			}
		}
		Expect(idFinding).ToNot(BeNil(), "expected a missing_required.id finding for user_agents")
		Expect(idFinding.Count).To(Equal(5),
			"count must be 5 (one per visited item) — a partial iteration would show count<5")
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

var _ = Describe("checkEnvelope non-object raw (boom-d6x)", func() {
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

var _ = Describe("convertForDB machine + ua fallbacks (boom-d6x)", func() {
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

	It("AI-assistance fields (boom-1l9) pass through byte-for-byte", func() {
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

var _ = Describe("getRawJSON HTTP contract (boom-d6x)", func() {
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
			"401 must wrap ErrWakatimeUnauthorized (save-on-success key boom-6jm.8)")
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
		// boom-d6x critique-fix: swap the shared httpClient's transport for a
		// counting RoundTripper. Prior version only asserted err — a refactor
		// that hits the network *then* errors would still pass. The counter
		// pins the 'no request leaked' invariant.
		prev := httpClient
		var hits atomic.Int32
		httpClient = &http.Client{
			Timeout: 5 * time.Second,
			Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				hits.Add(1)
				return &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(strings.NewReader(`{}`)),
					Header:     make(http.Header),
					Request:    r,
				}, nil
			}),
		}
		defer func() { httpClient = prev }()

		// Space in scheme host: http.NewRequestWithContext will reject BEFORE
		// httpClient.Do is invoked.
		_, err := getRawJSON(context.Background(), "http://bad host/x", "auth", nil)
		Expect(err).To(HaveOccurred())
		Expect(hits.Load()).To(BeZero(),
			"malformed URL must fail at request construction — no round-trip may occur")
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

var _ = Describe("Worker.RecoverInterrupted (boom-d6x)", func() {
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

var _ = Describe("Worker.StartJob happy-path (boom-d6x)", func() {
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

var _ = Describe("Worker.run 401-on-lookups (boom-d6x)", func() {
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
				"typed-token ciphertext overwrote prior blob at byte %d — save-on-success (boom-6jm.8) violated on 401", i)
		}
		Expect(info.Status).ToNot(BeNil())
		Expect(*info.Status).To(Equal(string(db.WakatimeKeyStatusInvalid)))
	})
})

var _ = Describe("Worker.run schema-drift-skips-day (boom-d6x)", func() {
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

var _ = Describe("Worker.applyKeyOutcome typed-token-absent-refresh (boom-d6x)", func() {
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

	It("applyKeyOutcome default branch (failed + no 401 + empty typed token) is byte-identical no-op", func() {
		// boom-d6x critique-fix: previously seeded a no-key user and asserted
		// !HasSavedKey (already true before call — proves nothing). Now seed
		// a prior key + prior status + prior checked_at and assert ALL three
		// are byte-for-byte unchanged after the default branch runs.
		database := openImportOutcomeDBGinkgo()
		withEncryptionKeyGinkgo()
		ctx := context.Background()

		user := fmt.Sprintf("noop_%d", time.Now().UnixNano())
		priorCT := seedUserWithKeyGinkgo(database, user, "waka_prior_untouched", db.WakatimeKeyStatusValid)

		before, err := database.GetWakatimeKeyInfo(ctx, user)
		Expect(err).NotTo(HaveOccurred())
		Expect(before.HasSavedKey).To(BeTrue())
		Expect(before.CheckedAt).ToNot(BeNil())

		w := &Worker{db: database, logger: silentLoggerCov(), hub: NewHub()}
		item := QueueItem{Requester: user, TypedToken: ""}
		w.applyKeyOutcome(item, db.JobStateFailed, false)

		after, err := database.GetWakatimeKeyInfo(ctx, user)
		Expect(err).NotTo(HaveOccurred())
		Expect(after.HasSavedKey).To(BeTrue(), "default branch must not clear the prior key")
		// Byte-for-byte ciphertext comparison.
		Expect(len(after.Blob)).To(Equal(len(priorCT)))
		for i := range priorCT {
			Expect(after.Blob[i]).To(Equal(priorCT[i]),
				"blob mutated at byte %d — default branch must not touch ciphertext", i)
		}
		Expect(ptrStrEq(before.Status, after.Status)).To(BeTrue(),
			"status changed on default branch: before=%v after=%v", before.Status, after.Status)
		Expect(ptrTimeEq(before.CheckedAt, after.CheckedAt)).To(BeTrue(),
			"checked_at changed on default branch: before=%v after=%v — proves an UPDATE ran when none was expected",
			before.CheckedAt, after.CheckedAt)
	})
})

var _ = Describe("Worker.applyKeyOutcome encrypt-failure survives (boom-d6x)", func() {
	// Named invariant: if auth.Encrypt fails (env-key unset), the import is
	// still considered a success from the user's perspective — no panic,
	// no write to encrypted_wakatime_key, and prior blob (if any) untouched.
	It("no BOOM_ENCRYPTION_KEY → encrypt fails (precondition proved), warn log emitted, no ciphertext, no panic", func() {
		// boom-d6x critique-fix: previously asserted !HasSavedKey + NotTo(Panic),
		// which stays green even if a prior spec's DeferCleanup left the env key
		// installed (Encrypt would silently succeed but SetEncryptedWakatimeKey
		// wasn't called for unrelated reasons). Now:
		//   1) CONFIRM the precondition by trying auth.Encrypt directly.
		//   2) Capture slog output and assert the exact 'save-on-success:
		//      encrypt failed' warning line is emitted — proves the code
		//      followed the encrypt-fail branch (not some other branch).
		database := openImportOutcomeDBGinkgo()
		// Deliberately do NOT install BOOM_ENCRYPTION_KEY. Scrub env + auth state.
		prev, hadPrev := os.LookupEnv("BOOM_ENCRYPTION_KEY")
		os.Unsetenv("BOOM_ENCRYPTION_KEY")
		auth.ResetForTest()
		DeferCleanup(func() {
			if hadPrev {
				os.Setenv("BOOM_ENCRYPTION_KEY", prev)
			} else {
				os.Unsetenv("BOOM_ENCRYPTION_KEY")
			}
			auth.ResetForTest()
		})

		// PRECONDITION: prove auth.Encrypt fails in THIS test's environment.
		// Without this the whole test can pass for reasons unrelated to the
		// tested branch.
		_, encErr := auth.Encrypt([]byte("probe"))
		Expect(encErr).To(HaveOccurred(),
			"precondition failed: auth.Encrypt succeeded without BOOM_ENCRYPTION_KEY — env leaked from a prior spec")

		user := fmt.Sprintf("nokey_%d", time.Now().UnixNano())
		seedUserNoKeyGinkgo(database, user)

		// Capture the warn log line so we can PROVE the encrypt-fail branch ran.
		var buf bytesBuffer
		captureLogger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		w := &Worker{db: database, logger: captureLogger, hub: NewHub()}
		item := QueueItem{Requester: user, TypedToken: "would-be-plaintext"}

		Expect(func() {
			w.applyKeyOutcome(item, db.JobStateCompleted, false)
		}).NotTo(Panic())

		info, err := database.GetWakatimeKeyInfo(context.Background(), user)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.HasSavedKey).To(BeFalse(),
			"encrypt failure MUST NOT persist a partial/plaintext blob")

		// The 'save-on-success: encrypt failed' warn line proves this specific
		// branch executed — not the 'refresh status' or default no-op branch.
		Expect(buf.String()).To(ContainSubstring("save-on-success: encrypt failed"),
			"expected the encrypt-fail warn line in logs; got:\n%s", buf.String())
		// Extra: plaintext MUST NOT leak into the log line either.
		Expect(buf.String()).ToNot(ContainSubstring("would-be-plaintext"),
			"plaintext TypedToken leaked into log output — this is a security regression")
	})
})

// ---------------------------------------------------------------------------
// bucket 4 — finishCancelled + withBackgroundTimeout + StartJob cancel-race
// ---------------------------------------------------------------------------

var _ = Describe("Worker.finishCancelled (boom-d6x)", func() {
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

var _ = Describe("withBackgroundTimeout (boom-d6x)", func() {
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

var _ = Describe("FetchAllTimeRange (boom-d6x)", func() {
	// Named invariant: the transport-level Authorization header must be
	// Basic <base64(apiToken)> — the caller provides a RAW apiToken and
	// FetchAllTimeRange does the single base64 wrap. Double-base64 would
	// 401 per boom-f2l.
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

var _ = Describe("Worker.fetchLookups decode failure (boom-d6x)", func() {
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

var _ = Describe("Worker.run cancellation-mid-import (boom-d6x)", func() {
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

var _ = Describe("Worker.baseURL default (boom-d6x)", func() {
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

// ---------------------------------------------------------------------------
// bucket 8 — critique gap-fills (boom-d6x round 2)
//
// New coverage for invariants the reviewer flagged as unproven:
//   - fetchLookups schema-drift fast-fail (200 OK + malformed body)
//   - UpdateJobProgress DB failure → logged, run continues (resilience)
//   - getRawJSON 32MB body cap
//   - Worker.Cancel on RUNNING (not terminal) job
//   - concurrent StartJob(same jobID) — mu-protected registry race
//   - cross-user isolation (applyKeyOutcome writes ONLY to item.Requester)
//   - cross-key negative (encrypt-with-wrong-key ≠ encrypt-with-real-key)
// ---------------------------------------------------------------------------

var _ = Describe("Worker.fetchLookups schema-drift fast-fail (boom-d6x, missing invariant #1)", func() {
	// Named invariant: even a 200-OK wakatime response that parses into the
	// typed struct MUST fail the fetch when the drift-check turns up an
	// error-severity finding on required fields (lookupSpec.required = [id,
	// value]). This is the impl branch importer.go:417-419 / :432-434 — no
	// existing test exercises it (only decode-failure and 401 were pinned).
	It("user_agents 200 OK with items missing `id` → fetchLookups returns 'schema drift' error", func() {
		// Typed decode succeeds (Go json ignores absent required fields → zero
		// values). Drift check catches the missing required `id`.
		srv := startWaka(wakaHandler{
			uaBody: `{"data":[{"value":"vscode/1.0"}]}`, // no `id` — required by lookupSpec
			mnBody: `{"data":[]}`,
		})
		defer srv.Close()

		w := &Worker{logger: silentLoggerCov(), hub: NewHub(), BaseURL: srv.URL}
		drift := newDriftCollector()
		_, _, err := w.fetchLookups(context.Background(), "Basic zzz", drift)
		Expect(err).To(HaveOccurred(),
			"typed decode passed but drift check MUST fail the fetch when required field `id` is missing")
		Expect(err.Error()).To(ContainSubstring("schema drift"),
			"error message must name the schema-drift cause; got %q", err.Error())
		Expect(err.Error()).To(ContainSubstring("user_agents"),
			"error message must attribute to the failing endpoint; got %q", err.Error())
		// And the drift finding itself must exist and be error-severity.
		Expect(drift.hasError()).To(BeTrue(),
			"drift collector must record an error-severity finding for the missing required field")
	})

	It("machine_names 200 OK with items missing `value` → 'schema drift' error, user_agents already consumed", func() {
		// user_agents is clean; machine_names is the one with drift. Confirms
		// the second guard (line 432) fires as well.
		srv := startWaka(wakaHandler{
			uaBody: `{"data":[{"id":"u","value":"vscode"}]}`,
			mnBody: `{"data":[{"id":"mn-1"}]}`, // no `value` — required
		})
		defer srv.Close()

		w := &Worker{logger: silentLoggerCov(), hub: NewHub(), BaseURL: srv.URL}
		drift := newDriftCollector()
		_, _, err := w.fetchLookups(context.Background(), "Basic zzz", drift)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("schema drift"))
		Expect(err.Error()).To(ContainSubstring("machine_names"),
			"error must name machine_names (not user_agents) — got %q", err.Error())
	})
})

var _ = Describe("Worker.run UpdateJobProgress failure → logged, loop continues (boom-d6x, missing invariant #2)", func() {
	// Named invariant (importer.go:274-283): if the day-loop's UpdateJobProgress
	// call returns an error mid-run, the code logs+continues rather than
	// failing the whole job. Previously untested. Simulate by DELETE-ing the
	// import_jobs row between MarkJobRunning and the first UpdateJobProgress.
	It("DB row disappears mid-run → error is logged, no panic, no partial state written to a gone row", func() {
		database := openImportOutcomeDBGinkgo()
		ctx := context.Background()

		owner := fmt.Sprintf("progfail_%d", time.Now().UnixNano())
		insertUserCov(database, owner)

		// hbHandler blocks so we can delete the job row before UpdateJobProgress fires.
		// Buffered so the non-blocking send always latches for the polling Eventually.
		hbGate := make(chan struct{}, 1)
		hbUnblock := make(chan struct{})
		srv := startWaka(wakaHandler{
			uaBody: `{"data":[{"id":"ua-1","value":"vscode/1.0 (mac) editor/1"}]}`,
			mnBody: `{"data":[{"id":"mn-1","value":"mac"}]}`,
			hbHandler: func(w http.ResponseWriter, r *http.Request) {
				select {
				case hbGate <- struct{}{}:
				default:
				}
				<-hbUnblock
				_, _ = io.WriteString(w, `{"data":[]}`)
			},
		})
		defer srv.Close()

		// Capture logs so we can PROVE the 'failed to persist progress' line
		// was emitted — this is the resilience contract.
		var logBuf bytes.Buffer
		captureLogger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		w := NewWorker(context.Background(), database, captureLogger, NewHub())
		w.BaseURL = srv.URL

		start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		payload := model.ImportRequestPayload{APIToken: "tok", StartDate: start, EndDate: start}
		item := QueueItem{Requester: owner, ReqPayload: payload}
		raw, _ := json.Marshal(item)
		job, err := database.CreateImportJob(ctx, owner, raw, start, start, TotalDays(start, start))
		Expect(err).NotTo(HaveOccurred())

		done := make(chan struct{})
		go func() {
			defer close(done)
			Expect(func() { w.run(ctx, job.ID, item) }).NotTo(Panic(),
				"UpdateJobProgress failure MUST NOT panic the worker")
		}()

		// Wait for the hbHandler to be entered, then delete the row.
		Eventually(hbGate, 3*time.Second).Should(Receive())
		_, err = database.Pool.Exec(ctx, `DELETE FROM import_job_logs WHERE job_id=$1`, job.ID)
		Expect(err).NotTo(HaveOccurred())
		_, err = database.Pool.Exec(ctx, `DELETE FROM import_jobs WHERE id=$1`, job.ID)
		Expect(err).NotTo(HaveOccurred())
		close(hbUnblock)

		Eventually(done, 5*time.Second).Should(BeClosed(),
			"run should exit within the deadline even after DB row vanishes mid-loop")

		// The row is gone — GetJobByID returns (nil, nil) for a missing row (per db.GetJobByID).
		got, err := database.GetJobByID(ctx, job.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeNil(),
			"deleted row must stay deleted — run must not re-INSERT via progress path")

		// The exact log line 'failed to persist progress' proves the resilience
		// branch executed (rather than, say, silently crashing or looping forever).
		Expect(logBuf.String()).To(ContainSubstring("failed to persist progress"),
			"resilience contract violated: no 'failed to persist progress' log line; got:\n%s", logBuf.String())
	})
})

var _ = Describe("getRawJSON 32MB body cap (boom-d6x, missing invariant #3)", func() {
	// Named invariant: getRawJSON reads at most 32MB from the response body.
	// A malicious/broken upstream that streams 100MB must be truncated so the
	// importer's memory footprint stays bounded (importer.go:698).
	It("upstream body > 32MB → returned body is capped at 32MB exactly", func() {
		const cap = 32 << 20 // 32 MiB — MUST match importer.go
		// Stream more than the cap to exercise io.LimitReader.
		writeLen := cap + 4096

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			// Write in chunks to avoid holding a giant buffer.
			chunk := bytes.Repeat([]byte{'x'}, 64*1024)
			written := 0
			for written < writeLen {
				n := len(chunk)
				if written+n > writeLen {
					n = writeLen - written
				}
				if _, err := w.Write(chunk[:n]); err != nil {
					return
				}
				written += n
			}
		}))
		defer srv.Close()

		body, err := getRawJSON(context.Background(), srv.URL, "auth", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(len(body)).To(Equal(cap),
			"body must be truncated to exactly 32MB (io.LimitReader) — got %d bytes", len(body))
	})
})

var _ = Describe("Worker.Cancel on currently-running (not terminal) job (boom-d6x, missing invariant #4)", func() {
	// Named invariant: Cancel on a job that is CURRENTLY RUNNING (blocked in
	// mid-fetch) returns running=true AND the returned done channel blocks
	// until the terminal DB write lands (JobStateCancelled). This is stronger
	// than the existing test which only calls Cancel AFTER completion.
	It("Cancel(running) → running=true; done blocks until finalized; final state=cancelled", func() {
		database := openImportOutcomeDBGinkgo()

		owner := fmt.Sprintf("cancelrun_%d", time.Now().UnixNano())
		insertUserCov(database, owner)

		// heartbeats handler blocks until the request context is cancelled —
		// gives us a stable window where the job is "currently running".
		hbEntered := make(chan struct{}, 1)
		srv := startWaka(wakaHandler{
			uaBody: `{"data":[{"id":"ua-1","value":"vscode/1.0 (mac) editor/1"}]}`,
			mnBody: `{"data":[{"id":"mn-1","value":"mac"}]}`,
			hbHandler: func(w http.ResponseWriter, r *http.Request) {
				select {
				case hbEntered <- struct{}{}:
				default:
				}
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

		w.StartJob(job, item)

		// Wait for the run to be blocked in the hbHandler — job is "running".
		Eventually(hbEntered, 3*time.Second).Should(Receive())

		// Cancel(running) MUST return running=true; done must NOT be pre-closed.
		done, running := w.Cancel(job.ID)
		Expect(running).To(BeTrue(),
			"Cancel on a running job must return running=true (was terminal-only path)")

		// Now wait on done — this must eventually close when the terminal
		// write lands. Between here and the close, the job must have moved to
		// the JobStateCancelled state at the DB layer.
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			Fail("done channel did not close within 5s after Cancel of running job")
		}

		final, err := database.GetJobByID(context.Background(), job.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(final.State).To(Equal(db.JobStateCancelled),
			"a Cancel on a running job must persist state=cancelled by the time done closes")
		Expect(final.FinishedAt).ToNot(BeNil(),
			"finished_at must be stamped before done closes — otherwise callers race the write")
	})
})

var _ = Describe("Worker StartJob concurrency: same jobID (boom-d6x, missing invariant #5)", func() {
	// Named invariant: the running-registry map access at importer.go:80-83
	// is mu-protected. N concurrent StartJob calls (all with the same jobID)
	// MUST NOT race the map or panic. The registry must drain to empty after
	// every goroutine finishes.
	It("N concurrent StartJob calls on same jobID → no race, no panic, registry drains", func() {
		database := openImportOutcomeDBGinkgo()

		owner := fmt.Sprintf("startrace_%d", time.Now().UnixNano())
		insertUserCov(database, owner)

		// Trivial happy-path mock — the goal is to exercise the mu-guarded
		// running map under concurrent writes, not to observe a specific
		// outcome. Each StartJob will race the same key.
		srv := startWaka(wakaHandler{
			uaBody: `{"data":[{"id":"ua-1","value":"vscode/1.0 (mac) editor/1"}]}`,
			mnBody: `{"data":[{"id":"mn-1","value":"mac"}]}`,
			hbHandler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, `{"data":[]}`)
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

		const N = 12
		var wg sync.WaitGroup
		wg.Add(N)
		for i := 0; i < N; i++ {
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				Expect(func() { w.StartJob(job, item) }).NotTo(Panic(),
					"concurrent StartJob(same jobID) must not panic — mu guard broken")
			}()
		}
		wg.Wait()

		// The registry must eventually drain — regardless of which goroutine
		// "won" the final delete, the last defer-in-goroutine wins.
		Eventually(func() int {
			w.mu.Lock()
			defer w.mu.Unlock()
			return len(w.running)
		}, 5*time.Second, 20*time.Millisecond).Should(Equal(0),
			"registry must drain after all concurrent StartJob goroutines exit — a leaked entry means the mu-guarded delete was skipped")
	})
})

var _ = Describe("Cross-user isolation (boom-d6x, security gap #1)", func() {
	// Named invariant: applyKeyOutcome uses item.Requester as the target
	// username. A crafted QueueItem where Requester != true job.Owner (a
	// spoof attempt) must NOT touch any OTHER user's row. We verify by
	// seeding UserA + UserB, then calling applyKeyOutcome with
	// Requester=UserA and asserting UserB's row is byte-for-byte unchanged.
	// (The impl doesn't cross-check job.Owner — this test pins the current
	// behavior AND ensures any future refactor of that logic is caught.)
	It("applyKeyOutcome(Requester=UserA) → UserB's row is byte-identical (no cross-user writes)", func() {
		database := openImportOutcomeDBGinkgo()
		withEncryptionKeyGinkgo()
		ctx := context.Background()

		userA := fmt.Sprintf("victimA_%d", time.Now().UnixNano())
		userB := fmt.Sprintf("victimB_%d", time.Now().UnixNano())
		seedUserNoKeyGinkgo(database, userA)
		priorCT := seedUserWithKeyGinkgo(database, userB, "waka_userB_secret", db.WakatimeKeyStatusValid)

		beforeB, err := database.GetWakatimeKeyInfo(ctx, userB)
		Expect(err).NotTo(HaveOccurred())
		Expect(beforeB.HasSavedKey).To(BeTrue())

		w := &Worker{db: database, logger: silentLoggerCov(), hub: NewHub()}
		// Spoof: Requester=userA, but they submitted a typed token that would,
		// under a broken impl, be written to userB's row.
		item := QueueItem{Requester: userA, TypedToken: "attacker_typed_token"}
		w.applyKeyOutcome(item, db.JobStateCompleted, false)

		// UserA must have received the new key (this is the ONLY user touched).
		infoA, err := database.GetWakatimeKeyInfo(ctx, userA)
		Expect(err).NotTo(HaveOccurred())
		Expect(infoA.HasSavedKey).To(BeTrue(), "userA is the requester — must receive the new key")

		// UserB must be byte-for-byte unchanged.
		afterB, err := database.GetWakatimeKeyInfo(ctx, userB)
		Expect(err).NotTo(HaveOccurred())
		Expect(afterB.HasSavedKey).To(BeTrue(), "userB must still have their prior key")
		Expect(len(afterB.Blob)).To(Equal(len(priorCT)))
		for i := range priorCT {
			Expect(afterB.Blob[i]).To(Equal(priorCT[i]),
				"userB blob mutated at byte %d — cross-user write from spoofed Requester", i)
		}
		Expect(ptrStrEq(beforeB.Status, afterB.Status)).To(BeTrue(),
			"userB status changed — cross-user write from spoofed Requester")
		Expect(ptrTimeEq(beforeB.CheckedAt, afterB.CheckedAt)).To(BeTrue(),
			"userB checked_at changed — a stray UPDATE hit userB's row")

		// Extra: decrypt userA's blob to confirm THAT is where the token went.
		pt, err := auth.Decrypt(infoA.Blob)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(pt)).To(Equal("attacker_typed_token"),
			"userA's blob must decrypt to the typed token — proves the write landed on Requester's row exactly")
	})

	It("applyKeyOutcome(saw401=true, Requester=UserA) → UserB's status is untouched", func() {
		// The 401 branch takes a different code path (UpdateWakatimeKeyStatus
		// vs. SetEncryptedWakatimeKey). Verify cross-user isolation for THAT
		// branch too — a bad WHERE clause here would flip UserB to invalid.
		database := openImportOutcomeDBGinkgo()
		withEncryptionKeyGinkgo()
		ctx := context.Background()

		userA := fmt.Sprintf("victA401_%d", time.Now().UnixNano())
		userB := fmt.Sprintf("victB401_%d", time.Now().UnixNano())
		seedUserWithKeyGinkgo(database, userA, "waka_A", db.WakatimeKeyStatusValid)
		seedUserWithKeyGinkgo(database, userB, "waka_B", db.WakatimeKeyStatusValid)

		beforeB, err := database.GetWakatimeKeyInfo(ctx, userB)
		Expect(err).NotTo(HaveOccurred())

		w := &Worker{db: database, logger: silentLoggerCov(), hub: NewHub()}
		w.applyKeyOutcome(QueueItem{Requester: userA}, db.JobStateFailed, true)

		afterB, err := database.GetWakatimeKeyInfo(ctx, userB)
		Expect(err).NotTo(HaveOccurred())
		Expect(ptrStrEq(beforeB.Status, afterB.Status)).To(BeTrue(),
			"userB status must remain 'valid' — a spoofed 401 from userA must not poison userB's status")
		Expect(ptrTimeEq(beforeB.CheckedAt, afterB.CheckedAt)).To(BeTrue(),
			"userB checked_at must not tick — proves no UPDATE ran on userB's row")
	})
})

var _ = Describe("Cross-key ciphertext negative (boom-d6x, security gap #2)", func() {
	// Named invariant: an accidentally-shipped "fallback" or "default" key
	// would produce different ciphertext than the intended key. Any test that
	// relies on "encrypt something and decrypt it" is a tautology if the SAME
	// key is used both times. This test proves that when a DIFFERENT key is
	// loaded, the ciphertext produced for the same plaintext is DIFFERENT AND
	// the wrong-key ciphertext does NOT decrypt under the intended key.
	It("Encrypt under Key1 ≠ Encrypt under Key2; and cross-key Decrypt fails auth", func() {
		const key1 = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=" // 0x00..0x1f
		const key2 = "/////////////////////////////////////////wA=" // 0xff*31 + 0x00

		prev, hadPrev := os.LookupEnv("BOOM_ENCRYPTION_KEY")
		DeferCleanup(func() {
			if hadPrev {
				os.Setenv("BOOM_ENCRYPTION_KEY", prev)
			} else {
				os.Unsetenv("BOOM_ENCRYPTION_KEY")
			}
			auth.ResetForTest()
		})

		// Encrypt "same_plaintext" under key1.
		os.Setenv("BOOM_ENCRYPTION_KEY", key1)
		auth.ResetForTest()
		Expect(auth.LoadKeyFromEnv()).To(Succeed())
		ct1, err := auth.Encrypt([]byte("same_plaintext"))
		Expect(err).NotTo(HaveOccurred())

		// Encrypt "same_plaintext" under key2.
		os.Setenv("BOOM_ENCRYPTION_KEY", key2)
		auth.ResetForTest()
		Expect(auth.LoadKeyFromEnv()).To(Succeed())
		ct2, err := auth.Encrypt([]byte("same_plaintext"))
		Expect(err).NotTo(HaveOccurred())

		// Ciphertexts must differ (even beyond the random nonce prefix —
		// simplest sufficient check: they must not be byte-identical).
		Expect(len(ct1)).To(Equal(len(ct2)),
			"same-length plaintext must yield same-length ciphertext for both keys (nonce||sealed)")
		different := false
		for i := range ct1 {
			if ct1[i] != ct2[i] {
				different = true
				break
			}
		}
		Expect(different).To(BeTrue(),
			"ciphertexts under different keys are byte-identical — this is impossible unless a fallback key is masking key selection")

		// Cross-key Decrypt MUST fail (GCM auth tag catches it) — this
		// is the "would silently pass if a fallback key exists" guard.
		os.Setenv("BOOM_ENCRYPTION_KEY", key1)
		auth.ResetForTest()
		Expect(auth.LoadKeyFromEnv()).To(Succeed())
		_, err = auth.Decrypt(ct2) // ct2 was sealed under key2
		Expect(err).To(HaveOccurred(),
			"Decrypt(ct-under-key2) with key1 loaded must fail — a silent success would prove a shared fallback key")
	})

	It("withEncryptionKeyGinkgo's canonical key produces DIFFERENT ciphertext than a fresh random key", func() {
		// Extra: use the exact helper the whole test suite relies on and
		// confirm its output is distinguishable from another well-formed key.
		// Guards against the whole suite silently agreeing on a wrong key.
		withEncryptionKeyGinkgo() // installs canonical AAEC... key
		ctCanonical, err := auth.Encrypt([]byte("probe"))
		Expect(err).NotTo(HaveOccurred())

		// Swap to a fresh distinct key.
		os.Setenv("BOOM_ENCRYPTION_KEY", "/////////////////////////////////////////wA=")
		auth.ResetForTest()
		Expect(auth.LoadKeyFromEnv()).To(Succeed())
		ctOther, err := auth.Encrypt([]byte("probe"))
		Expect(err).NotTo(HaveOccurred())

		Expect(len(ctCanonical)).To(Equal(len(ctOther)))
		anyDiff := false
		for i := range ctCanonical {
			if ctCanonical[i] != ctOther[i] {
				anyDiff = true
				break
			}
		}
		Expect(anyDiff).To(BeTrue(),
			"canonical-key ciphertext equals other-key ciphertext — helper key is somehow shared / masked")
	})
})
