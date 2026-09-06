// import_token_scrub_test.go — regression specs for boom-inih (CRITICAL):
// plaintext wakatime.com API keys were marshalled into import_jobs.value and
// shipped verbatim in the user-downloadable whole-DB backup
// (GET /api/v1/users/current/db/export lists import_jobs WITH `value`).
//
// Two layers are pinned here:
//
//  1. NEW rows — drive the real POST /import handler across all three token
//     provenances (typed / previously-saved / server env) and read the
//     persisted `value` column straight out of Postgres. That column IS the
//     backup payload, so grepping it is grepping the backup.
//  2. OLD rows — apply migration 00085's UP statement to legacy-shaped rows
//     and assert the secrets are gone while the rest of the payload survives.
package admin_test

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/importer"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

// storedJobValue returns import_jobs.value for a job id as text — byte-for-byte
// what internal/shared/db/dump.go COPYs into the backup ZIP.
func storedJobValue(database *db.DB, id int) string {
	var raw string
	Expect(database.Pool.QueryRow(context.Background(),
		`SELECT value::text FROM import_jobs WHERE id = $1`, id).Scan(&raw)).To(Succeed())
	return raw
}

// expectNoSecret asserts a secret appears in `blob` in none of the encodings it
// legitimately travels in elsewhere in the system.
func expectNoSecret(blob, secret, what string) {
	GinkgoHelper()
	Expect(blob).NotTo(ContainSubstring(secret),
		"%s: import_jobs.value holds the PLAINTEXT key — it ships in the backup ZIP (boom-inih): %s", what, blob)
	Expect(blob).NotTo(ContainSubstring(base64.StdEncoding.EncodeToString([]byte(secret))),
		"%s: import_jobs.value holds the base64 (basic-auth) form of the key: %s", what, blob)
	Expect(blob).NotTo(ContainSubstring(hex.EncodeToString([]byte(secret))),
		"%s: import_jobs.value holds the hex form of the key: %s", what, blob)
}

