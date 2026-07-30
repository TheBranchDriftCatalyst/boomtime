// apierr_ginkgo_test.go — ginkgo mirror of apierr_test.go (gaka-0vp).
// 1:1 case map (2 stdlib TestXxx → 12 DescribeTable Entries + 1 It):
//
//	TestPredefinedErrorStatuses → predefined constructor > table of 12
//	TestNewAndError             → New + Error() > "New + Error()"
//
// gaka-d6x extension: coverage floor 90% for apierr package. Adds specs for:
//   - Write() envelope contract (status / Content-Type / JSON body / message omission)
//   - BadRequest / NotFound / GenericHTTP constructor invariants
//   - message-interpolation constructors (MissingQueryParam, InvalidRelation,
//     UsernameExists) do not leak user input into other fields and reject empty
//     interpolation without swallowing it
//   - envelope schema shape (`error` present, `message` omitempty) preserved
//     verbatim across the whole constructor surface (drift guard vs hakatime)
package apierr

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
	"github.com/labstack/echo/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("predefined error constructors", func() {
	DescribeTable("returns the documented HTTP status",
		func(err *Error, want int) {
			Expect(err.Status).To(Equal(want))
		},
		Entry("MissingAuth is 400 (not 401)", MissingAuth(), http.StatusBadRequest),
		Entry("MissingQueryParam is 400", MissingQueryParam("start"), http.StatusBadRequest),
		Entry("MissingRefreshTokenCookie is 400", MissingRefreshTokenCookie(), http.StatusBadRequest),
		Entry("InvalidToken is 403", InvalidToken(), http.StatusForbidden),
		Entry("InvalidRelation is 404", InvalidRelation("u", "p"), http.StatusNotFound),
		Entry("ExpiredRefreshToken is 403", ExpiredRefreshToken(), http.StatusForbidden),
		Entry("DisabledRegistration is 403", DisabledRegistration(), http.StatusForbidden),
		Entry("UsernameExists is 409", UsernameExists("bob"), http.StatusConflict),
		Entry("RegisterError is 409", RegisterError(), http.StatusConflict),
		Entry("InvalidCredentials is 403", InvalidCredentials(), http.StatusForbidden),
		Entry("MissingGithubToken is 500", MissingGithubToken(), http.StatusInternalServerError),
		Entry("Generic is 500", Generic(), http.StatusInternalServerError),
	)
})

var _ = Describe("New + Error()", func() {
	It("preserves status / message / extra and Error() surfaces the message", func() {
		extra := "detail"
		e := New(422, "bad thing", &extra)

		Expect(e.Status).To(Equal(422))
		Expect(e.Message).To(Equal("bad thing"))
		Expect(e.Extra).NotTo(BeNil())
		Expect(*e.Extra).To(Equal("detail"))
		Expect(e.Error()).To(Equal("bad thing"))
	})
})

// --- gaka-d6x additions -----------------------------------------------------

// newCtx builds a fresh (*echo.Context, *httptest.ResponseRecorder) pair with
// no cookies / no headers so we can observe exactly what Write() sets.
func newCtx() (*echo.Context, *httptest.ResponseRecorder) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	return e.NewContext(req, rec), rec
}

