// secrets_and_recovery_test.go — regression specs for the 2026-09-06 audit
// findings owned by the importer:
//
//   - boom-inih  (CRITICAL) plaintext wakatime keys marshalled into
//     import_jobs.value (and therefore shipped in the whole-DB backup ZIP).
//   - boom-1vwl  (HIGH)     RecoverInterrupted ran on EVERY pod boot and
//     failed ALL queued/running import jobs fleet-wide.
//   - importer.go:477 (MED) the per-day drift skip only fired on the FIRST
//     offending day; every later day inserted mangled rows.
//   - importer.go:292 (LOW) a run where every day failed still terminated as
//     state='completed'.
//
// Each spec pins the SPECIFIC failure scenario from the audit, not adjacent
// coverage.
package importer

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/model"
)

// ---------------------------------------------------------------------------
// boom-inih — the marshalled QueueItem carries no plaintext key
// ---------------------------------------------------------------------------

var _ = Describe("QueueItem persistence redaction (boom-inih)", func() {
	// Named invariant: the bytes handed to CreateImportJob — i.e. exactly what
	// lands in import_jobs.value and, via dump.go, inside the user-downloadable
	// backup ZIP — must not contain the run's wakatime.com key in ANY form.
	It("json.Marshal(QueueItem) contains the key in no encoding, while the TokenSource sentinel survives", func() {
		const secret = "waka_do_not_persist_me_0a1b2c3d"
		start := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)

		item := QueueItem{
			Requester: "alice",
			ReqPayload: model.ImportRequestPayload{
				// Both the request-bound field AND the save-on-success field
				// are populated — the pre-fix handler marshalled both.
				APIToken:  secret,
				StartDate: start,
				EndDate:   start,
			},
			TokenSource: TokenSourceTyped,
			TypedToken:  secret,
		}

		raw, err := json.Marshal(item)
		Expect(err).NotTo(HaveOccurred())
		blob := string(raw)

		// Raw, plus the two encodings the key legitimately appears in
		// elsewhere in the system (basic-auth base64, hex dumps).
		Expect(blob).NotTo(ContainSubstring(secret),
			"import_jobs.value carries the PLAINTEXT wakatime key — it ships in the whole-DB backup ZIP (boom-inih): %s", blob)
		Expect(blob).NotTo(ContainSubstring(base64.StdEncoding.EncodeToString([]byte(secret))),
			"import_jobs.value carries the base64 (basic-auth) form of the wakatime key: %s", blob)
		Expect(blob).NotTo(ContainSubstring(hex.EncodeToString([]byte(secret))),
			"import_jobs.value carries the hex form of the wakatime key: %s", blob)

		// Structural check: no token-bearing key survives at all, but the
		// non-secret sentinel the worker needs does.
		var out map[string]any
		Expect(json.Unmarshal(raw, &out)).To(Succeed())
		Expect(out).NotTo(HaveKey("typedToken"),
			"typedToken key still persisted (boom-inih): %v", out)
		Expect(out["tokenSource"]).To(Equal(string(TokenSourceTyped)),
			"the TokenSource sentinel must survive — the worker resolves the real key from it")
		req, ok := out["reqPayload"].(map[string]any)
		Expect(ok).To(BeTrue(), "reqPayload missing from persisted value: %v", out)
		Expect(req["apiToken"]).To(Equal(""),
			"reqPayload.apiToken still carries a value (boom-inih): %v", req)
		// The non-secret request fields must still round-trip.
		Expect(req).To(HaveKey("startDate"))
		Expect(req).To(HaveKey("endDate"))
		Expect(out["requester"]).To(Equal("alice"))
	})

	// The in-memory item is untouched — redaction happens on the marshalled
	// copy only, so save-on-success (boom-6jm.8) still has the typed token.
	It("redaction does not mutate the in-memory item (save-on-success still works)", func() {
		const secret = "waka_still_here_in_memory"
		item := QueueItem{Requester: "bob", TokenSource: TokenSourceTyped, TypedToken: secret}
		_, err := json.Marshal(item)
		Expect(err).NotTo(HaveOccurred())
		Expect(item.TypedToken).To(Equal(secret),
			"MarshalJSON mutated the caller's item — applyKeyOutcome would lose the typed token")
	})
})

