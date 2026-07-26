// Package comfyui is a thin HTTP client for the mac-sdlc-node comfyui-shim
// (gaka-myv), the OpenAI-shaped image-generation endpoint fronting a local
// ComfyUI instance. See
// /Users/panda/catalyst-devspace/workspace/mac-sdlc-node/services/comfyui-shim/README.md
// for the shim's contract.
//
// Design decisions:
//
//   - Feature-gated at construction: NewClient returns (nil, nil) — not an
//     error — when the URL is empty. That lets the wiring code stay dumb:
//     "if the URL isn't set, we simply don't have a client, so we skip the
//     feature entirely." No conditional branches in the caller for the
//     off-by-default path.
//
//   - Retry with exponential backoff (1s, 2s, 4s) on transient 5xx or a
//     network error. Respects context cancellation between attempts so a
//     shutdown doesn't have to wait out the last backoff.
//
//   - Response-header timeout is 300s per attempt; overall client timeout
//     360s. SDXL Illustrious rounds trip in ~25s, but Chroma-HD (full
//     precision, max-quality FLUX-derivative on M-series GPUs) can spend
//     >60s in front of the pipeline before the shim even flushes its
//     first byte. The initial 60s/90s pair we shipped worked for SDXL
//     but caused chroma-hd to time out at "awaiting response headers" on
//     every attempt (4 retries × 60s = ~4 min of wasted work per label).
//     Fail-fast against an unreachable shim is preserved via a 5s
//     connection dial in the transport — the response wait only bounds
//     time-in-generation.
//
//   - We return image bytes + mime type. Both `b64_json` and `data:` URL
//     responses are handled — the shim defaults to b64_json, but a future
//     shim config change to `url` (data URLs) won't break the client.
//
// The shim's OpenAI-shaped response is:
//
//	{ "created": 123, "data": [ {"b64_json": "..."} | {"url": "data:image/png;base64,..."} ] }
package comfyui

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Client is the shim caller. `URL` is the base (e.g. http://localhost:8012).
// `HTTP` is exported so tests can swap in an httptest.Server round-tripper.
type Client struct {
	URL  string
	HTTP *http.Client
}

// NewClient returns a client for `url`, or (nil, nil) when url is empty so
// callers can treat "no url" and "feature disabled" as the same state. On a
// malformed URL (missing scheme) we return an error — the operator wants a
// loud failure at boot rather than a silent no-op.
func NewClient(url string) (*Client, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return nil, nil
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("comfyui: BOOM_COMFYUI_SHIM_URL %q missing http:// or https:// scheme", url)
	}
	url = strings.TrimRight(url, "/")

	// Per-attempt 60s ceiling — SDXL Illustrious on M-series generates ~15-30s.
	// A short 5s DIAL timeout ensures we fail fast if the shim isn't listening,
	// but ONE generation call can still legitimately take a while.
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ResponseHeaderTimeout: 600 * time.Second,
	}
	return &Client{
		URL:  url,
		HTTP: &http.Client{Timeout: 720 * time.Second, Transport: transport},
	}, nil
}

