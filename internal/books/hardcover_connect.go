package books

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/hardcover"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/jobs"
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

// PullHardcover enqueues the inbound Hardcover sync (the PULL half of the
// bidirectional sync) for the caller: it reads the user's Hardcover shelf and
// reconciles each entry's status/updated_at onto the matching local
// reading_item's minimal linkage. It runs on the jobs worker (owner-scoped
// payload) and returns the enqueued job id immediately rather than blocking on a
// paginated shelf sweep. BooksEnabled-gated. Idempotent to enqueue: the pull only
// updates linkage columns, so a duplicate run is harmless.
func (h *Handler) PullHardcover(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	if h.JobEnqueuer == nil {
		return apihelpers.RespondErr(c, apierr.BadRequest("background jobs are not available on this server"))
	}
	// Confirm Hardcover is actually connected before enqueueing, so the UI gets an
	// immediate, clear error instead of a job that no-ops later.
	info, ierr := hardcover.NewStore(h.DB).Info(c.Request().Context(), owner)
	if ierr != nil {
		return apihelpers.InternalErr(h.Logger, c, "hardcover connection lookup failed", ierr)
	}
	if !info.Connected {
		return apihelpers.RespondErr(c, apierr.BadRequest("connect Hardcover before running a pull"))
	}
	id, eerr := h.JobEnqueuer.Enqueue(c.Request().Context(), hardcover.PullJobKind, nil,
		jobs.Owner(owner), jobs.MaxAttempts(3))
	if eerr != nil {
		return apihelpers.InternalErr(h.Logger, c, "hardcover pull enqueue failed", eerr)
	}
	h.Logger.Info("hardcover pull enqueued", "user", owner, "jobId", id)
	return c.JSON(http.StatusAccepted, map[string]any{"enqueued": true, "jobId": id})
}

// MatchHardcover enqueues the explicit `hardcover-match` pipeline stage (the
// middle step of backfill → match → sync) for the caller: it resolves every
// still-unmatched reading_item to a Hardcover book_id/edition_id via the read-only
// match ladder and caches the linkage. It runs on the jobs worker (owner-scoped
// payload) and returns the enqueued job id immediately rather than blocking on the
// ladder. BooksEnabled-gated. Idempotent to enqueue: an already-matched row drops
// out of the worklist, so a duplicate run is harmless. Mirrors PullHardcover.
func (h *Handler) MatchHardcover(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	if h.JobEnqueuer == nil {
		return apihelpers.RespondErr(c, apierr.BadRequest("background jobs are not available on this server"))
	}
	// Confirm Hardcover is actually connected before enqueueing, so the UI gets an
	// immediate, clear error instead of a job that no-ops later.
	info, ierr := hardcover.NewStore(h.DB).Info(c.Request().Context(), owner)
	if ierr != nil {
		return apihelpers.InternalErr(h.Logger, c, "hardcover connection lookup failed", ierr)
	}
	if !info.Connected {
		return apihelpers.RespondErr(c, apierr.BadRequest("connect Hardcover before running a match"))
	}
	// ?force=1 requests a full re-check that ignores the 30d negative-cache window
	// (a row the ladder previously proved unmatchable is retried now). Carried in the
	// job payload; absent → the normal windowed sweep.
	var payload []byte
	force, _ := strconv.ParseBool(c.QueryParam("force"))
	if force {
		if b, merr := json.Marshal(hardcover.MatchPayload{Force: true}); merr == nil {
			payload = b
		}
	}
	id, eerr := h.JobEnqueuer.Enqueue(c.Request().Context(), hardcover.HardcoverMatchKind, payload,
		jobs.Owner(owner), jobs.MaxAttempts(3))
	if eerr != nil {
		return apihelpers.InternalErr(h.Logger, c, "hardcover match enqueue failed", eerr)
	}
	h.Logger.Info("hardcover match enqueued", "user", owner, "jobId", id, "force", force)
	return c.JSON(http.StatusAccepted, map[string]any{"enqueued": true, "jobId": id})
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
