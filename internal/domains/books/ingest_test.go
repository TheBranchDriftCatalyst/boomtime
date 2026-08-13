package books

import (
	"context"
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/hardcover"
)

// fakeKindle is an in-memory kindleSource: a fixed cookie jar + library, so
// sweep runs with no network.
type fakeKindle struct {
	cookies map[string]string
	library []amazon.CloudLibraryItem
}

func (f *fakeKindle) ExchangeWebsiteCookies(context.Context, *amazon.DeviceCredential) (map[string]string, error) {
	if f.cookies == nil {
		return map[string]string{"at-main": "token"}, nil
	}
	return f.cookies, nil
}
func (f *fakeKindle) KindleCloudLibrary(context.Context, map[string]string) ([]amazon.CloudLibraryItem, error) {
	return f.library, nil
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
	_ kindleSource = (*amazon.CloudReaderClient)(nil)
	_ metaResolver = (*hardcover.Client)(nil)
)

func TestStatusFromPercent(t *testing.T) {
	cases := []struct {
		pct        int
		wantStatus string
		wantFin    bool
	}{
		{0, "want", false},
		{1, "reading", false},
		{50, "reading", false},
		{99, "reading", false},
		{100, "read", true},
	}
	for _, c := range cases {
		gotStatus, gotFin := statusFromPercent(c.pct)
		if gotStatus != c.wantStatus || gotFin != c.wantFin {
			t.Fatalf("statusFromPercent(%d) = (%q,%v), want (%q,%v)", c.pct, gotStatus, gotFin, c.wantStatus, c.wantFin)
		}
	}
}

