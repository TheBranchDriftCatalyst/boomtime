package books

import (
	"context"
	"testing"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/hardcover"
)

// fakeKindle is an in-memory kindleSource: shelves + their records + per-ASIN
// sidecar positions, so sweep runs with no network.
type fakeKindle struct {
	datasets []amazon.Dataset
	records  map[string][]amazon.CollectionRecord // datasetID -> records
	sidecars map[string]*amazon.SidecarPosition   // asin -> position
}

func (f *fakeKindle) Datasets(context.Context, *amazon.DeviceCredential) ([]amazon.Dataset, error) {
	return f.datasets, nil
}
func (f *fakeKindle) CollectionRecords(_ context.Context, _ *amazon.DeviceCredential, datasetID string) ([]amazon.CollectionRecord, error) {
	return f.records[datasetID], nil
}
func (f *fakeKindle) Sidecar(_ context.Context, _ *amazon.DeviceCredential, asin string) (*amazon.SidecarPosition, error) {
	return f.sidecars[asin], nil
}

// fakeResolver is an in-memory metaResolver keyed by ASIN.
type fakeResolver struct {
	byASIN map[string]*hardcover.BookMeta
}

func (f *fakeResolver) LookupByASIN(_ context.Context, asin string) (*hardcover.BookMeta, error) {
	return f.byASIN[asin], nil
}

// assert the real clients satisfy the domain interfaces (compile-time contract).
var (
	_ kindleSource = (*amazon.KindleClient)(nil)
	_ metaResolver = (*hardcover.Client)(nil)
)

func TestShelfStatus(t *testing.T) {
	cases := []struct {
		name       string
		wantStatus string
		wantIs     bool
	}{
		{"Currently Readings", "reading", true},
		{"Currently Reading", "reading", true},
		{"Done Reading", "read", true},
		{"Have Not Read", "want", true},
		{"The Expanse", "", false}, // a series shelf: membership only
		{"My Favorites", "", false},
	}
	for _, c := range cases {
		gotStatus, gotIs := shelfStatus(c.name)
		if gotStatus != c.wantStatus || gotIs != c.wantIs {
			t.Fatalf("shelfStatus(%q) = (%q,%v), want (%q,%v)", c.name, gotStatus, gotIs, c.wantStatus, c.wantIs)
		}
	}
}

func lpr(pos int64, ms int64) *amazon.SidecarPosition {
	t := time.UnixMilli(ms).UTC()
	return &amazon.SidecarPosition{Position: pos, LastUpdated: &t}
}

