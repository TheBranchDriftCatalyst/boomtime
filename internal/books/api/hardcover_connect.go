package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/hardcover"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/jobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
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
// The body is bound by the typed seam (apiroute.NoContentBody) under the SAME
// apihelpers.BodyLimitSmall cap this handler applied itself — the body is a
// single opaque bearer token.
func (h *Handler) ConnectHardcover(c *echo.Context, req hardcoverConnectReq) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return aerr
	}
	if req.Token == "" {
		return apierr.BadRequest("token is required (use DELETE to clear)")
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), hardcoverValidateTimeout)
	defer cancel()
	client := hardcover.NewClient(req.Token)
	if _, err := client.Validate(ctx); err != nil {
		if errors.Is(err, hardcover.ErrBadToken) {
			return apierr.BadRequest("Hardcover rejected this token — check it and try again.")
		}
		if errors.Is(err, hardcover.ErrRateLimited) {
			return apierr.BadRequest("Hardcover is rate-limiting right now — please try again in a minute.")
		}
		// Network / timeout / unexpected — don't persist an unverified token.
		return fmt.Errorf("hardcover token validation failed: %w", err)
	}

	if err := hardcover.NewStore(h.DB).Save(c.Request().Context(), owner, req.Token, db.HardcoverKeyStatusValid); err != nil {
		return fmt.Errorf("hardcover token persist failed: %w", err)
	}
	h.Logger.Info("hardcover connected", "user", owner, "hardcoverConnected", true)
	return nil
}

// GetHardcoverConnection reports presence/status (never returns the token).
func (h *Handler) GetHardcoverConnection(c *echo.Context) (hardcoverConnectionResp, error) {
	var out hardcoverConnectionResp
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	info, err := hardcover.NewStore(h.DB).Info(c.Request().Context(), owner)
	if err != nil {
		return out, fmt.Errorf("hardcover connection lookup failed: %w", err)
	}
	resp := hardcoverConnectionResp{Connected: info.Connected}
	if info.Connected {
		resp.Status = info.Status
		if info.CheckedAt != nil {
			ts := info.CheckedAt.UTC().Format(time.RFC3339)
			resp.CheckedAt = &ts
		}
	}
	return resp, nil
}

// PullHardcover enqueues the inbound Hardcover sync (the PULL half of the
// bidirectional sync) for the caller: it reads the user's Hardcover shelf and
// reconciles each entry's status/updated_at onto the matching local
// reading_item's minimal linkage. It runs on the jobs worker (owner-scoped
// payload) and returns the enqueued job id immediately rather than blocking on a
// paginated shelf sweep. BooksEnabled-gated. Idempotent to enqueue: the pull only
// updates linkage columns, so a duplicate run is harmless.
func (h *Handler) PullHardcover(c *echo.Context) (enqueuedJobResponse, error) {
	var out enqueuedJobResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	if h.JobEnqueuer == nil {
		return out, apierr.BadRequest("background jobs are not available on this server")
	}
	// Confirm Hardcover is actually connected before enqueueing, so the UI gets an
	// immediate, clear error instead of a job that no-ops later.
	info, ierr := hardcover.NewStore(h.DB).Info(c.Request().Context(), owner)
	if ierr != nil {
		return out, fmt.Errorf("hardcover connection lookup failed: %w", ierr)
	}
	if !info.Connected {
		return out, apierr.BadRequest("connect Hardcover before running a pull")
	}
	id, eerr := h.JobEnqueuer.Enqueue(c.Request().Context(), hardcover.PullJobKind, nil,
		jobs.Owner(owner), jobs.MaxAttempts(3))
	if eerr != nil {
		return out, fmt.Errorf("hardcover pull enqueue failed: %w", eerr)
	}
	h.Logger.Info("hardcover pull enqueued", "user", owner, "jobId", id)
	return enqueuedJobResponse{Enqueued: true, JobID: id}, nil
}

// MatchHardcover enqueues the explicit `hardcover-match` pipeline stage (the
// middle step of backfill → match → sync) for the caller: it resolves every
// still-unmatched reading_item to a Hardcover book_id/edition_id via the read-only
// match ladder and caches the linkage. It runs on the jobs worker (owner-scoped
// payload) and returns the enqueued job id immediately rather than blocking on the
// ladder. BooksEnabled-gated. Idempotent to enqueue: an already-matched row drops
// out of the worklist, so a duplicate run is harmless. Mirrors PullHardcover.
func (h *Handler) MatchHardcover(c *echo.Context) (enqueuedJobResponse, error) {
	var out enqueuedJobResponse
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return out, aerr
	}
	if h.JobEnqueuer == nil {
		return out, apierr.BadRequest("background jobs are not available on this server")
	}
	// Confirm Hardcover is actually connected before enqueueing, so the UI gets an
	// immediate, clear error instead of a job that no-ops later.
	info, ierr := hardcover.NewStore(h.DB).Info(c.Request().Context(), owner)
	if ierr != nil {
		return out, fmt.Errorf("hardcover connection lookup failed: %w", ierr)
	}
	if !info.Connected {
		return out, apierr.BadRequest("connect Hardcover before running a match")
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
		return out, fmt.Errorf("hardcover match enqueue failed: %w", eerr)
	}
	h.Logger.Info("hardcover match enqueued", "user", owner, "jobId", id, "force", force)
	return enqueuedJobResponse{Enqueued: true, JobID: id}, nil
}

// DisconnectHardcover clears the stored token (idempotent).
func (h *Handler) DisconnectHardcover(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return aerr
	}
	if err := hardcover.NewStore(h.DB).Clear(c.Request().Context(), owner); err != nil {
		return fmt.Errorf("hardcover disconnect failed: %w", err)
	}
	h.Logger.Info("hardcover disconnected", "user", owner)
	return nil
}
