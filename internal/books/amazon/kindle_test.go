package amazon

import (
	"testing"
	"time"
)

// datasetsFixture is a captured whispersync datasets response: the shelves live
// under namespace CloudCollections.Items; a device-sync dataset in another
// namespace is present to prove the filter drops it.
const datasetsFixture = `{
  "datasets": [
    {"identifier": "ds-cur",   "name": "Currently Readings", "namespace": "CloudCollections.Items", "links": {"records": "/whispersync/v2/data/123/datasets/ds-cur/records"}},
    {"identifier": "ds-done",  "name": "Done Reading",       "namespace": "CloudCollections.Items", "links": {"records": "/whispersync/v2/data/123/datasets/ds-done/records"}},
    {"identifier": "ds-want",  "name": "Have Not Read",      "namespace": "CloudCollections.Items", "links": {"records": "/whispersync/v2/data/123/datasets/ds-want/records"}},
    {"identifier": "ds-exp",   "name": "The Expanse",        "namespace": "CloudCollections.Items", "links": {"records": "/whispersync/v2/data/123/datasets/ds-exp/records"}},
    {"identifier": "ds-sync",  "name": "device-sync",        "namespace": "Kindle.DeviceSync",      "links": {"records": "/whispersync/v2/data/123/datasets/ds-sync/records"}}
  ]
}`

func TestParseDatasetsAndCloudCollections(t *testing.T) {
	all, err := ParseDatasets([]byte(datasetsFixture))
	if err != nil {
		t.Fatalf("ParseDatasets: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("want 5 datasets, got %d", len(all))
	}
	shelves := CloudCollections(all)
	if len(shelves) != 4 {
		t.Fatalf("CloudCollections should keep the 4 CloudCollections.Items shelves, got %d", len(shelves))
	}
	for _, d := range shelves {
		if d.Namespace != CloudCollectionsNamespace {
			t.Fatalf("filter leaked non-shelf namespace %q", d.Namespace)
		}
	}
	if all[0].Name != "Currently Readings" || all[0].Identifier != "ds-cur" {
		t.Fatalf("dataset descriptor mismatch: %+v", all[0])
	}
}

// recordsFixture is a captured collection-records response: records is a MAP
// keyed by amzn://<ASIN>/BOOK. One entry is a tombstone (isDeleted) and one key
// is malformed to prove both are handled.
const recordsFixture = `{
  "records": {
    "amzn://B00ASIN001/BOOK": {"value": "1", "lastUpdatedTime": 1691000000000, "isDeleted": false},
    "amzn://B00ASIN002/BOOK": {"value": "1", "lastUpdatedTime": 1691100000000, "isDeleted": false},
    "amzn://B00DELETED/BOOK": {"value": "0", "lastUpdatedTime": 1691200000000, "isDeleted": true},
    "not-an-amzn-key":        {"value": "1", "lastUpdatedTime": 1691300000000, "isDeleted": false}
  }
}`

func TestParseCollectionRecords(t *testing.T) {
	recs, err := ParseCollectionRecords([]byte(recordsFixture))
	if err != nil {
		t.Fatalf("ParseCollectionRecords: %v", err)
	}
	got := map[string]CollectionRecord{}
	for _, r := range recs {
		got[r.ASIN] = r
	}
	// The malformed key is dropped; the tombstone is surfaced (IsDeleted) so the
	// domain layer can skip it.
	if _, ok := got[""]; ok {
		t.Fatalf("malformed key should not yield an ASIN")
	}
	if len(got) != 3 {
		t.Fatalf("want 3 parsed ASINs, got %d: %v", len(got), got)
	}
	r1, ok := got["B00ASIN001"]
	if !ok {
		t.Fatalf("ASIN B00ASIN001 not extracted from amzn://B00ASIN001/BOOK")
	}
	if r1.IsDeleted {
		t.Fatalf("B00ASIN001 should not be deleted")
	}
	if r1.LastUpdated == nil || r1.LastUpdated.UTC().Year() != 2023 {
		t.Fatalf("lastUpdatedTime (ms epoch) not parsed: %+v", r1.LastUpdated)
	}
	if del := got["B00DELETED"]; !del.IsDeleted {
		t.Fatalf("B00DELETED tombstone should carry IsDeleted=true")
	}
}

func TestAsinFromRecordKey(t *testing.T) {
	cases := map[string]string{
		"amzn://B00ASIN001/BOOK": "B00ASIN001",
		"amzn://B00ASIN002/EBOK": "B00ASIN002",
		"amzn://B00NOSUFFIX":     "B00NOSUFFIX",
		"garbage":                "",
		"":                       "",
	}
	for key, want := range cases {
		if got := asinFromRecordKey(key); got != want {
			t.Fatalf("asinFromRecordKey(%q) = %q, want %q", key, got, want)
		}
	}
}

// sidecarFixture is a captured Fiona CDE sidecar response: a kindle.lpr
// (last-page-read) record among an annotation record, to prove selection.
const sidecarFixture = `{
  "payload": {
    "records": [
      {"type": "kindle.annotation.bookmark", "position": 42, "lastUpdated": 1690000000000},
      {"type": "kindle.lpr", "position": 3801, "lastUpdated": 1691234567000}
    ]
  }
}`

func TestParseSidecar(t *testing.T) {
	sp := ParseSidecar([]byte(sidecarFixture))
	if sp == nil {
		t.Fatal("ParseSidecar returned nil for a payload containing a kindle.lpr record")
	}
	if sp.Position != 3801 {
		t.Fatalf("LPR position: want 3801, got %d (should pick the kindle.lpr record, not the bookmark)", sp.Position)
	}
	if sp.LastUpdated == nil {
		t.Fatal("LPR lastUpdated not parsed")
	}
	if want := time.UnixMilli(1691234567000).UTC(); !sp.LastUpdated.Equal(want) {
		t.Fatalf("LPR lastUpdated: want %v, got %v", want, sp.LastUpdated)
	}
}

func TestParseSidecarNoLPR(t *testing.T) {
	body := `{"payload":{"records":[{"type":"kindle.annotation.note","position":9}]}}`
	if sp := ParseSidecar([]byte(body)); sp != nil {
		t.Fatalf("expected nil when no kindle.lpr record present, got %+v", sp)
	}
}

func TestParseSidecarPercent(t *testing.T) {
	body := `{"payload":{"records":[{"type":"kindle.lpr","position":100,"percent":63.5,"lastUpdated":"2023-08-05T12:00:00Z"}]}}`
	sp := ParseSidecar([]byte(body))
	if sp == nil || sp.Percent == nil {
		t.Fatalf("expected a parsed percent, got %+v", sp)
	}
	if *sp.Percent != 63.5 {
		t.Fatalf("percent: want 63.5, got %v", *sp.Percent)
	}
}