// Healthz probes GET /healthz. Used at boot / by the CLI to fail loud when
// the shim URL is misconfigured. Returns nil on 200.
func (c *Client) Healthz(ctx context.Context) error {
	if c == nil {
		return errors.New("comfyui: client is nil (feature disabled)")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("comfyui healthz: %d %s: %s", resp.StatusCode, resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

type generateRequest struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	N              int    `json:"n,omitempty"`
	Size           string `json:"size,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"`
	Seed           *int64 `json:"seed,omitempty"`
}

type generateResponse struct {
	Created int64 `json:"created"`
	Data    []struct {
		B64JSON string `json:"b64_json,omitempty"`
		URL     string `json:"url,omitempty"`
	} `json:"data"`
}

// Generate calls POST /v1/images/generations. `model` is the shim pipeline
// name (e.g. "sdxl_illustrious_xl" — the shim accepts either the underscored
// or the aliased name). `seed` may be nil for a random-per-call generation.
//
// Retries transient failures (network / 5xx) with 1s, 2s, 4s backoff before
// giving up. On non-retryable errors (4xx) returns immediately. Respects ctx
// cancellation between attempts.
//
// Returns raw image bytes + mime type. Both b64_json and data-URL responses
// are handled.
func (c *Client) Generate(ctx context.Context, prompt, model string, seed *int64) (data []byte, mime string, err error) {
	if c == nil {
		return nil, "", errors.New("comfyui: client is nil (feature disabled)")
	}
	if strings.TrimSpace(prompt) == "" {
		return nil, "", errors.New("comfyui: empty prompt")
	}
	if strings.TrimSpace(model) == "" {
		return nil, "", errors.New("comfyui: empty model")
	}

	req := generateRequest{
		Model:          model,
		Prompt:         prompt,
		Size:           "1024x1024",
		ResponseFormat: "b64_json",
		Seed:           seed,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, "", err
	}

	backoffs := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}
	var lastErr error
	for attempt := 0; attempt < len(backoffs)+1; attempt++ {
		if attempt > 0 {
			// Sleep between retries; ctx cancellation short-circuits.
			select {
			case <-ctx.Done():
				return nil, "", ctx.Err()
			case <-time.After(backoffs[attempt-1]):
			}
		}
		data, mime, err = c.doOne(ctx, body)
		if err == nil {
			return data, mime, nil
		}
		// Non-retryable? Bail out.
		var re *retryable
		if !errors.As(err, &re) {
			return nil, "", err
		}
		lastErr = err
	}
	return nil, "", fmt.Errorf("comfyui: gave up after %d attempts: %w", len(backoffs)+1, lastErr)
}

// retryable wraps errors that should trigger a backoff+retry loop
// (network glitches, 5xx from the shim).
type retryable struct{ error }

func (c *Client) doOne(ctx context.Context, body []byte) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.URL+"/v1/images/generations", bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		// Network/connect errors are always retryable — the shim may be
		// temporarily unreachable during a launchd restart / brief blip.
		return nil, "", &retryable{err}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, "", &retryable{
			fmt.Errorf("comfyui: %d %s: %s", resp.StatusCode, resp.Status, strings.TrimSpace(string(msg))),
		}
	}
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, "", fmt.Errorf("comfyui: %d %s: %s", resp.StatusCode, resp.Status, strings.TrimSpace(string(msg)))
	}

	var gr generateResponse
	if err := json.NewDecoder(resp.Body).Decode(&gr); err != nil {
		return nil, "", fmt.Errorf("comfyui: decode response: %w", err)
	}
	if len(gr.Data) == 0 {
		return nil, "", errors.New("comfyui: no data in response")
	}
	item := gr.Data[0]

	if item.B64JSON != "" {
		raw, err := base64.StdEncoding.DecodeString(item.B64JSON)
		if err != nil {
			return nil, "", fmt.Errorf("comfyui: b64 decode: %w", err)
		}
		return raw, sniffMime(raw), nil
	}
	if item.URL != "" {
		// Handle a "data:image/png;base64,..." URL.
		const prefix = "data:"
		if !strings.HasPrefix(item.URL, prefix) {
			return nil, "", fmt.Errorf("comfyui: unsupported URL scheme in response: %.30s", item.URL)
		}
		comma := strings.IndexByte(item.URL, ',')
		if comma < 0 {
			return nil, "", errors.New("comfyui: malformed data URL")
		}
		meta := item.URL[len(prefix):comma]
		payload := item.URL[comma+1:]
		mime := "image/png"
		if idx := strings.Index(meta, ";"); idx >= 0 {
			mime = meta[:idx]
		} else if meta != "" {
			mime = meta
		}
		raw, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return nil, "", fmt.Errorf("comfyui: data URL b64 decode: %w", err)
		}
		return raw, mime, nil
	}
	return nil, "", errors.New("comfyui: response item has neither b64_json nor url")
}

// sniffMime picks a mime type from the first bytes of a PNG/JPEG/WEBP payload.
// Falls back to image/png (the shim's default output today).
func sniffMime(b []byte) string {
	switch {
	case len(b) >= 8 && bytes.Equal(b[:8], []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}):
		return "image/png"
	case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return "image/jpeg"
	case len(b) >= 12 && bytes.Equal(b[:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")):
		return "image/webp"
	}
	return "image/png"
}
