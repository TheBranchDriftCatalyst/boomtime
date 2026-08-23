// label_images.go: `boomtime label-images regenerate` — on-demand image
// generation via the ComfyUI shim (boom-myv).
//
// Two modes:
//
//   - --id <label-id>:  regenerate one label (deletes any existing row
//     first). Useful when tuning a single prompt.
//   - --all:            wipe every row and regenerate the whole catalog
//     under the current BOOM_COMFYUI_MODEL. Useful when switching
//     pipelines (e.g. `BOOM_COMFYUI_MODEL=chroma_hd_q8 boomtime
//     label-images regenerate --all`).
//
// Requires both BOOM_FEATURE_LABEL_IMAGES=on AND BOOM_COMFYUI_SHIM_URL set
// — the CLI is the same feature-gate as the startup worker. Without those,
// the command returns a clear "feature disabled" error and exits non-zero.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	labelimages "github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/worker/labelimages"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/spf13/cobra"
)

func labelImagesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "label-images",
		Short: "Label archetype image utilities (regenerate, list)",
	}
	cmd.AddCommand(labelImagesRegenerateCmd())
	return cmd
}

func labelImagesRegenerateCmd() *cobra.Command {
	var id string
	var all bool
	cmd := &cobra.Command{
		Use:   "regenerate",
		Short: "Regenerate one (--id) or all (--all) label images via the ComfyUI shim",
		Long: `Regenerate label archetype images via the mac-sdlc-node comfyui-shim.
Requires BOTH BOOM_FEATURE_LABEL_IMAGES=on AND BOOM_COMFYUI_SHIM_URL set.
Model comes from BOOM_COMFYUI_MODEL (default sdxl_illustrious_xl).

Examples:

  # Regen one label
  boomtime label-images regenerate --id late-night-coder

  # Swap pipeline + regen the whole catalog
  BOOM_COMFYUI_MODEL=chroma_hd_q8 boomtime label-images regenerate --all`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if id == "" && !all {
				return errors.New("either --id <label-id> or --all is required")
			}
			if id != "" && all {
				return errors.New("--id and --all are mutually exclusive")
			}

			cfg := config.Load()
			if !cfg.LabelImagesEnabled() {
				return fmt.Errorf("label-images feature is disabled — set BOOM_FEATURE_LABEL_IMAGES=on AND BOOM_COMFYUI_SHIM_URL=http://host:8012")
			}

			ctx := context.Background()
			database, err := db.New(ctx, cfg.DatabaseURL())
			if err != nil {
				return fmt.Errorf("db connect: %w", err)
			}
			defer database.Close()

			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
			w, err := labelimages.NewWorker(cfg, database, logger)
			if err != nil {
				return fmt.Errorf("worker: %w", err)
			}
			if w == nil {
				// Shouldn't reach here: LabelImagesEnabled was true, so
				// NewWorker should have returned a worker. Guard anyway.
				return errors.New("worker unexpectedly nil despite enabled feature")
			}

			if id != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Regenerating label %q under model %q via %s ...\n",
					id, cfg.ComfyUIModel, cfg.ComfyUIShimURL)
				if err := w.RegenerateOne(ctx, id); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "OK — regenerated %q.\n", id)
				return nil
			}

			// --all
			fmt.Fprintf(cmd.OutOrStdout(), "Regenerating ALL labels under model %q via %s ...\n",
				cfg.ComfyUIModel, cfg.ComfyUIShimURL)
			gen, failed, err := w.RegenerateAll(ctx)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Done — generated %d, failed %d.\n", gen, failed)
			if failed > 0 {
				return fmt.Errorf("%d label(s) failed to regenerate — see log for details", failed)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "Regenerate a single label by id (e.g. late-night-coder)")
	cmd.Flags().BoolVar(&all, "all", false, "Wipe every label_images row and regenerate the whole catalog")
	// Smart completion: TAB --id to pick a label from the DB catalog.
	_ = cmd.RegisterFlagCompletionFunc("id", completeLabelIds)
	return cmd
}
