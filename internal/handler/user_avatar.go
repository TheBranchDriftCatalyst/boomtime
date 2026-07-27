// user_avatar.go (gaka-9v4): per-user CHIBI avatar endpoints.
//
// Three surface areas:
//
//   POST /api/v1/admin/avatar/synthesize-prompt   (SSE, self-only-for-now)
//     Streams an OpenAI-compat chat completion via the configured LLM. The
//     wire body carries the top-3 label names + a compact activity synopsis
//     that the FE computed from the caller's stats. Response is proxied SSE
//     from the upstream — the FE reads it live with a small custom hook.
//
//   POST /api/v1/users/current/avatar/regenerate   (async, self-only)
//     Marks user_avatars.status='running', spawns a goroutine that calls
//     the comfyui shim, and returns 202 immediately. Client polls
//     status/streams the RIGHT panel via the status endpoint below.
//
//   GET  /api/v1/users/current/avatar/status       (self-only)
//     Compact tri-state {status, error, generatedAt} — never ships bytes.
//     Cheap enough to poll every 5s during a render.
//
//   GET  /api/v1/users/:username/avatar            (PUBLIC)
//     Serves the raw ready image bytes so the public dossier hero can drop
//     it into an <img>. 404s when status != 'ready' so an in-flight render
//     never leaks a stale byte-string down to a fresh viewer.
//
// The "admin" path on synthesize-prompt is a temporary constraint from the
// design: LLM cost is per-token and unbounded per user in the wrong hands,
// so the first cut restricts it to admins-or-self. `requireAdminOrSelf`
// centralises the check so opening it up is a one-line change.
package handler

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/comfyui"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/labstack/echo/v5"
)

// avatarSynthReq is the FE-supplied context for the LLM prompt-authoring
// call. Keeps the server dumb about how the top-3 / synopsis are computed
// (the evaluator lives on the FE already; duplicating it server-side would
// require rebuilding the whole publicprofile payload just for this one
// endpoint).
type avatarSynthReq struct {
	// TopLabels: up to 3 short label names (e.g. "PYTHON MASTER",
	// "LATE NIGHT CODER"). Empty slice is fine — the LLM will produce a
	// generic operator portrait in that case.
	TopLabels []string `json:"topLabels"`
	// Synopsis: one-line activity readout, e.g.
	//   "python-heavy · 6h/day · night-owl · streak 42d".
	// Free-form; the FE builds this from the same stats it already renders.
	Synopsis string `json:"synopsis"`
}

// avatarSynthSystemPrompt: the fixed style/aesthetic guardrails for the
// portrait-prompt author. Deliberately opinionated toward the chibi
// aesthetic requested in the feature spec — this is the ONE server-side
// prompt-engineering decision, everything else is user-tunable in the
// textarea before RENDER.
const avatarSynthSystemPrompt = `You are a portrait-prompt author for a diffusion image model.
Your ONLY job: emit a comma-separated SDXL/Chroma-style tag list describing a CHIBI PORTRAIT of a software operator, based on the caller's coding-activity summary.

Hard constraints:
- Output a SINGLE line, no preamble, no markdown, no quotes.
- 20-40 tags, comma-separated, ordered from most to least important.
- ALWAYS start with: "chibi portrait, single character, centered, head and shoulders, transparent background, cel shading, thick outlines".
- Then a few tags evoking the caller's dominant language / editor / vibe from the summary (e.g. "python enthusiast", "vim keyboard", "neon terminal glow").
- Then a mood/palette that matches the archetype labels (e.g. "night owl" -> "midnight cyan palette, tired eyes, cozy hoodie").
- End with quality tags: "clean line art, kawaii, expressive eyes, vibrant colors, subtle bloom".

Never include real names, brand logos, watermarks, text, or NSFW content.`

