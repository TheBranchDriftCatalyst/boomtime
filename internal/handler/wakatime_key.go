package handler

// wakatime_key.go — endpoints for the encrypted-at-rest imported Wakatime API
// key (gaka-6jm.2).
//
// SECURITY POSTURE (paranoid on purpose):
//
//   - The plaintext key is NEVER returned by any endpoint here. GET reports
//     `{"hasSavedKey", "keyStatus", "checkedAt"}` — no hint, no prefix/suffix,
//     no length. Leaking even a 4-char prefix meaningfully narrows the search
//     space for the wakatime.com Secret API Key (a bare UUID), so we don't.
//
//   - The plaintext key is NEVER logged. On save, we log
//     `hasSavedWakatimeKey=true` — never the value or its length.
//
//   - POST is validate-then-persist: the server probes wakatime.com's
//     /users/current with the supplied key BEFORE writing it. A 401/403
//     returns 400 to the client so an obviously-bad key never survives in
//     the DB (matches the user's expectation that "save = validated").
//     Network errors on the probe are NOT fatal: the save proceeds and the
//     status column is set to 'unknown' so the FE dot renders yellow.
//
//   - Encryption is loaded lazily inside internal/auth; when
//     BOOM_ENCRYPTION_KEY is not configured, POST returns a 500 with a
//     generic message and we log the actual reason server-side. Same on DB
//     errors — the client only ever sees the generic envelope.

import (
	"context"
	"encoding/base64"
	"net/http"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/auth"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/labstack/echo/v5"
)

// wakatimeKeySaveRequest is the JSON body for POST /api/v1/users/current/wakatime_key.
type wakatimeKeySaveRequest struct {
	Key string `json:"key"`
}

// wakatimeKeyGetResponse is the shape returned by GET .../wakatime_key.
//
//   - HasSavedKey is a hard yes/no.
//   - KeyStatus is nil (no saved key) or one of "valid" | "invalid" |
//     "unknown". Any unknown text stored in the column is passed through
//     verbatim; the FE treats unrecognized values as neutral.
//   - CheckedAt is an RFC3339 timestamp of the last validity update, or nil
//     if we've never checked (fresh save that failed to probe, or historic
//     rows).
type wakatimeKeyGetResponse struct {
	HasSavedKey bool    `json:"hasSavedKey"`
	KeyStatus   *string `json:"keyStatus,omitempty"`
	CheckedAt   *string `json:"checkedAt,omitempty"`
}

// wakatimeProbeURL is the wakatime.com endpoint used to validate a key at
// save-time. It's the smallest safe read that a valid key must succeed on;
// invalid keys fail with 401.
const wakatimeProbeURL = "https://wakatime.com/api/v1/users/current"

// wakatimeProbeTimeout keeps the save path snappy. 10s is generous — the
// endpoint is a single small JSON — but bounded so a hung upstream never
// makes the save UI look wedged.
const wakatimeProbeTimeout = 10 * time.Second

// probeWakatimeKey returns whether the key is accepted (2xx), explicitly
// rejected (401/403), or in an "unknown" state (network error, timeout, 5xx).
// The returned status maps directly to what we write to
// users.wakatime_key_status.
//
// Never returns the plaintext key or the raw response body — only the
// server-side effect (which status to store) is surfaced.
//
// owner is threaded through so the probe's warning log lines are tagged with
// the acting user (gaka-awh.2). It's not used for auth here — the caller has
// already resolved the owner — only as the slog attribute the LogHub filter
// keys off of.
func (h *Handler) probeWakatimeKey(ctx context.Context, owner, plaintext string) db.WakatimeKeyStatus {
	ctx, cancel := context.WithTimeout(ctx, wakatimeProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wakatimeProbeURL, nil)
	if err != nil {
		h.Logger.Warn("wakatime probe: build request failed", "user", owner, "err", err)
		return db.WakatimeKeyStatusUnknown
	}
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(plaintext)))
	resp, err := httpClient.Do(req)
	if err != nil {
		// Network / timeout / DNS — not the key's fault. FE dot goes yellow.
		h.Logger.Warn("wakatime probe: request failed", "user", owner, "err", err)
		return db.WakatimeKeyStatusUnknown
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return db.WakatimeKeyStatusValid
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return db.WakatimeKeyStatusInvalid
	default:
		// 5xx, rate-limit, unexpected. Save under 'unknown' so the FE doesn't
		// falsely claim invalidity — a later import can flip this.
		h.Logger.Warn("wakatime probe: unexpected status", "user", owner, "status", resp.StatusCode)
		return db.WakatimeKeyStatusUnknown
	}
}

