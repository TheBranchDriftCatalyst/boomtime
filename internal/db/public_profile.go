// public_profile.go: DB accessors for the opt-in public read-only profile
// (gaka-6jm.1).
//
// Storage lives on the `users` table (migrations/00025): a boolean
// `public_profile_enabled` and a nullable `public_slug`. A partial UNIQUE
// index on public_slug enforces global slug uniqueness only for non-null
// values, so many users can coexist with slug=NULL.
//
// This layer intentionally does NOT decide policy (slug format, reserved
// names, "profile is enabled" gating on the public route). Those live in
// internal/handler/profile.go so the DB accessors stay dumb enough to reuse
// from admin tooling / tests.
package db

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// GetPublicProfile returns the profile settings for a user. If the user
// exists but has no slug, slug is nil; enabled reflects the raw column
// value. If the user does not exist, returns (false, nil, nil) — callers
// distinguish "no user" from "no row" via a separate GetUserByName probe if
// needed. Callers should treat (enabled=true, slug=nil) as invalid state
// (the handler prevents saving it in the first place).
func (d *DB) GetPublicProfile(ctx context.Context, username string) (bool, *string, error) {
	row := d.Pool.QueryRow(ctx,
		`SELECT public_profile_enabled, public_slug FROM users WHERE username = $1`,
		username,
	)
	var enabled bool
	var slug *string
	if err := row.Scan(&enabled, &slug); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil, nil
		}
		return false, nil, err
	}
	return enabled, slug, nil
}

// SetPublicProfile upserts the enabled/slug pair for the caller. When
// enabled=false the caller may pass an empty slug and the existing slug is
// left untouched (so toggling off + back on retains the same slug). When
// enabled=true the slug is required and written verbatim; the caller is
// responsible for validating format and reserved-name policy before
// calling. A UNIQUE constraint violation on public_slug bubbles up as a
// normal *pgconn.PgError with Code == "23505" — handlers translate that
// to 409 Conflict.
func (d *DB) SetPublicProfile(ctx context.Context, username string, enabled bool, slug string) error {
	if username == "" {
		return errors.New("SetPublicProfile: empty username")
	}
	if !enabled && slug == "" {
		// Off + no slug supplied: leave the slug column alone.
		tag, err := d.Pool.Exec(ctx,
			`UPDATE users
			    SET public_profile_enabled = $2
			  WHERE username = $1`,
			username, enabled,
		)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return nil
	}
	// Either enabling (slug required + written) or off-with-new-slug (also
	// written). Slug is stored verbatim; caller validated format.
	tag, err := d.Pool.Exec(ctx,
		`UPDATE users
		    SET public_profile_enabled = $2,
		        public_slug            = $3
		  WHERE username = $1`,
		username, enabled, slug,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// LookupUsernameBySlug returns the username currently owning slug. Returns
// pgx.ErrNoRows when no row has this slug (the public handler translates
// that into 404). Does NOT check public_profile_enabled — that check lives
// at the handler level so a "slug exists but disabled" case can be
// distinguished from "slug never existed" in logs / metrics if we ever
// want it.
func (d *DB) LookupUsernameBySlug(ctx context.Context, slug string) (string, error) {
	if slug == "" {
		return "", pgx.ErrNoRows
	}
	row := d.Pool.QueryRow(ctx,
		`SELECT username FROM users WHERE public_slug = $1`,
		slug,
	)
	var username string
	if err := row.Scan(&username); err != nil {
		return "", err
	}
	return username, nil
}