// TestSweep exercises the whole read-side pipeline against fakes: Cloud Reader
// library → percentageRead-driven status/progress/finished, Amazon-supplied
// title/authors/cover, sample filtering, and the optional Hardcover linkage —
// with no network, no DB.
func TestSweep(t *testing.T) {
	fk := &fakeKindle{
		library: []amazon.CloudLibraryItem{
			{ASIN: "ASIN_READING", Title: "The Reading Book", Authors: []string{"Author, Ada:"}, PercentageRead: 42, CoverURL: "https://img/r.jpg", ResourceType: "EBOOK"},
			{ASIN: "ASIN_READ", Title: "The Finished Book", Authors: []string{"Writer, Bob:", "Coauthor, Cy:"}, PercentageRead: 100, CoverURL: "https://img/d.jpg", ResourceType: "EBOOK"},
			{ASIN: "ASIN_WANT", Title: "The Unopened Book", Authors: []string{"Poet, Pat:"}, PercentageRead: 0, CoverURL: "https://img/w.jpg", ResourceType: "EBOOK"},
			{ASIN: "ASIN_SAMPLE", Title: "A Sample", Authors: []string{"Nobody, No:"}, PercentageRead: 10, ResourceType: "EBOOK_SAMPLE"}, // must be filtered
			{ASIN: "", Title: "No ASIN", PercentageRead: 5, ResourceType: "EBOOK"},                                                       // must be dropped
		},
	}
	res := &fakeResolver{byASIN: map[string]*hardcover.BookMeta{
		// ASIN_READING gets a Hardcover linkage; Amazon title must still win.
		"ASIN_READING": {BookID: 111, EditionID: 222, Title: "Hardcover Title (ignored)", Authors: "Ignored", CoverURL: "https://hc/r.jpg"},
		// ASIN_READ intentionally has NO Hardcover entry → no linkage.
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
	if _, ok := byASIN["ASIN_SAMPLE"]; ok {
		t.Fatal("a Kindle sample leaked into the sweep")
	}
	if _, ok := byASIN[""]; ok {
		t.Fatal("an item with no ASIN leaked into the sweep")
	}
	if len(byASIN) != 3 {
		t.Fatalf("want 3 ingested items (samples/empty dropped), got %d: %v", len(byASIN), keys(byASIN))
	}

	// reading book: status reading, progress = percentageRead, not finished,
	// Amazon title/authors/cover, and the Hardcover linkage carried (Amazon title
	// must win over Hardcover's).
	r := byASIN["ASIN_READING"]
	if r.Item.Source != "kindle" {
		t.Fatalf("source: want kindle, got %q", r.Item.Source)
	}
	if r.Item.Status != "reading" || r.Item.Finished {
		t.Fatalf("reading book status/finished wrong: %+v", r.Item)
	}
	if r.Item.ProgressPercent != 42 {
		t.Fatalf("progress should equal percentageRead (42), got %d", r.Item.ProgressPercent)
	}
	if r.Item.AmazonASIN != "ASIN_READING" {
		t.Fatalf("amazon_asin not set: %q", r.Item.AmazonASIN)
	}
	if r.Item.Title != "The Reading Book" || r.Item.Authors != "Author, Ada" || r.Item.CoverURL != "https://img/r.jpg" {
		t.Fatalf("Amazon metadata not mapped (or Hardcover clobbered it): %+v", r.Item)
	}
	if r.BookID != 111 || r.EditionID != 222 || r.MatchConf != "asin" {
		t.Fatalf("Hardcover linkage not carried: bookID=%d editionID=%d conf=%q", r.BookID, r.EditionID, r.MatchConf)
	}

	// read book: status read, finished, progress 100, multi-author CSV, no
	// Hardcover linkage (no entry).
	rd := byASIN["ASIN_READ"]
	if rd.Item.Status != "read" || !rd.Item.Finished {
		t.Fatalf("read book should be finished: %+v", rd.Item)
	}
	if rd.Item.ProgressPercent != 100 {
		t.Fatalf("finished book progress: want 100, got %d", rd.Item.ProgressPercent)
	}
	if rd.Item.Authors != "Writer, Bob, Coauthor, Cy" {
		t.Fatalf("multi-author CSV wrong: %q", rd.Item.Authors)
	}
	if rd.BookID != 0 {
		t.Fatalf("ASIN with no Hardcover match should carry no linkage: bookID=%d", rd.BookID)
	}

	// want book: status want, not finished, progress 0.
	w := byASIN["ASIN_WANT"]
	if w.Item.Status != "want" || w.Item.Finished || w.Item.ProgressPercent != 0 {
		t.Fatalf("want book wrong: %+v", w.Item)
	}
	if w.Item.Title != "The Unopened Book" {
		t.Fatalf("want book title not mapped: %q", w.Item.Title)
	}
}

// TestSweepNoHardcover: with a nil resolver, rows still ingest with full Amazon
// metadata but no linkage.
func TestSweepNoHardcover(t *testing.T) {
	fk := &fakeKindle{
		library: []amazon.CloudLibraryItem{
			{ASIN: "ASIN_X", Title: "Standalone", Authors: []string{"Solo, Sam:"}, PercentageRead: 25, CoverURL: "https://img/x.jpg", ResourceType: "EBOOK"},
		},
	}
	s := &Service{kindle: fk}
	items, hcConnected, err := s.sweep(context.Background(), &amazon.DeviceCredential{}, "bob", nil)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if hcConnected {
		t.Fatal("hcConnected should be false with a nil resolver")
	}
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	it := items[0]
	if it.Item.ExternalID != "ASIN_X" || it.Item.Status != "reading" || it.Item.ProgressPercent != 25 {
		t.Fatalf("row mapping wrong: %+v", it.Item)
	}
	// Amazon metadata present even without Hardcover; no linkage.
	if it.Item.Title != "Standalone" || it.Item.Authors != "Solo, Sam" || it.Item.CoverURL == "" {
		t.Fatalf("Amazon metadata should be present without Hardcover: %+v", it.Item)
	}
	if it.BookID != 0 {
		t.Fatalf("nil-resolver sweep should carry no linkage, got bookID=%d", it.BookID)
	}
}

// TestBuildReadingItemHardcoverBackfill: Hardcover fills only fields Amazon left
// blank (title/authors/cover), never overwriting a present Amazon value.
func TestBuildReadingItemHardcoverBackfill(t *testing.T) {
	// Amazon left title blank; Hardcover backfills it, but keeps Amazon's author.
	lib := amazon.CloudLibraryItem{ASIN: "A1", Title: "", Authors: []string{"Amazon, Author:"}, PercentageRead: 60, CoverURL: ""}
	meta := &hardcover.BookMeta{BookID: 9, EditionID: 90, Title: "Backfilled Title", Authors: "Hardcover Author", CoverURL: "https://hc/c.jpg"}
	ki := buildReadingItem("u", lib, meta)
	if ki.Item.Title != "Backfilled Title" {
		t.Fatalf("blank Amazon title should be backfilled from Hardcover, got %q", ki.Item.Title)
	}
	if ki.Item.Authors != "Amazon, Author" {
		t.Fatalf("present Amazon author must win over Hardcover, got %q", ki.Item.Authors)
	}
	if ki.Item.CoverURL != "https://hc/c.jpg" {
		t.Fatalf("blank Amazon cover should be backfilled, got %q", ki.Item.CoverURL)
	}
	if ki.BookID != 9 || ki.EditionID != 90 {
		t.Fatalf("linkage not carried: %+v", ki)
	}
}

func keys(m map[string]kindleItem) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
