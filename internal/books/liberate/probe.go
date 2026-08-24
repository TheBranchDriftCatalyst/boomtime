// probe.go — the LIVE VERIFICATION harness (boom-w20s.19).
//
// The liberation protocol has a handful of details that were derived from
// upstream (mkb79/audible-cli, Libation) rather than observed on this tree, and
// every one of them fails in a way that does NOT look like a bug:
//
//   - a wrong POST canonical string is an Amazon-side auth error, not a local one
//   - a wrong voucher key derivation decrypts CLEANLY to garbage
//   - a codec we cannot remux only reveals itself deep inside ffmpeg
//
// So rather than write the assumptions into the pipeline and wait to be
// surprised, this file exercises them against the real account and REPORTS what
// actually happened. It sweeps every candidate key derivation and names the one
// that worked, which is precisely the question boom-w20s.19 exists to answer.
//
// It mounts on the EXISTING admin books-diagnostics surface as source=liberation
// (internal/books/admin/diagnostics.go), so it appears as a third button next to
// Audible and Kindle rather than as a new page.
//
// REDACTION. This is an admin-facing dump of a response that contains a content
// voucher and a capability URL. Nothing in the returned report may contain
// material an attacker could use: redactLicenseBody strips the voucher, and only
// the scheme+host of the CDN URL is shown. See the package doc.
package liberate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/amazon"
)

// Verdict is a probe outcome. It is deliberately coarser than an HTTP status —
// the question a probe answers is "is this assumption correct", not "what did
// the server say".
type Verdict string

const (
	VerdictPass Verdict = "pass"
	VerdictFail Verdict = "fail"
	VerdictWarn Verdict = "warn"
	VerdictSkip Verdict = "skip"
)

// Probe is one verification result, shaped to render through the existing
// admin diagnostics ProbeView.
type Probe struct {
	Name     string          `json:"name"`
	Endpoint string          `json:"endpoint,omitempty"`
	Status   int             `json:"status,omitempty"`
	OK       bool            `json:"ok"`
	Verdict  Verdict         `json:"verdict,omitempty"`
	Detail   string          `json:"detail,omitempty"`
	Error    string          `json:"error,omitempty"`
	Body     json.RawMessage `json:"body,omitempty"`
	BodyText string          `json:"bodyText,omitempty"`
}

// Report is the full liberation verification sweep.
type Report struct {
	ASIN   string  `json:"asin"`
	Probes []Probe `json:"probes"`
}

// RunProbes executes the verification sweep against one owned title.
//
// asin may be empty, in which case the first title in the user's Audible library
// is used — an operator running this from the admin UI should not have to go
// find an ASIN by hand.
func RunProbes(ctx context.Context, cred *amazon.DeviceCredential, asin string) Report {
	rep := Report{ASIN: asin}
	if cred == nil {
		rep.Probes = append(rep.Probes, Probe{
			Name: "device credential", Verdict: VerdictFail,
			Error: "no Amazon credential — connect Amazon in Settings first",
		})
		return rep
	}

	// 0. Credential completeness. CustomerID is a hard prerequisite for the
	// voucher derivation and older registrations predate its capture, so check it
	// FIRST — otherwise every downstream probe fails for a misleading reason.
	rep.Probes = append(rep.Probes, credentialProbe(cred))

	if asin == "" {
		picked, p := pickFirstLibraryASIN(ctx, cred)
		rep.Probes = append(rep.Probes, p)
		if picked == "" {
			return rep
		}
		asin = picked
		rep.ASIN = picked
	}

	// 1. The POST canonical string. This is the first time Sign() has ever been
	// called with a non-empty body on this tree.
	lr, raw, status, licProbe := licenseProbe(ctx, cred, asin)
	rep.Probes = append(rep.Probes, licProbe)
	if lr == nil || !lr.Granted() {
		return rep
	}
	_ = status
	_ = raw

	// 2. The key derivation sweep — the headline question.
	rep.Probes = append(rep.Probes, voucherOrderProbe(cred, asin, lr.ContentLicense.LicenseResponse))

	// 3. What the CDN expects, and 4. what codec we were actually handed.
	rep.Probes = append(rep.Probes, offlineURLProbe(ctx, cred, lr))
	rep.Probes = append(rep.Probes, contentFormatProbe(lr))
	rep.Probes = append(rep.Probes, chapterProbe(lr))
	return rep
}

