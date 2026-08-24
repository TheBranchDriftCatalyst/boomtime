package liberate

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// The request body is signed byte-for-byte, so its exact shape is part of the
// protocol rather than an implementation detail. These assertions are on the
// MARSHALLED JSON, not on the struct, because that is what gets signed.
func TestBuildLicenseRequestWireShape(t *testing.T) {
	raw, err := buildLicenseRequest()
	if err != nil {
		t.Fatalf("buildLicenseRequest: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}
	if got["consumption_type"] != "Download" {
		t.Errorf("consumption_type = %v, want Download — anything else licenses a STREAM, not a file", got["consumption_type"])
	}
	if got["quality"] != "High" {
		t.Errorf("quality = %v, want High", got["quality"])
	}

	feats, ok := got["supported_media_features"].(map[string]any)
	if !ok {
		t.Fatal("supported_media_features missing")
	}
	if feats["chapter_titles_type"] != "Tree" {
		t.Errorf("chapter_titles_type = %v, want Tree (Flat would lose part titles)", feats["chapter_titles_type"])
	}

	// The DRM list is a deliberate restriction, not an oversight: requesting a
	// streaming DRM invites Amazon to answer with a manifest instead of a
	// downloadable file, which is useless to a backup tool.
	drm := toStrings(t, feats["drm_types"])
	if !contains(drm, "Adrm") {
		t.Errorf("drm_types = %v, must include Adrm (the downloadable AAXC scheme)", drm)
	}
	for _, streaming := range []string{"Widevine", "PlayReady", "FairPlay", "Hls", "Dash", "HlsCmaf"} {
		if contains(drm, streaming) {
			t.Errorf("drm_types includes streaming DRM %q — that risks a manifest instead of a file", streaming)
		}
	}

	// mp4a.40.42 must be requested or newer xHE-AAC titles will not license at
	// all. Whether we can then REMUX them is a separate question.
	if codecs := toStrings(t, feats["codecs"]); !contains(codecs, "mp4a.40.2") || !contains(codecs, "mp4a.40.42") {
		t.Errorf("codecs = %v, want both mp4a.40.2 and mp4a.40.42", codecs)
	}

	groups, _ := got["response_groups"].(string)
	for _, need := range []string{"content_reference", "chapter_info", "pdf_url"} {
		if !strings.Contains(groups, need) {
			t.Errorf("response_groups %q missing %q", groups, need)
		}
	}
}

// The path is BOTH signed and sent, so an unescaped odd character would
// desynchronise the signature from the URL — an Amazon-side auth error that
// looks nothing like a local encoding bug.
func TestLicensePathEscapesASIN(t *testing.T) {
	tests := map[string]string{
		"B0BTESTASIN":   "/1.0/content/B0BTESTASIN/licenserequest",
		"weird/../asin": "/1.0/content/weird%2F..%2Fasin/licenserequest",
		"with space":    "/1.0/content/with%20space/licenserequest",
	}
	for asin, want := range tests {
		if got := licensePath(asin); got != want {
			t.Errorf("licensePath(%q) = %q, want %q", asin, got, want)
		}
	}
}

func TestLicenseResponseGranted(t *testing.T) {
	tests := []struct {
		name string
		json string
		want bool
	}{
		{"granted with a voucher", `{"content_license":{"status_code":"Granted","license_response":"abc"}}`, true},
		{"granted but no voucher", `{"content_license":{"status_code":"Granted"}}`, false},
		{"denied", `{"content_license":{"status_code":"Denied","message":"not owned"}}`, false},
		{"empty", `{}`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var lr LicenseResponse
			if err := json.Unmarshal([]byte(tc.json), &lr); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := lr.Granted(); got != tc.want {
				t.Errorf("Granted() = %v, want %v", got, tc.want)
			}
		})
	}
	var nilResp *LicenseResponse
	if nilResp.Granted() {
		t.Error("nil receiver reported Granted")
	}
}

// The nested chapter tree and the branding offsets both have to survive parsing,
// because chapters.go builds the M4B chapter marks from them.
func TestLicenseResponseParsesChapterTree(t *testing.T) {
	const fixture = `{"content_license":{
		"status_code":"Granted","license_response":"sealed",
		"content_metadata":{
			"content_reference":{"content_format":"AAX_44_128","sku":"BK_TEST_001","version":"4"},
			"content_url":{"offline_url":"https://cds.audible.com/x.aaxc?Policy=abc"},
			"chapter_info":{
				"brandIntroDurationMs":2043,"brandOutroDurationMs":5061,
				"runtime_length_ms":3600000,"is_accurate":true,
				"chapters":[
					{"title":"Part One","start_offset_ms":0,"length_ms":1800000,
					 "chapters":[{"title":"Chapter 1","start_offset_ms":2043,"length_ms":900000}]},
					{"title":"Part Two","start_offset_ms":1800000,"length_ms":1800000}
				]}}}}`

	var lr LicenseResponse
	if err := json.Unmarshal([]byte(fixture), &lr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !lr.Granted() {
		t.Fatal("fixture should be Granted")
	}
	ci := lr.ContentLicense.ContentMetadata.ChapterInfo
	if len(ci.Chapters) != 2 {
		t.Fatalf("top-level chapters = %d, want 2", len(ci.Chapters))
	}
	if len(ci.Chapters[0].Chapters) != 1 {
		t.Errorf("nested chapters lost: %+v", ci.Chapters[0])
	}
	if ci.BrandIntroDurationMs != 2043 || ci.BrandOutroDurationMs != 5061 {
		t.Errorf("brand offsets = %d/%d, want 2043/5061", ci.BrandIntroDurationMs, ci.BrandOutroDurationMs)
	}
	if ref := lr.ContentLicense.ContentMetadata.ContentReference; ref.ContentFormat != "AAX_44_128" {
		t.Errorf("content_format = %q", ref.ContentFormat)
	}
	if lr.ContentLicense.ContentMetadata.ContentURL.OfflineURL == "" {
		t.Error("offline_url did not parse")
	}
}

// ErrLicenseDenied must stay errors.Is-comparable after annotation: the retry
// policy depends on it, and retrying a Denied title in a loop is how an account
// gets flagged.
func TestErrLicenseDeniedSurvivesWrapping(t *testing.T) {
	if !errors.Is(wrapf(ErrLicenseDenied, "not owned"), ErrLicenseDenied) {
		t.Fatal("wrapped denial is not errors.Is-comparable to ErrLicenseDenied")
	}
}

func toStrings(t *testing.T, v any) []string {
	t.Helper()
	arr, ok := v.([]any)
	if !ok {
		t.Fatalf("expected an array, got %T", v)
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		out = append(out, e.(string))
	}
	return out
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}
