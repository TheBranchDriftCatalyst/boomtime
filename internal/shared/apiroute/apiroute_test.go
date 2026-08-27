package apiroute_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apiroute"
)

// Deliberately UNEXPORTED, mirroring the ~59 private DTOs in the codebase. The
// previous schema approach needed a value handed into the openapi package, which
// unexported types cannot satisfy; capturing the type inside the owning package
// is what makes them work without promotion.
type probeResp struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type probeReq struct {
	Query string `json:"query"`
}

func TestGETRecordsResponseType(t *testing.T) {
	apiroute.Reset()
	e := echo.New()
	apiroute.GET(e, "/probe", func(c *echo.Context) (probeResp, error) {
		return probeResp{Name: "x", Count: 2}, nil
	})

	op, ok := apiroute.Lookup(http.MethodGet, "/probe")
	if !ok {
		t.Fatal("registration was not recorded — the spec would fall back to a stub")
	}
	if op.Resp == nil || op.Resp.Name() != "probeResp" {
		t.Fatalf("Resp = %v, want probeResp", op.Resp)
	}
	if op.Req != nil {
		t.Errorf("Req = %v, want nil for a GET", op.Req)
	}
	if op.Status != http.StatusOK {
		t.Errorf("Status = %d, want 200", op.Status)
	}

	// The route must still SERVE correctly — a seam that documents well and
	// responds wrongly is worse than no seam.
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/probe", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got probeResp
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body did not decode: %v (%s)", err, rec.Body.String())
	}
	if got.Name != "x" || got.Count != 2 {
		t.Errorf("body = %+v, want {x 2}", got)
	}
}

func TestPOSTRecordsBothTypesAndBinds(t *testing.T) {
	apiroute.Reset()
	e := echo.New()
	apiroute.POST(e, "/probe", func(c *echo.Context, req probeReq) (probeResp, error) {
		return probeResp{Name: req.Query, Count: len(req.Query)}, nil
	})

	op, ok := apiroute.Lookup(http.MethodPost, "/probe")
	if !ok {
		t.Fatal("registration was not recorded")
	}
	if op.Req == nil || op.Req.Name() != "probeReq" {
		t.Fatalf("Req = %v, want probeReq", op.Req)
	}
	if op.Resp == nil || op.Resp.Name() != "probeResp" {
		t.Fatalf("Resp = %v, want probeResp", op.Resp)
	}

	// The body must actually reach the handler — the seam binds on its behalf.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/probe", jsonBody(`{"query":"abcd"}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var got probeResp
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Name != "abcd" || got.Count != 4 {
		t.Errorf("body = %+v — the request body did not bind through the seam", got)
	}
}

// NoBody must record a nil Req so the spec says "no request body" rather than
// documenting an empty object, which would tell clients to send `{}`.
func TestNoBodySentinelRecordsNilRequestType(t *testing.T) {
	apiroute.Reset()
	e := echo.New()
	apiroute.POSTNoBody(e, "/trigger", func(c *echo.Context) (probeResp, error) {
		return probeResp{Name: "triggered"}, nil
	})
	op, _ := apiroute.Lookup(http.MethodPost, "/trigger")
	if op.Req != nil {
		t.Errorf("Req = %v, want nil — NoBody must not surface as a schema", op.Req)
	}
}

// Accepted exists because every liberation mutation enqueues rather than running
// inline; a spec claiming 200 would misstate a contract clients depend on.
func TestAcceptedRecordsAndWrites202(t *testing.T) {
	apiroute.Reset()
	e := echo.New()
	apiroute.Accepted(e, http.MethodPost, "/enqueue", func(c *echo.Context) (probeResp, error) {
		return probeResp{Name: "queued"}, nil
	})
	op, _ := apiroute.Lookup(http.MethodPost, "/enqueue")
	if op.Status != http.StatusAccepted {
		t.Errorf("recorded Status = %d, want 202", op.Status)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/enqueue", nil))
	if rec.Code != http.StatusAccepted {
		t.Errorf("wrote %d, want 202 — the recorded status and the real one disagree", rec.Code)
	}
}

// An *apierr.Error must keep its status and message; anything else becomes the
// generic 500. Preserving this is what lets handlers migrate without changing
// their error behaviour.
func TestErrorContractPreserved(t *testing.T) {
	apiroute.Reset()
	e := echo.New()
	apiroute.GET(e, "/bad", func(c *echo.Context) (probeResp, error) {
		return probeResp{}, apierr.BadRequest("nope")
	})
	apiroute.GET(e, "/boom", func(c *echo.Context) (probeResp, error) {
		return probeResp{}, errNotAPIError{}
	})

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/bad", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("apierr status = %d, want 400", rec.Code)
	}
	if body := rec.Body.String(); !contains(body, "nope") {
		t.Errorf("apierr message lost: %s", body)
	}

	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("plain error status = %d, want 500", rec.Code)
	}
	// The plain-error branch must not leak the internal message to the client.
	if body := rec.Body.String(); contains(body, "raw internal detail") {
		t.Errorf("internal error text leaked to the client: %s", body)
	}
}

type errNotAPIError struct{}

func (errNotAPIError) Error() string { return "raw internal detail" }

func jsonBody(s string) io.Reader { return strings.NewReader(s) }

func contains(h, n string) bool { return strings.Contains(h, n) }
