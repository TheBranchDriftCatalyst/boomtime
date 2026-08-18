package books

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/audiobooks"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/jobs"
	"github.com/labstack/echo/v5"
)

// amazon_connect.go — the "Connect Amazon" device-registration endpoints shared
// by catalyst-books (Kindle) + catalyst-audiobooks (Audible): ONE Amazon device
// link feeds both. Gated behind Cfg.BooksEnabled(). It is a paste-the-maplanding
// URL exchange (Amazon redirects to its own page after login, not to us — see
// internal/amazon/register.go):
//
//	POST   /api/v1/amazon/connect/start    {marketplace}          → {authorizeUrl, session}
//	POST   /api/v1/amazon/connect/complete {session, redirectUrl} → 204 (stores credential)
//	POST   /api/v1/amazon/connect/import   {authFile}             → 204 (.audible import path)
//	GET    /api/v1/amazon                                          → {connected, status, checkedAt}
//	DELETE /api/v1/amazon                                          → 204 (disconnect)

type amazonStartReq struct {
	Marketplace string `json:"marketplace"`
}
type amazonStartResp struct {
	AuthorizeURL string `json:"authorizeUrl"`
	Session      string `json:"session"` // opaque, encrypted RegistrationSession
}
type amazonCompleteReq struct {
	Session     string `json:"session"`
	RedirectURL string `json:"redirectUrl"`
}
type amazonImportReq struct {
	AuthFile json.RawMessage `json:"authFile"`
}
type amazonConnectionResp struct {
	Connected bool    `json:"connected"`
	Status    *string `json:"status,omitempty"`
	CheckedAt *string `json:"checkedAt,omitempty"`
}

// ConnectAmazonStart builds the Amazon authorize URL + seals the PKCE session.
func (h *Handler) ConnectAmazonStart(c *echo.Context) error {
	if _, aerr := apihelpers.IdentifyOwner(h.DB, c); aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	var req amazonStartReq
	_ = apihelpers.BindJSONWithLimit(c, &req, 4*1024) // marketplace optional → US default
	authURL, sess, err := amazon.BuildAuthorizeURL(amazon.Marketplace(req.Marketplace))
	if err != nil {
		return apihelpers.RespondErr(c, apierr.BadRequest(err.Error()))
	}
	sealed, serr := sealAmazonSession(sess)
	if serr != nil {
		return apihelpers.InternalErr(h.Logger, c, "amazon session seal failed", serr)
	}
	return c.JSON(http.StatusOK, amazonStartResp{AuthorizeURL: authURL, Session: sealed})
}

// ConnectAmazonComplete exchanges the pasted maplanding URL for the device
// credential and stores it encrypted.
func (h *Handler) ConnectAmazonComplete(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	var req amazonCompleteReq
	if berr := apihelpers.BindJSONWithLimit(c, &req, 16*1024); berr != nil {
		return apihelpers.RespondErr(c, berr)
	}
	if req.Session == "" || req.RedirectURL == "" {
		return apihelpers.RespondErr(c, apierr.BadRequest("`session` and `redirectUrl` are required"))
	}
	sess, oerr := openAmazonSession(req.Session)
	if oerr != nil {
		return apihelpers.RespondErr(c, apierr.BadRequest("invalid or expired session — restart Connect Amazon"))
	}
	cred, rerr := amazon.CompleteRegistration(c.Request().Context(), sess, req.RedirectURL, time.Now())
	if rerr != nil {
		return apihelpers.RespondErr(c, apierr.BadRequest(rerr.Error()))
	}
	if serr := amazon.NewStore(h.DB).Save(c.Request().Context(), owner, cred); serr != nil {
		return apihelpers.InternalErr(h.Logger, c, "amazon credential save failed", serr)
	}
	h.Logger.Info("amazon connected", "user", owner, "marketplace", cred.Marketplace)
	return apihelpers.NoContent(c)
}