// SynthesizeAvatarPrompt: POST /api/v1/admin/avatar/synthesize-prompt.
// Streams SSE lines proxied from the upstream OpenAI-compat chat/completions
// endpoint. The wire shape (each data: line carries a JSON with a delta
// content string) matches the OpenAI stream so the FE hook can use the
// exact same parser other consumers already have.
//
// Auth: the caller MUST be resolvable to an owner (a valid API token). The
// current design ships self-only via the "admin OR self" check below —
// which for the initial cut simplifies to "must be logged in" since the FE
// only ever hits this for the current user. When we open avatar prompts
// up to any user, keep the resolveUser check and drop requireAdminOrSelf.
//
// The LLM key never touches the FE — we forward the request server-side.
// A missing key returns 503 with a clear "not configured" message so the
// operator sees exactly what env var to set.
func (h *Handler) SynthesizeAvatarPrompt(c *echo.Context) error {
	// Endpoint sits under /api/v1/admin/ so we gate on the admin
	// allowlist. Any logged-in user can hit it in principle — flipping
	// this to `resolveUser` alone is a one-line change when we open the
	// feature to arbitrary users. Until then admins keep LLM cost tight.
	if _, aerr := h.requireAdmin(c); aerr != nil {
		return respondErr(c, aerr)
	}
	if !h.Cfg.LLMEnabled() {
		return respondErr(c, apierr.New(http.StatusServiceUnavailable,
			"LLM not configured — set BOOM_LLM_API_KEY on the server", nil))
	}

	var req avatarSynthReq
	if aerr := BindJSONWithLimit(c, &req, BodyLimitSmall); aerr != nil {
		return respondErr(c, aerr)
	}

	userMsg := buildAvatarUserMessage(req)

	// OpenAI-shaped chat/completions request, streaming.
	body, err := json.Marshal(map[string]any{
		"model":  h.Cfg.LLMModel,
		"stream": true,
		"messages": []map[string]string{
			{"role": "system", "content": avatarSynthSystemPrompt},
			{"role": "user", "content": userMsg},
		},
		"temperature": 0.7,
	})
	if err != nil {
		return h.internalErr(c, "avatar synth: marshal upstream request", err)
	}

	// 60s cap: gpt-4o-mini typically ships a 100-tag tokenized answer in
	// <5s. A hard ceiling stops a stuck upstream from wedging the response
	// writer forever.
	ctx, cancel := context.WithTimeout(c.Request().Context(), 60*time.Second)
	defer cancel()

	upstreamURL := h.Cfg.LLMBaseURL + "/chat/completions"
	upReq, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bytes.NewReader(body))
	if err != nil {
		return h.internalErr(c, "avatar synth: build upstream request", err)
	}
	upReq.Header.Set("Content-Type", "application/json")
	upReq.Header.Set("Authorization", "Bearer "+h.Cfg.LLMAPIKey)
	upReq.Header.Set("Accept", "text/event-stream")

	upResp, err := avatarLLMClient.Do(upReq)
	if err != nil {
		h.Logger.Warn("avatar synth: upstream call failed", "err", err)
		return respondErr(c, apierr.New(http.StatusBadGateway,
			"LLM upstream call failed", nil))
	}
	defer upResp.Body.Close()

	if upResp.StatusCode != http.StatusOK {
		// Read a small snippet for the log — don't leak the LLM's error
		// message body to the client (may reveal key / rate-limit details).
		msg, _ := io.ReadAll(io.LimitReader(upResp.Body, 512))
		h.Logger.Warn("avatar synth: upstream non-200",
			"status", upResp.StatusCode, "body", strings.TrimSpace(string(msg)))
		return respondErr(c, apierr.New(http.StatusBadGateway,
			fmt.Sprintf("LLM upstream returned %d", upResp.StatusCode), nil))
	}

	// Proxy the SSE stream 1:1 down to the FE. Set headers BEFORE the
	// first write so the browser latches onto text/event-stream and
	// disables its default response buffering.
	resp := c.Response()
	resp.Header().Set("Content-Type", "text/event-stream")
	resp.Header().Set("Cache-Control", "no-cache")
	resp.Header().Set("Connection", "keep-alive")
	resp.Header().Set("X-Accel-Buffering", "no") // nginx: don't buffer
	resp.WriteHeader(http.StatusOK)

	// Echo's *Response wraps http.ResponseWriter; net/http's underlying
	// writer implements http.Flusher and echo forwards the interface.
	// httptest.ResponseRecorder does not — the SSE test uses hand-
	// crafted streaming via msw on the FE side and does not exercise
	// this branch server-side.
	flusher, canFlush := resp.Writer.(http.Flusher)
	scanner := bufio.NewScanner(upResp.Body)
	// SSE lines can be large (long delta contents) — bump the scanner buffer.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if _, err := resp.Write(line); err != nil {
			return nil // client hung up — nothing meaningful to log
		}
		if _, err := resp.Write([]byte("\n")); err != nil {
			return nil
		}
		if canFlush {
			flusher.Flush()
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, context.Canceled) {
		h.Logger.Warn("avatar synth: SSE proxy read error", "err", err)
	}
	return nil
}

