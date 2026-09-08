// Package annotations is the cross-source ANNOTATION INGEST (boom-siwi.5): it
// pulls Kindle highlights and notes — and, from phase 2, Audible clips and
// bookmarks — into book_annotations.
//
// It is its own package rather than a file under ingest/kindle or ingest/audible
// because it is the first genuinely CROSS-SOURCE stage: the Kindle half drives
// the read.amazon.com cookie transport and the Audible half drives the
// ADP-signed Fiona sidecar. Filing it under either domain would misplace the
// other.
//
// TWO INVARIANTS, both learned the hard way elsewhere in this repo:
//
//  1. This stage NEVER touches credential health. ExchangeWebsiteCookies refuses
//     a non-US credential outright (cloudReaderMarketplaceErr), and that error is
//     not credhealth.Transient — so a stage that marked health here would flip a
//     perfectly good UK/DE credential to `invalid` and tell the user to
//     reconnect Amazon, purely because an OPTIONAL feature cannot reach a
//     US-only surface. Credential health is owned by the Kindle and Audible
//     ingests; a third writer is a regression.
//
//  2. A parse failure retires NOTHING. Amazon publishes no delete signal, so the
//     reconcile infers deletion from absence — and a broken parser produces
//     exactly the same absence as a user who deleted every highlight. Per book,
//     a shape error logs and skips. Per sweep, if EVERY book failed to parse,
//     the stage returns an error: that is a broken parser, not an empty account,
//     and it must turn the job red rather than quietly reporting a healthy sync
//     of zero.
package annotations

import (
	"context"
	"log/slog"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/logctx"
)

// KindleAnnotationsKind is the jobs registry kind for the Kindle notebook sweep.
//
// Phase 2 adds a SEPARATE books-audible-annotations kind rather than extending
// this one: the two have opposite cost profiles (a free index plus ~14 requests
// versus one sidecar call per candidate audiobook), different transports, and
// independent failure modes — the same reasons the Kindle ingest already splits
// sync / insights / reconcile / reading-time across four kinds against one
// credential.
const KindleAnnotationsKind = "books-kindle-annotations"

// source tags every row this package writes.
const source = "kindle"

// kindleAnnotationPace is the delay between per-book notebook fetches.
//
// Deliberately 5x the Fiona sidecar's 50ms. The notebook is a cookie-
// authenticated WEBSITE surface, not an API, and it shares its cookie jar with
// KindleCloudLibrary and FetchKindleInsights — so getting throttled or
// captcha'd here would break the entire Kindle ingest, not just this optional
// stage. Fourteen books at 250ms is three and a half seconds; that is a cheap
// insurance premium. A var so tests can zero it, mirroring reconcilePaceDelay.
var kindleAnnotationPace = 250 * time.Millisecond

// notebookSource is the Kindle notebook wire seam — an interface so tests can
// drive the sweep with fakes and no network, mirroring kindleSource /
// positionSource in ingest/kindle.
type notebookSource interface {
	ExchangeWebsiteCookies(ctx context.Context, cred *amazon.DeviceCredential) (map[string]string, error)
	ListAnnotatedBooks(ctx context.Context, cookies map[string]string) ([]amazon.NotebookBook, error)
	FetchBookAnnotations(ctx context.Context, cookies map[string]string, asin string) ([]amazon.KindleAnnotation, error)
}

// liveNotebook is the real transport, adapting the package-level funcs onto the
// seam.
type liveNotebook struct{}

func (liveNotebook) ExchangeWebsiteCookies(ctx context.Context, cred *amazon.DeviceCredential) (map[string]string, error) {
	return amazon.ExchangeWebsiteCookies(ctx, cred)
}

func (liveNotebook) ListAnnotatedBooks(ctx context.Context, cookies map[string]string) ([]amazon.NotebookBook, error) {
	return amazon.ListAnnotatedBooks(ctx, cookies)
}

func (liveNotebook) FetchBookAnnotations(ctx context.Context, cookies map[string]string, asin string) ([]amazon.KindleAnnotation, error) {
	return amazon.FetchBookAnnotations(ctx, cookies, asin)
}

// Service is the annotation ingest.
type Service struct {
	DB     *db.DB
	Amazon *amazon.Store
	Logger *slog.Logger

	notebook notebookSource
}

// New constructs the ingest with the real transports.
func New(database *db.DB, az *amazon.Store, logger *slog.Logger) *Service {
	return &Service{DB: database, Amazon: az, Logger: logger, notebook: liveNotebook{}}
}

// SetNotebookSource swaps the notebook transport — the test seam.
func (s *Service) SetNotebookSource(n notebookSource) *Service { s.notebook = n; return s }

func (s *Service) logInfo(ctx context.Context, msg string, args ...any) {
	logctx.FromContext(ctx, s.Logger).Info(msg, args...)
}

func (s *Service) logWarn(ctx context.Context, msg string, args ...any) {
	logctx.FromContext(ctx, s.Logger).Warn(msg, args...)
}