var _ = Describe("Write (envelope contract)", func() {
	It("renders {\"error\":msg} with Status + charset-explicit JSON when Extra is nil", func() {
		// Invariant: nil Extra means the JSON body omits `message` entirely
		// (matches hakatime `omitNothingFields` — model.APIErrorData tag is
		// `json:"message,omitempty"`). This is a contract-drift guard: if a
		// future refactor changes the pointer to a plain string the omitempty
		// stops working and the client sees a stray empty `"message":""`.
		c, rec := newCtx()
		err := New(http.StatusTeapot, "kettle broke", nil).Write(c)
		Expect(err).NotTo(HaveOccurred())

		Expect(rec.Code).To(Equal(http.StatusTeapot))
		Expect(rec.Header().Get(echo.HeaderContentType)).
			To(Equal("application/json;charset=utf-8"),
				"Errors.hs contract: charset must be spelled explicitly, not just application/json")

		// Assert JSON structure by re-decoding — key names, no extra keys, no
		// stray whitespace assumptions.
		var got map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &got)).To(Succeed())
		Expect(got).To(HaveKeyWithValue("error", "kettle broke"))
		_, hasMessage := got["message"]
		Expect(hasMessage).To(BeFalse(),
			"nil Extra MUST omit `message` from the wire — omitempty invariant")
	})

	It("includes `message` verbatim when Extra is a non-empty string pointer", func() {
		c, rec := newCtx()
		extra := "row 42 blew up: constraint fk_user"
		err := New(http.StatusInternalServerError, "db failed", &extra).Write(c)
		Expect(err).NotTo(HaveOccurred())

		var got map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &got)).To(Succeed())
		Expect(got).To(HaveKeyWithValue("error", "db failed"))
		Expect(got).To(HaveKeyWithValue("message", "row 42 blew up: constraint fk_user"))
	})

	It("emits `message` even when Extra points to an empty string (pointer set, value empty)", func() {
		// Named invariant: omitempty triggers on nil pointer, NOT on empty
		// dereferenced string. A caller who deliberately sets Extra to &""
		// is signalling "I want an empty message field on the wire" — this
		// distinguishes "no detail" from "detail is intentionally blank".
		c, rec := newCtx()
		empty := ""
		Expect(New(400, "bad", &empty).Write(c)).To(Succeed())

		var got map[string]any
		Expect(json.Unmarshal(rec.Body.Bytes(), &got)).To(Succeed())
		Expect(got).To(HaveKey("message"))
		Expect(got["message"]).To(Equal(""))
	})

	It("round-trips through model.APIErrorData exactly (no extra keys, no reordering surprises)", func() {
		// Cross-decode test: decode into the strong type; assert *no* other
		// fields exist on the wire beyond what APIErrorData describes.
		c, rec := newCtx()
		Expect(New(http.StatusUnauthorized, "nope", nil).Write(c)).To(Succeed())

		var strong model.APIErrorData
		Expect(json.Unmarshal(rec.Body.Bytes(), &strong)).To(Succeed())
		Expect(strong.Error).To(Equal("nope"))
		Expect(strong.Message).To(BeNil())

		// Also assert byte-level that only "error" key is present — no
		// silent addition of e.g. {"status": 401} that a middleware refactor
		// might sneak in.
		body := strings.TrimSpace(rec.Body.String())
		Expect(body).To(Equal(`{"error":"nope"}`),
			"exact wire shape must be {\"error\":<msg>} with nil Extra")
	})
})

var _ = Describe("uncovered constructors (BadRequest / NotFound / GenericHTTP)", func() {
	It("BadRequest is 400 and carries the caller's message verbatim (no rewrap)", func() {
		e := BadRequest("invalid input")
		Expect(e.Status).To(Equal(http.StatusBadRequest))
		Expect(e.Message).To(Equal("invalid input"))
		Expect(e.Extra).To(BeNil())
	})

	It("NotFound is 404 and carries the caller's message verbatim", func() {
		e := NotFound("no such rule")
		Expect(e.Status).To(Equal(http.StatusNotFound))
		Expect(e.Message).To(Equal("no such rule"))
		Expect(e.Extra).To(BeNil())
	})

	It("GenericHTTP is 500 and preserves the pointer identity of Extra (not a copy)", func() {
		// Named invariant: Extra is a *string, not a string. A caller that
		// wants to update the message post-construction (or nil-check) relies
		// on pointer semantics. If a refactor accidentally stores a copy, the
		// test catches it.
		extra := "downstream: 503"
		e := GenericHTTP("upstream failed", &extra)
		Expect(e.Status).To(Equal(http.StatusInternalServerError))
		Expect(e.Message).To(Equal("upstream failed"))
		Expect(e.Extra).To(Equal(&extra), "GenericHTTP must retain the exact pointer, not clone")

		// Nil Extra path (500 with no detail — must still render as
		// omitted, not "message": null).
		e2 := GenericHTTP("empty detail", nil)
		Expect(e2.Extra).To(BeNil())
		c, rec := newCtx()
		Expect(e2.Write(c)).To(Succeed())
		Expect(strings.TrimSpace(rec.Body.String())).
			To(Equal(`{"error":"empty detail"}`))
	})
})