// ImportAmazonAuth stores a device credential parsed from a .audible auth file.
func (h *Handler) ImportAmazonAuth(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	var req amazonImportReq
	if berr := apihelpers.BindJSONWithLimit(c, &req, 64*1024); berr != nil {
		return apihelpers.RespondErr(c, berr)
	}
	if len(req.AuthFile) == 0 {
		return apihelpers.RespondErr(c, apierr.BadRequest("`authFile` (the .audible JSON) is required"))
	}
	cred, perr := amazon.ImportAuthFile(req.AuthFile, time.Now())
	if perr != nil {
		return apihelpers.RespondErr(c, apierr.BadRequest(perr.Error()))
	}
	if serr := amazon.NewStore(h.DB).Save(c.Request().Context(), owner, cred); serr != nil {
		return apihelpers.InternalErr(h.Logger, c, "amazon credential save failed", serr)
	}
	h.Logger.Info("amazon connected (import)", "user", owner, "marketplace", cred.Marketplace)
	return apihelpers.NoContent(c)
}

// GetAmazonConnection reports presence/status (never returns the credential).
func (h *Handler) GetAmazonConnection(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	info, err := amazon.NewStore(h.DB).Info(c.Request().Context(), owner)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "amazon connection lookup failed", err)
	}
	resp := amazonConnectionResp{Connected: info.Connected}
	if info.Connected {
		resp.Status = info.Status
		if info.CheckedAt != nil {
			ts := info.CheckedAt.UTC().Format(time.RFC3339)
			resp.CheckedAt = &ts
		}
	}
	return c.JSON(http.StatusOK, resp)
}

// DisconnectAmazon clears the stored credential (idempotent).
func (h *Handler) DisconnectAmazon(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	if err := amazon.NewStore(h.DB).Disconnect(c.Request().Context(), owner); err != nil {
		return apihelpers.InternalErr(h.Logger, c, "amazon disconnect failed", err)
	}
	h.Logger.Info("amazon disconnected", "user", owner)
	return apihelpers.NoContent(c)
}

// SyncAudible triggers an Audible library sync into the siloed reading_items
// table and returns how many items were synced. This is where the ADP request
// signing gets verified against real Audible.
func (h *Handler) SyncAudible(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	svc := audiobooks.New(h.DB, amazon.NewStore(h.DB), h.Logger)
	n, err := svc.SyncUser(c.Request().Context(), owner)
	if err != nil {
		// Surface the Amazon-side error (status + snippet) so a signing/format
		// mismatch is debuggable from the UI, exactly like the connect flow.
		return apihelpers.RespondErr(c, apierr.BadRequest(err.Error()))
	}
	h.Logger.Info("audible synced", "user", owner, "items", n)
	return c.JSON(http.StatusOK, map[string]any{"synced": n, "source": "audible"})
}

// BackfillAudible enqueues the one-shot, all-time Audible backfill for the
// caller (full library sweep + finished sweep + monthly listening aggregates).
// It runs on the jobs worker — the endpoint returns the enqueued job id
// immediately rather than blocking on a multi-page sweep. Idempotent to enqueue:
// the backfill itself upserts, so a duplicate run is harmless.
func (h *Handler) BackfillAudible(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	if h.JobEnqueuer == nil {
		return apihelpers.RespondErr(c, apierr.BadRequest("background jobs are not available on this server"))
	}
	// Confirm the user actually has an Amazon credential before enqueueing, so
	// the UI gets an immediate, clear error instead of a job that fails later.
	if _, lerr := amazon.NewStore(h.DB).Load(c.Request().Context(), owner); lerr != nil {
		return apihelpers.RespondErr(c, apierr.BadRequest("connect Amazon before running a backfill"))
	}
	id, eerr := h.JobEnqueuer.Enqueue(c.Request().Context(), audiobooks.AudibleBackfillKind, nil,
		jobs.Owner(owner), jobs.MaxAttempts(1))
	if eerr != nil {
		return apihelpers.InternalErr(h.Logger, c, "audible backfill enqueue failed", eerr)
	}
	h.Logger.Info("audible backfill enqueued", "user", owner, "jobId", id)
	return c.JSON(http.StatusAccepted, map[string]any{"enqueued": true, "jobId": id})
}

