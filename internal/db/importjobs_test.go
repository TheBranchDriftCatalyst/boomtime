// importjobs_ginkgo_test.go — ginkgo mirror of importjobs_test.go (gaka-0vp.13).
// 1:1 case map (2 stdlib TestXxx → 2 Its):
//   TestOneRunningJobPerOwner  → "GetRunningJobByOwner: one active per owner"
//   TestJobProgressAndLogs     → "UpdateJobProgress + InsertJobLog + GetJobLogs"
package db

import (
	"context"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("import_jobs", func() {
	ginkgo.It("GetRunningJobByOwner returns the active job and nothing after completion", func() {
		d := openTestDBG()
		ctx := context.Background()

		owner := "jobtest_user_" + time.Now().Format("150405.000000")
		_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1, '\x00', '\x00') ON CONFLICT DO NOTHING`, owner)

		start := time.Now().AddDate(0, 0, -2).UTC()
		end := time.Now().UTC()

		job, err := d.CreateImportJob(ctx, owner, []byte(`{"a":1}`), start, end, 3)
		Expect(err).NotTo(HaveOccurred())
		ginkgo.DeferCleanup(func() {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM import_jobs WHERE owner = $1`, owner)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username = $1`, owner)
		})

		// A fresh queued job counts as the active job.
		running, err := d.GetRunningJobByOwner(ctx, owner)
		Expect(err).NotTo(HaveOccurred())
		Expect(running).NotTo(BeNil())
		Expect(running.ID).To(Equal(job.ID))

		// After it reaches a terminal state, no active job remains.
		_, err = d.FinishImportJob(ctx, job.ID, JobStateCompleted, nil)
		Expect(err).NotTo(HaveOccurred())
		running, err = d.GetRunningJobByOwner(ctx, owner)
		Expect(err).NotTo(HaveOccurred())
		Expect(running).To(BeNil())
	})

	ginkgo.It("UpdateJobProgress persists counts/day and InsertJobLog is retrievable + afterId-filtered", func() {
		d := openTestDBG()
		ctx := context.Background()

		owner := "jobtest_prog_" + time.Now().Format("150405.000000")
		_, _ = d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1, '\x00', '\x00') ON CONFLICT DO NOTHING`, owner)

		job, err := d.CreateImportJob(ctx, owner, []byte(`{}`), time.Now().UTC(), time.Now().UTC(), 2)
		Expect(err).NotTo(HaveOccurred())
		ginkgo.DeferCleanup(func() {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM import_jobs WHERE owner = $1`, owner)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username = $1`, owner)
		})

		_, err = d.MarkJobRunning(ctx, job.ID)
		Expect(err).NotTo(HaveOccurred())
		updated, err := d.UpdateJobProgress(ctx, job.ID, 1, 42, "2025-04-01")
		Expect(err).NotTo(HaveOccurred())
		Expect(updated.ProcessedDays).To(BeEquivalentTo(1))
		Expect(updated.ImportedCount).To(BeEquivalentTo(42))
		Expect(updated.CurrentDay).NotTo(BeNil())
		Expect(*updated.CurrentDay).To(Equal("2025-04-01"))

		l, err := d.InsertJobLog(ctx, job.ID, "info", "imported 42 heartbeats for 2025-04-01")
		Expect(err).NotTo(HaveOccurred())
		logs, err := d.GetJobLogs(ctx, job.ID, 0, 100)
		Expect(err).NotTo(HaveOccurred())
		Expect(logs).To(HaveLen(1))
		Expect(logs[0].ID).To(Equal(l.ID))

		// afterId filtering: nothing newer than the last id.
		logs2, err := d.GetJobLogs(ctx, job.ID, l.ID, 100)
		Expect(err).NotTo(HaveOccurred())
		Expect(logs2).To(HaveLen(0))
	})
})
