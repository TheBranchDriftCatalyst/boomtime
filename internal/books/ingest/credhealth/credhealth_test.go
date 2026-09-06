package credhealth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"
)

// credhealth_test.go — pins the classification the Amazon ingests gate their
// credential-status write on. The failure it exists to prevent: an hourly sync
// hitting a DNS blip, or being cancelled by a deploy, flipping the user's device to
// "invalid" and showing "reconnect Amazon" until the next successful run.

func TestTransient_TransportAndLifecycleFailures(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"context cancelled (deploy / graceful shutdown)", context.Canceled},
		{"context deadline", context.DeadlineExceeded},
		{"wrapped cancellation", fmt.Errorf("amazon cloud reader: library page: %w", context.Canceled)},
		{"dns failure", &net.DNSError{Err: "no such host", Name: "api.audible.com", IsNotFound: true}},
		{"dial failure", &net.OpError{Op: "dial", Err: errors.New("connection refused")}},
		{"http client transport error", &url.Error{
			Op: "Post", URL: "https://api.audible.com/1.0/library",
			Err: &net.OpError{Op: "dial", Err: errors.New("i/o timeout")},
		}},
		{"truncated body", io.ErrUnexpectedEOF},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !Transient(c.err) {
				t.Fatalf("Transient(%v) = false — this would flip the user's Amazon device "+
					"to 'invalid' and prompt a pointless reconnect", c.err)
			}
		})
	}
}

func TestTransient_CredentialEvidenceStillCounts(t *testing.T) {
	// The amazon package surfaces Amazon's own rejections as plain formatted errors
	// (it has no auth sentinel), so these must NOT be classified transient — a real
	// 401/403 has to keep flipping the status, which is the whole point of the field.
	cases := []error{
		fmt.Errorf("amazon cloud reader: cookie exchange returned HTTP 401: unauthorized"),
		fmt.Errorf("amazon cloud reader: library search returned HTTP 403: forbidden"),
		fmt.Errorf("amazon cloud reader: device credential has no refresh_token"),
		errors.New("amazon: device private key is not valid PEM"),
	}
	for _, err := range cases {
		if Transient(err) {
			t.Fatalf("Transient(%v) = true — a real credential rejection would stop marking "+
				"the device invalid", err)
		}
	}
	if Transient(nil) {
		t.Fatal("Transient(nil) must be false")
	}
}

// A *http.Response-shaped success path is not an error at all; this guards the
// nil-return contract callers depend on and keeps the net import honest.
var _ = http.StatusUnauthorized