// TestSweep exercises the whole read-side pipeline against fakes: shelf→status
// union (read>reading>want, series shelf = membership only), sidecar position +
// date, and Hardcover metadata + linkage — with no network, no DB.
func TestSweep(t *testing.T) {
	fk := &fakeKindle{
		datasets: []amazon.Dataset{
			{Identifier: "ds-cur", Name: "Currently Readings", Namespace: amazon.CloudCollectionsNamespace},
			{Identifier: "ds-done", Name: "Done Reading", Namespace: amazon.CloudCollectionsNamespace},
			{Identifier: "ds-want", Name: "Have Not Read", Namespace: amazon.CloudCollectionsNamespace},
			{Identifier: "ds-exp", Name: "The Expanse", Namespace: amazon.CloudCollectionsNamespace},
			{Identifier: "ds-sync", Name: "device-sync", Namespace: "Kindle.DeviceSync"}, // must be ignored
		},
		records: map[string][]amazon.CollectionRecord{
			"ds-cur":  {{ASIN: "ASIN_READING"}, {ASIN: "ASIN_SERIES"}}, // ASIN_SERIES also on the series shelf
			"ds-done": {{ASIN: "ASIN_READ"}},
			"ds-want": {{ASIN: "ASIN_WANT"}},
			"ds-exp":  {{ASIN: "ASIN_SERIES"}}, // membership only — must not blank the "reading" status
			"ds-sync": {{ASIN: "ASIN_GHOST"}},  // from an ignored namespace — must not appear
		},
		sidecars: map[string]*amazon.SidecarPosition{
			"ASIN_READING": lpr(1200, 1691234567000),
			"ASIN_READ":    lpr(9999, 1690000000000),
		},
	}
	res := &fakeResolver{byASIN: map[string]*hardcover.BookMeta{
		"ASIN_READING": {BookID: 111, EditionID: 222, Title: "The Reading Book", Authors: "Ada Author", CoverURL: "https://img/r.jpg"},
		// ASIN_READ intentionally has NO Hardcover entry → ingests ASIN-only.
	}}

	s := &Service{kindle: fk}
	items, hcConnected, err := s.sweep(context.Background(), &amazon.DeviceCredential{}, "alice", res)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if !hcConnected {
		t.Fatal("hcConnected should be true when a resolver is supplied")
	}

	byASIN := map[string]kindleItem{}
	for _, ki := range items {
		byASIN[ki.Item.ExternalID] = ki
	}
	if _, ok := byASIN["ASIN_GHOST"]; ok {
		t.Fatal("an ASIN from a non-CloudCollections namespace leaked into the sweep")
	}
	if len(byASIN) != 4 {
		t.Fatalf("want 4 unique ASINs, got %d: %v", len(byASIN), keys(byASIN))
	}

	// reading book: status reading, source kindle, amazon_asin set, progress 0
	// (position is not a percent), started_at from the sidecar date, and full
	// Hardcover metadata + linkage.
	r := byASIN["ASIN_READING"]
	if r.Item.Source != "kindle" {
		t.Fatalf("source: want kindle, got %q", r.Item.Source)
	}
	if r.Item.Status != "reading" || r.Item.Finished {
		t.Fatalf("reading book status/finished wrong: %+v", r.Item)
	}
	if r.Item.AmazonASIN != "ASIN_READING" {
		t.Fatalf("amazon_asin not set: %q", r.Item.AmazonASIN)
	}
	if r.Item.ProgressPercent != 0 {
		t.Fatalf("progress should be 0 (LPR position is not a percent), got %d", r.Item.ProgressPercent)
	}
	if r.Item.StartedAt == nil || r.Item.StartedAt.UTC() != time.UnixMilli(1691234567000).UTC() {
		t.Fatalf("started_at should come from the sidecar date, got %v", r.Item.StartedAt)
	}
	if r.Item.Title != "The Reading Book" || r.Item.Authors != "Ada Author" || r.Item.CoverURL == "" {
		t.Fatalf("Hardcover metadata not mapped: %+v", r.Item)
	}
	if r.BookID != 111 || r.EditionID != 222 || r.MatchConf != "asin" {
		t.Fatalf("Hardcover linkage not carried: bookID=%d editionID=%d conf=%q", r.BookID, r.EditionID, r.MatchConf)
	}

	// read book: status read, finished true, progress 100, finished_at from the
	// sidecar date, and (no Hardcover entry) blank title + no linkage.
	rd := byASIN["ASIN_READ"]
	if rd.Item.Status != "read" || !rd.Item.Finished {
		t.Fatalf("read book should be finished: %+v", rd.Item)
	}
	if rd.Item.ProgressPercent != 100 {
		t.Fatalf("finished book progress: want 100, got %d", rd.Item.ProgressPercent)
	}
	if rd.Item.FinishedAt == nil || rd.Item.FinishedAt.UTC() != time.UnixMilli(1690000000000).UTC() {
		t.Fatalf("finished_at should come from the sidecar date, got %v", rd.Item.FinishedAt)
	}
	if rd.Item.Title != "" || rd.BookID != 0 {
		t.Fatalf("ASIN with no Hardcover match should ingest ASIN-only: %+v (bookID=%d)", rd.Item, rd.BookID)
	}

	// want book: status want, not finished, progress 0, no sidecar → no dates.
	w := byASIN["ASIN_WANT"]
	if w.Item.Status != "want" || w.Item.Finished || w.Item.ProgressPercent != 0 {
		t.Fatalf("want book wrong: %+v", w.Item)
	}
	if w.Item.StartedAt != nil || w.Item.FinishedAt != nil {
		t.Fatalf("want book should have no dates: %+v", w.Item)
	}

	// series-only book: on Currently Readings AND The Expanse → the union keeps
	// the strongest status ("reading"), the series shelf must not blank it.
	se := byASIN["ASIN_SERIES"]
	if se.Item.Status != "reading" {
		t.Fatalf("series+currently union should keep reading, got %q", se.Item.Status)
	}
}

// TestSweepNoHardcover: with a nil resolver, rows still ingest with ASIN only.
func TestSweepNoHardcover(t *testing.T) {
	fk := &fakeKindle{
		datasets: []amazon.Dataset{{Identifier: "ds-cur", Name: "Currently Readings", Namespace: amazon.CloudCollectionsNamespace}},
		records:  map[string][]amazon.CollectionRecord{"ds-cur": {{ASIN: "ASIN_X"}}},
	}
	s := &Service{kindle: fk}
	items, hcConnected, err := s.sweep(context.Background(), &amazon.DeviceCredential{}, "bob", nil)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if hcConnected {
		t.Fatal("hcConnected should be false with a nil resolver")
	}
	if len(items) != 1 || items[0].Item.Title != "" || items[0].BookID != 0 {
		t.Fatalf("nil-resolver sweep should ingest ASIN-only, got %+v", items)
	}
	if items[0].Item.ExternalID != "ASIN_X" || items[0].Item.Status != "reading" {
		t.Fatalf("row mapping wrong: %+v", items[0].Item)
	}
}

// TestBuildReadingItemStatusUnion asserts the status-rank helper directly.
func TestStatusRank(t *testing.T) {
	if !(statusRank("read") > statusRank("reading") && statusRank("reading") > statusRank("want") && statusRank("want") > statusRank("")) {
		t.Fatal("status rank order must be read > reading > want > unknown")
	}
}

// TestBuildReadingItemDeletedTombstoneSkipped ensures collectShelves drops a
// deleted record.
func TestCollectShelvesSkipsDeleted(t *testing.T) {
	fk := &fakeKindle{
		datasets: []amazon.Dataset{{Identifier: "ds", Name: "Currently Readings", Namespace: amazon.CloudCollectionsNamespace}},
		records: map[string][]amazon.CollectionRecord{"ds": {
			{ASIN: "KEEP"},
			{ASIN: "GONE", IsDeleted: true},
		}},
	}
	s := &Service{kindle: fk}
	got, err := s.collectShelves(context.Background(), &amazon.DeviceCredential{}, fk.datasets)
	if err != nil {
		t.Fatalf("collectShelves: %v", err)
	}
	if _, ok := got["GONE"]; ok {
		t.Fatal("deleted record should be skipped")
	}
	if _, ok := got["KEEP"]; !ok {
		t.Fatal("non-deleted record should be kept")
	}
}

func keys(m map[string]kindleItem) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
