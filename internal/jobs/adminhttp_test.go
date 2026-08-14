// adminhttp_test.go — proves the PORTABLE jobs-admin seam works with NO host
// coupling: a plain echo group, a fake admin guard (no boomtime auth), a stub
// object store, and the late-bound Deps accessors returning nil until wired.
// The DB-backed paths (kind-filtered clear, list/queues over real rows) are
// covered end-to-end through the host mount in internal/admin; here we lock the
// seam contract itself — guard injection, the group-root "" route, 503 when the
// subsystem is nil, and the bulk/single log-clear plumbing — without a database.
package jobs

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/objstore"
)

// stubObjStore records Delete/List calls; a nil-safe test double for objstore.Store.
type stubObjStore struct {
	listKeys   []string
	deleteKeys []string
}

func (s *stubObjStore) Put(context.Context, string, io.Reader, int64, string) error { return nil }
func (s *stubObjStore) Get(context.Context, string) (io.ReadCloser, error) {
	return nil, objstore.ErrNotFound
}
func (s *stubObjStore) Delete(_ context.Context, key string) error {
	s.deleteKeys = append(s.deleteKeys, key)
	return nil
}
func (s *stubObjStore) Exists(context.Context, string) (bool, error)   { return false, nil }
func (s *stubObjStore) List(context.Context, string) ([]string, error) { return s.listKeys, nil }

// discardLogger is shared with cancel_test.go (same package).

// mount builds a bare echo with the plugin mounted under the host's chosen
// prefix, exactly as a porting host would. guardOwner=="" with guardErr set
// simulates a denied caller.
func mount(d Deps) *echo.Echo {
	e := echo.New()
	g := e.Group("/api/v1/admin/jobs")
	RegisterAdminRoutes(g, d)
	return e
}

func nilSubsystem() Deps {
	return Deps{
		Store:    func() *Store { return nil },
		Enqueuer: func() Enqueuer { return nil },
		Registry: func() *Registry { return nil },
		ObjStore: func() objstore.Store { return nil },
		Guard:    func(*echo.Context) (string, error) { return "admin-1", nil },
		Logger:   discardLogger(),
	}
}

func do(e *echo.Echo, method, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// The injected guard is the ONLY authorization the plugin knows — a denying
// guard must short-circuit every route before any subsystem work.
func TestAdminRoutes_GuardIsInjectable(t *testing.T) {
	d := nilSubsystem()
	d.Guard = func(*echo.Context) (string, error) {
		return "", apierr.New(http.StatusForbidden, "nope", nil)
	}
	// A store that would blow up if reached — proves the guard fires first.
	obj := &stubObjStore{listKeys: []string{JobLogKey(1)}}
	d.ObjStore = func() objstore.Store { return obj }
	e := mount(d)

	for _, target := range []struct {
		method, path string
	}{
		{http.MethodGet, "/api/v1/admin/jobs"},
		{http.MethodGet, "/api/v1/admin/jobs/queues"},
		{http.MethodDelete, "/api/v1/admin/jobs/logs"},
		{http.MethodDelete, "/api/v1/admin/jobs/7/logs"},
	} {
		rec := do(e, target.method, target.path)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s: got %d, want 403", target.method, target.path, rec.Code)
		}
	}
	if len(obj.deleteKeys) != 0 {
		t.Fatalf("guard-denied request still touched the object store: %v", obj.deleteKeys)
	}
}

// The group-root "" route must resolve (regression guard for echo group empty
// path), and a nil subsystem answers 503 rather than 404/panic.
func TestAdminRoutes_ListRootResolves_503WhenUnwired(t *testing.T) {
	rec := do(mount(nilSubsystem()), http.MethodGet, "/api/v1/admin/jobs")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /api/v1/admin/jobs (nil store): got %d, want 503 (route must resolve)", rec.Code)
	}
}

// Bulk clear with no kind wipes every listed log object and reports the count.
func TestAdminRoutes_BulkClearAll(t *testing.T) {
	d := nilSubsystem()
	obj := &stubObjStore{listKeys: []string{JobLogKey(1), JobLogKey(4), JobLogKey(9)}}
	d.ObjStore = func() objstore.Store { return obj }

	rec := do(mount(d), http.MethodDelete, "/api/v1/admin/jobs/logs")
	if rec.Code != http.StatusOK {
		t.Fatalf("bulk clear: got %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"deleted":3`) {
		t.Fatalf("bulk clear body = %q, want deleted:3", body)
	}
	if len(obj.deleteKeys) != 3 {
		t.Fatalf("bulk clear deleted %d keys, want 3: %v", len(obj.deleteKeys), obj.deleteKeys)
	}
}

// nil object store → clean no-op, never an error.
func TestAdminRoutes_BulkClear_NoStoreNoop(t *testing.T) {
	rec := do(mount(nilSubsystem()), http.MethodDelete, "/api/v1/admin/jobs/logs")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"deleted":0`) {
		t.Fatalf("bulk clear (no store): got %d body=%q, want 200 deleted:0", rec.Code, rec.Body.String())
	}
}

// A kind filter with the subsystem unwired can't resolve ids → deletes nothing.
func TestAdminRoutes_BulkClearKind_NoStoreNoop(t *testing.T) {
	d := nilSubsystem()
	obj := &stubObjStore{listKeys: []string{JobLogKey(1)}}
	d.ObjStore = func() objstore.Store { return obj }

	rec := do(mount(d), http.MethodDelete, "/api/v1/admin/jobs/logs?kind=whatever")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"deleted":0`) {
		t.Fatalf("kind clear (nil store): got %d body=%q, want 200 deleted:0", rec.Code, rec.Body.String())
	}
	if len(obj.deleteKeys) != 0 {
		t.Fatalf("kind clear with nil store deleted keys: %v", obj.deleteKeys)
	}
}

// Single-job clear deletes exactly that job's log key.
func TestAdminRoutes_SingleClear(t *testing.T) {
	d := nilSubsystem()
	obj := &stubObjStore{}
	d.ObjStore = func() objstore.Store { return obj }

	rec := do(mount(d), http.MethodDelete, "/api/v1/admin/jobs/42/logs")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"deleted":true`) {
		t.Fatalf("single clear: got %d body=%q", rec.Code, rec.Body.String())
	}
	if len(obj.deleteKeys) != 1 || obj.deleteKeys[0] != JobLogKey(42) {
		t.Fatalf("single clear keys = %v, want [%s]", obj.deleteKeys, JobLogKey(42))
	}
}
