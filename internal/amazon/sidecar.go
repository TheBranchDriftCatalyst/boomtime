// sidecar.go — the FORWARD Kindle reading-TIME position source (gaka-books). It
// device-signs a GET against the Fiona CDE sidecar for one book and returns its
// current last-page-read POSITION so the books domain can append a sample and
// gap-sum consecutive samples into reading SESSIONS (the heartbeat model, applied
// to reading position instead of edit activity).
//
// WIRE SHAPE (captured live via scratchpad/kindle_sidecar_probe.py):
//
//	GET https://cde-ta-g7g.amazon.com/FionaCDEServiceEngine/sidecar?type=EBOK&key=<ASIN>
//	(ADP device-signed, same signing as every other amazon call)
//	200 application/json:
//	  {"md5":"<hex>","payload":{"records":[
//	     {"type":"kindle.lpr","location":"9283",
//	      "annotationId":"<deviceId>-<ASIN>-EBOK-furthest-page-read",
//	      "creationTime":"2026-08-07 03:03:02.0"}
//	  ]}}
//	404 (tiny body): the book has no reading state → clean miss (most books).
//
// It is a SNAPSHOT — exactly one kindle.lpr record: the CURRENT furthest-page-read
// position + when Amazon set it. NOT a history. So the composition stays
// forward-only: each poll captures (location, creationTime); an unchanged
// creationTime dedupes to no new sample (the (owner,asin,sampled_at) unique
// index), and a location that advanced at a new creationTime is a reading event
// the composition gap-sums into a session. No past minutes can be backfilled from
// this source.
//
// NOTE: kindle.go carries an older best-effort ParseSidecar/SidecarPosition pair
// from the whispersync era (it guessed at position/percent field names). This
// file is the finalized, live-verified path the reading-time job uses; the
// kindle.go pair is now superseded and can be retired in a later cleanup.

package amazon

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// KindleSidecar is the forward reading-time position source the books domain
// depends on: given a device credential + ASIN, return the book's current
// last-page-read position and when it was observed. `ok` is false for a clean
// miss (the book has no recorded position yet — a 404), distinct from an error.
type KindleSidecar interface {
	FetchLastPagePosition(ctx context.Context, cred *DeviceCredential, asin string) (position int64, sampledAt time.Time, ok bool, err error)
}

// KindleSidecarClient is the real device-signed implementation. Thin: the
// credential + signing come from the shared amazon package; it only knows the
// Fiona CDE host + the (now-finalized) response shape.
type KindleSidecarClient struct{}

// NewKindleSidecarClient constructs the Fiona CDE sidecar position source.
func NewKindleSidecarClient() *KindleSidecarClient { return &KindleSidecarClient{} }

var _ KindleSidecar = (*KindleSidecarClient)(nil)

// FetchLastPagePosition device-signs a GET against
// cde-ta-g7g.amazon.com/FionaCDEServiceEngine/sidecar?type=EBOK&key=<asin> and
// returns the last-page-read position + Amazon's own creationTime for it. A 404
// is a clean miss (ok=false, no error) — a stateless (never-opened) book has no
// position.
func (c *KindleSidecarClient) FetchLastPagePosition(ctx context.Context, cred *DeviceCredential, asin string) (int64, time.Time, bool, error) {
	if cred == nil {
		return 0, time.Time{}, false, ErrNotRegistered
	}
	if asin == "" {
		return 0, time.Time{}, false, nil
	}
	path := "/FionaCDEServiceEngine/sidecar?type=EBOK&key=" + asin
	body, status, err := SignedGet(ctx, cred, KindleCDEHost, path)
	if err != nil {
		return 0, time.Time{}, false, err
	}
	// 404 = no position recorded for this book → clean miss, not an error, so a
	// never-opened book doesn't fail the poll sweep (mirrors KindleClient.Sidecar).
	if status == 404 {
		return 0, time.Time{}, false, nil
	}
	if status < 200 || status >= 300 {
		return 0, time.Time{}, false, sidecarHTTPError(asin, status, body)
	}
	return parseLastPagePosition(asin, body)
}

// parseLastPagePosition extracts (position, creationTime) from a sidecar 200
// body: the first payload.records entry of type "kindle.lpr". `location` is a
// STRING integer (the Kindle position/offset); `creationTime` is a
// space-separated datetime with an optional fractional second. A 200 with no
// kindle.lpr record is treated as a clean miss (ok=false) rather than an error.
func parseLastPagePosition(asin string, body []byte) (int64, time.Time, bool, error) {
	var resp struct {
		Payload struct {
			Records []struct {
				Type         string `json:"type"`
				Location     string `json:"location"`
				CreationTime string `json:"creationTime"`
			} `json:"records"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, time.Time{}, false, fmt.Errorf("kindle sidecar %q: parse failed: %w (body: %s)", asin, err, kindleSnippet(body))
	}
	for _, rec := range resp.Payload.Records {
		if rec.Type != "kindle.lpr" {
			continue
		}
		loc := strings.TrimSpace(rec.Location)
		if loc == "" {
			continue
		}
		position, perr := strconv.ParseInt(loc, 10, 64)
		if perr != nil {
			return 0, time.Time{}, false, fmt.Errorf("kindle sidecar %q: non-integer location %q: %w", asin, rec.Location, perr)
		}
		return position, parseSidecarCreationTime(rec.CreationTime), true, nil
	}
	return 0, time.Time{}, false, nil
}

// parseSidecarCreationTime reads the kindle.lpr creationTime — a space-separated
// datetime with an optional trailing fractional second, e.g. "2026-08-07
// 03:03:02.0". No zone is carried, so it is parsed as UTC: session composition is
// delta-based (zone-independent) and day buckets inherit UTC. Returns the zero
// time on an empty/unparseable value so the caller can fall back to poll time.
func parseSidecarCreationTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	// The ".9" fractional form makes the fraction optional, so this one layout
	// covers both "…02.0" and "…02"; the extra layouts are belt-and-suspenders.
	for _, layout := range []string{
		"2006-01-02 15:04:05.9",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05.9",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// sidecarHTTPError wraps a non-2xx/404 sidecar status with a truncated body
// snippet (reusing kindle.go's kindleSnippet).
func sidecarHTTPError(asin string, status int, body []byte) error {
	return fmt.Errorf("kindle sidecar %q: HTTP %d: %s", asin, status, kindleSnippet(body))
}