var _ = Describe("Worker.resolveToken (boom-inih)", func() {
	// Named invariant: with no key on the durable row, the worker must still
	// authenticate the run — by decrypting users.encrypted_wakatime_key for
	// TokenSourceSaved and by reading the server env key for
	// TokenSourceServer.
	It("resolves the SAVED key by decrypting users.encrypted_wakatime_key at run time", func() {
		database := openImportOutcomeDBGinkgo()
		withEncryptionKeyGinkgo()

		user := fmt.Sprintf("resolve_saved_%d", time.Now().UnixNano())
		seedUserWithKeyGinkgo(database, user, "waka_saved_from_last_time", db.WakatimeKeyStatusValid)

		w := &Worker{db: database, logger: silentLoggerCov(), hub: NewHub(), ServerAPIKey: "server-env-key"}
		got := w.resolveToken(context.Background(),
			QueueItem{Requester: user, TokenSource: TokenSourceSaved},
			func(string, string) {})
		Expect(got).To(Equal("waka_saved_from_last_time"),
			"the worker must decrypt the saved key at run time — the job row no longer carries one")
	})

	It("falls back to the server env key when the saved key is gone", func() {
		database := openImportOutcomeDBGinkgo()
		withEncryptionKeyGinkgo()

		user := fmt.Sprintf("resolve_gone_%d", time.Now().UnixNano())
		seedUserNoKeyGinkgo(database, user)

		w := &Worker{db: database, logger: silentLoggerCov(), hub: NewHub(), ServerAPIKey: "server-env-key"}
		got := w.resolveToken(context.Background(),
			QueueItem{Requester: user, TokenSource: TokenSourceSaved},
			func(string, string) {})
		Expect(got).To(Equal("server-env-key"))
	})

	It("resolves the server env key for TokenSourceServer without touching the DB", func() {
		w := &Worker{logger: silentLoggerCov(), hub: NewHub(), ServerAPIKey: "server-env-key"}
		got := w.resolveToken(context.Background(),
			QueueItem{Requester: "nobody", TokenSource: TokenSourceServer},
			func(string, string) {})
		Expect(got).To(Equal("server-env-key"))
	})
})

// ---------------------------------------------------------------------------
// boom-1vwl — a drain/worker pod booting must not kill a live import
// ---------------------------------------------------------------------------

var _ = Describe("ShouldRecoverInterrupted role gate (boom-1vwl)", func() {
	// Named invariant: only a server-role, non-drain process may reclaim
	// import jobs. The KEDA ScaledJob spawns pods with --role=worker and
	// BOOM_JOBS_DRAIN=true against the SAME Postgres on every pending
	// avatar-render / label-image / liberation job.
	DescribeTable("role/drain matrix",
		func(isServerRole, jobsDrain, want bool) {
			Expect(ShouldRecoverInterrupted(isServerRole, jobsDrain)).To(Equal(want))
		},
		Entry("server pod (role=server|all, no drain) reclaims", true, false, true),
		Entry("KEDA drain pod (role=worker + BOOM_JOBS_DRAIN) must NOT reclaim", false, true, false),
		Entry("plain worker pod must NOT reclaim", false, false, false),
		Entry("role=all running as a drain pod must NOT reclaim", true, true, false),
	)
})

