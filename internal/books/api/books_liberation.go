package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/liberate"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/jobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
)

// books_liberation.go — the HTTP surface for the Libation rebuild (boom-w20s.15).
// Registered only when LiberationEnabled() (books on + liberation flag on + a
// library path configured), so with the feature off these paths 404 rather than
// existing and failing — the same convention the rest of the books routes use.
//
//	POST   /api/v1/books/items/:externalId/liberate  → 202 { enqueued, jobId }
//	DELETE /api/v1/books/items/:externalId/liberate  → 200 { forgotten }
//	POST   /api/v1/books/liberate/sweep              → 202 { enqueued, jobId, pending }
//	GET    /api/v1/books/liberation/status           → 200 { counts, pending, excluded, libraryPath }
//	GET    /api/v1/books/liberation/excluded         → 200 { items: [...] }
//
// Every mutation is ENQUEUED rather than run inline. A liberation is minutes of
// download plus minutes of remux; doing it in the request would hold an HTTP
// connection open past any sane proxy timeout and lose the work on a deploy.

// liberateBookResponse is POST /api/v1/books/items/:externalId/liberate (202).
type liberateBookResponse struct {
	Enqueued bool   `json:"enqueued"`
	JobID    int64  `json:"jobId"`
	ASIN     string `json:"asin"`
}

// LiberateBook enqueues one title for liberation.
func (h *Handler) LiberateBook(c *echo.Context) (liberateBookResponse, error) {
	var out liberateBookResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	asin := c.Param("externalId")
	if asin == "" {
		return out, apierr.BadRequest("missing book id")
	}
	if h.JobEnqueuer == nil {
		return out, apierr.BadRequest("background jobs are not available on this server")
	}
	svc := h.liberation()
	if svc == nil {
		return out, apierr.BadRequest("liberation is not configured on this server")
	}

	// Confirm ownership BEFORE enqueuing, so a bad id is an immediate 404 rather
	// than a job that fails minutes later in a log nobody is watching.
	item, lerr := svc.Store.LoadItem(c.Request().Context(), owner, asin)
	if errors.Is(lerr, liberate.ErrItemNotFound) {
		return out, apierr.New(http.StatusNotFound, "no Audible title with that id in your library", nil)
	}
	if lerr != nil {
		return out, fmt.Errorf("liberation: load item failed: %w", lerr)
	}

	force := c.QueryParam("force") == "true"
	// 409 on an in-flight run. Without this, double-clicking the button starts a
	// second download of the same 600 MB file.
	if !force && isInFlight(item.LiberationStatus) {
		return out, apierr.New(http.StatusConflict,
			"this book is already being liberated ("+item.LiberationStatus+")", nil)
	}

	payload, merr := json.Marshal(liberate.BookPayload{Owner: owner, ASIN: asin, Force: force})
	if merr != nil {
		return out, fmt.Errorf("liberation: marshal payload: %w", merr)
	}
	// MaxAttempts(1): a retry re-downloads the whole book, so an automatic retry
	// is expensive and rarely the right call. Failures are visible in the UI and
	// re-runnable by hand.
	id, eerr := h.JobEnqueuer.Enqueue(c.Request().Context(), liberate.LiberateBookKind, payload,
		jobs.Owner(owner), jobs.MaxAttempts(1))
	if eerr != nil {
		return out, fmt.Errorf("liberation: enqueue failed: %w", eerr)
	}
	h.Logger.Info("liberation enqueued", "user", owner, "asin", asin, "jobId", id, "force", force)
	return liberateBookResponse{Enqueued: true, JobID: id, ASIN: asin}, nil
}

// forgetLiberationResponse is DELETE /api/v1/books/items/:externalId/liberate.
type forgetLiberationResponse struct {
	Forgotten bool `json:"forgotten"`
	// FileDeleted is true only when ?deleteFile=true actually removed the audio.
	FileDeleted bool `json:"fileDeleted"`
}

// ForgetLiberation clears the liberation state for one title, optionally
// deleting the file. It is the "I want this off my disk" / "re-do this one"
// control.
//
// The file is deleted ONLY when ?deleteFile=true. Defaulting to keeping it means
// a mis-click costs a database row rather than a 600 MB download.
func (h *Handler) ForgetLiberation(c *echo.Context) (forgetLiberationResponse, error) {
	var out forgetLiberationResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	asin := c.Param("externalId")
	if asin == "" {
		return out, apierr.BadRequest("missing book id")
	}
	svc := h.liberation()
	if svc == nil {
		return out, apierr.BadRequest("liberation is not configured on this server")
	}
	ctx := c.Request().Context()

	item, lerr := svc.Store.LoadItem(ctx, owner, asin)
	if errors.Is(lerr, liberate.ErrItemNotFound) {
		return out, apierr.New(http.StatusNotFound, "no Audible title with that id in your library", nil)
	}
	if lerr != nil {
		return out, fmt.Errorf("liberation: load item failed: %w", lerr)
	}

	deleted := false
	if c.QueryParam("deleteFile") == "true" && item.AudioPath != "" {
		if rerr := svc.Sink.Remove(ctx, item.AudioPath); rerr != nil {
			return out, fmt.Errorf("liberation: remove file failed: %w", rerr)
		}
		deleted = true
	}
	if _, cerr := svc.Store.ClearLiberation(ctx, owner, asin); cerr != nil {
		return out, fmt.Errorf("liberation: clear state failed: %w", cerr)
	}
	h.Logger.Info("liberation forgotten", "user", owner, "asin", asin, "fileDeleted", deleted)
	return forgetLiberationResponse{Forgotten: true, FileDeleted: deleted}, nil
}

