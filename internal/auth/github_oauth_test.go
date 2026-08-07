// github_oauth_test.go — mock-GitHub coverage of GithubOAuthResolver (gaka-2ip
// Phase 1), modeled on oidc_callback_test.go's httptest-provider approach. A
// local server plays github.com: it serves /login/oauth/access_token and
// api.github.com's /user, so the whole exchange (code → token → login capture)
// runs deterministically with no live GitHub.
package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockGithub spins an httptest server. tokenResp/userResp are the JSON bodies
// returned by the token + user endpoints; tokenStatus/userStatus their HTTP
// codes. Returns the server (caller Closes) and a resolver pointed at it.
func mockGithub(t *testing.T, tokenStatus int, tokenResp string, userStatus int, userResp string) (*httptest.Server, *GithubOAuthResolver) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		// Prove the exchange sends Accept: application/json + the form fields.
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("token exchange missing Accept: application/json, got %q", r.Header.Get("Accept"))
		}
		_ = r.ParseForm()
		if r.Form.Get("code") == "" {
			t.Errorf("token exchange missing code form field")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(tokenStatus)
		_, _ = w.Write([]byte(tokenResp))
	})
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("/user missing Bearer authorization, got %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(userStatus)
		_, _ = w.Write([]byte(userResp))
	})
	srv := httptest.NewServer(mux)
	r := NewGithubOAuthResolverForTest("cid", "csecret", "https://boom/cb",
		srv.URL+"/login/oauth/authorize", srv.URL+"/login/oauth/access_token", srv.URL+"/user")
	return srv, r
}

func TestGithubExchange_HappyPath(t *testing.T) {
	srv, r := mockGithub(t, http.StatusOK,
		`{"access_token":"gho_TOKEN123","token_type":"bearer","scope":"read:user"}`,
		http.StatusOK, `{"login":"octocat","id":1}`)
	defer srv.Close()

	token, login, err := r.Exchange(context.Background(), "good-code")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if token != "gho_TOKEN123" {
		t.Fatalf("token=%q want gho_TOKEN123", token)
	}
	if login != "octocat" {
		t.Fatalf("login=%q want octocat", login)
	}
}

func TestGithubExchange_BadVerificationCode(t *testing.T) {
	// GitHub returns HTTP 200 with an `error` field for a bad code — we MUST
	// inspect the body, not trust the status.
	srv, r := mockGithub(t, http.StatusOK,
		`{"error":"bad_verification_code","error_description":"..."}`,
		http.StatusOK, `{"login":"octocat"}`)
	defer srv.Close()

	if _, _, err := r.Exchange(context.Background(), "bad-code"); err == nil {
		t.Fatal("expected error on bad_verification_code")
	}
}

func TestGithubExchange_TokenRejectedByUserAPI(t *testing.T) {
	// Token obtained but GitHub rejects it at /user (401) → connect fails.
	srv, r := mockGithub(t, http.StatusOK,
		`{"access_token":"gho_stale","token_type":"bearer"}`,
		http.StatusUnauthorized, `{"message":"Bad credentials"}`)
	defer srv.Close()

	if _, _, err := r.Exchange(context.Background(), "good-code"); err == nil {
		t.Fatal("expected error when /user returns 401")
	}
}

func TestGithubExchange_EmptyCode(t *testing.T) {
	srv, r := mockGithub(t, http.StatusOK, `{}`, http.StatusOK, `{}`)
	defer srv.Close()
	if _, _, err := r.Exchange(context.Background(), ""); err == nil {
		t.Fatal("expected error on empty code")
	}
}

func TestGithubAuthCodeURL(t *testing.T) {
	r := NewGithubOAuthResolver("my-client", "secret", "https://boom.example/auth/github/callback")
	u := r.AuthCodeURL("signed-state-abc")
	for _, want := range []string{
		"client_id=my-client",
		"scope=read%3Auser",
		"state=signed-state-abc",
		"redirect_uri=https%3A%2F%2Fboom.example%2Fauth%2Fgithub%2Fcallback",
	} {
		if !strings.Contains(u, want) {
			t.Fatalf("AuthCodeURL missing %q; got %s", want, u)
		}
	}
}

func TestGithubResolverNilWhenUnconfigured(t *testing.T) {
	if r := NewGithubOAuthResolver("", "secret", "cb"); r != nil {
		t.Fatal("expected nil resolver when client_id is empty")
	}
	if r := NewGithubOAuthResolver("cid", "", "cb"); r != nil {
		t.Fatal("expected nil resolver when client_secret is empty")
	}
}

// TestGithubTokenResponseShape guards the JSON field mapping we depend on.
func TestGithubTokenResponseShape(t *testing.T) {
	var tr githubTokenResponse
	if err := json.Unmarshal([]byte(`{"access_token":"x","error":"y"}`), &tr); err != nil {
		t.Fatal(err)
	}
	if tr.AccessToken != "x" || tr.Error != "y" {
		t.Fatalf("shape mismatch: %+v", tr)
	}
}
