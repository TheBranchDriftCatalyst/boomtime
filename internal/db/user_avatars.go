// user_avatars.go (gaka-9v4): DB accessors for the per-user AI-generated
// CHIBI portrait. One row per user, PK on username, lazy-populated by the
// Settings > Avatar tab. Read path serves the raw bytes; write path is
// two-phase (SetAvatarStatus running → SaveUserAvatar ready) so a long-
// running shim call is visible to the FE as a status column poll rather
// than a wedged HTTP request.
//
// Mirrors internal/db/label_images.go for shape and naming, but with a
// status column and nullable bytes because the shim call runs async
// server-side (5-25 min on chroma-hd).
package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// UserAvatarStatus is the string enum stored in user_avatars.status.
// The DB CHECK constraint pins the allowed values; this Go alias exists
// so callers stop passing free-form strings.
type UserAvatarStatus string

const (
	UserAvatarStatusPending UserAvatarStatus = "pending"
	UserAvatarStatusRunning UserAvatarStatus = "running"
	UserAvatarStatusReady   UserAvatarStatus = "ready"
	UserAvatarStatusError   UserAvatarStatus = "error"
)

// UserAvatar is the row shape read back by the public serve endpoint and
// by the status endpoint. Bytes are omitted from status-only reads via
// GetUserAvatarStatus below to avoid shipping a MB blob down the wire on
// every poll.
type UserAvatar struct {
	Username     string
	ImageBytes   []byte
	MimeType     string
	Prompt       string
	Model        string
	Seed         *int64
	Status       UserAvatarStatus
	ErrorMessage string
	GeneratedAt  *time.Time
	UpdatedAt    time.Time
}

// UserAvatarStatusInfo is the compact row returned to the FE's polling
// GET /api/v1/users/current/avatar/status endpoint — everything the FE
// needs to render the RENDER / SYNTHESIZING / DONE tri-state without
// shipping any image bytes.
type UserAvatarStatusInfo struct {
	Status       UserAvatarStatus
	ErrorMessage string
	GeneratedAt  *time.Time
	UpdatedAt    time.Time
}

// SaveUserAvatar upserts the ready image bytes + provenance for `username`.
// Flips status to 'ready' unconditionally — this is the terminal success
// write, called from the goroutine that finished the shim call. `seed` may
// be nil when the shim response omits a resolved seed. Empty mime defaults
// to image/png (also the column default).
func (d *DB) SaveUserAvatar(ctx context.Context, username string, bytes []byte, mime, model, prompt string, seed *int64) error {
	if username == "" {
		return errors.New("SaveUserAvatar: empty username")
	}
	if len(bytes) == 0 {
		return errors.New("SaveUserAvatar: empty bytes")
	}
	if mime == "" {
		mime = "image/png"
	}
	_, err := d.Pool.Exec(ctx, `
		INSERT INTO user_avatars
			(username, image_bytes, mime_type, model, prompt, seed,
			 status, error_message, generated_at, updated_at)
		VALUES
			($1, $2, $3, NULLIF($4,''), NULLIF($5,''), $6,
			 'ready', NULL, now(), now())
		ON CONFLICT (username) DO UPDATE
		   SET image_bytes   = EXCLUDED.image_bytes,
		       mime_type     = EXCLUDED.mime_type,
		       model         = EXCLUDED.model,
		       prompt        = EXCLUDED.prompt,
		       seed          = EXCLUDED.seed,
		       status        = 'ready',
		       error_message = NULL,
		       generated_at  = now(),
		       updated_at    = now()`,
		username, bytes, mime, model, prompt, seed)
	return err
}

// SetAvatarStatus is the light-touch status writer used by the async
// worker: mark the row 'running' before the shim call, or 'error' + a
// message when the shim / DB fails partway through. Never touches the
// image_bytes column so the previous ready-image survives a failed
// re-render attempt (better UX than "click regen and lose the old one
// while the new one might fail").
//
// Upserts so the first-ever call for a user creates the row.
func (d *DB) SetAvatarStatus(ctx context.Context, username string, status UserAvatarStatus, errMsg string) error {
	if username == "" {
		return errors.New("SetAvatarStatus: empty username")
	}
	switch status {
	case UserAvatarStatusPending, UserAvatarStatusRunning, UserAvatarStatusReady, UserAvatarStatusError:
		// ok
	default:
		return errors.New("SetAvatarStatus: unknown status")
	}
	// NULLIF empty errMsg so a status transition into 'running' clears any
	// prior 'error' message without the caller having to remember to.
	_, err := d.Pool.Exec(ctx, `
		INSERT INTO user_avatars (username, status, error_message, updated_at)
		VALUES ($1, $2, NULLIF($3,''), now())
		ON CONFLICT (username) DO UPDATE
		   SET status        = EXCLUDED.status,
		       error_message = EXCLUDED.error_message,
		       updated_at    = now()`,
		username, string(status), errMsg)
	return err
}

// GetUserAvatar reads the full row (including bytes). Returns (nil, false,
// nil) when the user has no avatar row at all so the handler can 404
// without an internal-error branch. Callers serving the image should also
// treat status != ready as "not found" to avoid streaming a stale blob
// from a re-render that hasn't landed yet.
func (d *DB) GetUserAvatar(ctx context.Context, username string) (*UserAvatar, bool, error) {
	if username == "" {
		return nil, false, errors.New("GetUserAvatar: empty username")
	}
	row := d.Pool.QueryRow(ctx, `
		SELECT username, image_bytes, mime_type,
		       COALESCE(prompt,''), COALESCE(model,''), seed,
		       status, COALESCE(error_message,''),
		       generated_at, updated_at
		  FROM user_avatars
		 WHERE username = $1`, username)
	var a UserAvatar
	var status string
	if err := row.Scan(&a.Username, &a.ImageBytes, &a.MimeType,
		&a.Prompt, &a.Model, &a.Seed,
		&status, &a.ErrorMessage,
		&a.GeneratedAt, &a.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	a.Status = UserAvatarStatus(status)
	return &a, true, nil
}

// GetUserAvatarStatus is the bytes-free companion the FE uses to poll
// state — status column + generated_at + last-error only. Absent row →
// (nil, false, nil) so the FE can render an empty-state without a 404
// masquerading as an error.
func (d *DB) GetUserAvatarStatus(ctx context.Context, username string) (*UserAvatarStatusInfo, bool, error) {
	if username == "" {
		return nil, false, errors.New("GetUserAvatarStatus: empty username")
	}
	row := d.Pool.QueryRow(ctx, `
		SELECT status, COALESCE(error_message,''), generated_at, updated_at
		  FROM user_avatars
		 WHERE username = $1`, username)
	var info UserAvatarStatusInfo
	var status string
	if err := row.Scan(&status, &info.ErrorMessage, &info.GeneratedAt, &info.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	info.Status = UserAvatarStatus(status)
	return &info, true, nil
}