// readingItemDTO is the view payload (never the raw source blob). The richer
// metadata fields (cover/subtitle/series/narrators/runtime/goodreads rating)
// let the Books page render covers + fuller rows; pointers stay optional.
type readingItemDTO struct {
	Source          string   `json:"source"`
	ExternalID      string   `json:"externalId"`
	Title           string   `json:"title"`
	Authors         string   `json:"authors"`
	Status          string   `json:"status"` // EFFECTIVE = override ?? derived
	ProgressPercent int      `json:"progressPercent"`
	Finished        bool     `json:"finished"`
	StartedAt       *string  `json:"startedAt,omitempty"`
	FinishedAt      *string  `json:"finishedAt,omitempty"` // EFFECTIVE
	Rating          *float64 `json:"rating,omitempty"`     // EFFECTIVE
	SyncedAt        string   `json:"syncedAt"`

	// Curation override layer (migration 00069). statusDerived is the raw Amazon
	// value; the *Override fields are the sticky user/Hardcover overrides (null when
	// none); statusIsOverride drives the FE curated-vs-auto indicator. Status/Rating/
	// FinishedAt above are the EFFECTIVE (override ?? derived) values.
	StatusDerived      string   `json:"statusDerived"`
	StatusOverride     *string  `json:"statusOverride"`
	StatusIsOverride   bool     `json:"statusIsOverride"`
	RatingOverride     *float64 `json:"ratingOverride"`
	FinishedAtOverride *string  `json:"finishedAtOverride"`
	CoverURL           string   `json:"coverUrl,omitempty"`
	Subtitle           string   `json:"subtitle,omitempty"`
	Series             string   `json:"series,omitempty"`
	Narrators          string   `json:"narrators,omitempty"`
	RuntimeMin         *int     `json:"runtimeMin,omitempty"`
	GoodreadsRating    *float64 `json:"goodreadsRating,omitempty"`

	// Identifiers for precise external linking (ASIN is the reliable id; ISBN is
	// NULL for audiobooks). external_id already carries the ASIN; amazonAsin is
	// the print/kindle sibling ASIN when known.
	ISBN       string `json:"isbn,omitempty"`
	AmazonASIN string `json:"amazonAsin,omitempty"`

	// Hardcover match state (migration 00063). Omitted while unmatched — a nil
	// hardcoverBookId is the honest "not matched yet" signal the Books table
	// renders. Once the match sync runs these populate and the row links direct
	// to its Hardcover book page.
	HardcoverBookID    *int64  `json:"hardcoverBookId,omitempty"`
	HardcoverStatus    *string `json:"hardcoverStatus,omitempty"`
	HardcoverMatchedAt *string `json:"hardcoverMatchedAt,omitempty"`
	// hardcoverSlug is the book's Hardcover slug — the FE deep-link prefers it
	// (/books/<slug>) over the numeric id, which 404s on Hardcover's book pages.
	HardcoverSlug *string `json:"hardcoverSlug,omitempty"`
	// hardcoverLists is the book's Hardcover list names (migration 00077) — a
	// property of the book. Rendered as chips in the detail panel + a "List" axis.
	HardcoverLists []string `json:"hardcoverLists,omitempty"`
}

