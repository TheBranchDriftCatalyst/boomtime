// Package labelimages runs the ComfyUI-shim-backed label archetype image
// generation loop (boom-myv). Two entrypoints:
//
//   - Run(ctx): iterate the shipped catalog, generate + save any label that
//     doesn't already have a row in label_images. Called from `boomtime run`
//     after migrations complete, in a detached goroutine so server startup
//     is never blocked on image generation.
//
//   - RegenerateOne / RegenerateAll: on-demand paths for the
//     `boomtime label-images regenerate` CLI subcommand — same generation
//     logic, but deletes existing rows first so the model/prompt/seed
//     provenance columns reflect the fresh call. `regenerate --all` blows
//     away every row up front to guarantee an operator-visible clean-slate.
//
// Feature gate: NewWorker returns nil when the feature is off (missing flag
// OR missing shim URL). Every entrypoint on a nil receiver is a graceful
// no-op so callers don't need conditional branches.
package labelimages

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/comfyui"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/labelcatalog"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

// systemPromptCacheTTL bounds how often the worker re-reads the singleton
// label_gen_config row during a regen loop. Short so an admin edit is
// visible on the next regen batch, long enough that a burst of per-label
// regens doesn't re-SELECT the config on every entry.
const systemPromptCacheTTL = 30 * time.Second

// Worker is the label-image generation runner. Fields are unexported so
// tests must go through the constructor + methods.
type Worker struct {
	db     *db.DB
	client *comfyui.Client
	model  string
	logger *slog.Logger
	// entries is the catalog snapshot the worker iterates. Injectable so
	// unit tests can pass a small fake set without loading the full
	// shipped catalog. Nil = use labelcatalog.Entries.
	entries []labelcatalog.Entry

	// systemPrompt cache — populated on first call, refreshed every
	// systemPromptCacheTTL. Guarded by sysMu so parallel calls to
	// generateAndSave don't racy-read. When the DB read fails the worker
	// falls back to "" (no prefix) rather than aborting the whole
	// generation.
	sysMu      sync.Mutex
	sysPrompt  string
	sysFetched time.Time
}

// NewWorker constructs a worker when the feature is on (both flag AND URL).
// Returns nil, nil when off — feature-off is not an error. On a config
// problem (URL set but malformed) returns an error so `boomtime run` can
// exit loud.
func NewWorker(cfg *config.Config, database *db.DB, logger *slog.Logger) (*Worker, error) {
	if !cfg.LabelImagesEnabled() {
		return nil, nil
	}
	client, err := comfyui.NewClient(cfg.ComfyUIShimURL)
	if err != nil {
		return nil, fmt.Errorf("labelimages: %w", err)
	}
	if client == nil {
		// Belt-and-braces: LabelImagesEnabled already gated the empty-URL
		// case, but defend against a future config change.
		return nil, nil
	}
	return &Worker{
		db:     database,
		client: client,
		model:  cfg.ComfyUIModel,
		logger: logger,
	}, nil
}

// newWorkerForTest builds a worker with an injected catalog + client. Not
// exported — tests inside this package use it via label_images_test.go.
func newWorkerForTest(database *db.DB, client *comfyui.Client, model string, logger *slog.Logger, entries []labelcatalog.Entry) *Worker {
	return &Worker{db: database, client: client, model: model, logger: logger, entries: entries}
}

// catalog returns the entries this worker should iterate over.
//
// Order of preference:
//  1. Explicit `entries` field (tests inject via newWorkerForTest so the
//     unit suite doesn't need a live labels table).
//  2. DB-backed labels catalog (post-boom-364.3 — the new source of truth).
//     Every row with a non-empty optimized_prompt becomes an entry.
//  3. Fall back to the compiled-in labelcatalog.Entries baseline if the DB
//     read fails or the table is empty (the old pre-pivot behavior). This
//     keeps the worker functional on a brand-new DB where migrations
//     haven't fully applied yet.
func (w *Worker) catalog() []labelcatalog.Entry {
	if w.entries != nil {
		return w.entries
	}
	if w.db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		rows, err := w.db.ListLabels(ctx)
		if err != nil {
			w.logger.Warn("labelimages: DB catalog read failed, falling back to compiled baseline",
				"err", err, "baseline_count", len(labelcatalog.Entries))
			return labelcatalog.Entries
		}
		out := make([]labelcatalog.Entry, 0, len(rows))
		for _, r := range rows {
			if r.OptimizedPrompt == "" {
				// No prompt = no image to generate. Skip silently so tier
				// labels (which don't ship images today) don't spam the
				// log at every regen tick.
				continue
			}
			out = append(out, labelcatalog.Entry{
				ID:          r.ID,
				Description: r.Description,
				Prompt:      r.OptimizedPrompt,
			})
		}
		if len(out) > 0 {
			return out
		}
		w.logger.Warn("labelimages: DB catalog empty, falling back to compiled baseline",
			"baseline_count", len(labelcatalog.Entries))
	}
	return labelcatalog.Entries
}

