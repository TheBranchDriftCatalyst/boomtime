// Tests for the user-model capability/identity primitives (gaka-0oe.1). Pure
// logic — no DB, no HTTP — so these run deterministically and are the flag-ON
// behavior guarantee the substrate needs before any handler is refactored.
package auth

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("AllCapsIdentity (flag-off identity)", func() {
	It("grants every capability and is never disabled", func() {
		id := AllCapsIdentity("panda")
		Expect(id.Username).To(Equal("panda"))
		Expect(id.Role).To(Equal(RoleFull))
		Expect(id.Disabled).To(BeFalse())
		for _, c := range AllCapabilities {
			Expect(id.Can(c)).To(BeTrue(), "expected %s granted", c)
		}
	})
})

var _ = Describe("BuildIdentity role defaults", func() {
	It("RoleFull grants everything except admin", func() {
		id := BuildIdentity("u", string(RoleFull), nil, false)
		Expect(id.Can(CapReadDashboards)).To(BeTrue())
		Expect(id.Can(CapCurate)).To(BeTrue())
		Expect(id.Can(CapIngestHeartbeats)).To(BeTrue())
		Expect(id.Can(CapImport)).To(BeTrue())
		Expect(id.Can(CapBackup)).To(BeTrue())
		Expect(id.Can(CapGenerateRollups)).To(BeTrue())
		Expect(id.Can(CapAdmin)).To(BeFalse())
		Expect(id.IsAdmin()).To(BeFalse())
	})

	It("RoleLight can read + curate but NOT ingest/import/backup/rollups", func() {
		id := BuildIdentity("u", string(RoleLight), nil, false)
		Expect(id.Can(CapReadDashboards)).To(BeTrue())
		Expect(id.Can(CapCurate)).To(BeTrue())
		Expect(id.Can(CapIngestHeartbeats)).To(BeFalse())
		Expect(id.Can(CapImport)).To(BeFalse())
		Expect(id.Can(CapBackup)).To(BeFalse())
		Expect(id.Can(CapGenerateRollups)).To(BeFalse())
		Expect(id.Can(CapAdmin)).To(BeFalse())
	})

	It("RoleService may ingest only (write-only automation)", func() {
		id := BuildIdentity("ci", string(RoleService), nil, false)
		Expect(id.Can(CapIngestHeartbeats)).To(BeTrue())
		Expect(id.Can(CapReadDashboards)).To(BeFalse())
		Expect(id.Can(CapCurate)).To(BeFalse())
		Expect(id.Can(CapGenerateRollups)).To(BeFalse())
	})

	It("RoleAdmin grants everything including admin", func() {
		id := BuildIdentity("root", string(RoleAdmin), nil, false)
		for _, c := range AllCapabilities {
			Expect(id.Can(c)).To(BeTrue(), "expected admin to have %s", c)
		}
		Expect(id.IsAdmin()).To(BeTrue())
	})

	It("an unknown role fails closed to RoleLight", func() {
		id := BuildIdentity("u", "wizard", nil, false)
		Expect(id.Role).To(Equal(RoleLight))
		Expect(id.Can(CapReadDashboards)).To(BeTrue())
		Expect(id.Can(CapImport)).To(BeFalse())
	})
})

var _ = Describe("BuildIdentity capability overrides", func() {
	It("a boolean override flips a role default", func() {
		// A light user explicitly granted import.
		id := BuildIdentity("u", string(RoleLight), []byte(`{"import":true}`), false)
		Expect(id.Can(CapImport)).To(BeTrue())
		Expect(id.Can(CapBackup)).To(BeFalse()) // untouched
	})

	It("can also revoke a role default", func() {
		id := BuildIdentity("u", string(RoleFull), []byte(`{"backup":false}`), false)
		Expect(id.Can(CapBackup)).To(BeFalse())
		Expect(id.Can(CapImport)).To(BeTrue()) // untouched
	})

	It("ignores non-boolean values and unknown keys", func() {
		// storage_quota_bytes (a future quota key) + an unknown key must not
		// affect the capability gate.
		id := BuildIdentity("u", string(RoleLight),
			[]byte(`{"storage_quota_bytes":1073741824,"nonsense":true,"curate":false}`), false)
		Expect(id.Can(CapReadDashboards)).To(BeTrue())
		Expect(id.Can(CapCurate)).To(BeFalse()) // the one known bool override applied
	})

	It("tolerates a malformed blob (falls back to role defaults)", func() {
		id := BuildIdentity("u", string(RoleLight), []byte(`{not json`), false)
		Expect(id.Can(CapReadDashboards)).To(BeTrue())
		Expect(id.Can(CapImport)).To(BeFalse())
	})
})

var _ = Describe("Identity disabled fail-closed", func() {
	It("a disabled identity can do nothing, even at RoleAdmin", func() {
		id := BuildIdentity("root", string(RoleAdmin), nil, true)
		Expect(id.Disabled).To(BeTrue())
		for _, c := range AllCapabilities {
			Expect(id.Can(c)).To(BeFalse(), "disabled identity must deny %s", c)
		}
		Expect(id.IsAdmin()).To(BeFalse())
	})

	It("a nil identity denies everything (defensive)", func() {
		var id *Identity
		Expect(id.Can(CapReadDashboards)).To(BeFalse())
	})
})

var _ = Describe("ValidRole", func() {
	It("accepts the four known roles and rejects others", func() {
		for _, r := range []Role{RoleFull, RoleLight, RoleService, RoleAdmin} {
			Expect(ValidRole(string(r))).To(BeTrue(), "%s should be valid", r)
		}
		Expect(ValidRole("wizard")).To(BeFalse())
		Expect(ValidRole("")).To(BeFalse())
	})
})