// buildAvatarUserMessage assembles the one user-message the LLM sees. Kept
// as its own function so a unit test can assert the format without a live
// LLM call. Empty topLabels / synopsis is handled gracefully — the LLM
// gets a generic "new operator" prompt.
func buildAvatarUserMessage(req avatarSynthReq) string {
	var b strings.Builder
	b.WriteString("Author a chibi-portrait prompt for a software operator with this profile:\n")
	if len(req.TopLabels) > 0 {
		b.WriteString("- top archetypes: ")
		b.WriteString(strings.Join(req.TopLabels, ", "))
		b.WriteString("\n")
	} else {
		b.WriteString("- top archetypes: NEW OPERATOR (no dominant traits yet)\n")
	}
	if strings.TrimSpace(req.Synopsis) != "" {
		b.WriteString("- activity synopsis: ")
		b.WriteString(strings.TrimSpace(req.Synopsis))
		b.WriteString("\n")
	}
	b.WriteString("\nRemember: SINGLE LINE, comma-separated SDXL tags, starting with the fixed prefix.")
	return b.String()
}

// avatarLLMClient is a dedicated http.Client for the SSE proxy. 65s total
// timeout is slightly larger than the per-request ctx timeout to give the
// context cancel priority (client.Timeout is a hard axe, ctx is graceful).
var avatarLLMClient = &http.Client{Timeout: 65 * time.Second}

// avatarRegenReq is the body for the async render endpoint. `model`,
// `size`, and `seed` are optional operator escape hatches — matching the
// per-label admin regen payload shape.
type avatarRegenReq struct {
	Prompt string `json:"prompt"`
	Model  string `json:"model,omitempty"`
	Size   string `json:"size,omitempty"`
	Seed   *int64 `json:"seed,omitempty"`
}

// avatarRegenTimeout bounds the async goroutine. Matches the comfyui
// shim's ~45min per-request ceiling (chroma-hd end-to-end). A wedged shim
// call will eventually fail out and flip status to 'error' rather than
// leaking the goroutine.
const avatarRegenTimeout = 50 * time.Minute

