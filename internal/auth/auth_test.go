package auth

import "testing"

func TestPasswordRoundTrip(t *testing.T) {
	hash, salt, err := HashPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if len(hash) != keyLen || len(salt) != saltLen {
		t.Fatalf("hash len %d salt len %d", len(hash), len(salt))
	}
	if !VerifyPassword("s3cret", hash, salt) {
		t.Fatal("VerifyPassword should accept the correct password")
	}
	if VerifyPassword("wrong", hash, salt) {
		t.Fatal("VerifyPassword should reject a wrong password")
	}
}

func TestParseAuthHeader(t *testing.T) {
	// Stored token = base64(uuid); client sends "Basic <base64(uuid)>".
	stored := ToBase64("a2c1b8f0-0000-4000-8000-000000000000")
	tkn, ok := ParseAuthHeader("Basic " + stored)
	if !ok || tkn != stored {
		t.Fatalf("ParseAuthHeader = %q,%v want %q,true", tkn, ok, stored)
	}
	if _, ok := ParseAuthHeader(""); ok {
		t.Fatal("empty header should not parse")
	}
	if _, ok := ParseAuthHeader("Bearer xyz"); ok {
		t.Fatal("non-Basic header should not parse")
	}
}

func TestParseRefreshCookie(t *testing.T) {
	v, ok := ParseRefreshCookie("foo=bar; refresh_token=abc123; baz=qux")
	if !ok || v != "abc123" {
		t.Fatalf("ParseRefreshCookie = %q,%v want abc123,true", v, ok)
	}
	if _, ok := ParseRefreshCookie("foo=bar"); ok {
		t.Fatal("missing cookie should not parse")
	}
}
