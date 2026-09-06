// books_liberation_sweep_body_test.go — the sweep body contract (audit
// boom-l827). POST /api/v1/books/liberate/sweep binds {limit, force} IN the
// handler rather than through the typed seam, and it used to do so with
// `_ = c.Bind(&body)`: EVERY bind failure silently became limit=0/force=false,
// i.e. "liberate the entire library, unforced", behind a 202 that looked like
// success. These pin the three shapes bindSweepBody must keep apart.
package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
)

// sweepCtx builds a POST context carrying the given raw request body. A nil
// body models the no-body request the sweep must keep accepting.
func sweepCtx(rawBody *string) *echo.Context {
	e := echo.New()
	var req *http.Request
	if rawBody == nil {
		req = httptest.NewRequest(http.MethodPost, "/api/v1/books/liberate/sweep", nil)
	} else {
		req = httptest.NewRequest(http.MethodPost, "/api/v1/books/liberate/sweep", strings.NewReader(*rawBody))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	}
	return e.NewContext(req, httptest.NewRecorder())
}

func sweepStrp(s string) *string { return &s }

// TestBindSweepBody_MalformedBodyIsRejectedNotTreatedAsEverything is the
// finding itself: {"limit":"10"} (a string where a number belongs) used to bind
// to nothing and enqueue a FULL-library sweep — hundreds of GB — while the
// caller got a 202. It must be a 400 instead.
func TestBindSweepBody_MalformedBodyIsRejectedNotTreatedAsEverything(t *testing.T) {
	malformed := []string{
		`{"limit": "10"}`,   // string where a number belongs
		`{"limit": 10`,      // truncated object
		`[1,2,3]`,           // wrong container
		`"not-json-at-all"`, // a JSON string that isn't a wrapped object
		`{"limit": -5}`,     // negative: ListUnliberated only applies LIMIT when > 0
	}
	for _, raw := range malformed {
		got, err := bindSweepBody(sweepCtx(&raw))
		if err == nil {
			t.Fatalf("body %s: accepted as limit=%d force=%v — a malformed body silently means "+
				"\"liberate the entire library, unforced\"", raw, got.Limit, got.Force)
		}
		var aerr *apierr.Error
		if !errors.As(err, &aerr) {
			t.Fatalf("body %s: want an *apierr.Error (400), got %T: %v", raw, err, err)
		}
		if aerr.Status != http.StatusBadRequest {
			t.Fatalf("body %s: status = %d, want 400", raw, aerr.Status)
		}
	}
}

// TestBindSweepBody_AbsentBodyStillMeansEverythingUnforced protects the
// behaviour the old lenient bind existed to provide: the sweep takes no body at
// all in the common case, and that must stay a 202, not a 400.
func TestBindSweepBody_AbsentBodyStillMeansEverythingUnforced(t *testing.T) {
	for name, raw := range map[string]*string{
		"no body":      nil,
		"empty":        sweepStrp(``),
		"whitespace":   sweepStrp("  \n"),
		"null":         sweepStrp(`null`),
		"empty object": sweepStrp(`{}`),
	} {
		got, err := bindSweepBody(sweepCtx(raw))
		if err != nil {
			t.Fatalf("%s: rejected, want the everything-unforced default: %v", name, err)
		}
		if got.Limit != 0 || got.Force {
			t.Fatalf("%s: got %+v, want the zero value (everything, unforced)", name, got)
		}
	}
}

// TestBindSweepBody_OptionsActuallyTakeEffect is the other half of the finding:
// limit/force were not merely unvalidated, they were UNREACHABLE — every body
// that carried them failed to bind and was discarded.
func TestBindSweepBody_OptionsActuallyTakeEffect(t *testing.T) {
	got, err := bindSweepBody(sweepCtx(sweepStrp(`{"limit": 10, "force": true}`)))
	if err != nil {
		t.Fatalf("well-formed body rejected: %v", err)
	}
	if got.Limit != 10 || !got.Force {
		t.Fatalf("got %+v, want {Limit:10 Force:true} — the sweep options were dropped", got)
	}
}

// TestBindSweepBody_DoubleEncodedWebClientBodyStillBinds pins the compatibility
// case. web/shared/lib/api.ts sweepLiberation passes an already-JSON.stringify'd
// string into request(), whose doRequest stringifies it AGAIN, so the wire body
// is a JSON *string* wrapping the object. Rejecting it would break the live
// "Liberate all" button; ignoring it is what made force/limit unreachable. It is
// unwrapped once and decoded.
func TestBindSweepBody_DoubleEncodedWebClientBodyStillBinds(t *testing.T) {
	// Exactly what JSON.stringify(JSON.stringify({force:true})) puts on the wire.
	got, err := bindSweepBody(sweepCtx(sweepStrp(`"{\"force\":true}"`)))
	if err != nil {
		t.Fatalf("double-encoded body rejected — this is what the web client sends: %v", err)
	}
	if !got.Force {
		t.Fatalf("got %+v, want Force:true from the double-encoded body", got)
	}
	// The current caller sends {} through the same double-encoding path.
	got, err = bindSweepBody(sweepCtx(sweepStrp(`"{}"`)))
	if err != nil {
		t.Fatalf(`double-encoded "{}" rejected — this is today's only FE call site: %v`, err)
	}
	if got.Limit != 0 || got.Force {
		t.Fatalf("got %+v, want the zero value", got)
	}
}
