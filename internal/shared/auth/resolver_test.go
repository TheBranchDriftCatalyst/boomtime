// Tests for the pluggable IdentityResolver (gaka-0oe.2). The DB resolve paths
// (ResolveBearer/ResolveCookie) get their parity coverage from the full
// handler suite, which now routes every identity resolution through
// CurrentResolver(); these pin the provider-selection + not-supported surface.
package auth

import (
	"context"
	"net/http"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// stubResolver is a no-op provider used to prove SetResolver swaps the active
// provider (and ignores nil).
type stubResolver struct{}

func (stubResolver) ProviderName() string { return "stub" }
func (stubResolver) ResolveBearer(context.Context, *db.DB, string) (*Identity, *apierr.Error) {
	return nil, nil
}
func (stubResolver) ResolveCookie(context.Context, *db.DB, string) (*Identity, *apierr.Error) {
	return nil, nil
}
func (stubResolver) CompleteLogin(context.Context, *db.DB, string, string) (*Identity, *apierr.Error) {
	return nil, nil
}

var _ = Describe("IdentityResolver", func() {
	It("defaults to the local provider", func() {
		Expect(CurrentResolver().ProviderName()).To(Equal("local"))
		_, isLocal := CurrentResolver().(LocalPasswordResolver)
		Expect(isLocal).To(BeTrue())
	})

	It("LocalPasswordResolver.CompleteLogin is not supported (404 — local login is password-based)", func() {
		id, aerr := LocalPasswordResolver{}.CompleteLogin(context.Background(), nil, "code", "state")
		Expect(id).To(BeNil())
		Expect(aerr).NotTo(BeNil())
		Expect(aerr.Status).To(Equal(http.StatusNotFound))
	})

	It("SetResolver swaps the active provider and ignores nil", func() {
		orig := CurrentResolver()
		defer SetResolver(orig)

		SetResolver(stubResolver{})
		Expect(CurrentResolver().ProviderName()).To(Equal("stub"))

		SetResolver(nil) // nil is ignored — never leaves the process without a resolver
		Expect(CurrentResolver().ProviderName()).To(Equal("stub"))
	})
})
