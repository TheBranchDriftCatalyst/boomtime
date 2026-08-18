package amazon

// kindle.go — the KINDLE (ebook) wire surface. Where amazon.go owns the shared
// device credential and client.go owns generic device-signed GETs, this file
// adds the Kindle-specific hosts + a small whispersync/sidecar client the
// catalyst-books domain drives. Everything here authenticates off the SAME
// DeviceCredential that Audible uses (Sign / SignedGet) — one Amazon
// registration authenticates both; only the HOSTS differ per surface.
//
// Endpoint map (reverse-engineered live, docs/design/catalyst-books-domain-
// architecture.md §6):
//
//	whispersync datasets  host api.amazon.com
//	  /whispersync/v2/data/<customer_id>/datasets
//	  -> the user's SHELVES (namespace "CloudCollections.Items")
//	collection records    host api.amazon.com
//	  /whispersync/v2/data/<customer_id>/datasets/<dataset_id>/records
//	  -> a MAP keyed by amzn://<ASIN>/BOOK (the books on that shelf)
//	per-book sidecar      host cde-ta-g7g.amazon.com
//	  /FionaCDEServiceEngine/sidecar?type=EBOK&key=<ASIN>
//	  -> kindle.lpr (last-page-read: position + timestamp)
//
// The parse functions are pure (body -> typed) so they unit-test against real
// captured fixtures without a network. The KindleClient methods are the thin
// SignedGet + parse wrappers the domain calls at runtime.

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Kindle API hosts. These are marketplace-independent (unlike the Audible
// api.audible.<tld> host) — the whispersync + Fiona CDE services live on a
// single global host regardless of the account's Marketplace.
const (
	// WhispersyncHost serves CloudCollections (shelves) + their records.
	WhispersyncHost = "api.amazon.com"
	// KindleCDEHost serves the per-book Fiona CDE sidecar (reading position).
	KindleCDEHost = "cde-ta-g7g.amazon.com"

	// CloudCollectionsNamespace is the whispersync dataset namespace whose
	// datasets are the user's SHELVES (Currently Reading / Done Reading / …).
	// Other namespaces (device sync state, annotations) are ignored.
	CloudCollectionsNamespace = "CloudCollections.Items"
)

// Dataset is one whispersync dataset descriptor. For the books domain the ones
// that matter carry Namespace == CloudCollectionsNamespace — those are shelves,
// and Name is the shelf label ("Currently Readings", a series name, …).
type Dataset struct {
	Identifier string `json:"identifier"`
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
}

// CollectionRecord is one book's membership on a shelf, parsed out of the
// records map. ASIN is extracted from the amzn://<ASIN>/BOOK map key; IsDeleted
// tombstones a removed entry; LastUpdated is the record's own timestamp.
type CollectionRecord struct {
	ASIN        string
	LastUpdated *time.Time
	IsDeleted   bool
}

// SidecarPosition is the reading position snapshot from a kindle.lpr record:
// the last-page-read Position, an optional Percent (rarely present — position is
// usually an opaque locator with no total, so progress is NOT derivable from it
// alone), and the LastUpdated timestamp that dates the read.
type SidecarPosition struct {
	Position    int64
	Percent     *float64
	LastUpdated *time.Time
}

// KindleClient performs device-signed Kindle whispersync/sidecar calls. It is a
// thin wrapper — the credential + signing come from the shared amazon package;
// the client only knows the Kindle hosts + response shapes.
type KindleClient struct{}

// NewKindleClient constructs the Kindle whispersync/sidecar client.
func NewKindleClient() *KindleClient { return &KindleClient{} }

// Datasets GETs the whispersync dataset list for the credential's customer id
// and returns the parsed descriptors. cred.CustomerID (the numeric user_id, NOT
// an ASIN) keys the path.
func (k *KindleClient) Datasets(ctx context.Context, cred *DeviceCredential) ([]Dataset, error) {
	if cred == nil {
		return nil, ErrNotRegistered
	}
	if strings.TrimSpace(cred.CustomerID) == "" {
		return nil, fmt.Errorf("amazon kindle: device credential has no customer_id (needed for whispersync)")
	}
	path := "/whispersync/v2/data/" + cred.CustomerID + "/datasets"
	body, status, err := SignedGet(ctx, cred, WhispersyncHost, path)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("whispersync datasets returned HTTP %d: %s", status, kindleSnippet(body))
	}
	return ParseDatasets(body)
}

