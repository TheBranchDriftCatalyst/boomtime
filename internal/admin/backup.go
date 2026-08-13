package admin

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/labstack/echo/v5"
)

// backup.go: whole-database Save/Load ("backup" download + destructive
// upload/restore) surfaced on the Heartbeats page.

// restoreConfirmValue must be passed as ?confirm=<value> on the import route.
// It exists purely so a stray curl/fetch can never wipe a database by accident;
// the UI supplies it after its own typed-REPLACE confirmation.
const restoreConfirmValue = "replace-all-data"

// backupMu single-flights dump/restore: the restore TRUNCATEs every table, so
// a concurrent export (or second restore) must be rejected, not interleaved.
var backupMu sync.Mutex

// restoreMaxBytes caps the uploaded archive size (spooled to a temp file).
// Default 4 GiB; tune with BOOM_RESTORE_MAX_BYTES.
func restoreMaxBytes() int64 {
	if v := os.Getenv("BOOM_RESTORE_MAX_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return 4 << 30
}

// DBExport: GET /api/v1/users/current/db/export — stream a full logical dump
// of the ENTIRE database (all users' data, password hashes, tokens, settings)
// as a ZIP attachment. Auth'd like every other route; the payload is
// inherently whole-DB on this single-tenant server.
func (h *Handler) DBExport(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	if !backupMu.TryLock() {
		return apihelpers.RespondErr(c, apierr.New(http.StatusConflict, "another backup or restore is in progress", nil))
	}
	defer backupMu.Unlock()

	filename := "boomtime-backup-" + time.Now().UTC().Format("20060102-150405") + ".zip"
	c.Response().Header().Set(echo.HeaderContentType, "application/zip")
	c.Response().Header().Set(echo.HeaderContentDisposition, `attachment; filename=`+filename)

	if err := h.DB.DumpAll(c.Request().Context(), c.Response()); err != nil {
		// Headers/body may already be partially written; the truncated zip
		// will fail client-side validation. Log with context.
		h.Logger.Error("db export failed", "owner", owner, "err", err)
		return err
	}
	h.Logger.Info("database exported", "owner", owner)
	return nil
}

// DBImport: POST /api/v1/users/current/db/import?confirm=replace-all-data —
// upload a backup archive and REPLACE the entire application state with it.
// Triple-guarded: server-side confirm param, client typed-REPLACE modal, and
// the single-flight mutex + running-import rejection. The archive is spooled
// and fully validated before anything is truncated; the restore itself is one
// transaction. Afterwards the derived tables are rebuilt per sender and every
// cached aggregate is dropped.
func (h *Handler) DBImport(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwner(h.DB, c)
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	if c.QueryParam("confirm") != restoreConfirmValue {
		return apihelpers.RespondErr(c, apierr.BadRequest("missing confirm=replace-all-data — this endpoint erases the entire database"))
	}
	if !backupMu.TryLock() {
		return apihelpers.RespondErr(c, apierr.New(http.StatusConflict, "another backup or restore is in progress", nil))
	}
	defer backupMu.Unlock()

	ctx := c.Request().Context()
	if active, err := h.DB.HasActiveImportJobs(ctx); err != nil {
		return apihelpers.InternalErr(h.Logger, c, "restore: active-import check failed", err)
	} else if active {
		return apihelpers.RespondErr(c, apierr.New(http.StatusConflict, "an import job is running — wait for it to finish or cancel it first", nil))
	}

	// Spool the upload to a temp file: zip.NewReader needs a ReaderAt+size,
	// and full validation must happen before any destructive step.
	tmp, err := os.CreateTemp("", "boomtime-restore-*.zip")
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "restore: temp file failed", err)
	}
	defer func() {
		tmp.Close()
		os.Remove(tmp.Name())
	}()
	body := http.MaxBytesReader(c.Response(), c.Request().Body, restoreMaxBytes())
	size, err := io.Copy(tmp, body)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return apihelpers.RespondErr(c, apierr.New(http.StatusRequestEntityTooLarge,
				fmt.Sprintf("backup exceeds the %d-byte upload limit (BOOM_RESTORE_MAX_BYTES)", tooBig.Limit), nil))
		}
		return apihelpers.InternalErr(h.Logger, c, "restore: reading upload failed", err)
	}
	zr, err := zip.NewReader(tmp, size)
	if err != nil {
		return apihelpers.RespondErr(c, apierr.BadRequest("uploaded file is not a valid backup archive (zip)"))
	}

	summary, err := h.DB.RestoreAll(ctx, zr)
	if err != nil {
		var verr *db.RestoreValidationError
		if errors.As(err, &verr) {
			return apihelpers.RespondErr(c, apierr.BadRequest(verr.Msg))
		}
		var sverr *db.RestoreVersionError
		if errors.As(err, &sverr) {
			return apihelpers.RespondErr(c, apierr.New(http.StatusConflict, sverr.Error(), nil))
		}
		return apihelpers.InternalErr(h.Logger, c, "restore failed", err)
	}

	// Safety-net rebuild of gap_seconds + the rollup for every restored sender
	// (the dump carries them, but a rebuild guarantees consistency), then drop
	// ALL cached aggregates — every cache key starts with "<owner>|", so the
	// empty prefix clears everything.
	senders, err := h.DB.Senders(ctx)
	if err != nil {
		return apihelpers.InternalErr(h.Logger, c, "restore: listing senders failed", err)
	}
	for _, s := range senders {
		if err := h.DB.ResyncDerived(ctx, s); err != nil {
			return apihelpers.InternalErr(h.Logger, c, "restore: derived resync failed", err)
		}
	}
	if h.Cache != nil {
		h.Cache.InvalidatePrefix("")
	}

	h.Logger.Info("database restored from backup",
		"requested_by", owner, "rows", summary.TotalRows, "gooseVersion", summary.GooseVersion)
	return c.JSON(http.StatusOK, summary)
}