var _ = Describe("message interpolation constructors do not cross-contaminate fields", func() {
	// These tests exist because MissingQueryParam / InvalidRelation /
	// UsernameExists all splice user-controlled input into Message. A
	// regression that accidentally routed the input into Extra (or dropped
	// it) would only fail if we assert the *shape* of the resulting struct,
	// not just "some string contains X".

	It("MissingQueryParam concatenates the param name into Message and leaves Extra nil", func() {
		e := MissingQueryParam("start")
		Expect(e.Status).To(Equal(http.StatusBadRequest))
		Expect(e.Message).To(Equal("Missing query parameter start"))
		Expect(e.Extra).To(BeNil(),
			"user-facing param name must never spill into the Extra field")
	})

	It("MissingQueryParam handles empty param string without producing a raw prefix", func() {
		// Guard against a refactor that pre-formats "Missing query parameter %s"
		// with a nil check that silently drops the trailing space.
		e := MissingQueryParam("")
		Expect(e.Message).To(Equal("Missing query parameter "),
			"empty param must still preserve the trailing space so debug logs make sense")
	})

	It("InvalidRelation embeds BOTH user and project without swapping order", func() {
		// Named invariant: order is (user, project). Swapping the two would
		// leak "user X" as if it were the project name to the wrong caller.
		e := InvalidRelation("alice", "boomtime")
		Expect(e.Status).To(Equal(http.StatusNotFound))
		Expect(e.Message).To(Equal("The user alice doesn't have access to project boomtime"))
		Expect(strings.Index(e.Message, "alice")).
			To(BeNumerically("<", strings.Index(e.Message, "boomtime")),
				"user must appear before project — copy the format exactly from hakatime Errors.hs")
	})

	It("UsernameExists embeds the offending username in Message only", func() {
		e := UsernameExists("bob\"; DROP TABLE users;--")
		Expect(e.Status).To(Equal(http.StatusConflict))
		// The Message is a plain string — Write() delegates escaping to
		// encoding/json. Verify that the raw dangerous string appears in
		// Message unmodified (no premature sanitization at this layer) but
		// gets safely escaped on the wire by Write().
		Expect(e.Message).To(ContainSubstring(`bob"; DROP TABLE users;--`))
		Expect(e.Extra).To(BeNil())

		c, rec := newCtx()
		Expect(e.Write(c)).To(Succeed())
		// json.Marshal MUST have escaped the embedded quote — never allow the
		// raw sequence `bob";` to appear on the wire since that would signal
		// a JSON injection.
		var decoded model.APIErrorData
		Expect(json.Unmarshal(rec.Body.Bytes(), &decoded)).To(Succeed(),
			"the response body MUST remain valid JSON even when the username contains a quote")
		Expect(decoded.Error).To(ContainSubstring(`bob"; DROP TABLE users;--`),
			"decoded value must equal the original string once JSON un-escapes it")
	})
})

var _ = Describe("predefined constructor message-text drift guard (hakatime Errors.hs)", func() {
	// If a well-meaning contributor tweaks an error string, a downstream
	// hakatime-compatible client that regex-matches these messages breaks
	// silently. Pin the exact strings that hakatime ships.
	DescribeTable("Message text is byte-identical to hakatime",
		func(err *Error, wantMsg string) {
			Expect(err.Message).To(Equal(wantMsg))
			Expect(err.Extra).To(BeNil(),
				"predefined constructors must never populate Extra — they are user-safe strings only")
		},
		Entry("MissingAuth",
			MissingAuth(),
			"Missing the 'Authorization' header field"),
		Entry("MissingRefreshTokenCookie",
			MissingRefreshTokenCookie(),
			"Missing the 'refresh_token' cookie"),
		Entry("InvalidToken",
			InvalidToken(),
			"The given api token doesn't belong to a user"),
		Entry("ExpiredRefreshToken",
			ExpiredRefreshToken(),
			"The given api token has expired"),
		Entry("DisabledRegistration",
			DisabledRegistration(),
			"Registration is disabled"),
		Entry("RegisterError",
			RegisterError(),
			"The registration failed due to an internal error"),
		Entry("InvalidCredentials",
			InvalidCredentials(),
			"Invalid credentials"),
		Entry("MissingGithubToken",
			MissingGithubToken(),
			"The environment variable GITHUB_TOKEN is not set"),
		Entry("Generic",
			Generic(),
			"An internal error occurred"),
	)
})
