package auth

import (
	"context"
	"sync"
	"testing"
)

// The security-critical invariant. When discovery fails under
// BOOM_AUTH_PROVIDER=oidc, the process now keeps serving instead of exiting —
// which is only safe if it does NOT quietly become a local-password
// deployment. If this ever reports "local", an operator who configured OIDC is
// silently accepting username+password logins, and the logs look fine.
func TestPendingOIDCResolverDoesNotDowngradeToLocalAuth(t *testing.T) {
	if got := (PendingOIDCResolver{}).ProviderName(); got != "oidc" {
		t.Fatalf("degraded OIDC reported provider %q — a deployment configured for OIDC must never present as %q", got, got)
	}
	// And it must not BE the local resolver, however it names itself.
	if _, isLocal := any(PendingOIDCResolver{}).(LocalPasswordResolver); isLocal {
		t.Fatal("PendingOIDCResolver resolved to LocalPasswordResolver")
	}
}

// Login is the one operation that genuinely needs the issuer, so it is the one
// that fails — and it fails 503 (transient, retry in progress), not 404
// ("not configured"), which would tell a user to go fix their config.
func TestPendingOIDCResolverRejectsLoginAsTransient(t *testing.T) {
	_, err := (PendingOIDCResolver{}).CompleteLogin(context.Background(), nil, "code", "state")
	if err == nil {
		t.Fatal("degraded OIDC completed a login without a verified issuer")
	}
	if err.Status != 503 {
		t.Fatalf("status = %d, want 503 (transient); 4xx would read as a permanent misconfiguration", err.Status)
	}
}

// The background retry swaps the process-global resolver while requests are
// reading it. Before this change those globals were written once at boot and
// never again, so plain reads were safe; they are not anymore. Run under -race.
func TestResolverSwapIsRaceFree(t *testing.T) {
	t.Cleanup(func() { SetResolver(LocalPasswordResolver{}) })

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = CurrentResolver().ProviderName()
					_ = OIDCResolverInstance()
				}
			}
		}()
	}
	for i := 0; i < 200; i++ {
		SetResolver(PendingOIDCResolver{})
		SetResolver(LocalPasswordResolver{})
		SetOIDCResolver(nil)
	}
	close(stop)
	wg.Wait()
}