var _ = Describe("POST /import never persists a wakatime key (boom-inih)", func() {
	// Named invariant: whatever the token provenance, the durable job row
	// carries only a non-secret TokenSource sentinel. Pre-fix the handler
	// resolved the key into payload.APIToken and json.Marshal'ed it (plus
	// TypedToken) into import_jobs.value, where nothing ever cleared it.
	submit := func(deps *importDeps, token string, body map[string]any) int {
		rec := jsonReq(deps.Router, http.MethodPost, "/api/v1/users/current/import", token, body)
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())
		return jobIDFromSubmit(rec)
	}

	It("TYPED key → value carries tokenSource=typed and no key material", func() {
		deps := newImportDeps("")
		_, token := deps.Hz.MintUser("scrub_typed")
		ctx := context.Background()

		secret := "waka_typed_secret_" + strconv.FormatInt(time.Now().UnixNano(), 10)
		now := time.Now().UTC()
		id := submit(deps, token, map[string]any{
			"apiToken":  secret,
			"startDate": now.Format(time.RFC3339),
			"endDate":   now.Format(time.RFC3339),
		})
		DeferCleanup(func() {
			_, _ = deps.Hz.DB.MarkRunningJobsFailed(ctx, "scrub_typed cleanup")
			_, _ = deps.Hz.DB.Pool.Exec(ctx, `DELETE FROM import_job_logs WHERE job_id = $1`, id)
			_, _ = deps.Hz.DB.Pool.Exec(ctx, `DELETE FROM import_jobs WHERE id = $1`, id)
		})

		blob := storedJobValue(deps.Hz.DB, id)
		expectNoSecret(blob, secret, "typed key")

		var stored map[string]any
		Expect(json.Unmarshal([]byte(blob), &stored)).To(Succeed())
		Expect(stored).NotTo(HaveKey("typedToken"))
		Expect(stored["tokenSource"]).To(Equal(string(importer.TokenSourceTyped)))
	})

	It("previously-SAVED key → value carries tokenSource=saved and no decrypted key", func() {
		installEncryptionKeyForTest()
		deps := newImportDeps("") // no server env key: forces the saved branch
		user, token := deps.Hz.MintUser("scrub_saved")
		ctx := context.Background()

		secret := "waka_saved_secret_" + strconv.FormatInt(time.Now().UnixNano(), 10)
		ct, err := auth.Encrypt([]byte(secret))
		Expect(err).NotTo(HaveOccurred())
		Expect(deps.Hz.DB.SetEncryptedWakatimeKey(ctx, user, ct, db.WakatimeKeyStatusValid)).To(Succeed())

		now := time.Now().UTC()
		id := submit(deps, token, map[string]any{
			// blank apiToken → the saved key is the run's credential
			"startDate": now.Format(time.RFC3339),
			"endDate":   now.Format(time.RFC3339),
		})
		DeferCleanup(func() {
			_, _ = deps.Hz.DB.MarkRunningJobsFailed(ctx, "scrub_saved cleanup")
			_, _ = deps.Hz.DB.Pool.Exec(ctx, `DELETE FROM import_job_logs WHERE job_id = $1`, id)
			_, _ = deps.Hz.DB.Pool.Exec(ctx, `DELETE FROM import_jobs WHERE id = $1`, id)
		})

		blob := storedJobValue(deps.Hz.DB, id)
		expectNoSecret(blob, secret, "decrypted saved key")

		var stored map[string]any
		Expect(json.Unmarshal([]byte(blob), &stored)).To(Succeed())
		Expect(stored["tokenSource"]).To(Equal(string(importer.TokenSourceSaved)),
			"the worker needs the sentinel to know it must decrypt users.encrypted_wakatime_key at run time")
	})

	It("SERVER env key → value carries tokenSource=server and never the operator's key", func() {
		// The worst case in the finding: with no typed and no saved key the
		// handler fell back to the server-wide key, so the OPERATOR's secret
		// landed inside a per-user backup ZIP.
		serverKey := "server_env_operator_secret_" + strconv.FormatInt(time.Now().UnixNano(), 10)
		deps := newImportDeps(serverKey)
		_, token := deps.Hz.MintUser("scrub_serverkey")
		ctx := context.Background()

		now := time.Now().UTC()
		id := submit(deps, token, map[string]any{
			"startDate": now.Format(time.RFC3339),
			"endDate":   now.Format(time.RFC3339),
		})
		DeferCleanup(func() {
			_, _ = deps.Hz.DB.MarkRunningJobsFailed(ctx, "scrub_serverkey cleanup")
			_, _ = deps.Hz.DB.Pool.Exec(ctx, `DELETE FROM import_job_logs WHERE job_id = $1`, id)
			_, _ = deps.Hz.DB.Pool.Exec(ctx, `DELETE FROM import_jobs WHERE id = $1`, id)
		})

		blob := storedJobValue(deps.Hz.DB, id)
		expectNoSecret(blob, serverKey, "server env key")

		var stored map[string]any
		Expect(json.Unmarshal([]byte(blob), &stored)).To(Succeed())
		Expect(stored["tokenSource"]).To(Equal(string(importer.TokenSourceServer)))
	})
})

// ---------------------------------------------------------------------------
// migration 00085 — retire the keys already sitting in import_jobs.value
// ---------------------------------------------------------------------------

// migration00085UpSQL returns the UP body of the scrub migration, read from the
// repo so the spec exercises the SHIPPED statement rather than a copy. The
// harness has already migrated the DB, so rows seeded by this spec are inserted
// after 00085 ran — the statement has to be replayed explicitly (which also
// proves it is idempotent).
func migration00085UpSQL() string {
	GinkgoHelper()
	_, thisFile, _, ok := runtime.Caller(0)
	Expect(ok).To(BeTrue())
	path := filepath.Join(filepath.Dir(thisFile),
		"..", "..", "shared", "db", "migrations", "00085_scrub_import_job_tokens.sql")
	raw, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred(), "migration 00085 not found at %s", path)

	body := string(raw)
	up := body[strings.Index(body, "-- +goose Up"):]
	if i := strings.Index(up, "-- +goose Down"); i >= 0 {
		up = up[:i]
	}
	// Drop the goose directives; the remaining text is one statement.
	var kept, code []string
	for _, line := range strings.Split(up, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "-- +goose") {
			continue
		}
		kept = append(kept, line)
		// `code` is the comment-free statement — the safety assertions below
		// must not trip over the prose explaining WHY there is no RETURNING.
		if !strings.HasPrefix(trimmed, "--") && trimmed != "" {
			code = append(code, trimmed)
		}
	}
	out := strings.TrimSpace(strings.Join(kept, "\n"))
	stmt := strings.ToUpper(strings.Join(code, " "))
	Expect(stmt).To(ContainSubstring("UPDATE PUBLIC.IMPORT_JOBS"))
	// SECURITY: the migration must not surface the values it removes.
	Expect(stmt).NotTo(ContainSubstring("RETURNING"),
		"the scrub migration must not RETURN the secrets it deletes")
	Expect(stmt).NotTo(ContainSubstring("RAISE"),
		"the scrub migration must not log the secrets it deletes")
	Expect(stmt).NotTo(ContainSubstring("SELECT"),
		"the scrub migration must not SELECT the secret values anywhere")
	return out
}