// Run generates + saves images for every catalog entry that isn't already
// in the DB. Sequential + non-blocking wrt the caller (invoke it in a
// goroutine). Errors on individual labels are logged and the loop
// continues — a single failing prompt should not stop the whole batch.
func (w *Worker) Run(ctx context.Context) {
	if w == nil {
		return
	}
	entries := w.catalog()
	w.logger.Info("labelimages: starting run", "candidates", len(entries), "model", w.model)
	var generated, skipped, failed int
	for _, e := range entries {
		if ctx.Err() != nil {
			w.logger.Warn("labelimages: run cancelled", "err", ctx.Err())
			return
		}
		has, err := w.db.HasLabelImage(ctx, e.ID)
		if err != nil {
			w.logger.Error("labelimages: has-check failed", "id", e.ID, "err", err)
			failed++
			continue
		}
		if has {
			skipped++
			continue
		}
		if err := w.generateAndSave(ctx, e); err != nil {
			w.logger.Error("labelimages: generation failed", "id", e.ID, "err", err)
			failed++
			continue
		}
		generated++
	}
	w.logger.Info("labelimages: run complete", "generated", generated, "skipped_existing", skipped, "failed", failed)
}

// RegenerateEntry is the exported single-call generate+save path used by the
// imagejobs pool Executor (boom-8bz). Unlike RegenerateOne it takes the fully-
// resolved catalog Entry (id + prompt + optional model/size/seed overrides)
// instead of looking it up from the DB — the imagejobs.Registry already holds
// exactly those fields when the operator submits a regen, so the caller
// should supply them directly. Delete-before-save keeps parity with
// RegenerateOne (an operator hitting Regen expects a fresh row, not a UPSERT
// that leaves the old row_id in place).
//
// Returns nil on success or a wrapped error on shim/DB failure. Ctx cancel
// propagates to the shim call (comfyui.Client.Generate honors ctx).
func (w *Worker) RegenerateEntry(ctx context.Context, e labelcatalog.Entry) error {
	if w == nil {
		return errors.New("labelimages: feature disabled")
	}
	if e.ID == "" || e.Prompt == "" {
		return fmt.Errorf("labelimages: RegenerateEntry requires non-empty ID + Prompt (got id=%q)", e.ID)
	}
	if err := w.db.DeleteLabelImage(ctx, e.ID); err != nil {
		return fmt.Errorf("labelimages: delete old row: %w", err)
	}
	return w.generateAndSave(ctx, e)
}

// RegenerateOne wipes an id's existing row (if any), regenerates, saves.
// Errors from shim/DB are returned to the caller so the CLI surfaces
// non-zero exit on failure.
//
// Post boom-364.3 the DB is the source of truth: look up id → prompt
// there first, fall back to the compiled labelcatalog baseline so the
// CLI still works on a partially-migrated dev DB.
func (w *Worker) RegenerateOne(ctx context.Context, id string) error {
	if w == nil {
		return errors.New("labelimages: feature disabled (set BOOM_FEATURE_LABEL_IMAGES=on and BOOM_COMFYUI_SHIM_URL)")
	}
	var entry labelcatalog.Entry
	if w.db != nil {
		if row, err := w.db.GetLabel(ctx, id); err == nil && row != nil {
			if row.OptimizedPrompt == "" {
				return fmt.Errorf("labelimages: label %q has no optimized_prompt — nothing to generate", id)
			}
			entry = labelcatalog.Entry{ID: row.ID, Description: row.Description, Prompt: row.OptimizedPrompt}
		}
	}
	if entry.ID == "" {
		fallback, ok := labelcatalog.ByID(id)
		if !ok {
			return fmt.Errorf("labelimages: unknown label id %q (not in DB catalog, not in compiled baseline)", id)
		}
		entry = fallback
	}
	if err := w.db.DeleteLabelImage(ctx, id); err != nil {
		return fmt.Errorf("labelimages: delete old row: %w", err)
	}
	return w.generateAndSave(ctx, entry)
}

// RegenerateAll wipes every row up front, then generates + saves everything
// in the current catalog. Sequential; a per-label failure is logged and
// the loop continues so a single flake doesn't derail a fresh rebuild.
// Returns (generated, failed, error).
func (w *Worker) RegenerateAll(ctx context.Context) (int, int, error) {
	if w == nil {
		return 0, 0, errors.New("labelimages: feature disabled")
	}
	if err := w.db.TruncateLabelImages(ctx); err != nil {
		return 0, 0, fmt.Errorf("labelimages: truncate: %w", err)
	}
	return w.RegenerateList(ctx, w.catalog())
}

