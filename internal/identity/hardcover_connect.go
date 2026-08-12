package identity

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/hardcover"
	"github.com/labstack/echo/v5"
)

// hardcover_connect.go — the "Connect Hardcover" endpoints (catalyst-books push
// target). Hardcover is the SYNC TARGET; a user pastes their bearer token (from
// Hardcover account settings), we validate it with a me{} query and store it
// encrypted. The token expires yearly + resets every Jan 1, so a re-paste is a
// routine event — the status flips to invalid on a 401 and the UI prompts it.
//
//	POST   /api/v1/hardcover/connect {token} → 204 (validate + store)   [BooksEnabled]
//	GET    /api/v1/hardcover                  → {connected, status, checkedAt}
//	DELETE /api/v1/hardcover                  → 204 (disconnect)
//
// SECURITY POSTURE (mirrors the Wakatime-key card):
//   - The plaintext token is NEVER returned by any endpoint. GET reports only
//     {connected, status, checkedAt} — no prefix, no length, no hint.
//   - The plaintext token is NEVER logged. On save we log
//     `hardcoverConnected=true` — never the value.
//   - POST is validate-then-persist: a 401 from Hardcover returns 400 to the
//     client so an obviously-bad token never survives in the DB.

type hardcoverConnectReq struct {
	Token string `json:"token"`
}

type hardcoverConnectionResp struct {
	Connected bool    `json:"connected"`
	Status    *string `json:"status,omitempty"`
	CheckedAt *string `json:"checkedAt,omitempty"`
}

// hardcoverValidateTimeout bounds the connect-time me{} probe so a hung upstream
// never wedges the save UI.
const hardcoverValidateTimeout = 15 * time.Second

// ConnectHardcover validates a pasted bearer token against Hardcover's me{}
// query and, if accepted, stores it encrypted. BooksEnabled-gated. Returns 400
// on a rejected/blank token, 204 on success.
func (h *Handler) ConnectHardcover(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	var req hardcoverConnectReq
	// Small cap — the body is a single opaque bearer token.
	if berr := apihelpers.BindJSONWithLimit(c, &req, apihelpers.BodyLimitSmall); berr != nil {
		return apihelpers.RespondErr(c, berr)
	}
	if req.Token == "" {
		return apihelpers.RespondErr(c, apierr.BadRequest("token is required (use DELETE to clear)"))
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), hardcoverValidateTimeout)
	defer cancel()
	client := hardcover.NewClient(req.Token)
	if _, err := client.Validate(ctx); err != nil {
		if errors.Is(err, hardcover.ErrBadToken) {
			return apihelpers.RespondErr(c, apierr.BadRequest("Hardcover rejected this token — check it and try again."))
		}
		if errors.Is(err, hardcover.ErrRateLimited) {
			return apihelpers.RespondErr(c, apierr.BadRequest("Hardcover is rate-limiting right now — please try again in a minute."))
		}
		// Network / timeout / unexpected — don't persist an unverified token.
		return apihelpers.InternalErr(h.Logger, c, "hardcover token validation failed", err)
	}

	if err := hardcover.NewStore(h.DB).Save(c.Request().Context(), owner, req.Token, db.HardcoverKeyStatusValid); err != nil {
		return apihelpers.InternalErr(h.Logger, c, "hardcover token persist failed", err)
	}
	h.Logger.Info("hardcover connected", "user", owner, "hardcoverConnected", true)
	return apihelpers.NoContent(c)
}

// GetHardcoverConnection reports presence/status (never returns the token).
func (h *Handler) GetHardcoverConnection(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	info, err := hardcover.NewStore(h.DB).Info(c.Request().Context(), owner)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "hardcover connection lookup failed", err)
	}
	resp := hardcoverConnectionResp{Connected: info.Connected}
	if info.Connected {
		resp.Status = info.Status
		if info.CheckedAt != nil {
			ts := info.CheckedAt.UTC().Format(time.RFC3339)
			resp.CheckedAt = &ts
		}
	}
	return c.JSON(http.StatusOK, resp)
}

// DisconnectHardcover clears the stored token (idempotent).
func (h *Handler) DisconnectHardcover(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	if err := hardcover.NewStore(h.DB).Clear(c.Request().Context(), owner); err != nil {
		return apihelpers.InternalErr(h.Logger, c, "hardcover disconnect failed", err)
	}
	h.Logger.Info("hardcover disconnected", "user", owner)
	return apihelpers.NoContent(c)
}