// sweepLiberationResponse is POST /api/v1/books/liberate/sweep (202).
type sweepLiberationResponse struct {
	Enqueued bool  `json:"enqueued"`
	JobID    int64 `json:"jobId"`
	// Pending is returned so the UI can state how many books (and therefore
	// roughly how many GB) the user just committed to.
	Pending int `json:"pending"`
}

// sweepBody is the OPTIONAL {limit, force} body of POST /books/liberate/sweep.
// Absent means "everything, unforced" — the sweep's documented default.
type sweepBody struct {
	Limit int  `json:"limit"`
	Force bool `json:"force"`
}

// sweepBodyLimit caps the optional sweep body. It is two scalars, so the seam's
// own default for bodies (apihelpers.BodyLimitSmall) is already generous;
// apiroute.Accepted installs no cap of its own because it registers no binder.
const sweepBodyLimit = apihelpers.BodyLimitSmall

// bindSweepBody decodes the optional sweep body, distinguishing the three cases
// the old `_ = c.Bind(&body)` collapsed into one:
//
//   - ABSENT / empty body        → the zero value: everything, unforced. OK.
//   - a JSON object              → bound normally. {"limit":10,"force":true}
//     now actually takes effect instead of being silently discarded.
//   - a JSON *string* wrapping   → unwrapped once and re-decoded. The web client
//     an object                    used to double-encode this body (api.ts passed
//     an already-JSON.stringify'd string into request(), whose doRequest
//     stringifies again), so it arrived as "\"{\\\"force\\\":true}\"". api.ts now
//     passes the object, but a browser holding a CACHED pre-fix bundle still
//     sends the old shape across a deploy — rejecting it would 400 their
//     "Liberate all" button, so it is accepted and unwrapped.
//   - anything else              → 400. A malformed body (e.g. {"limit":"10"})
//     used to mean "liberate the ENTIRE library, unforced" — hundreds of GB
//     behind a 202 that looked like success. It is now an error the caller sees.
//
// A negative limit is rejected for the same reason: liberate.ListUnliberated
// only applies `LIMIT` when limit > 0, so {"limit":-5} silently meant
// "everything" too.
func bindSweepBody(c *echo.Context) (sweepBody, error) {
	var body sweepBody
	req := c.Request()
	if req == nil || req.Body == nil {
		return body, nil
	}
	raw, rerr := io.ReadAll(io.LimitReader(req.Body, sweepBodyLimit+1))
	if rerr != nil {
		return body, apierr.BadRequest("could not read the sweep body")
	}
	if int64(len(raw)) > sweepBodyLimit {
		return body, apierr.New(http.StatusRequestEntityTooLarge, "payload too large", nil)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return body, nil // absent body: everything, unforced
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		// Legacy double-encoded client: a JSON string whose CONTENT is the object.
		var inner string
		if serr := json.Unmarshal(raw, &inner); serr != nil {
			return sweepBody{}, apierr.BadRequest(`invalid sweep body: expected {"limit": <number>, "force": <bool>}`)
		}
		inner = strings.TrimSpace(inner)
		if inner == "" {
			return sweepBody{}, nil
		}
		if ierr := json.Unmarshal([]byte(inner), &body); ierr != nil {
			return sweepBody{}, apierr.BadRequest(`invalid sweep body: expected {"limit": <number>, "force": <bool>}`)
		}
	}
	if body.Limit < 0 {
		return sweepBody{}, apierr.BadRequest("limit must be zero (everything) or a positive count")
	}
	return body, nil
}

