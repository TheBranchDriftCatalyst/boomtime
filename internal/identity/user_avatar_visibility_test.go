// user_avatar_visibility_test.go — regression for the public-avatar leak found
// in the 2026-09-06 audit (internal/identity/user_avatar.go).
//
// GET /api/v1/users/:username/avatar is unauthenticated and was keyed by the
// RAW username with no public_profile_enabled check, so a user who never opted
// into a public profile still had their AI-rendered avatar served to any
// anonymous visitor who knew or guessed the username — and, because a
// ready-avatar user answered 200 while everything else answered 404, the route
// doubled as a username-existence oracle. Every other public surface goes the
// other way: the dossier is reachable only through an opt-in slug and
// PublicProfile/resolvePublicSlug return one indistinguishable 404 for every
// negative case.
//
// The gate has to be narrow, not blunt: the app header and the settings avatar
// tab render the signed-in user's OWN avatar through this exact route, so the
// owner must keep reading it while their profile is private.
package identity_test

import (
	"context"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

var _ = Describe("Public avatar visibility gate", func() {
	// avatarGET issues a GET against the public avatar route, optionally with a
	// bearer token and/or a refresh cookie.
	avatarGET := func(e http.Handler, username, bearer, cookie string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/users/"+username+"/avatar", nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Basic "+bearer)
		}
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: "refresh_token", Value: cookie})
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}

	It("hides a PRIVATE user's ready avatar from anonymous callers, but still serves it to the owner", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		ctx := context.Background()

		username, bearer := hz.MintUser("avatar_private_g")
		img := []byte("\x89PNG\r\n\x1a\nprivate-chibi-bytes")
		Expect(hz.DB.SaveUserAvatar(ctx, username, img, "image/png", "chroma_hd", "prompt", nil)).To(Succeed())

		// Sanity: this user never enabled a public profile.
		enabled, _, err := hz.DB.GetPublicProfile(ctx, username)
		Expect(err).NotTo(HaveOccurred())
		Expect(enabled).To(BeFalse(), "precondition: the user must be private")

		// (1) Anonymous → must be the SAME terse 404 the not-ready case gives,
		//     so the route confirms nothing about the account either way.
		anon := avatarGET(e, username, "", "")
		Expect(anon).To(testutil.HaveStatus(http.StatusNotFound),
			"anonymous GET leaked a private user's avatar (%d bytes) — the route has no "+
				"public_profile_enabled check, so any guessed username yields the image and a "+
				"200-vs-404 existence oracle", anon.Body.Len())
		Expect(anon.Body.String()).NotTo(ContainSubstring("PNG"),
			"404 envelope carried image bytes")

		// The negative answer must be byte-identical to a genuinely absent
		// avatar — otherwise the gate itself becomes the oracle.
		unknown := avatarGET(e, "no-such-user-avatar-gate", "", "")
		Expect(unknown).To(testutil.HaveStatus(http.StatusNotFound))
		Expect(anon.Body.String()).To(Equal(unknown.Body.String()),
			"private-user 404 differs from unknown-user 404 — still an existence oracle")

		// (2) The owner's own bearer → 200 with the bytes (settings avatar tab).
		self := avatarGET(e, username, bearer, "")
		Expect(self).To(testutil.HaveStatus(http.StatusOK),
			"owner can no longer read their OWN avatar: body=%s", self.Body.String())
		Expect(self.Body.String()).To(Equal(string(img)))

		// (3) The owner's session COOKIE → 200. This is the path that actually
		//     matters: the header <img> cannot set an Authorization header.
		td := db.TokenData{
			Owner:        username,
			Token:        auth.ToBase64(auth.NewRawToken()),
			RefreshToken: auth.ToBase64(auth.NewRawToken()),
		}
		Expect(hz.DB.CreateAccessTokens(ctx, td, hz.Cfg.SessionExpiry)).To(Succeed())
		viaCookie := avatarGET(e, username, "", td.RefreshToken)
		Expect(viaCookie).To(testutil.HaveStatus(http.StatusOK),
			"owner's cookie-authed <img> broke: body=%s", viaCookie.Body.String())
		Expect(viaCookie.Body.String()).To(Equal(string(img)))

		// Served ONLY because the caller is the owner, so it must not be
		// storable in a shared cache that would hand it to the next visitor.
		Expect(viaCookie.Header().Get("Cache-Control")).To(HavePrefix("private"),
			"owner-only avatar is marked shared-cacheable: %q",
			viaCookie.Header().Get("Cache-Control"))
		Expect(self.Header().Get("Cache-Control")).To(HavePrefix("private"))

		// (4) A DIFFERENT signed-in user must not get it.
		_, otherBearer := hz.MintUser("avatar_private_other_g")
		other := avatarGET(e, username, otherBearer, "")
		Expect(other).To(testutil.HaveStatus(http.StatusNotFound),
			"another authenticated user read a private avatar: body=%s", other.Body.String())
	})

	It("keeps serving a PUBLIC-profile user's avatar to anonymous callers (dossier hero)", func() {
		hz := testutil.NewHarness(GinkgoT())
		e := hz.Router()
		ctx := context.Background()

		username, _ := hz.MintUser("avatar_public_g")
		img := []byte("\x89PNG\r\n\x1a\npublic-chibi-bytes")
		Expect(hz.DB.SaveUserAvatar(ctx, username, img, "image/png", "chroma_hd", "prompt", nil)).To(Succeed())
		Expect(hz.DB.SetPublicProfile(ctx, username, true, "slug-"+username)).To(Succeed())

		rec := avatarGET(e, username, "", "")
		Expect(rec).To(testutil.HaveStatus(http.StatusOK),
			"public-profile avatar must stay anonymously readable: body=%s", rec.Body.String())
		Expect(rec.Header().Get("Content-Type")).To(Equal("image/png"))
		Expect(rec.Body.String()).To(Equal(string(img)))
		Expect(rec.Header().Get("Cache-Control")).To(Equal("public, max-age=30"),
			"the opt-in dossier hero's cache header must be unchanged")
	})
})