func credentialProbe(cred *amazon.DeviceCredential) Probe {
	p := Probe{Name: "0 · device credential completeness"}
	var missing []string
	if cred.DeviceSerial == "" {
		missing = append(missing, "device_serial")
	}
	if cred.CustomerID == "" {
		missing = append(missing, "customer_id")
	}
	if cred.AdpToken == "" {
		missing = append(missing, "adp_token")
	}
	if len(missing) > 0 {
		p.Verdict = VerdictFail
		p.Error = "missing " + strings.Join(missing, ", ") + " — reconnect Amazon to capture them"
		return p
	}
	p.OK, p.Verdict = true, VerdictPass
	p.Detail = fmt.Sprintf("marketplace=%s · device_type=%s · serial + customer_id present",
		cred.Marketplace, amazon.DeviceType())
	return p
}

// pickFirstLibraryASIN grabs one owned title so the sweep has something to license.
func pickFirstLibraryASIN(ctx context.Context, cred *amazon.DeviceCredential) (string, Probe) {
	host := amazon.AudibleAPIHost(cred.Marketplace)
	path := "/1.0/library?response_groups=product_desc&num_results=1&page=1"
	p := Probe{Name: "0b · pick a title from the library", Endpoint: "https://" + host + path}

	body, status, err := amazon.SignedGet(ctx, cred, host, path)
	p.Status = status
	if err != nil {
		p.Verdict, p.Error = VerdictFail, err.Error()
		return "", p
	}
	var lib struct {
		Items []struct {
			ASIN  string `json:"asin"`
			Title string `json:"title"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &lib); err != nil || len(lib.Items) == 0 {
		p.Verdict = VerdictFail
		p.Error = "could not read a title from the library response"
		return "", p
	}
	p.OK, p.Verdict = true, VerdictPass
	p.Detail = fmt.Sprintf("using %q (%s)", lib.Items[0].Title, lib.Items[0].ASIN)
	return lib.Items[0].ASIN, p
}

// licenseProbe verifies the signed POST end-to-end.
func licenseProbe(ctx context.Context, cred *amazon.DeviceCredential, asin string) (*LicenseResponse, []byte, int, Probe) {
	host := amazon.AudibleAPIHost(cred.Marketplace)
	p := Probe{
		Name:     "1 · POST licenserequest (verifies the signed-POST canonical string)",
		Endpoint: "https://" + host + licensePath(asin),
	}
	reqBody, err := buildLicenseRequest()
	if err != nil {
		p.Verdict, p.Error = VerdictFail, err.Error()
		return nil, nil, 0, p
	}
	raw, status, err := amazon.SignedPost(ctx, cred, host, licensePath(asin), reqBody)
	p.Status = status
	if err != nil {
		p.Verdict, p.Error = VerdictFail, err.Error()
		return nil, raw, status, p
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		p.Verdict = VerdictFail
		p.Error = fmt.Sprintf("HTTP %d — the signed-POST canonical string is REJECTED. "+
			"Sign() folds the body into the canonical string; confirm the exact posted bytes are what get signed.", status)
		p.BodyText = truncate(string(raw), 4000)
		return nil, raw, status, p
	}
	var lr LicenseResponse
	if jerr := json.Unmarshal(raw, &lr); jerr != nil {
		p.Verdict, p.Error = VerdictFail, "response is not JSON: "+jerr.Error()
		p.BodyText = truncate(string(raw), 4000)
		return nil, raw, status, p
	}
	p.Body = redactLicenseBody(raw)
	switch {
	case lr.ContentLicense.StatusCode == "Denied":
		p.Verdict = VerdictWarn
		p.Detail = "signing WORKED (Amazon answered), but this title is Denied — try another ASIN. " +
			"Message: " + lr.ContentLicense.Message
	case lr.Granted():
		p.OK, p.Verdict = true, VerdictPass
		p.Detail = "signed POST accepted and a license was granted — the canonical string is correct"
	default:
		p.Verdict = VerdictWarn
		p.Detail = "unexpected status_code " + lr.ContentLicense.StatusCode
	}
	return &lr, raw, status, p
}

// voucherOrderProbe sweeps every candidate key derivation and reports which one
// actually unseals the voucher. This is the answer boom-w20s.19 exists to get.
func voucherOrderProbe(cred *amazon.DeviceCredential, asin, sealed string) Probe {
	p := Probe{Name: "2 · voucher key derivation (sweeps every candidate concat order)"}

	var winners []string
	for _, order := range AllKeyOrders {
		if _, err := DecryptVoucherWith(order, cred, asin, sealed); err == nil {
			winners = append(winners, order.String())
		}
	}
	switch len(winners) {
	case 0:
		p.Verdict = VerdictFail
		p.Error = "NO candidate order unsealed the voucher. The derivation has changed upstream — " +
			"compare against mkb79/audible-cli decrypt_voucher before guessing further."
	case 1:
		p.OK, p.Verdict = true, VerdictPass
		p.Detail = "unsealed by: " + winners[0]
		if winners[0] != OrderCanonical.String() {
			p.Verdict = VerdictWarn
			p.Detail += " — this is NOT the canonical order DecryptVoucher uses; " +
				"update keyMaterial's default and the voucher_test fixture."
		}
	default:
		p.Verdict = VerdictWarn
		p.Detail = "multiple orders unsealed the voucher (" + strings.Join(winners, ", ") +
			") — the sweep cannot disambiguate; prefer the canonical one."
		p.OK = true
	}
	return p
}

// offlineURLProbe determines what the CDN download actually requires.
//
// LIVE RESULT (2026-08-24, B09GCYRZRQ): a bare GET returns 403. The presigned
// URL is NOT self-authorizing — the ADP headers are mandatory on the download,
// not merely harmless. fetch.go must sign it.
//
// So the probe now does two passes: bare first (that is the actual question),
// then signed if the bare attempt was rejected. Without the second pass a 403
// ends the probe knowing only "not self-authorizing", and never learns whether
// the CDN honours Range — which is what decides if epic B's resumable download
// is viable. It requests a single byte and never downloads the book.
func offlineURLProbe(ctx context.Context, cred *amazon.DeviceCredential, lr *LicenseResponse) Probe {
	p := Probe{Name: "3 · CDN offline_url (auth requirement + range support)"}
	raw := lr.ContentLicense.ContentMetadata.ContentURL.OfflineURL
	if raw == "" {
		p.Verdict, p.Error = VerdictFail, "license granted but no offline_url in the response"
		return p
	}
	u, err := url.Parse(raw)
	if err != nil {
		p.Verdict, p.Error = VerdictFail, "offline_url is not a URL: "+err.Error()
		return p
	}
	// The presigned URL is a capability: showing it would let anyone reading this
	// admin page download the book. Host only.
	p.Endpoint = u.Scheme + "://" + u.Host + "/… (presigned, redacted)"

	bare, bareErr := rangeProbe(ctx, raw, nil)
	if bareErr != nil {
		p.Verdict, p.Error = VerdictFail, bareErr.Error()
		return p
	}
	if okStatus(bare.status) {
		p.OK, p.Verdict, p.Status = true, VerdictPass, bare.status
		p.Detail = "self-authorizing (no ADP headers needed) · " + describeRange(bare)
		return p
	}

	// Bare rejected → does signing fix it?
	signed, signedErr := rangeProbe(ctx, raw, func(r *http.Request) error {
		h, serr := amazon.Sign(cred, "GET", u.RequestURI(), nil, time.Now())
		if serr != nil {
			return serr
		}
		r.Header.Set("x-adp-token", h.AdpToken)
		r.Header.Set("x-adp-alg", h.AdpAlg)
		r.Header.Set("x-adp-signature", h.Signature)
		return nil
	})
	if signedErr != nil {
		p.Verdict = VerdictWarn
		p.Status = bare.status
		p.Detail = fmt.Sprintf("HTTP %d bare; signed retry failed: %v", bare.status, signedErr)
		return p
	}
	p.Status = signed.status
	if okStatus(signed.status) {
		p.OK, p.Verdict = true, VerdictWarn
		p.Detail = fmt.Sprintf("NOT self-authorizing — bare GET was HTTP %d, ADP-signed GET was HTTP %d. "+
			"fetch.go MUST sign the download. · %s", bare.status, signed.status, describeRange(signed))
		return p
	}
	p.Verdict = VerdictFail
	p.Detail = fmt.Sprintf("bare GET HTTP %d AND ADP-signed GET HTTP %d — neither works; "+
		"the download may need cookies or a different header set than the API calls",
		bare.status, signed.status)
	return p
}

// rangeResult is one conditional-GET outcome.
type rangeResult struct {
	status       int
	contentType  string
	acceptRanges string
	contentRange string
}

func okStatus(code int) bool {
	return code == http.StatusPartialContent || code == http.StatusOK
}

// describeRange reports whether the CDN honoured the one-byte Range request.
// A 206 with a Content-Range is the strong signal that resumable download works;
// a 200 means the server ignored Range and would stream the whole file.
func describeRange(r rangeResult) string {
	switch {
	case r.status == http.StatusPartialContent:
		return fmt.Sprintf("RANGE SUPPORTED (206, content-range=%q) — epic B resumable download is viable · content-type=%s",
			r.contentRange, r.contentType)
	case r.acceptRanges != "" && r.acceptRanges != "none":
		return fmt.Sprintf("range likely supported (accept-ranges=%q, got %d) · content-type=%s",
			r.acceptRanges, r.status, r.contentType)
	default:
		return fmt.Sprintf("range NOT honoured (got %d, accept-ranges=%q) — resume would need a full refetch · content-type=%s",
			r.status, r.acceptRanges, r.contentType)
	}
}

// rangeProbe issues a one-byte Range GET, optionally decorating the request.
// The body is closed immediately — this must never pull the audiobook.
func rangeProbe(ctx context.Context, rawURL string, decorate func(*http.Request) error) (rangeResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return rangeResult{}, err
	}
	req.Header.Set("Range", "bytes=0-0")
	if decorate != nil {
		if derr := decorate(req); derr != nil {
			return rangeResult{}, derr
		}
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return rangeResult{}, err
	}
	defer resp.Body.Close()
	return rangeResult{
		status:       resp.StatusCode,
		contentType:  resp.Header.Get("Content-Type"),
		acceptRanges: resp.Header.Get("Accept-Ranges"),
		contentRange: resp.Header.Get("Content-Range"),
	}, nil
}

// contentFormatProbe records the codec/DRM flavour we were handed. This is the
// input to the epic-D decision (design doc §10): whether ffmpeg can remux the
// real library or whether a native decoder is needed.
func contentFormatProbe(lr *LicenseResponse) Probe {
	ref := lr.ContentLicense.ContentMetadata.ContentReference
	p := Probe{Name: "4 · content format (drives the ffmpeg-vs-native decision)"}
	if ref.ContentFormat == "" {
		p.Verdict, p.Error = VerdictWarn, "no content_reference.content_format in the response"
		return p
	}
	p.OK, p.Verdict = true, VerdictPass
	p.Detail = fmt.Sprintf("content_format=%s · sku=%s · version=%s", ref.ContentFormat, ref.SKU, ref.FileVersion)
	// AAX_* is the classic AAC-LC family ffmpeg handles. Anything else is worth
	// flagging now rather than discovering inside a failed remux.
	if !strings.HasPrefix(ref.ContentFormat, "AAX_") {
		p.Verdict = VerdictWarn
		p.Detail += " — NOT an AAX_* format; verify ffmpeg can remux this before relying on it"
	}
	return p
}

// chapterProbe confirms the chapter tree arrived and is shaped as chapters.go expects.
func chapterProbe(lr *LicenseResponse) Probe {
	ci := lr.ContentLicense.ContentMetadata.ChapterInfo
	p := Probe{Name: "5 · chapter tree"}
	if len(ci.Chapters) == 0 {
		p.Verdict, p.Error = VerdictWarn, "no chapter_info.chapters — the M4B would have no chapter marks"
		return p
	}
	var nested int
	for _, c := range ci.Chapters {
		if len(c.Chapters) > 0 {
			nested++
		}
	}
	p.OK, p.Verdict = true, VerdictPass
	p.Detail = fmt.Sprintf("%d top-level chapters (%d with nested children) · runtime=%dms · accurate=%v · brand intro/outro=%d/%dms",
		len(ci.Chapters), nested, ci.RuntimeLengthMs, ci.IsAccurate, ci.BrandIntroDurationMs, ci.BrandOutroDurationMs)
	return p
}

// redactLicenseBody strips the secret-bearing fields from a raw license response
// so the rest can be shown to an admin. On any parse trouble it returns nothing
// rather than risking a partial dump — fail closed.
func redactLicenseBody(raw []byte) json.RawMessage {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil
	}
	cl, ok := doc["content_license"].(map[string]any)
	if !ok {
		return nil
	}
	if v, ok := cl["license_response"].(string); ok {
		cl["license_response"] = fmt.Sprintf("[redacted — %d base64 chars]", len(v))
	}
	// "voucher" is an alternate carrier for the same material on some responses.
	delete(cl, "voucher")
	if cm, ok := cl["content_metadata"].(map[string]any); ok {
		if cu, ok := cm["content_url"].(map[string]any); ok {
			if v, ok := cu["offline_url"].(string); ok {
				if u, err := url.Parse(v); err == nil {
					cu["offline_url"] = u.Scheme + "://" + u.Host + "/… (presigned, redacted)"
				} else {
					cu["offline_url"] = "[redacted]"
				}
			}
		}
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil
	}
	return out
}
