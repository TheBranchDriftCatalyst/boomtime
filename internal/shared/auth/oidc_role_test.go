// Unit tests for the OIDC group→role mapping (boom-0oe.11) — pure logic, the
// deterministic core of tier assignment from Authentik groups.
package auth

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("RoleFromGroups", func() {
	g2r := map[string]string{
		"boomtime-admin": "admin",
		"boomtime-full":  "full",
		"boomtime-light": "light",
		"boomtime-svc":   "service",
	}

	It("maps a single group to its role", func() {
		Expect(RoleFromGroups([]string{"boomtime-full"}, g2r)).To(Equal(RoleFull))
		Expect(RoleFromGroups([]string{"boomtime-svc"}, g2r)).To(Equal(RoleService))
	})

	It("picks the HIGHEST-privilege role when in multiple groups (order-independent)", func() {
		Expect(RoleFromGroups([]string{"boomtime-light", "boomtime-admin"}, g2r)).To(Equal(RoleAdmin))
		Expect(RoleFromGroups([]string{"boomtime-admin", "boomtime-light"}, g2r)).To(Equal(RoleAdmin))
		Expect(RoleFromGroups([]string{"boomtime-full", "boomtime-light"}, g2r)).To(Equal(RoleFull))
	})

	It("fails closed to RoleLight when no group maps", func() {
		Expect(RoleFromGroups([]string{"authentik Admins", "unrelated"}, g2r)).To(Equal(RoleLight))
		Expect(RoleFromGroups(nil, g2r)).To(Equal(RoleLight))
	})

	It("ignores groups that map to an invalid role", func() {
		bad := map[string]string{"boomtime-x": "wizard", "boomtime-full": "full"}
		Expect(RoleFromGroups([]string{"boomtime-x"}, bad)).To(Equal(RoleLight))
		Expect(RoleFromGroups([]string{"boomtime-x", "boomtime-full"}, bad)).To(Equal(RoleFull))
	})
})