func toReadingItemDTO(it db.ReadingItem) readingItemDTO {
	d := readingItemDTO{
		Source: it.Source, ExternalID: it.ExternalID, Title: it.Title, Authors: it.Authors,
		Status: it.EffectiveStatus(), ProgressPercent: it.ProgressPercent, Finished: it.Finished,
		Rating: it.EffectiveRating(), SyncedAt: it.SyncedAt.UTC().Format(time.RFC3339),
		CoverURL: it.CoverURL, Subtitle: it.Subtitle, Series: it.Series,
		Narrators: it.Narrators, RuntimeMin: it.RuntimeMin, GoodreadsRating: it.GoodreadsRating,
		ISBN: it.ISBN, AmazonASIN: it.AmazonASIN,
		HardcoverBookID: it.HardcoverBookID, HardcoverStatus: it.HardcoverStatus,
		HardcoverSlug:  it.HardcoverSlug,
		StatusDerived:  it.Status,
		StatusOverride: it.StatusOverride, StatusIsOverride: it.StatusOverride != nil,
		RatingOverride: it.RatingOverride,
	}
	// hardcover_lists is a jsonb array of list names; decode it into the DTO's
	// string slice (nil/invalid → omitted).
	if len(it.HardcoverLists) > 0 {
		var names []string
		if err := json.Unmarshal(it.HardcoverLists, &names); err == nil && len(names) > 0 {
			d.HardcoverLists = names
		}
	}
	if it.StartedAt != nil {
		s := it.StartedAt.UTC().Format(time.RFC3339)
		d.StartedAt = &s
	}
	if fa := it.EffectiveFinishedAt(); fa != nil {
		s := fa.UTC().Format(time.RFC3339)
		d.FinishedAt = &s
	}
	if it.FinishedAtOverride != nil {
		s := it.FinishedAtOverride.UTC().Format(time.RFC3339)
		d.FinishedAtOverride = &s
	}
	if it.HardcoverMatchedAt != nil {
		s := it.HardcoverMatchedAt.UTC().Format(time.RFC3339)
		d.HardcoverMatchedAt = &s
	}
	return d
}

// GetReadingItems lets the user SEE exactly what book/audiobook data is synced
// (siloed — one table, never the core models). ?source= filters to audible/kindle.
func (h *Handler) GetReadingItems(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	items, err := h.DB.ListReadingItems(c.Request().Context(), owner, c.QueryParam("source"))
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "reading items list failed", err)
	}
	out := make([]readingItemDTO, 0, len(items))
	for _, it := range items {
		out = append(out, toReadingItemDTO(it))
	}
	return c.JSON(http.StatusOK, map[string]any{"items": out})
}

// DeleteReadingItemsHandler wipes the user's synced book data on request
// (delete-on-request; ?source= scopes to one source, else all). Idempotent.
func (h *Handler) DeleteReadingItemsHandler(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	source := c.QueryParam("source")
	n, err := h.DB.DeleteReadingItems(c.Request().Context(), owner, source)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "reading items delete failed", err)
	}
	// The Kindle insights snapshot is kindle-scoped, so wipe it too when the
	// delete covers all sources or Kindle specifically (a best-effort companion —
	// a failure here doesn't fail the primary reading-items wipe).
	if source == "" || source == "kindle" {
		if _, derr := h.DB.DeleteKindleReadingInsights(c.Request().Context(), owner); derr != nil {
			h.Logger.Warn("kindle insights snapshot delete failed", "user", owner, "err", derr)
		}
	}
	h.Logger.Info("reading items deleted", "user", owner, "source", source, "rows", n)
	return c.JSON(http.StatusOK, map[string]any{"deleted": n})
}

// sealAmazonSession encrypts the RegistrationSession into an opaque token so the
// PKCE code_verifier never leaves the server in the clear.
func sealAmazonSession(sess amazon.RegistrationSession) (string, error) {
	blob, err := json.Marshal(sess)
	if err != nil {
		return "", err
	}
	ct, err := auth.Encrypt(blob)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(ct), nil
}

func openAmazonSession(token string) (amazon.RegistrationSession, error) {
	ct, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return amazon.RegistrationSession{}, err
	}
	blob, err := auth.Decrypt(ct)
	if err != nil {
		return amazon.RegistrationSession{}, err
	}
	var sess amazon.RegistrationSession
	if err := json.Unmarshal(blob, &sess); err != nil {
		return amazon.RegistrationSession{}, err
	}
	return sess, nil
}
