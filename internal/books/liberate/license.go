// license.go — step 1 of liberation: ask Audible for permission to download a
// title we own, and get back the sealed voucher + the CDN URL + the chapter tree.
// This is the Go equivalent of Libation's AudibleApi content-licensing call.
// See docs/design/catalyst-books-liberation-architecture.md §2.1.
package liberate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/amazon"
)

// ErrLicenseDenied is the TERMINAL "Amazon says no" outcome (status_code
// "Denied"): the title is not owned, was returned, or is not downloadable in
// this marketplace. It must never be retried — the liberation service maps it to
// liberation_status='denied' and moves on. Distinguishing it from a transport
// failure is the whole point of it being its own error: a retry loop on Denied
// is how you get an account flagged.
var ErrLicenseDenied = errors.New("liberate: Audible denied the content license")

// licenseRequestBody is the POST payload. It is marshalled ONCE and the exact
// bytes are both signed and sent (see amazon.SignedPost) — never re-marshalled.
//
// The field set mirrors what the Audible iOS app sends. Two choices are worth
// knowing:
//
//   - drm_types is limited to Mpeg + Adrm. The streaming DRMs (Widevine,
//     PlayReady, FairPlay, Hls*) are deliberately NOT requested: asking for them
//     invites Amazon to hand back a streaming manifest instead of a downloadable
//     file, which is not what a backup tool wants.
//   - chapter_titles_type "Tree" returns NESTED chapters (parts containing
//     chapters). chapters.go flattens them; asking for Flat instead would lose
//     the part titles that make a long book navigable. NOTE (live-verified
//     2026-08-24): Tree does NOT guarantee nesting — a 37-chapter title came
//     back entirely flat. The flatten must handle both shapes and must not
//     assume any depth.
type licenseRequestBody struct {
	SupportedMediaFeatures struct {
		ChapterTitlesType string   `json:"chapter_titles_type"`
		Codecs            []string `json:"codecs"`
		DrmTypes          []string `json:"drm_types"`
	} `json:"supported_media_features"`
	ConsumptionType string `json:"consumption_type"`
	Quality         string `json:"quality"`
	ResponseGroups  string `json:"response_groups"`
	Spatial         bool   `json:"spatial"`
}

// buildLicenseRequest returns the marshalled request body. Split out from
// RequestLicense so the unit test can assert the exact wire shape without a
// network round-trip, and so the probe can display it.
func buildLicenseRequest() ([]byte, error) {
	var b licenseRequestBody
	b.SupportedMediaFeatures.ChapterTitlesType = "Tree"
	// mp4a.40.2 = AAC-LC (the overwhelming majority of the catalogue).
	// mp4a.40.42 = xHE-AAC/USAC — requested so newer titles license at all;
	// whether ffmpeg can then REMUX them is a separate question that decrypt.go
	// records as unsupported_codec rather than guessing about here.
	b.SupportedMediaFeatures.Codecs = []string{"mp4a.40.2", "mp4a.40.42", "ec+3", "ac-4"}
	b.SupportedMediaFeatures.DrmTypes = []string{"Mpeg", "Adrm"}
	b.ConsumptionType = "Download"
	b.Quality = "High"
	b.ResponseGroups = "content_reference,chapter_info,pdf_url,last_position_heard"
	b.Spatial = false
	return json.Marshal(&b)
}

// Chapter is one node of Audible's chapter tree. Chapters nest via Chapters,
// which is why chapter_titles_type is "Tree".
type Chapter struct {
	Title         string    `json:"title"`
	StartOffsetMs int64     `json:"start_offset_ms"`
	LengthMs      int64     `json:"length_ms"`
	Chapters      []Chapter `json:"chapters,omitempty"`
}

// ChapterInfo is the chapter tree plus the branding offsets. Audible's chapter
// offsets are measured from the start of the FILE, which includes the "This is
// Audible" intro — brandIntroDurationMs/brandOutroDurationMs describe those
// segments so chapters.go can reason about them.
type ChapterInfo struct {
	BrandIntroDurationMs int64     `json:"brandIntroDurationMs"`
	BrandOutroDurationMs int64     `json:"brandOutroDurationMs"`
	RuntimeLengthMs      int64     `json:"runtime_length_ms"`
	IsAccurate           bool      `json:"is_accurate"`
	Chapters             []Chapter `json:"chapters"`
}

