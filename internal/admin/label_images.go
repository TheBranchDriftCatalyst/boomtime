// label_images.go: GET /api/v1/labels/:id/image — the PUBLIC image bytes
// endpoint (gaka-myv). Mirrors the WidgetSvg shape (see widgets.go):
// resolve id, serve bytes with an aggressive Cache-Control, 404 when
// absent.
//
// Cache-Control: `public, max-age=31536000, immutable` for one year. That
// is aggressive on purpose — label images are content-fixed (regenerating
// changes the bytes but the FE appends ?v=<generated_at.epoch> to the
// <img src>, so the URL changes and the cache is naturally busted). The
// endpoint deliberately IGNORES the ?v param — it's a browser cache-bust
// only, not a routing parameter.
//
// Feature-gate note: reads do NOT check config.LabelImagesEnabled(). Once
// a row is in the DB, it stays servable even if the operator later toggles
// the feature off — that's the safer default (no sudden 404s for users
// whose profiles are already showing images). The gate only guards writes
// (the startup worker + the CLI regenerate command).
package admin

import (
	"net/http"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/labstack/echo/v5"
)

// LabelImage: GET /api/v1/labels/:id/image (PUBLIC).
// Serves the raw image bytes for a shared label archetype (see
// internal/labelcatalog for the shipped set). 404 when no row.
func (h *Handler) LabelImage(c *echo.Context) error {
	id := c.Param("id")
	if id == "" {
		return apihelpers.RespondErr(c, apierr.BadRequest("missing label id"))
	}
	li, ok, err := h.DB.GetLabelImage(c.Request().Context(), id)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "label image lookup failed", err)
	}
	if !ok {
		return apihelpers.RespondErr(c, apierr.NotFound("label image not found"))
	}
	// One year, immutable — safe because the FE cache-busts via
	// ?v=<generated_at.epoch> when a regenerated row bumps the timestamp.
	c.Response().Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	return c.Blob(http.StatusOK, li.MimeType, li.ImageBytes)
}