// RegenerateAvatar: POST /api/v1/users/current/avatar/regenerate. Enqueues
// an image generation goroutine and returns 202 with the current status
// row. The FE watches /avatar/status for the terminal ready/error
// transition — no queue registry (one avatar per user, no batching win).
func (h *Handler) RegenerateAvatar(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	if !h.Cfg.LabelImagesEnabled() {
		// Reuses the label-images gate: same shim, same operator toggle.
		// Different message so the operator knows this feature is the
		// affected caller when the shim is missing.
		return respondErr(c, apierr.New(http.StatusServiceUnavailable,
			"avatar rendering unavailable — set BOOM_FEATURE_LABEL_IMAGES=on and BOOM_COMFYUI_SHIM_URL, then restart", nil))
	}

	var req avatarRegenReq
	if aerr := BindJSONWithLimit(c, &req, BodyLimitMedium); aerr != nil {
		return respondErr(c, aerr)
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return respondErr(c, apierr.BadRequest("prompt is required"))
	}

	ctx := c.Request().Context()
	// Guard against a concurrent RENDER click from the same user — if a
	// job is already running, refuse cleanly instead of spawning a
	// duplicate goroutine that would race with itself on the write path.
	if info, ok, err := h.DB.GetUserAvatarStatus(ctx, owner); err != nil {
		return h.internalErr(c, "avatar status lookup failed", err)
	} else if ok && info.Status == db.UserAvatarStatusRunning {
		return respondErr(c, apierr.New(http.StatusConflict,
			"avatar render already in flight — wait for it to finish or fail", nil))
	}

	// Reserve the row up front so a poll immediately after the 202 sees
	// 'running' — no gap where the poll reads the old 'ready' state.
	if err := h.DB.SetAvatarStatus(ctx, owner, db.UserAvatarStatusRunning, ""); err != nil {
		return h.internalErr(c, "avatar reserve failed", err)
	}

	// Detach from the request context so the goroutine survives the
	// handler returning. We still bound it by avatarRegenTimeout below.
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = h.Cfg.ComfyUIModel
	}
	size := strings.TrimSpace(req.Size)
	prompt := strings.TrimSpace(req.Prompt)
	seed := req.Seed

	shimClient, cerr := comfyui.NewClient(h.Cfg.ComfyUIShimURL)
	if cerr != nil || shimClient == nil {
		// Shouldn't reach here (LabelImagesEnabled gated the URL) but be
		// defensive: flip status back to error before returning so the FE
		// isn't stuck on 'running' forever.
		_ = h.DB.SetAvatarStatus(ctx, owner, db.UserAvatarStatusError,
			fmt.Sprintf("comfyui client init failed: %v", cerr))
		return h.internalErr(c, "avatar shim init failed", cerr)
	}

	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), avatarRegenTimeout)
		defer cancel()
		// Named `imgBytes` to avoid shadowing the top-level "bytes" package
		// import used for the SSE proxy request builder.
		imgBytes, mime, gerr := shimClient.Generate(bgCtx, prompt, model, size, seed)
		if gerr != nil {
			h.Logger.Warn("avatar regen: shim call failed", "user", owner, "err", gerr)
			if serr := h.DB.SetAvatarStatus(bgCtx, owner, db.UserAvatarStatusError, gerr.Error()); serr != nil {
				h.Logger.Error("avatar regen: status flip to error failed",
					"user", owner, "err", serr)
			}
			return
		}
		if serr := h.DB.SaveUserAvatar(bgCtx, owner, imgBytes, mime, model, prompt, seed); serr != nil {
			h.Logger.Error("avatar regen: save failed", "user", owner, "err", serr)
			_ = h.DB.SetAvatarStatus(bgCtx, owner, db.UserAvatarStatusError,
				fmt.Sprintf("save failed: %v", serr))
			return
		}
		h.Logger.Info("avatar regen: saved", "user", owner, "bytes", len(imgBytes), "mime", mime)
	}()

	return c.JSON(http.StatusAccepted, map[string]any{
		"status": string(db.UserAvatarStatusRunning),
	})
}

// GetAvatarStatus: GET /api/v1/users/current/avatar/status. Cheap poll.
// Returns {status, error?, generatedAt?} — no bytes. When there's no row
// at all, returns status="none" (not "pending") so the FE renders the
// empty-state distinctly from a reserved-but-not-yet-running state.
func (h *Handler) GetAvatarStatus(c *echo.Context) error {
	_, owner, aerr := h.resolveUser(c)
	if aerr != nil {
		return respondErr(c, aerr)
	}
	info, ok, err := h.DB.GetUserAvatarStatus(c.Request().Context(), owner)
	if err != nil {
		return h.internalErr(c, "avatar status failed", err)
	}
	if !ok {
		return c.JSON(http.StatusOK, map[string]any{
			"status": "none",
		})
	}
	out := map[string]any{
		"status":    string(info.Status),
		"updatedAt": info.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if info.ErrorMessage != "" {
		out["error"] = info.ErrorMessage
	}
	if info.GeneratedAt != nil {
		out["generatedAt"] = info.GeneratedAt.UTC().Format(time.RFC3339)
	}
	return c.JSON(http.StatusOK, out)
}

// UserAvatar: GET /api/v1/users/:username/avatar (PUBLIC).
// Serves the raw ready image bytes. Status != ready → 404 so an in-flight
// render never leaks a stale byte-string to a fresh viewer. Cache-Control
// is a modest 30s (not immutable): a user re-renders iteratively during
// onboarding, and we want the new bytes to propagate promptly. The FE
// hero can still cache-bust more aggressively via ?v=<generatedAt.epoch>
// when it has the value.
func (h *Handler) UserAvatar(c *echo.Context) error {
	username := c.Param("username")
	if username == "" {
		return respondErr(c, apierr.BadRequest("missing username"))
	}
	av, ok, err := h.DB.GetUserAvatar(c.Request().Context(), username)
	if err != nil {
		return h.internalErr(c, "user avatar lookup failed", err)
	}
	if !ok || av.Status != db.UserAvatarStatusReady || len(av.ImageBytes) == 0 {
		return respondErr(c, apierr.NotFound("avatar not ready"))
	}
	c.Response().Header().Set("Cache-Control", "public, max-age=30")
	return c.Blob(http.StatusOK, av.MimeType, av.ImageBytes)
}
