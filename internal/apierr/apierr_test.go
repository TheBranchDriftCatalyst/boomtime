package apierr

import (
	"net/http"
	"testing"
)

func TestPredefinedErrorStatuses(t *testing.T) {
	cases := []struct {
		name string
		err  *Error
		want int
	}{
		{"MissingAuth", MissingAuth(), http.StatusBadRequest},                             // 400, NOT 401
		{"MissingQueryParam", MissingQueryParam("start"), http.StatusBadRequest},          // 400
		{"MissingRefreshTokenCookie", MissingRefreshTokenCookie(), http.StatusBadRequest}, // 400
		{"InvalidToken", InvalidToken(), http.StatusForbidden},                            // 403
		{"InvalidRelation", InvalidRelation("u", "p"), http.StatusNotFound},               // 404
		{"InvalidTagRelation", InvalidTagRelation("u", "t"), http.StatusNotFound},         // 404
		{"ExpiredRefreshToken", ExpiredRefreshToken(), http.StatusForbidden},              // 403
		{"DisabledRegistration", DisabledRegistration(), http.StatusForbidden},            // 403
		{"UsernameExists", UsernameExists("bob"), http.StatusConflict},                    // 409
		{"RegisterError", RegisterError(), http.StatusConflict},                           // 409
		{"InvalidCredentials", InvalidCredentials(), http.StatusForbidden},                // 403
		{"MissingGithubToken", MissingGithubToken(), http.StatusInternalServerError},      // 500
		{"Generic", Generic(), http.StatusInternalServerError},                            // 500
	}
	for _, tc := range cases {
		if tc.err.Status != tc.want {
			t.Errorf("%s.Status = %d, want %d", tc.name, tc.err.Status, tc.want)
		}
	}
}

func TestNewAndError(t *testing.T) {
	extra := "detail"
	e := New(422, "bad thing", &extra)
	if e.Status != 422 {
		t.Errorf("Status = %d, want 422", e.Status)
	}
	if e.Message != "bad thing" {
		t.Errorf("Message = %q, want %q", e.Message, "bad thing")
	}
	if e.Extra == nil || *e.Extra != "detail" {
		t.Errorf("Extra = %v, want pointer to %q", e.Extra, "detail")
	}
	if e.Error() != "bad thing" {
		t.Errorf("Error() = %q, want %q", e.Error(), "bad thing")
	}
}