// CollectionRecords GETs one shelf dataset's records and returns the (non-
// deleted) book memberships parsed from the amzn://<ASIN>/BOOK map keys.
func (k *KindleClient) CollectionRecords(ctx context.Context, cred *DeviceCredential, datasetID string) ([]CollectionRecord, error) {
	if cred == nil {
		return nil, ErrNotRegistered
	}
	path := "/whispersync/v2/data/" + cred.CustomerID + "/datasets/" + datasetID + "/records"
	body, status, err := SignedGet(ctx, cred, WhispersyncHost, path)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("whispersync records returned HTTP %d: %s", status, kindleSnippet(body))
	}
	return ParseCollectionRecords(body)
}

// Sidecar GETs the per-book Fiona CDE sidecar for an ASIN and returns the
// kindle.lpr reading position, or (nil, nil) when the book has no position yet
// (a clean miss — e.g. an owned-but-unopened book).
func (k *KindleClient) Sidecar(ctx context.Context, cred *DeviceCredential, asin string) (*SidecarPosition, error) {
	if cred == nil {
		return nil, ErrNotRegistered
	}
	asin = strings.TrimSpace(asin)
	if asin == "" {
		return nil, nil
	}
	path := "/FionaCDEServiceEngine/sidecar?type=EBOK&key=" + asin
	body, status, err := SignedGet(ctx, cred, KindleCDEHost, path)
	if err != nil {
		return nil, err
	}
	// A 404 here means "no position recorded for this book" — treat as a clean
	// miss, not an error, so a want-shelf book the user never opened doesn't fail
	// the sweep.
	if status == 404 {
		return nil, nil
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("kindle sidecar %q returned HTTP %d: %s", asin, status, kindleSnippet(body))
	}
	return ParseSidecar(body), nil
}

// ---------------------------------------------------------------------------
// Pure parsers (unit-tested against captured fixtures)
// ---------------------------------------------------------------------------

// ParseDatasets extracts the dataset descriptors from a whispersync datasets
// response: {"datasets":[{identifier,name,namespace,links{records}}]}.
func ParseDatasets(body []byte) ([]Dataset, error) {
	var resp struct {
		Datasets []Dataset `json:"datasets"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("whispersync datasets parse failed: %w (body: %s)", err, kindleSnippet(body))
	}
	return resp.Datasets, nil
}

// CloudCollections filters a dataset list down to the CloudCollections shelves.
func CloudCollections(datasets []Dataset) []Dataset {
	out := make([]Dataset, 0, len(datasets))
	for _, d := range datasets {
		if d.Namespace == CloudCollectionsNamespace {
			out = append(out, d)
		}
	}
	return out
}

// ParseCollectionRecords extracts book memberships from a records response. The
// records field is a MAP keyed by amzn://<ASIN>/BOOK; each value carries a
// lastUpdatedTime + isDeleted. Deleted tombstones are surfaced (IsDeleted=true)
// so the caller can decide — but records whose ASIN can't be parsed are skipped.
func ParseCollectionRecords(body []byte) ([]CollectionRecord, error) {
	var resp struct {
		Records map[string]struct {
			LastUpdatedTime json.RawMessage `json:"lastUpdatedTime"`
			IsDeleted       bool            `json:"isDeleted"`
		} `json:"records"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("whispersync records parse failed: %w (body: %s)", err, kindleSnippet(body))
	}
	out := make([]CollectionRecord, 0, len(resp.Records))
	for key, rec := range resp.Records {
		asin := asinFromRecordKey(key)
		if asin == "" {
			continue
		}
		out = append(out, CollectionRecord{
			ASIN:        asin,
			LastUpdated: parseKindleTime(rec.LastUpdatedTime),
			IsDeleted:   rec.IsDeleted,
		})
	}
	return out, nil
}