var _ = Describe("migration 00085 scrubs legacy import_jobs.value tokens (boom-inih)", func() {
	It("removes reqPayload.apiToken + typedToken from historical rows, keeps everything else, and is idempotent", func() {
		hz := testutil.NewHarness(GinkgoT())
		ctx := context.Background()
		owner := "scrub_mig_" + strconv.FormatInt(time.Now().UnixNano(), 10)
		_, err := hz.DB.Pool.Exec(ctx,
			`INSERT INTO users (username, hashed_password, salt_used) VALUES ($1, '\x00', '\x00') ON CONFLICT DO NOTHING`,
			owner)
		Expect(err).NotTo(HaveOccurred())

		const resolved = "waka_legacy_resolved_key_do_not_keep"
		const typed = "waka_legacy_typed_key_do_not_keep"
		now := time.Now().UTC()

		// A row exactly as the pre-fix handler wrote it.
		legacy := []byte(`{"reqPayload":{"apiToken":"` + resolved + `",` +
			`"startDate":"2025-01-01T00:00:00Z","endDate":"2025-01-02T00:00:00Z"},` +
			`"requester":"` + owner + `","typedToken":"` + typed + `"}`)
		legacyJob, err := hz.DB.CreateImportJob(ctx, owner, legacy, now, now, 2)
		Expect(err).NotTo(HaveOccurred())

		// Shape edge cases that must not make the statement error: an empty
		// object, and a non-object jsonb value (tests write these).
		emptyJob, err := hz.DB.CreateImportJob(ctx, owner, []byte(`{}`), now, now, 1)
		Expect(err).NotTo(HaveOccurred())
		arrayJob, err := hz.DB.CreateImportJob(ctx, owner, []byte(`[1,2]`), now, now, 1)
		Expect(err).NotTo(HaveOccurred())
		// A row already written by the FIXED handler — must be left alone.
		modern := []byte(`{"reqPayload":{"apiToken":"","startDate":"2025-01-01T00:00:00Z",` +
			`"endDate":"2025-01-02T00:00:00Z"},"requester":"` + owner + `","tokenSource":"saved"}`)
		modernJob, err := hz.DB.CreateImportJob(ctx, owner, modern, now, now, 2)
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(func() {
			bg := context.Background()
			_, _ = hz.DB.Pool.Exec(bg, `DELETE FROM import_job_logs WHERE job_id IN (SELECT id FROM import_jobs WHERE owner=$1)`, owner)
			_, _ = hz.DB.Pool.Exec(bg, `DELETE FROM import_jobs WHERE owner=$1`, owner)
			_, _ = hz.DB.Pool.Exec(bg, `DELETE FROM users WHERE username=$1`, owner)
		})

		// Sanity: the legacy row really does hold both secrets right now.
		before := storedJobValue(hz.DB, legacyJob.ID)
		Expect(before).To(ContainSubstring(resolved))
		Expect(before).To(ContainSubstring(typed))

		sql := migration00085UpSQL()
		for pass := 1; pass <= 2; pass++ { // second pass proves idempotency
			_, err = hz.DB.Pool.Exec(ctx, sql)
			Expect(err).NotTo(HaveOccurred(), "migration 00085 failed on pass %d", pass)
		}

		after := storedJobValue(hz.DB, legacyJob.ID)
		expectNoSecret(after, resolved, "legacy resolved key")
		expectNoSecret(after, typed, "legacy typed key")

		var scrubbed map[string]any
		Expect(json.Unmarshal([]byte(after), &scrubbed)).To(Succeed())
		Expect(scrubbed).NotTo(HaveKey("typedToken"))
		Expect(scrubbed["requester"]).To(Equal(owner), "the migration dropped non-secret payload fields")
		req, ok := scrubbed["reqPayload"].(map[string]any)
		Expect(ok).To(BeTrue(), "reqPayload destroyed by the migration: %s", after)
		Expect(req).NotTo(HaveKey("apiToken"))
		Expect(req["startDate"]).To(Equal("2025-01-01T00:00:00Z"),
			"the migration must strip only the key, not the whole request payload")
		Expect(req["endDate"]).To(Equal("2025-01-02T00:00:00Z"))

		// Edge-case rows survive untouched.
		Expect(storedJobValue(hz.DB, emptyJob.ID)).To(Equal("{}"))
		Expect(storedJobValue(hz.DB, arrayJob.ID)).To(Equal("[1, 2]"))
		Expect(storedJobValue(hz.DB, modernJob.ID)).To(ContainSubstring(`"tokenSource": "saved"`))
	})
})

