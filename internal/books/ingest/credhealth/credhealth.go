// Package credhealth answers ONE question for the Amazon-backed ingest jobs: does
// this error mean the user's device credential is dead, or does it just mean the
// network (or the deploy) got in the way?
//
// WHY it exists: every Amazon sweep used to translate ANY error into
// UpdateAmazonDeviceStatus(..., invalid) — so a DNS blip, a pod eviction
// mid-deploy, or a job context cancelled by a graceful shutdown flipped the
// settings UI to "reconnect Amazon" and left it there until the next successful
// run (up to a full sync interval). The credential status is a USER-FACING claim
// about their credential; it must only move on evidence about the credential.
//
// The rule is deliberately conservative and fail-safe in the direction of NOT
// touching the status: anything recognisably transport- or lifecycle-shaped is
// Transient, and everything else (including Amazon's own HTTP-4xx bodies, which
// the amazon package surfaces as plain formatted errors) still counts as evidence
// and flips the status exactly as before. Widening the transient set later is
// safe; narrowing it re-opens the false "reconnect" prompt.
//
// It is shared by internal/books/ingest/{audible,kindle} so the two ingests can
// never drift into slightly different notions of "the credential is dead".
package credhealth

import (
	"context"
	"errors"
	"io"
	"net"
)

// Transient reports whether err is a transport/lifecycle failure rather than
// evidence that the Amazon device credential itself is no longer usable.
//
// Callers use it to gate the credential-status write, not the error itself — a
// transient error is still returned to the job layer, which retries it.
func Transient(err error) bool {
	if err == nil {
		return false // no error at all — nothing to classify
	}
	// Deploy/shutdown/timeout: the job's context died, we never learned anything
	// about the credential.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	// Dial/DNS/TLS/timeout failures: *url.Error, *net.OpError and *net.DNSError all
	// implement net.Error, and http.Client wraps its transport errors in *url.Error.
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	// A connection dropped mid-body.
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return true
	}
	return false
}