// SweepLiberation enqueues the whole-library sweep.
//
// The optional {limit, force} body is bound HERE, not by apiroute.AcceptedBody:
// an ABSENT body must stay a no-op ("everything, unforced"), and the seam's
// binding registrars turn every bind failure — a missing body included — into a
// hard 400. bindSweepBody handles the absent, plain and legacy double-encoded
// shapes; a body that is genuinely unparseable is a 400 rather than a silent
// full-library sweep. Registered through apiroute.Accepted, which captures the
// 202 + response type and leaves the body to us.
func (h *Handler) SweepLiberation(c *echo.Context) (sweepLiberationResponse, error) {
	var out sweepLiberationResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	if h.JobEnqueuer == nil {
		return out, apierr.BadRequest("background jobs are not available on this server")
	}
	svc := h.liberation()
	if svc == nil {
		return out, apierr.BadRequest("liberation is not configured on this server")
	}
	ctx := c.Request().Context()

	body, berr := bindSweepBody(c)
	if berr != nil {
		return out, berr
	}

	pending, perr := svc.LiberateAll(ctx, owner, body.Limit)
	if perr != nil {
		return out, fmt.Errorf("liberation: list pending failed: %w", perr)
	}
	payload, merr := json.Marshal(liberate.SweepPayload{Owner: owner, Limit: body.Limit, Force: body.Force})
	if merr != nil {
		return out, fmt.Errorf("liberation: marshal payload: %w", merr)
	}
	id, eerr := h.JobEnqueuer.Enqueue(ctx, liberate.LiberateSweepKind, payload,
		jobs.Owner(owner), jobs.MaxAttempts(1))
	if eerr != nil {
		return out, fmt.Errorf("liberation: sweep enqueue failed: %w", eerr)
	}
	h.Logger.Info("liberation sweep enqueued", "user", owner, "jobId", id, "pending", len(pending))
	return sweepLiberationResponse{Enqueued: true, JobID: id, Pending: len(pending)}, nil
}

// Response types for the liberation endpoints.
//
// These were map[string]any literals. Naming them is not cosmetic: it is what
// lets the OpenAPI spec carry a real schema, because a map has no shape to
// reflect. Registering through internal/shared/apiroute captures these types at
// the call site, so the docs cannot drift from the handler — and the compiler,
// not a reviewer, is what enforces it.

// liberationStatusResponse is GET /api/v1/books/liberation/status.
type liberationStatusResponse struct {
	// Counts is liberation_status -> number of titles.
	Counts map[string]int `json:"counts"`
	// Pending is how many titles a sweep would queue right now.
	Pending int `json:"pending"`
	// Excluded is how many the sweep will never pick up on its own — a count
	// only; the rows come from the excluded endpoint when someone opens the list.
	Excluded int `json:"excluded"`
	// LibraryPath is where liberated files land, for diagnosing "it says
	// liberated but I cannot find the file".
	LibraryPath string `json:"libraryPath"`
}

// liberationExcludedResponse is GET /api/v1/books/liberation/excluded.
type liberationExcludedResponse struct {
	Items []liberate.ExcludedItem `json:"items"`
}

// LiberationStatus reports the owner's liberation state.
func (h *Handler) LiberationStatus(c *echo.Context) (liberationStatusResponse, error) {
	var out liberationStatusResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	svc := h.liberation()
	if svc == nil {
		return out, apierr.BadRequest("liberation is not configured on this server")
	}
	ctx := c.Request().Context()

	counts, cerr := svc.Store.StatusCounts(ctx, owner)
	if cerr != nil {
		return out, fmt.Errorf("liberation: status counts failed: %w", cerr)
	}
	pending, perr := svc.LiberateAll(ctx, owner, 0)
	if perr != nil {
		return out, fmt.Errorf("liberation: list pending failed: %w", perr)
	}
	excluded, eerr := svc.Store.ListExcluded(ctx, owner)
	if eerr != nil {
		return out, fmt.Errorf("liberation: list excluded failed: %w", eerr)
	}
	return liberationStatusResponse{
		Counts:   counts,
		Pending:  len(pending),
		Excluded: len(excluded),
		// The library path is operator information, not a secret, and seeing it
		// is how you diagnose "it says liberated but I can't find the file".
		LibraryPath: h.Cfg.BooksLibraryPath,
	}, nil
}

// LiberationExcluded lists the titles the sweep will not pick up on its own.
//
// Counts alone cannot answer "should it have given up on that one?" — that needs
// the title, the status and the error side by side. Before this endpoint the
// excluded rows were correctly skipped and completely invisible, which is how
// three podcasts spent a week being re-requested from Amazon without anyone
// being able to see them in the UI.
func (h *Handler) LiberationExcluded(c *echo.Context) (liberationExcludedResponse, error) {
	var out liberationExcludedResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	svc := h.liberation()
	if svc == nil {
		return out, apierr.BadRequest("liberation is not configured on this server")
	}
	items, err := svc.Store.ListExcluded(c.Request().Context(), owner)
	if err != nil {
		return out, fmt.Errorf("liberation: list excluded failed: %w", err)
	}
	return liberationExcludedResponse{Items: items}, nil
}

// liberation returns the shared liberation service, or nil when unavailable.
func (h *Handler) liberation() *liberate.Service {
	if h == nil {
		return nil
	}
	return h.Liberation
}

// isInFlight reports whether a status means a run is currently underway.
func isInFlight(status string) bool {
	switch status {
	case liberate.StatusLicensing, liberate.StatusDownloading, liberate.StatusConverting:
		return true
	default:
		return false
	}
}
