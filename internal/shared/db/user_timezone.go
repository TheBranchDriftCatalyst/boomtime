// user_timezone.go: per-user IANA timezone storage + the 3-level TZ resolver
// used by every SQL that extracts a dow / hour / date bucket from time_sent
// (boom-dg7).
//
// Resolution order (applied by ResolveTimezone):
//
//  1. users.timezone if non-empty (an explicit user pick)
//  2. defaultTZ argument (operator-configured via BOOM_DEFAULT_TIMEZONE)
//  3. "UTC"
//
// The resolver NEVER returns "" — every caller can safely thread the result
// into `AT TIME ZONE $N` without a nil/empty guard. `time.LoadLocation` is not
// called here: the write path (SetUserTimezone) validates on the way in, and
// on the read path an invalid stored value would cause Postgres to raise a
// clear error at query time — never a silent misfire.
package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// GetUserTimezone returns the raw stored `users.timezone` value for a user.
// Returns "" if the user has never picked a timezone (the migration DEFAULT).
// Returns (pgx.ErrNoRows) if the user does not exist so callers can
// distinguish "unset" from "no such user".
func (d *DB) GetUserTimezone(ctx context.Context, username string) (string, error) {
	if username == "" {
		return "", errors.New("GetUserTimezone: empty username")
	}
	var tz string
	err := d.Pool.QueryRow(ctx,
		`SELECT timezone FROM users WHERE username = $1`, username,
	).Scan(&tz)
	if err != nil {
		return "", err
	}
	return tz, nil
}

// SetUserTimezone writes an IANA timezone name for a user. Callers MUST
// validate `tz` against `time.LoadLocation` before calling — this function
// does the round-trip validation here as a belt-and-suspenders check so a
// direct-DB write cannot land an invalid value that would break every
// subsequent AT TIME ZONE query.
//
// Empty tz is allowed and means "clear the explicit pick, fall back to
// BOOM_DEFAULT_TIMEZONE / UTC" — the delete-analog for the PATCH endpoint.
// Returns pgx.ErrNoRows if the username does not exist.
func (d *DB) SetUserTimezone(ctx context.Context, username, tz string) error {
	if username == "" {
		return errors.New("SetUserTimezone: empty username")
	}
	if tz != "" {
		if _, err := time.LoadLocation(tz); err != nil {
			return err
		}
	}
	tag, err := d.Pool.Exec(ctx,
		`UPDATE users SET timezone = $2 WHERE username = $1`,
		username, tz,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ResolveTimezone applies the documented 3-level chain to yield an effective
// IANA name. NEVER returns "" — callers can thread the result into a bind
// param for AT TIME ZONE without further guarding. `userTZ` is the raw
// stored value (empty=unset); `defaultTZ` is the operator's env default
// (empty=unset).
func ResolveTimezone(userTZ, defaultTZ string) string {
	if userTZ != "" {
		return userTZ
	}
	if defaultTZ != "" {
		return defaultTZ
	}
	return "UTC"
}
