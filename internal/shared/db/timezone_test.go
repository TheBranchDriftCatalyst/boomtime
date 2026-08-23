// timezone_ginkgo_test.go — ginkgo mirror of timezone_test.go (boom-0vp.13).
// 1:1 case map (5 stdlib TestXxx → 4 Its + 1 DescribeTable(5)):
//
//	TestPunchcard_HourReflectsUserTZ           → It "Punchcard hour reflects user tz (PT vs UTC)"
//	TestUserActivity_DayBucketReflectsUserTZ   → It "UserActivity day bucket reflects user tz"
//	TestResolveTimezone_3LevelChain            → DescribeTable "ResolveTimezone 3-level chain"
//	TestSetUserTimezone_RejectsInvalidIANA     → It "SetUserTimezone rejects invalid IANA + roundtrips valid"
//	TestGetTotalTimeToday_UsesUserLocalMidnight → It "GetTotalTimeToday uses user local midnight"
package db

import (
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("timezone-aware queries", func() {
	ginkgo.It("Punchcard hour + dow reflect the user tz (PT vs UTC control)", func() {
		d := openTestDBG()
		f := newSenderG(d, "tzpunch")
		sender := f.Sender()
		ctx := f.Ctx()
		f.Projects("P")

		when := time.Date(2025, 1, 15, 6, 30, 0, 0, time.UTC)
		insertSeedG(d, ctx, sender, hbSeed{project: "P", entity: "a.go", ts: when, gap: 300})

		t0 := time.Date(2025, 1, 13, 0, 0, 0, 0, time.UTC)
		t1 := time.Date(2025, 1, 16, 0, 0, 0, 0, time.UTC)

		cellsPT, err := d.GetPunchcard(ctx, sender, t0, t1, 15, "America/Los_Angeles",
			HiddenSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(findCell(cellsPT, 2, 22)).To(BeEquivalentTo(300),
			"PT punchcard dow=2 hour=22 should have 300s (pre-fix would land in dow=3 hour=6)")
		Expect(findCell(cellsPT, 3, 6)).To(BeEquivalentTo(0),
			"PT punchcard dow=3 hour=6 should be empty (that's the UTC bucket)")

		cellsUTC, err := d.GetPunchcard(ctx, sender, t0, t1, 15, "UTC",
			HiddenSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(findCell(cellsUTC, 3, 6)).To(BeEquivalentTo(300))
	})

	ginkgo.It("UserActivity day bucket reflects the user tz (23:00 PT is prior day vs UTC)", func() {
		d := openTestDBG()
		f := newSenderG(d, "tzday")
		sender := f.Sender()
		ctx := f.Ctx()
		f.Projects("P")

		when := time.Date(2025, 6, 15, 6, 0, 0, 0, time.UTC)
		insertSeedG(d, ctx, sender, hbSeed{project: "P", entity: "a.go", ts: when, gap: 500})

		t0 := time.Date(2025, 6, 13, 0, 0, 0, 0, time.UTC)
		t1 := time.Date(2025, 6, 16, 0, 0, 0, 0, time.UTC)

		rowsPT, err := d.GetUserActivity(ctx, sender, t0, t1, 15, "America/Los_Angeles",
			HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(rowsPT).To(HaveLen(1))
		Expect(rowsPT[0].Day.Format("2006-01-02")).To(Equal("2025-06-14"),
			"PT day = 2025-06-14 proves the AT TIME ZONE shift is active")

		rowsUTC, err := d.GetUserActivity(ctx, sender, t0, t1, 15, "UTC",
			HiddenSets{}, RenameSets{}, MemberSets{}, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(rowsUTC).To(HaveLen(1))
		Expect(rowsUTC[0].Day.Format("2006-01-02")).To(Equal("2025-06-15"))
	})

	ginkgo.DescribeTable("ResolveTimezone 3-level chain: user > env > UTC",
		func(userTZ, envTZ, want string) {
			got := ResolveTimezone(userTZ, envTZ)
			Expect(got).To(Equal(want))
			Expect(got).NotTo(BeEmpty(), "resolver returned empty — every AT TIME ZONE bind would fail")
		},
		ginkgo.Entry("user wins over env", "America/New_York", "America/Los_Angeles", "America/New_York"),
		ginkgo.Entry("user wins over empty env", "America/New_York", "", "America/New_York"),
		ginkgo.Entry("env wins when user empty", "", "America/Los_Angeles", "America/Los_Angeles"),
		ginkgo.Entry("UTC when both empty", "", "", "UTC"),
		ginkgo.Entry("user empty + env empty falls to UTC (never returns empty)", "", "", "UTC"),
	)

	ginkgo.It("SetUserTimezone rejects invalid IANA + roundtrips valid + empty is allowed", func() {
		d := openTestDBG()
		f := newSenderG(d, "tzset")
		sender := f.Sender()
		ctx := f.Ctx()

		Expect(d.SetUserTimezone(ctx, sender, "")).To(Succeed())

		Expect(d.SetUserTimezone(ctx, sender, "America/Los_Angeles")).To(Succeed())
		got, err := d.GetUserTimezone(ctx, sender)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("America/Los_Angeles"))

		Expect(d.SetUserTimezone(ctx, sender, "Mars/Olympus")).To(HaveOccurred())
		got2, err := d.GetUserTimezone(ctx, sender)
		Expect(err).NotTo(HaveOccurred())
		Expect(got2).To(Equal("America/Los_Angeles"), "bogus set must not persist")
	})

	ginkgo.It("GetTotalTimeToday uses the user's local midnight (PT and UTC agree at 12:00 UTC)", func() {
		d := openTestDBG()
		f := newSenderG(d, "tzday2")
		sender := f.Sender()
		ctx := f.Ctx()
		f.Projects("P")

		nowUTC := time.Now().UTC()
		when := time.Date(nowUTC.Year(), nowUTC.Month(), nowUTC.Day(), 12, 0, 0, 0, time.UTC)
		if !when.Before(nowUTC) {
			ginkgo.Skip("test run before 12:00 UTC; skipping to avoid future-dated heartbeat")
		}
		insertSeedG(d, ctx, sender, hbSeed{project: "P", entity: "a.go", ts: when, gap: 500})

		totUTC, err := d.GetTotalTimeToday(ctx, sender, "UTC", HiddenSets{})
		Expect(err).NotTo(HaveOccurred())
		Expect(totUTC).To(BeEquivalentTo(500))

		totPT, err := d.GetTotalTimeToday(ctx, sender, "America/Los_Angeles", HiddenSets{})
		Expect(err).NotTo(HaveOccurred())
		Expect(totPT).To(BeEquivalentTo(500))
	})
})

// -- helpers restored from internal/db/timezone_test.go (boom-0vp.17) --
func findCell(cells []PunchcardCell, dow, hour int) int64 {
	for _, c := range cells {
		if c.Dow == dow && c.Hour == hour {
			return c.Seconds
		}
	}
	return 0
}
