package api

import (
	"encoding/json"
	"errors"
	"net/http"

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

// LiberateBook enqueues one title for liberation.
func (h *Handler) LiberateBook(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	asin := c.Param("externalId")
	if asin == "" {
		return apihelpers.RespondErr(c, apierr.BadRequest("missing book id"))
	}
	if h.JobEnqueuer == nil {
		return apihelpers.RespondErr(c, apierr.BadRequest("background jobs are not available on this server"))
	}
	svc := h.liberation()
	if svc == nil {
		return apihelpers.RespondErr(c, apierr.BadRequest("liberation is not configured on this server"))
	}

	// Confirm ownership BEFORE enqueuing, so a bad id is an immediate 404 rather
	// than a job that fails minutes later in a log nobody is watching.
	item, lerr := svc.Store.LoadItem(c.Request().Context(), owner, asin)
	if errors.Is(lerr, liberate.ErrItemNotFound) {
		return apihelpers.RespondErr(c, apierr.New(http.StatusNotFound, "no Audible title with that id in your library", nil))
	}
	if lerr != nil {
		return apihelpers.InternalErr(h.Logger, c, "liberation: load item failed", lerr)
	}

	force := c.QueryParam("force") == "true"
	// 409 on an in-flight run. Without this, double-clicking the button starts a
	// second download of the same 600 MB file.
	if !force && isInFlight(item.LiberationStatus) {
		return apihelpers.RespondErr(c, apierr.New(http.StatusConflict,
			"this book is already being liberated ("+item.LiberationStatus+")", nil))
	}

	payload, merr := json.Marshal(liberate.BookPayload{Owner: owner, ASIN: asin, Force: force})
	if merr != nil {
		return apihelpers.InternalErr(h.Logger, c, "liberation: marshal payload", merr)
	}
	// MaxAttempts(1): a retry re-downloads the whole book, so an automatic retry
	// is expensive and rarely the right call. Failures are visible in the UI and
	// re-runnable by hand.
	id, eerr := h.JobEnqueuer.Enqueue(c.Request().Context(), liberate.LiberateBookKind, payload,
		jobs.Owner(owner), jobs.MaxAttempts(1))
	if eerr != nil {
		return apihelpers.InternalErr(h.Logger, c, "liberation: enqueue failed", eerr)
	}
	h.Logger.Info("liberation enqueued", "user", owner, "asin", asin, "jobId", id, "force", force)
	return c.JSON(http.StatusAccepted, map[string]any{"enqueued": true, "jobId": id, "asin": asin})
}

// ForgetLiberation clears the liberation state for one title, optionally
// deleting the file. It is the "I want this off my disk" / "re-do this one"
// control.
//
// The file is deleted ONLY when ?deleteFile=true. Defaulting to keeping it means
// a mis-click costs a database row rather than a 600 MB download.
func (h *Handler) ForgetLiberation(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	asin := c.Param("externalId")
	if asin == "" {
		return apihelpers.RespondErr(c, apierr.BadRequest("missing book id"))
	}
	svc := h.liberation()
	if svc == nil {
		return apihelpers.RespondErr(c, apierr.BadRequest("liberation is not configured on this server"))
	}
	ctx := c.Request().Context()

	item, lerr := svc.Store.LoadItem(ctx, owner, asin)
	if errors.Is(lerr, liberate.ErrItemNotFound) {
		return apihelpers.RespondErr(c, apierr.New(http.StatusNotFound, "no Audible title with that id in your library", nil))
	}
	if lerr != nil {
		return apihelpers.InternalErr(h.Logger, c, "liberation: load item failed", lerr)
	}

	deleted := false
	if c.QueryParam("deleteFile") == "true" && item.AudioPath != "" {
		if rerr := svc.Sink.Remove(ctx, item.AudioPath); rerr != nil {
			return apihelpers.InternalErr(h.Logger, c, "liberation: remove file failed", rerr)
		}
		deleted = true
	}
	if _, cerr := svc.Store.ClearLiberation(ctx, owner, asin); cerr != nil {
		return apihelpers.InternalErr(h.Logger, c, "liberation: clear state failed", cerr)
	}
	h.Logger.Info("liberation forgotten", "user", owner, "asin", asin, "fileDeleted", deleted)
	return c.JSON(http.StatusOK, map[string]any{"forgotten": true, "fileDeleted": deleted})
}

// SweepLiberation enqueues the whole-library sweep.
func (h *Handler) SweepLiberation(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	if h.JobEnqueuer == nil {
		return apihelpers.RespondErr(c, apierr.BadRequest("background jobs are not available on this server"))
	}
	svc := h.liberation()
	if svc == nil {
		return apihelpers.RespondErr(c, apierr.BadRequest("liberation is not configured on this server"))
	}
	ctx := c.Request().Context()

	var body struct {
		Limit int  `json:"limit"`
		Force bool `json:"force"`
	}
	// A malformed/absent body is fine — it means "everything, unforced".
	_ = c.Bind(&body)

	pending, perr := svc.LiberateAll(ctx, owner, body.Limit)
	if perr != nil {
		return apihelpers.InternalErr(h.Logger, c, "liberation: list pending failed", perr)
	}
	payload, merr := json.Marshal(liberate.SweepPayload{Owner: owner, Limit: body.Limit, Force: body.Force})
	if merr != nil {
		return apihelpers.InternalErr(h.Logger, c, "liberation: marshal payload", merr)
	}
	id, eerr := h.JobEnqueuer.Enqueue(ctx, liberate.LiberateSweepKind, payload,
		jobs.Owner(owner), jobs.MaxAttempts(1))
	if eerr != nil {
		return apihelpers.InternalErr(h.Logger, c, "liberation: sweep enqueue failed", eerr)
	}
	h.Logger.Info("liberation sweep enqueued", "user", owner, "jobId", id, "pending", len(pending))
	// pending is returned so the UI can state how many books (and therefore
	// roughly how many GB) the user just committed to.
	return c.JSON(http.StatusAccepted, map[string]any{
		"enqueued": true, "jobId": id, "pending": len(pending),
	})
}

// LiberationStatus reports the owner's liberation state.
func (h *Handler) LiberationStatus(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	svc := h.liberation()
	if svc == nil {
		return apihelpers.RespondErr(c, apierr.BadRequest("liberation is not configured on this server"))
	}
	ctx := c.Request().Context()

	counts, cerr := svc.Store.StatusCounts(ctx, owner)
	if cerr != nil {
		return apihelpers.InternalErr(h.Logger, c, "liberation: status counts failed", cerr)
	}
	pending, perr := svc.LiberateAll(ctx, owner, 0)
	if perr != nil {
		return apihelpers.InternalErr(h.Logger, c, "liberation: list pending failed", perr)
	}
	excluded, eerr := svc.Store.ListExcluded(ctx, owner)
	if eerr != nil {
		return apihelpers.InternalErr(h.Logger, c, "liberation: list excluded failed", eerr)
	}
	return c.JSON(http.StatusOK, map[string]any{
		"counts":  counts,
		"pending": len(pending),
		// Count only — the list itself is one request away. The toolbar needs to
		// know WHETHER to offer the affordance on every load; it needs the rows
		// only once someone opens it.
		"excluded": len(excluded),
		// The library path is operator information, not a secret, and seeing it
		// is how you diagnose "it says liberated but I can't find the file".
		"libraryPath": h.Cfg.BooksLibraryPath,
	})
}

// LiberationExcluded lists the titles the sweep will not pick up on its own.
//
// Counts alone cannot answer "should it have given up on that one?" — that needs
// the title, the status and the error side by side. Before this endpoint the
// excluded rows were correctly skipped and completely invisible, which is how
// three podcasts spent a week being re-requested from Amazon without anyone
// being able to see them in the UI.
func (h *Handler) LiberationExcluded(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	svc := h.liberation()
	if svc == nil {
		return apihelpers.RespondErr(c, apierr.BadRequest("liberation is not configured on this server"))
	}
	items, err := svc.Store.ListExcluded(c.Request().Context(), owner)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "liberation: list excluded failed", err)
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
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
