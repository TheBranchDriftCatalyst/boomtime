// Package labelimages runs the ComfyUI-shim-backed label archetype image
// generation loop (gaka-myv). Two entrypoints:
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

	"github.com/TheBranchDriftCatalyst/boomtime/internal/comfyui"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/labelcatalog"
)

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
func (w *Worker) catalog() []labelcatalog.Entry {
	if w.entries != nil {
		return w.entries
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

// RegenerateOne wipes an id's existing row (if any), regenerates, saves.
// Errors from shim/DB are returned to the caller so the CLI surfaces
// non-zero exit on failure.
func (w *Worker) RegenerateOne(ctx context.Context, id string) error {
	if w == nil {
		return errors.New("labelimages: feature disabled (set BOOM_FEATURE_LABEL_IMAGES=on and BOOM_COMFYUI_SHIM_URL)")
	}
	entry, ok := labelcatalog.ByID(id)
	if !ok {
		return fmt.Errorf("labelimages: unknown label id %q (see internal/labelcatalog for the shipped set)", id)
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

// generateAndSave is the shared inner call: hit the shim, persist the
// bytes with provenance columns filled in.
func (w *Worker) generateAndSave(ctx context.Context, e labelcatalog.Entry) error {
	w.logger.Info("labelimages: generating", "id", e.ID, "model", w.model)
	bytes, mime, err := w.client.Generate(ctx, e.Prompt, w.model, nil)
	if err != nil {
		return fmt.Errorf("shim: %w", err)
	}
	if err := w.db.SaveLabelImage(ctx, e.ID, bytes, mime, w.model, e.Prompt, nil); err != nil {
		return fmt.Errorf("save: %w", err)
	}
	w.logger.Info("labelimages: saved", "id", e.ID, "bytes", len(bytes), "mime", mime)
	return nil
}
