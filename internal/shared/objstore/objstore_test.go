package objstore

import (
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
)

// TestNewDisabledWhenUnconfigured: an unconfigured (or partially configured) S3
// yields (nil, nil) so callers treat a nil *Client as "persistence off" without
// an error path. A minio testcontainer is out of scope — the Put/Get/Delete wire
// behavior is covered where it's used; here we lock the gating contract.
func TestNewDisabledWhenUnconfigured(t *testing.T) {
	cases := []struct {
		name string
		cfg  *config.Config
	}{
		{"nil config", nil},
		{"all empty", &config.Config{}},
		{"missing bucket", &config.Config{S3Endpoint: "localhost:9000", S3AccessKey: "a", S3SecretKey: "s"}},
		{"missing creds", &config.Config{S3Endpoint: "localhost:9000", S3Bucket: "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := New(tc.cfg)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c != nil {
				t.Fatalf("expected nil client when unconfigured, got %#v", c)
			}
		})
	}
}

// TestNewEnabledWhenConfigured: a fully-configured S3 yields a live *Client. minio
// is lazy (no dial until the first call), so this constructs without a server.
func TestNewEnabledWhenConfigured(t *testing.T) {
	cfg := &config.Config{
		S3Endpoint:  "localhost:9000",
		S3Bucket:    "boomtime-test",
		S3AccessKey: "access",
		S3SecretKey: "secret",
		S3Region:    "us-east-1",
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c == nil {
		t.Fatal("expected a client for a fully-configured S3")
	}
	if c.bucket != "boomtime-test" {
		t.Fatalf("bucket = %q, want boomtime-test", c.bucket)
	}
}