// ContentReference identifies the exact file Amazon is licensing.
// ContentFormat (e.g. "AAX_44_128", "AAX_22_64") is the discriminator that tells
// us whether the remuxer can handle it, and it is persisted on the row so that
// "how many of my titles are on a codec we can't handle" is a SQL question rather
// than a log-scraping exercise (design doc §10 epic D is triggered by that count).
type ContentReference struct {
	ContentFormat  string `json:"content_format"`
	FileVersion    string `json:"version"`
	SKU            string `json:"sku"`
	ASIN           string `json:"asin"`
	ContentSizeMB  int64  `json:"content_size_in_bytes"`
	Acr            string `json:"acr"`
	MarketplaceID  string `json:"marketplace"`
	TempoDurationS int64  `json:"tempo_duration_in_seconds,omitempty"`
}

// ContentMetadata is the descriptive half of the license response.
type ContentMetadata struct {
	ContentReference ContentReference `json:"content_reference"`
	ChapterInfo      ChapterInfo      `json:"chapter_info"`
	ContentURL       struct {
		OfflineURL string `json:"offline_url"`
	} `json:"content_url"`
	LastPositionHeard struct {
		PositionMs int64  `json:"position_ms"`
		Status     string `json:"status"`
	} `json:"last_position_heard"`
}

// ContentLicense is the license envelope.
type ContentLicense struct {
	StatusCode string `json:"status_code"` // "Granted" | "Denied"
	Message    string `json:"message"`
	LicenseID  string `json:"license_id"`
	// LicenseResponse is the SEALED voucher — base64, AES-CBC. It is a secret;
	// never log it, never return it through an API. voucher.go unseals it.
	LicenseResponse string          `json:"license_response"`
	ContentMetadata ContentMetadata `json:"content_metadata"`
	PDFURL          string          `json:"pdf_url"`
	Voucher         json.RawMessage `json:"voucher,omitempty"`
}

// LicenseResponse is the parsed licenserequest response.
type LicenseResponse struct {
	ContentLicense ContentLicense `json:"content_license"`
}

// Granted reports whether Amazon issued a usable license.
func (r *LicenseResponse) Granted() bool {
	return r != nil && r.ContentLicense.StatusCode == "Granted" && r.ContentLicense.LicenseResponse != ""
}

// licensePath builds the request target. The ASIN is path-escaped: it is
// external data and this string is both signed and sent, so an unescaped odd
// character would desynchronise the signature from the URL.
func licensePath(asin string) string {
	return "/1.0/content/" + url.PathEscape(asin) + "/licenserequest"
}

// RequestLicense asks Audible to license one owned title for download.
//
// Returns the parsed response AND the raw body: the raw is what the admin probe
// displays (after redaction) and what a captured fixture is made from, so the
// caller never has to re-marshal to see what actually came back.
func RequestLicense(ctx context.Context, cred *amazon.DeviceCredential, asin string) (*LicenseResponse, []byte, error) {
	if cred == nil {
		return nil, nil, amazon.ErrNotRegistered
	}
	if asin == "" {
		return nil, nil, errors.New("liberate: empty asin")
	}
	body, err := buildLicenseRequest()
	if err != nil {
		return nil, nil, fmt.Errorf("liberate: build license request: %w", err)
	}
	host := amazon.AudibleAPIHost(cred.Marketplace)
	raw, status, err := amazon.SignedPost(ctx, cred, host, licensePath(asin), body)
	if err != nil {
		return nil, raw, fmt.Errorf("liberate: licenserequest %s: %w", asin, err)
	}
	if status < 200 || status >= 300 {
		return nil, raw, fmt.Errorf("liberate: licenserequest %s: HTTP %d: %s", asin, status, truncate(string(raw), 512))
	}
	var lr LicenseResponse
	if err := json.Unmarshal(raw, &lr); err != nil {
		return nil, raw, fmt.Errorf("liberate: licenserequest %s: parse: %w", asin, err)
	}
	if lr.ContentLicense.StatusCode == "Denied" {
		return &lr, raw, fmt.Errorf("%w: %s", ErrLicenseDenied, lr.ContentLicense.Message)
	}
	if !lr.Granted() {
		return &lr, raw, fmt.Errorf("liberate: licenserequest %s: no license in response (status_code=%q)",
			asin, lr.ContentLicense.StatusCode)
	}
	return &lr, raw, nil
}

// truncate bounds a string for error messages so a full HTML error page from a
// CDN edge never lands in a log line intact.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…(truncated)"
}

// wrapf wraps err with a message, preserving errors.Is/As so a Denied outcome
// stays identifiable after annotation.
func wrapf(err error, msg string) error { return fmt.Errorf("%w: %s", err, msg) }
