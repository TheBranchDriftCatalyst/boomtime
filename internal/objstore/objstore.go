// Package objstore is a thin S3/MinIO object-storage wrapper for boomtime's
// durable byte blobs — currently the persisted background-job log streams
// (gaka-hney): a FINISHED job's logs live in the in-memory LogHub ring only
// until it rolls over, so we flush them to `job-logs/<id>.jsonl` on completion
// and serve them back to the Admin Jobs viewer.
//
// It deliberately mirrors internal/cardstore (same minio-go v7, pure Go, links
// under CGO_ENABLED=0) but exposes a generic key/reader surface behind the Store
// interface so callers — and tests — can stub it. A nil Store is a valid
// "persistence off" value: New returns (nil, nil) when S3 is unconfigured and
// every caller treats nil as "skip persistence" rather than erroring.
package objstore

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/config"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/metrics"
)

// ErrNotFound is returned by Get/Exists when the object does not exist. Callers
// map it to a 404 rather than a 500 — a missing key is a clean miss, not a
// transport failure. Compare with errors.Is.
var ErrNotFound = errors.New("objstore: object not found")

// Store is the minimal object-storage surface boomtime needs. The concrete impl
// is *Client (minio-go); tests substitute a stub. A nil Store means persistence
// is disabled — never construct a typed-nil into this interface (New returns an
// untyped nil, and callers guard the concrete return before assigning it).
type Store interface {
	// Put writes (overwrites) the object at key from r. size is the exact byte
	// length (minio streams a known-length body); pass contentType for the
	// stored object's metadata.
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	// Get opens the object at key. Returns ErrNotFound when it's absent. The
	// caller must Close the returned reader.
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	// Delete removes the object at key. A missing key is NOT an error (S3 delete
	// is idempotent) so this is safe to call blind.
	Delete(ctx context.Context, key string) error
	// Exists reports whether the object at key is present. Absence returns
	// (false, nil), not an error.
	Exists(ctx context.Context, key string) (bool, error)
	// List returns every object key under prefix (recursive). An empty prefix
	// lists the whole bucket. A prefix that matches nothing returns an empty
	// slice, not an error. Backs the admin bulk log-clear (delete every stored
	// job-log under the `job-logs/` prefix).
	List(ctx context.Context, prefix string) ([]string, error)
}

// Client is the minio-go-backed Store.
type Client struct {
	mc     *minio.Client
	bucket string
}

// compile-time assertion that *Client satisfies Store.
var _ Store = (*Client)(nil)

// New builds a Client from the resolved S3 config. It returns (nil, nil) when S3
// is unconfigured (cfg.S3Enabled() false) so callers can treat a nil *Client as
// "persistence off" with a plain nil-check — no error path for the common
// local-dev case where no bucket is provisioned. A non-nil error is only a
// genuine client-construction failure.
//
// The underlying http.Client routes through metrics.InstrumentTransport so every
// S3 call is counted in the outbound RED metrics as
// http_client_requests_total{host=<endpoint>}.
func New(cfg *config.Config) (*Client, error) {
	if cfg == nil || !cfg.S3Enabled() {
		return nil, nil
	}
	mc, err := minio.New(cfg.S3Endpoint, &minio.Options{
		Creds:     credentials.NewStaticV4(cfg.S3AccessKey, cfg.S3SecretKey, ""),
		Secure:    cfg.S3UseSSL,
		Region:    cfg.S3Region,
		Transport: metrics.InstrumentTransport(nil),
	})
	if err != nil {
		return nil, fmt.Errorf("objstore: minio client: %w", err)
	}
	return &Client{mc: mc, bucket: cfg.S3Bucket}, nil
}

// Put implements Store.
func (c *Client) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	_, err := c.mc.PutObject(ctx, c.bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return fmt.Errorf("objstore: put %q: %w", key, err)
	}
	return nil
}

// Get implements Store. A missing object is reported as ErrNotFound.
func (c *Client) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := c.mc.GetObject(ctx, c.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("objstore: get %q: %w", key, err)
	}
	// GetObject is lazy — the transport error surfaces on the first Stat/Read.
	// Stat here so a missing key becomes ErrNotFound BEFORE we hand back a reader
	// the caller would only fail on later.
	if _, serr := obj.Stat(); serr != nil {
		_ = obj.Close()
		if isNotFound(serr) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("objstore: stat %q: %w", key, serr)
	}
	return obj, nil
}

// Delete implements Store. A missing key is not an error.
func (c *Client) Delete(ctx context.Context, key string) error {
	if err := c.mc.RemoveObject(ctx, c.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("objstore: delete %q: %w", key, err)
	}
	return nil
}

// List implements Store. It walks the bucket under prefix (recursive) and
// collects the object keys. A per-object Err aborts the walk — a partial listing
// would let a bulk delete silently miss objects.
func (c *Client) List(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	for obj := range c.mc.ListObjects(ctx, c.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if obj.Err != nil {
			return nil, fmt.Errorf("objstore: list %q: %w", prefix, obj.Err)
		}
		keys = append(keys, obj.Key)
	}
	return keys, nil
}

// Exists implements Store.
func (c *Client) Exists(ctx context.Context, key string) (bool, error) {
	if _, err := c.mc.StatObject(ctx, c.bucket, key, minio.StatObjectOptions{}); err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("objstore: exists %q: %w", key, err)
	}
	return true, nil
}

// isNotFound classifies a minio error as "no such object/bucket".
func isNotFound(err error) bool {
	switch minio.ToErrorResponse(err).Code {
	case "NoSuchKey", "NoSuchBucket":
		return true
	}
	return false
}