// GetWakatimeKey: GET /api/v1/users/current/wakatime_key — reports whether
// the caller has a saved encrypted Wakatime key on file and the last-known
// validity + check timestamp. Deliberately does NOT include the key or any
// prefix of the plaintext.
func (h *Handler) GetWakatimeKey(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	info, err := h.DB.GetWakatimeKeyInfo(c.Request().Context(), owner)
	if err != nil {
		return h.internalErr(c, "wakatime key lookup failed", err)
	}
	resp := wakatimeKeyGetResponse{HasSavedKey: info.HasSavedKey}
	if info.HasSavedKey {
		resp.KeyStatus = info.Status
		if info.CheckedAt != nil {
			// RFC3339 so date-fns's parseISO chain (used by the FE) is happy.
			ts := info.CheckedAt.UTC().Format(time.RFC3339)
			resp.CheckedAt = &ts
		}
	}
	return c.JSON(http.StatusOK, resp)
}

// SaveWakatimeKey: POST /api/v1/users/current/wakatime_key — probe wakatime.com
// with the supplied key, and if accepted (or the probe couldn't reach a
// verdict), encrypt-and-store the plaintext. Overwrites any prior saved
// key. Returns 204 on success, 400 if the probe conclusively rejected the
// key ("Wakatime rejected this key — check it and try again.").
//
// Body: {"key": "<raw wakatime api key>"}. A blank key is rejected as 400 so
// clients don't accidentally clobber a saved key with an empty POST — use
// DELETE for that.
func (h *Handler) SaveWakatimeKey(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	var req wakatimeKeySaveRequest
	// gaka-bi2: 4 KiB cap — the body is a single opaque key string; anything
	// larger cannot be a real Wakatime API key and just wastes memory before
	// the encrypt step.
	if aerr := BindJSONWithLimit(c, &req, BodyLimitSmall); aerr != nil {
		return respondErr(c, aerr)
	}
	if req.Key == "" {
		return respondErr(c, apierr.BadRequest("key is required (use DELETE to clear)"))
	}

	// Validate against wakatime.com BEFORE writing. Rejecting an invalid key
	// here keeps the "saved = validated" invariant simple for the FE and
	// stops us from ever storing (and later trying to import with) a bogus
	// value.
	status := h.probeWakatimeKey(c.Request().Context(), owner, req.Key)
	if status == db.WakatimeKeyStatusInvalid {
		return respondErr(c, apierr.BadRequest("Wakatime rejected this key — check it and try again."))
	}

	ct, err := auth.Encrypt([]byte(req.Key))
	if err != nil {
		return h.internalErr(c, "wakatime key encrypt failed", err)
	}
	if err := h.DB.SetEncryptedWakatimeKey(c.Request().Context(), owner, ct, status); err != nil {
		return h.internalErr(c, "wakatime key persist failed", err)
	}
	// Log the fact of a save (no value, no length). Status stays high-level.
	h.Logger.Info("wakatime key saved", "user", owner, "hasSavedWakatimeKey", true, "status", string(status))
	return noContent(c)
}

// DeleteWakatimeKey: DELETE /api/v1/users/current/wakatime_key — clear any
// saved encrypted key + its status metadata for the caller. Idempotent (204
// whether or not one existed) so the FE doesn't need to first-check with GET.
func (h *Handler) DeleteWakatimeKey(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	if err := h.DB.ClearEncryptedWakatimeKey(c.Request().Context(), owner); err != nil {
		return h.internalErr(c, "wakatime key clear failed", err)
	}
	h.Logger.Info("wakatime key cleared", "user", owner)
	return noContent(c)
}