// asinFromRecordKey pulls the ASIN out of an "amzn://<ASIN>/BOOK" record key.
// Returns "" for any key that doesn't fit the shape.
func asinFromRecordKey(key string) string {
	const prefix = "amzn://"
	if !strings.HasPrefix(key, prefix) {
		return ""
	}
	rest := key[len(prefix):]
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	return strings.TrimSpace(rest)
}

// ParseSidecar extracts the kindle.lpr (last-page-read) position from a sidecar
// response: {"payload":{"records":[{type:"kindle.lpr", <position>, <timestamp>}]}}.
// Returns nil when there is no kindle.lpr record (nothing read yet). Field names
// vary slightly across captures, so position + timestamp are read defensively.
func ParseSidecar(body []byte) *SidecarPosition {
	var resp struct {
		Payload struct {
			Records []map[string]json.RawMessage `json:"records"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil
	}
	for _, rec := range resp.Payload.Records {
		if lprType(rec) != "kindle.lpr" {
			continue
		}
		return sidecarFromRecord(rec)
	}
	return nil
}

func lprType(rec map[string]json.RawMessage) string {
	var t string
	if raw, ok := rec["type"]; ok {
		_ = json.Unmarshal(raw, &t)
	}
	return t
}

// sidecarFromRecord walks a kindle.lpr record: a position-ish integer field, an
// optional percent, and a timestamp-ish field. The exact key names differ
// between captures (position / pos / value; lastUpdated / timestamp /
// lastUpdatedTime), so match by substring.
func sidecarFromRecord(rec map[string]json.RawMessage) *SidecarPosition {
	sp := &SidecarPosition{}
	for k, v := range rec {
		lk := strings.ToLower(k)
		switch {
		case sp.LastUpdated == nil && (strings.Contains(lk, "updated") || strings.Contains(lk, "timestamp") || strings.Contains(lk, "time")):
			sp.LastUpdated = parseKindleTime(v)
		case sp.Percent == nil && strings.Contains(lk, "percent"):
			if f := parseFloat(v); f != nil {
				sp.Percent = f
			}
		case sp.Position == 0 && (lk == "position" || lk == "pos" || strings.HasSuffix(lk, "position") || lk == "value"):
			sp.Position = parseInt(v)
		}
	}
	return sp
}

func parseInt(raw json.RawMessage) int64 {
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		if i, err := n.Int64(); err == nil {
			return i
		}
		if f, err := n.Float64(); err == nil {
			return int64(f)
		}
	}
	// A stringified number ("1234").
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if i, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
			return i
		}
	}
	return 0
}

func parseFloat(raw json.RawMessage) *float64 {
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		if f, err := n.Float64(); err == nil {
			return &f
		}
	}
	return nil
}

// parseKindleTime reads a whispersync/sidecar timestamp — an epoch number
// (seconds or milliseconds) or an RFC3339/date string — into UTC, or nil.
func parseKindleTime(raw json.RawMessage) *time.Time {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" || string(raw) == `""` {
		return nil
	}
	if raw[0] == '"' {
		var str string
		if err := json.Unmarshal(raw, &str); err != nil {
			return nil
		}
		str = strings.TrimSpace(str)
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
			if t, err := time.Parse(layout, str); err == nil {
				u := t.UTC()
				return &u
			}
		}
		if n, err := strconv.ParseInt(str, 10, 64); err == nil {
			return epochToTime(n)
		}
		return nil
	}
	if n, err := strconv.ParseInt(string(raw), 10, 64); err == nil {
		return epochToTime(n)
	}
	// A float epoch (e.g. 1691000000.0).
	if f := parseFloat(raw); f != nil {
		return epochToTime(int64(*f))
	}
	return nil
}

// epochToTime interprets a positive epoch as seconds, or milliseconds when the
// magnitude is too large to be seconds (> 1e12).
func epochToTime(n int64) *time.Time {
	if n <= 0 {
		return nil
	}
	var t time.Time
	if n > 1_000_000_000_000 {
		t = time.UnixMilli(n).UTC()
	} else {
		t = time.Unix(n, 0).UTC()
	}
	return &t
}

func kindleSnippet(b []byte) string {
	const max = 300
	s := strings.TrimSpace(string(b))
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}
