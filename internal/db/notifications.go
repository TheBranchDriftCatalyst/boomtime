package db

import (
	"context"
	"encoding/json"
	"time"
)

// notifications.go — durable notification storage (migration 00079). The notify
// hub persists DURABLE events here so they survive a missing WS session and are
// delivered on the user's next connect; ephemeral events never land here.

// Notification is one durable notification row.
type Notification struct {
	ID        int64          `json:"id"`
	Owner     string         `json:"-"`
	Type      string         `json:"type"`
	Title     string         `json:"title"`
	Body      string         `json:"body,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
	CreatedAt time.Time      `json:"at"`
	ReadAt    *time.Time     `json:"readAt,omitempty"`
}

// SaveNotification inserts a durable notification. The signature is primitives-only
// (no notify.Event) SO the db package need not import notify — *DB satisfies
// notify.Persister structurally. `at` zero → now(). Best-effort caller: a save
// error means the event still fanned out live, it just isn't replayable.
func (d *DB) SaveNotification(ctx context.Context, owner, typ, title, body string, data map[string]any, at time.Time) error {
	var raw []byte
	if len(data) > 0 {
		b, err := json.Marshal(data)
		if err != nil {
			return err
		}
		raw = b
	}
	var createdAt any = at
	if at.IsZero() {
		createdAt = time.Now()
	}
	_, err := d.Pool.Exec(ctx,
		`INSERT INTO notifications (owner, type, title, body, data, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		owner, typ, title, body, raw, createdAt)
	return err
}

// ListNotifications returns the owner's most recent durable notifications (newest
// first, capped by limit), plus the current unread count. Delivered to the FE on
// session start so nothing fired while offline is missed.
func (d *DB) ListNotifications(ctx context.Context, owner string, limit int) ([]Notification, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := d.Pool.Query(ctx,
		`SELECT id, type, title, body, data, created_at, read_at
		   FROM notifications
		  WHERE owner = $1
		  ORDER BY created_at DESC
		  LIMIT $2`,
		owner, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Notification
	unread := 0
	for rows.Next() {
		var n Notification
		var raw []byte
		if err := rows.Scan(&n.ID, &n.Type, &n.Title, &n.Body, &raw, &n.CreatedAt, &n.ReadAt); err != nil {
			return nil, 0, err
		}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &n.Data)
		}
		if n.ReadAt == nil {
			unread++
		}
		out = append(out, n)
	}
	return out, unread, rows.Err()
}

// MarkNotificationsRead stamps read_at=now() on the owner's unread notifications
// (all of them). Returns how many rows flipped. Idempotent (already-read rows are
// untouched by the WHERE read_at IS NULL guard).
func (d *DB) MarkNotificationsRead(ctx context.Context, owner string) (int64, error) {
	tag, err := d.Pool.Exec(ctx,
		`UPDATE notifications SET read_at = now()
		  WHERE owner = $1 AND read_at IS NULL`,
		owner)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