// RegenerateList generates + saves images for a caller-supplied list of
// entries. Does NOT truncate — the caller decides whether to wipe first.
// Used by the admin regen endpoint so the FE can pass the FULL catalog
// (baseline + memecore + kawaii + space-marine + whatever else the TS
// side ships) without the Go baseline catalog needing to mirror it.
func (w *Worker) RegenerateList(ctx context.Context, entries []labelcatalog.Entry) (int, int, error) {
	if w == nil {
		return 0, 0, errors.New("labelimages: feature disabled")
	}
	var generated, failed int
	for _, e := range entries {
		if ctx.Err() != nil {
			return generated, failed, ctx.Err()
		}
		if e.ID == "" || e.Prompt == "" {
			w.logger.Warn("labelimages: skipping entry with empty id or prompt", "id", e.ID)
			failed++
			continue
		}
		if err := w.generateAndSave(ctx, e); err != nil {
			w.logger.Error("labelimages: entry failed", "id", e.ID, "err", err)
			failed++
			continue
		}
		generated++
	}
	return generated, failed, nil
}

// systemPrompt returns the current global generation prefix, cached for
// systemPromptCacheTTL. On DB error we log + return "" so a transient DB
// blip doesn't fail every regen — the worker still ships a valid (but
// unprefixed) prompt to comfyui.
func (w *Worker) systemPrompt(ctx context.Context) string {
	w.sysMu.Lock()
	defer w.sysMu.Unlock()
	if time.Since(w.sysFetched) < systemPromptCacheTTL {
		return w.sysPrompt
	}
	sp, err := w.db.GetGenConfig(ctx)
	if err != nil {
		w.logger.Warn("labelimages: gen-config read failed, falling back to no system prompt",
			"err", err)
		sp = ""
	}
	w.sysPrompt = sp
	w.sysFetched = time.Now()
	return sp
}

// buildFinalPrompt joins the systemPrompt (style), the label description
// (narrative), and the per-label optimizedPrompt (scene) into one SDXL
// tag-list. Composition order is deliberate: diffusion models weight
// left-to-right, so style-first sets the aesthetic, then the narrative
// establishes WHO / WHAT the character is, then the scene composition
// lands the specific visual layout on top.
//
// Empty parts are dropped so the join stays clean — an operator with
// a blank systemPrompt still gets `${description}, ${entryPrompt}`, and
// a label without a description falls back to the pre-boom-8bz
// `${systemPrompt}, ${entryPrompt}` shape.
func buildFinalPrompt(systemPrompt, description, entryPrompt string) string {
	parts := make([]string, 0, 3)
	for _, s := range []string{systemPrompt, description, entryPrompt} {
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return strings.Join(parts, ", ")
}

// generateAndSave is the shared inner call: hit the shim, persist the
// bytes with provenance columns filled in.
//
// Per-entry overrides (e.Model / e.Size / e.Seed) take precedence over
// the worker's env-configured defaults so the Admin tab's per-label
// editor can iterate on prompts + pipelines + seeds without changing
// prod config. The systemPrompt from label_gen_config is prepended
// as an SDXL-style tag prefix (see buildFinalPrompt).
func (w *Worker) generateAndSave(ctx context.Context, e labelcatalog.Entry) error {
	model := e.Model
	if model == "" {
		model = w.model
	}
	sysPrompt := w.systemPrompt(ctx)
	finalPrompt := buildFinalPrompt(sysPrompt, e.Description, e.Prompt)
	w.logger.Info("labelimages: generating",
		"id", e.ID, "model", model, "size", e.Size,
		"seed_set", e.Seed != nil,
		"sys_prefix_len", len(strings.TrimSpace(sysPrompt)),
		"desc_len", len(strings.TrimSpace(e.Description)),
		"final_prompt_len", len(finalPrompt))
	bytes, mime, err := w.client.Generate(ctx, finalPrompt, model, e.Size, e.Seed)
	if err != nil {
		return fmt.Errorf("shim: %w", err)
	}
	// Persist the FINAL rendered prompt (system + per-label) so the row's
	// provenance is self-contained — you can reproduce the image later
	// with just the row, no separate systemPrompt lookup needed.
	if err := w.db.SaveLabelImage(ctx, e.ID, bytes, mime, model, finalPrompt, e.Seed); err != nil {
		return fmt.Errorf("save: %w", err)
	}
	w.logger.Info("labelimages: saved", "id", e.ID, "bytes", len(bytes), "mime", mime)
	return nil
}
