// Package cardstore is the durable S3/MinIO-backed cache for rendered social
// cards (boom-fym5). It holds exactly one object per user — cards/<user>.png,
// overwritten on regen — so it's a bounded, durable cache rather than an
// ever-growing archive. The og.png handler passes through it: serve the cached
// PNG when fresh, otherwise render → Put → serve. MinIO stays private
// (in-cluster); the app is the only S3 client, so nothing is exposed publicly.
//
// The client (minio-go v7) is pure Go — no CGO — so it links into the
// CGO_ENABLED=0 alpine image alongside the resvg-go rasterizer.
package cardstore

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// TTL is how long a cached card is considered fresh. Older objects are
// re-rendered on the next request — this is the "regen daily" cadence, done
// lazily (no scheduler) off each object's LastModified.
const TTL = 24 * time.Hour

// objectKey is the single durable key per user (overwritten on each regen).
func objectKey(username string) string { return "cards/" + username + ".png" }

// fresh reports whether an object last written at lastMod is still within TTL
// as of now — the "regen daily" boundary, pulled out as a pure func so it's
// tested without a live S3.
func fresh(lastMod, now time.Time) bool { return now.Sub(lastMod) < TTL }

// Store is the S3/MinIO-backed card cache. A nil *Store is a valid "disabled"
// value — callers guard on it and render live instead.
type Store struct {
	client *minio.Client
	bucket string
}

// New builds a Store from resolved S3 settings. Returns (nil, nil) when any
// required setting is missing so callers can treat a nil *Store as "no cache,
// render live" without special-casing.
func New(endpoint, bucket, accessKey, secretKey string, useSSL bool) (*Store, error) {
	if endpoint == "" || bucket == "" || accessKey == "" || secretKey == "" {
		return nil, nil
	}
	cl, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("cardstore: minio client: %w", err)
	}
	return &Store{client: cl, bucket: bucket}, nil
}

// Cached is a cache hit: the PNG bytes plus whether they're still within TTL.
type Cached struct {
	PNG   []byte
	Fresh bool
}

// Get returns the cached card for username. ok=false is a clean miss (no such
// object). A present-but-stale object returns ok=true, Fresh=false so the
// caller decides whether to refresh. Errors are only for genuine transport /
// permission failures — a missing key is never an error.
func (s *Store) Get(ctx context.Context, username string) (Cached, bool, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, objectKey(username), minio.GetObjectOptions{})
	if err != nil {
		return Cached{}, false, err
	}
	defer obj.Close()

	info, err := obj.Stat()
	if err != nil {
		// A missing object (or bucket) is a miss, not a failure.
		switch minio.ToErrorResponse(err).Code {
		case "NoSuchKey", "NoSuchBucket":
			return Cached{}, false, nil
		}
		return Cached{}, false, err
	}

	var buf bytes.Buffer
	buf.Grow(int(info.Size))
	if _, err := buf.ReadFrom(obj); err != nil {
		return Cached{}, false, err
	}
	return Cached{PNG: buf.Bytes(), Fresh: fresh(info.LastModified, time.Now())}, true, nil
}

// Put writes (overwrites) the user's card — one durable object per user.
func (s *Store) Put(ctx context.Context, username string, png []byte) error {
	_, err := s.client.PutObject(ctx, s.bucket, objectKey(username),
		bytes.NewReader(png), int64(len(png)),
		minio.PutObjectOptions{ContentType: "image/png"})
	return err
}

// Delete drops the user's cached card. Used to invalidate on card edits so a
// theme/tagline change shows on the next unfurl instead of waiting out the TTL.
// A missing object is not an error.
func (s *Store) Delete(ctx context.Context, username string) error {
	return s.client.RemoveObject(ctx, s.bucket, objectKey(username), minio.RemoveObjectOptions{})
}