var _ = Describe("Worker.RecoverInterrupted lease scoping (boom-1vwl)", func() {
	// Named invariant: a boot sweep from ANOTHER process must not terminate an
	// import whose lease is still fresh. Pre-fix this called
	// MarkRunningJobsFailed, whose UPDATE had only
	// `WHERE state IN ('queued','running')` — no owner, pod or heartbeat
	// scoping — so the user's live import flipped to 'failed' while its
	// goroutine kept running on the server pod.
	It("a second pod's boot sweep leaves a freshly-progressing import RUNNING", func() {
		database := openImportOutcomeDBGinkgo()
		ctx := context.Background()

		owner := fmt.Sprintf("lease_live_%d", time.Now().UnixNano())
		insertUserCov(database, owner)

		start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		live, err := database.CreateImportJob(ctx, owner, []byte(`{}`), start, start, 5)
		Expect(err).NotTo(HaveOccurred())
		// MarkJobRunning + a progress tick are exactly what a live run does;
		// both stamp updated_at = now(), i.e. a fresh lease.
		_, err = database.MarkJobRunning(ctx, live.ID)
		Expect(err).NotTo(HaveOccurred())
		_, err = database.UpdateJobProgress(ctx, live.ID, 1, 42, "2025-01-01")
		Expect(err).NotTo(HaveOccurred())

		// A DIFFERENT pod boots (its own Worker over the same Postgres).
		other := &Worker{db: database, logger: silentLoggerCov(), hub: NewHub(), StaleAfter: time.Hour}
		other.RecoverInterrupted(ctx)

		after, err := database.GetJobByID(ctx, live.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(after.State).To(Equal(db.JobStateRunning),
			"another pod's boot sweep killed a LIVE import (boom-1vwl): state=%s error=%v", after.State, after.Error)
		Expect(after.FinishedAt).To(BeNil(),
			"another pod's boot sweep stamped finished_at on a live import (boom-1vwl)")
	})

	It("still reclaims a job whose lease has expired (durability contract preserved)", func() {
		database := openImportOutcomeDBGinkgo()
		ctx := context.Background()

		owner := fmt.Sprintf("lease_dead_%d", time.Now().UnixNano())
		insertUserCov(database, owner)

		start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		zombie, err := database.CreateImportJob(ctx, owner, []byte(`{}`), start, start, 5)
		Expect(err).NotTo(HaveOccurred())
		_, err = database.MarkJobRunning(ctx, zombie.ID)
		Expect(err).NotTo(HaveOccurred())
		// Age the lease past the TTL — the process behind it died.
		_, err = database.Pool.Exec(ctx,
			`UPDATE import_jobs SET updated_at = now() - interval '45 minutes' WHERE id = $1`, zombie.ID)
		Expect(err).NotTo(HaveOccurred())

		w := &Worker{db: database, logger: silentLoggerCov(), hub: NewHub(), StaleAfter: 10 * time.Minute}
		w.RecoverInterrupted(ctx)

		after, err := database.GetJobByID(ctx, zombie.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(after.State).To(Equal(db.JobStateFailed),
			"an expired-lease zombie must still be reclaimed at boot")
		Expect(after.Error).NotTo(BeNil())
		Expect(*after.Error).To(Equal("interrupted by restart"))
	})

	It("MarkStaleJobsFailed with an owner never touches another owner's rows", func() {
		database := openImportOutcomeDBGinkgo()
		ctx := context.Background()

		ownerA := fmt.Sprintf("lease_a_%d", time.Now().UnixNano())
		ownerB := fmt.Sprintf("lease_b_%d", time.Now().UnixNano())
		insertUserCov(database, ownerA)
		insertUserCov(database, ownerB)

		start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		jobA, err := database.CreateImportJob(ctx, ownerA, []byte(`{}`), start, start, 1)
		Expect(err).NotTo(HaveOccurred())
		jobB, err := database.CreateImportJob(ctx, ownerB, []byte(`{}`), start, start, 1)
		Expect(err).NotTo(HaveOccurred())
		_, err = database.Pool.Exec(ctx,
			`UPDATE import_jobs SET updated_at = now() - interval '45 minutes' WHERE id = ANY($1)`,
			[]int{jobA.ID, jobB.ID})
		Expect(err).NotTo(HaveOccurred())

		ids, err := database.MarkStaleJobsFailed(ctx, "submit-path reclaim", 10*time.Minute, ownerA)
		Expect(err).NotTo(HaveOccurred())
		Expect(ids).To(ConsistOf(jobA.ID))

		b, err := database.GetJobByID(ctx, jobB.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(b.State).To(Equal(db.JobStateQueued),
			"owner-scoped reclaim leaked into another owner's job")
	})
})

// ---------------------------------------------------------------------------
// importer.go:477 — the drift skip must be a PER-DAY verdict
// ---------------------------------------------------------------------------

var _ = Describe("Worker.run multi-day drift skip (audit importer.go:477)", func() {
	// Named invariant: when wakatime.com drops a required heartbeat field for
	// a multi-day import, EVERY affected day is skipped — not just the first.
	// Pre-fix the guard was `!before && drift.hasError()`; findings dedupe by
	// (endpoint, kind, field), so from day 2 on `before` was already true and
	// convertForDB inserted rows with the zero value (a missing "time" lands
	// at 1970-01-01) for the rest of the run.
	It("days 1 AND 2 drifted, day 3 clean → only day 3's heartbeat is stored", func() {
		database := openImportOutcomeDBGinkgo()
		ctx := context.Background()

		owner := fmt.Sprintf("driftmulti_%d", time.Now().UnixNano())
		insertUserCov(database, owner)

		// Missing BOTH `entity` and `time` (required, error severity). If the
		// day is not skipped, convertForDB writes entity='' at 1970-01-01.
		const drifted = `{"data":[{"user_agent_id":"ua-1","machine_name_id":"mn-1","type":"file"}]}`
		const clean = `{"data":[{"user_agent_id":"ua-1","machine_name_id":"mn-1",` +
			`"entity":"/tmp/clean.go","type":"file","time":1735862400.0}]}`

		var hbHits int32
		srv := startWaka(wakaHandler{
			uaBody: `{"data":[{"id":"ua-1","value":"vscode/1.0 (mac) my-editor/1.0"}]}`,
			mnBody: `{"data":[{"id":"mn-1","value":"mac"}]}`,
			hbHandler: func(w http.ResponseWriter, _ *http.Request) {
				if atomic.AddInt32(&hbHits, 1) <= 2 {
					_, _ = io.WriteString(w, drifted)
					return
				}
				_, _ = io.WriteString(w, clean)
			},
		})
		defer srv.Close()

		w := NewWorker(context.Background(), database, silentLoggerCov(), NewHub())
		w.BaseURL = srv.URL

		// start..end+1 == 3 days, so exactly one clean day follows two drifted.
		start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		end := start.AddDate(0, 0, 1)
		Expect(DayRange(start, end)).To(HaveLen(3))

		payload := model.ImportRequestPayload{StartDate: start, EndDate: end}
		item := QueueItem{Requester: owner, ReqPayload: payload, TokenSource: TokenSourceTyped, TypedToken: "tok"}
		raw, err := json.Marshal(item)
		Expect(err).NotTo(HaveOccurred())
		job, err := database.CreateImportJob(ctx, owner, raw, start, end, TotalDays(start, end))
		Expect(err).NotTo(HaveOccurred())

		w.run(ctx, job.ID, item)

		var total, mangled int
		Expect(database.Pool.QueryRow(ctx,
			`SELECT count(*) FROM heartbeats WHERE sender = $1`, owner).Scan(&total)).To(Succeed())
		Expect(database.Pool.QueryRow(ctx,
			`SELECT count(*) FROM heartbeats WHERE sender = $1 AND (entity = '' OR time_sent < timestamp '1971-01-01')`,
			owner).Scan(&mangled)).To(Succeed())

		Expect(mangled).To(Equal(0),
			"a drifted day was inserted with zero-valued required fields — the drift skip only fired for the FIRST day")
		Expect(total).To(Equal(1),
			"expected only the clean day's heartbeat to be stored, got %d rows", total)

		final, err := database.GetJobByID(ctx, job.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(final.State).To(Equal(db.JobStateCompleted),
			"per-day resilience: a partial failure must still complete")
	})
})

// ---------------------------------------------------------------------------
// importer.go:292 — an all-days-failed run is not a success
// ---------------------------------------------------------------------------

var _ = Describe("Worker.run all-days-failed verdict (audit importer.go:292)", func() {
	// Named invariant: when every day errors (sustained 429/5xx after the
	// lookups succeed) the job terminates FAILED. Pre-fix it fell through to
	// state='completed' with importedCount=0 — a green job the user believed
	// had imported their history — and applyKeyOutcome then re-stamped the
	// saved key's status as 'valid' on the strength of zero successful days.
	It("every day 429s → state=failed and a saved key's status is NOT refreshed to valid", func() {
		database := openImportOutcomeDBGinkgo()
		withEncryptionKeyGinkgo()
		ctx := context.Background()

		owner := fmt.Sprintf("alldaysfail_%d", time.Now().UnixNano())
		seedUserWithKeyGinkgo(database, owner, "waka_saved_key", db.WakatimeKeyStatusInvalid)

		srv := startWaka(wakaHandler{
			uaBody: `{"data":[{"id":"ua-1","value":"vscode/1.0 (mac) my-editor/1.0"}]}`,
			mnBody: `{"data":[{"id":"mn-1","value":"mac"}]}`,
			hbHandler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = io.WriteString(w, `{"error":"rate limited"}`)
			},
		})
		defer srv.Close()

		w := NewWorker(context.Background(), database, silentLoggerCov(), NewHub())
		w.BaseURL = srv.URL

		start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		payload := model.ImportRequestPayload{StartDate: start, EndDate: start}
		item := QueueItem{Requester: owner, ReqPayload: payload, TokenSource: TokenSourceSaved}
		raw, err := json.Marshal(item)
		Expect(err).NotTo(HaveOccurred())
		job, err := database.CreateImportJob(ctx, owner, raw, start, start, TotalDays(start, start))
		Expect(err).NotTo(HaveOccurred())

		w.run(ctx, job.ID, item)

		final, err := database.GetJobByID(ctx, job.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(final.State).To(Equal(db.JobStateFailed),
			"an import where EVERY day failed reported success (importedCount=%d)", final.ImportedCount)
		Expect(final.Error).NotTo(BeNil())
		Expect(strings.ToLower(*final.Error)).To(ContainSubstring("day(s) failed"),
			"terminal error should name the all-days-failed condition, got %q", *final.Error)
		Expect(final.ImportedCount).To(Equal(int64(0)))

		info, err := database.GetWakatimeKeyInfo(ctx, owner)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Status).NotTo(BeNil())
		Expect(*info.Status).To(Equal(string(db.WakatimeKeyStatusInvalid)),
			"key_status was refreshed to 'valid' by a run in which zero days succeeded")
	})
})
