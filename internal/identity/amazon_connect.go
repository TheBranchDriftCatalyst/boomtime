package identity

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/domains/audiobooks"
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

// readingItemDTO is the view payload (never the raw source blob).
type readingItemDTO struct {
	Source          string   `json:"source"`
	ExternalID      string   `json:"externalId"`
	Title           string   `json:"title"`
	Authors         string   `json:"authors"`
	Status          string   `json:"status"`
	ProgressPercent int      `json:"progressPercent"`
	Finished        bool     `json:"finished"`
	StartedAt       *string  `json:"startedAt,omitempty"`
	FinishedAt      *string  `json:"finishedAt,omitempty"`
	Rating          *float64 `json:"rating,omitempty"`
	SyncedAt        string   `json:"syncedAt"`
}

func toReadingItemDTO(it db.ReadingItem) readingItemDTO {
	d := readingItemDTO{
		Source: it.Source, ExternalID: it.ExternalID, Title: it.Title, Authors: it.Authors,
		Status: it.Status, ProgressPercent: it.ProgressPercent, Finished: it.Finished,
		Rating: it.Rating, SyncedAt: it.SyncedAt.UTC().Format(time.RFC3339),
	}
	if it.StartedAt != nil {
		s := it.StartedAt.UTC().Format(time.RFC3339)
		d.StartedAt = &s
	}
	if it.FinishedAt != nil {
		s := it.FinishedAt.UTC().Format(time.RFC3339)
		d.FinishedAt = &s
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
	n, err := h.DB.DeleteReadingItems(c.Request().Context(), owner, c.QueryParam("source"))
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "reading items delete failed", err)
	}
	h.Logger.Info("reading items deleted", "user", owner, "source", c.QueryParam("source"), "rows", n)
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
