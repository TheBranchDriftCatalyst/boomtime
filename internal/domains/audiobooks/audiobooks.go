// Package audiobooks is the catalyst-audiobooks ingestion domain: AUDIBLE
// listening. THIN by design — it maps the Audible library (fetched via the
// SHARED internal/amazon device credential) into the siloed reading_items table,
// and (later) registers its periodic sync job on the catalyst-go-jobs scheduler.
// It owns no auth (that's internal/amazon) and no push (that's internal/hardcover).
//
// Standard domain layout (mirrors internal/domains/books):
//
//	<name>.go   — package doc + Service{deps} + FetchLibrary/SyncUser (this file)
//	jobs.go     — <Name>SyncKind + RegisterJobs (added with the scheduler wiring)
//	routes.go   — query endpoints (added with the query API)
package audiobooks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
)

// AudibleSyncKind is the catalyst-go-jobs kind for the periodic Audible sync.
const AudibleSyncKind = "audiobooks-audible-sync"

// Service is the catalyst-audiobooks domain entrypoint.
type Service struct {
	DB     *db.DB
	Amazon *amazon.Store
	Logger *slog.Logger
}

// New constructs the audiobooks (Audible) domain service.
func New(database *db.DB, az *amazon.Store, logger *slog.Logger) *Service {
	return &Service{DB: database, Amazon: az, Logger: logger}
}

// LibraryItem is the subset of an Audible /1.0/library item we read.
type LibraryItem struct {
	ASIN            string `json:"asin"`
	Title           string `json:"title"`
	Subtitle        string `json:"subtitle"`
	Authors         []struct {
		Name string `json:"name"`
	} `json:"authors"`
	IsFinished       bool    `json:"is_finished"`
	PercentComplete  float64 `json:"percent_complete"`
	RuntimeLengthMin int     `json:"runtime_length_min"`
}

func (li LibraryItem) authorsCSV() string {
	names := make([]string, 0, len(li.Authors))
	for _, a := range li.Authors {
		if a.Name != "" {
			names = append(names, a.Name)
		}
	}
	return strings.Join(names, ", ")
}

// FetchLibrary makes the signed GET /1.0/library call (this is where the ADP
// request signing gets verified against real Audible). Returns the parsed items.
func (s *Service) FetchLibrary(ctx context.Context, cred *amazon.DeviceCredential) ([]LibraryItem, error) {
	host := amazon.AudibleAPIHost(cred.Marketplace)
	// percent_complete + is_finished only come back if requested in response_groups.
	path := "/1.0/library?response_groups=product_desc,contributors,product_attrs,is_finished,percent_complete&num_results=1000&page=1"
	body, status, err := amazon.SignedGet(ctx, cred, host, path)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("audible /1.0/library returned HTTP %d: %s", status, snippet(body))
	}
	var lr struct {
		Items []LibraryItem `json:"items"`
	}
	if err := json.Unmarshal(body, &lr); err != nil {
		return nil, fmt.Errorf("audible library parse failed: %w (body: %s)", err, snippet(body))
	}
	return lr.Items, nil
}

// SyncUser loads the user's Amazon credential, fetches the Audible library, and
// upserts it into the siloed reading_items table (source=audible). Returns the
// number of items synced. Idempotent.
func (s *Service) SyncUser(ctx context.Context, owner string) (int, error) {
	cred, err := s.Amazon.Load(ctx, owner)
	if err != nil {
		return 0, err
	}
	items, err := s.FetchLibrary(ctx, cred)
	if err != nil {
		// A device-auth rejection flips the credential status so the UI can prompt
		// a reconnect (mirrors the github token-status pattern).
		_ = s.Amazon.DB.UpdateAmazonDeviceStatus(ctx, owner, db.AmazonDeviceStatusInvalid)
		return 0, err
	}
	_ = s.Amazon.DB.UpdateAmazonDeviceStatus(ctx, owner, db.AmazonDeviceStatusValid)

	for _, it := range items {
		raw, _ := json.Marshal(it)
		status := "reading"
		switch {
		case it.IsFinished:
			status = "read"
		case it.PercentComplete <= 0:
			status = "want"
		}
		if err := s.DB.UpsertReadingItem(ctx, db.ReadingItem{
			Owner:           owner,
			Source:          "audible",
			ExternalID:      it.ASIN,
			Title:           it.Title,
			Authors:         it.authorsCSV(),
			Status:          status,
			ProgressPercent: int(it.PercentComplete),
			Finished:        it.IsFinished,
			RawMeta:         raw,
		}); err != nil {
			return 0, fmt.Errorf("upsert %q: %w", it.ASIN, err)
		}
	}
	return len(items), nil
}

// snippet returns a short, safe preview of a response body for error messages.
func snippet(b []byte) string {
	const max = 300
	s := strings.TrimSpace(string(b))
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}
