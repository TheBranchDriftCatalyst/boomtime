package cardstore

import (
	"testing"
	"time"
)

// New must return a nil (disabled) store whenever ANY required setting is
// missing — the og.png handler relies on `Cards == nil` to fall back to live
// rendering, so a half-configured env must never yield a broken client.
func TestNewDisabledWhenUnconfigured(t *testing.T) {
	cases := []struct {
		name                             string
		endpoint, bucket, access, secret string
	}{
		{"all empty", "", "", "", ""},
		{"no endpoint", "", "b", "a", "s"},
		{"no bucket", "e", "", "a", "s"},
		{"no access key", "e", "b", "", "s"},
		{"no secret key", "e", "b", "a", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := New(c.endpoint, c.bucket, c.access, c.secret, false)
			if err != nil {
				t.Fatalf("New: unexpected error: %v", err)
			}
			if s != nil {
				t.Fatalf("expected nil (disabled) store, got %+v", s)
			}
		})
	}
}

// A fully-configured env yields a usable store bound to the bucket.
func TestNewEnabledWhenFullyConfigured(t *testing.T) {
	s, err := New("minio-hl.minio.svc:9000", "boomtime-cards", "key", "secret", false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s == nil {
		t.Fatal("expected a store, got nil")
	}
	if s.bucket != "boomtime-cards" {
		t.Errorf("bucket = %q, want boomtime-cards", s.bucket)
	}
}

// One durable object per user: stable key, overwrite (not append), distinct
// users never collide.
func TestObjectKey(t *testing.T) {
	if got := objectKey("panda"); got != "cards/panda.png" {
		t.Errorf("objectKey(panda) = %q, want cards/panda.png", got)
	}
	if objectKey("panda") != objectKey("panda") {
		t.Error("objectKey must be stable for the same user (overwrite one object)")
	}
	if objectKey("a") == objectKey("b") {
		t.Error("distinct users must map to distinct keys")
	}
}

// fresh is the "regen daily" boundary — under TTL fresh, at/over TTL stale.
func TestFresh(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		age  time.Duration
		want bool
	}{
		{"just written", 0, true},
		{"one hour", time.Hour, true},
		{"just under TTL", TTL - time.Minute, true},
		{"exactly TTL", TTL, false},
		{"a day and a half", 36 * time.Hour, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := fresh(now.Add(-c.age), now); got != c.want {
				t.Errorf("fresh(age=%s) = %v, want %v", c.age, got, c.want)
			}
		})
	}
}