// ---------------------------------------------------------------------------
// boom-1vwl — the submit path reclaims only ITS OWN expired-lease zombie
// ---------------------------------------------------------------------------

var _ = Describe("POST /import stale-job reclaim (boom-1vwl)", func() {
	// Named invariant: startup recovery is now server-role-only and
	// lease-scoped, so a zombie 'running' row can outlive the boot sweep that
	// used to clear it. The submit path reclaims the caller's OWN expired
	// lease so a dead process can't lock the user out of importing forever —
	// and must not touch anyone else's rows, nor a live run.
	It("reclaims the caller's expired-lease job but leaves a FRESH one (and other owners) alone", func() {
		deps := newImportDeps("")
		userA, tokenA := deps.Hz.MintUser("reclaim_a")
		userB, _ := deps.Hz.MintUser("reclaim_b")
		ctx := context.Background()

		now := time.Now().UTC()
		zombie, err := deps.Hz.DB.CreateImportJob(ctx, userA, []byte(`{}`), now, now, 1)
		Expect(err).NotTo(HaveOccurred())
		_, err = deps.Hz.DB.MarkJobRunning(ctx, zombie.ID)
		Expect(err).NotTo(HaveOccurred())
		other, err := deps.Hz.DB.CreateImportJob(ctx, userB, []byte(`{}`), now, now, 1)
		Expect(err).NotTo(HaveOccurred())
		_, err = deps.Hz.DB.MarkJobRunning(ctx, other.ID)
		Expect(err).NotTo(HaveOccurred())
		// Both leases expire; only user A submits.
		_, err = deps.Hz.DB.Pool.Exec(ctx,
			`UPDATE import_jobs SET updated_at = now() - interval '45 minutes' WHERE id = ANY($1)`,
			[]int{zombie.ID, other.ID})
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(func() {
			bg := context.Background()
			_, _ = deps.Hz.DB.MarkRunningJobsFailed(bg, "reclaim cleanup")
			_, _ = deps.Hz.DB.Pool.Exec(bg, `DELETE FROM import_job_logs WHERE job_id IN (SELECT id FROM import_jobs WHERE owner = ANY($1))`, []string{userA, userB})
			_, _ = deps.Hz.DB.Pool.Exec(bg, `DELETE FROM import_jobs WHERE owner = ANY($1)`, []string{userA, userB})
		})

		rec := jsonReq(deps.Router, http.MethodPost, "/api/v1/users/current/import", tokenA, map[string]any{
			"apiToken":  "waka_reclaim_probe",
			"startDate": now.Format(time.RFC3339),
			"endDate":   now.Format(time.RFC3339),
		})
		Expect(rec).To(testutil.HaveStatus(http.StatusOK), "body=%s", rec.Body.String())
		newID := jobIDFromSubmit(rec)
		Expect(newID).NotTo(Equal(zombie.ID),
			"the expired-lease zombie blocked the new import (one-active-job-per-owner guard saw it as live)")

		deadA, err := deps.Hz.DB.GetJobByID(ctx, zombie.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(deadA.State).To(Equal(db.JobStateFailed))

		liveB, err := deps.Hz.DB.GetJobByID(ctx, other.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(liveB.State).To(Equal(db.JobStateRunning),
			"a user's submit reclaimed ANOTHER owner's job — the reclaim must be owner-scoped")
	})
})
