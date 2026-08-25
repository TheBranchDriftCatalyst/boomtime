package amazon

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/metrics"
)

// AudibleAPIHost returns the Audible API host for a marketplace (api.audible.<tld>).
// The Audible library + stats endpoints live here.
func AudibleAPIHost(mk Marketplace) string {
	info, ok := marketplaces[mk]
	if !ok {
		info = marketplaces[MarketplaceUS]
	}
	return "api.audible." + info.domain
}

// SignedGet performs a device-signed GET against an Amazon/Audible API host.
// pathAndQuery is the request target (e.g. "/1.0/library?response_groups=...").
// Returns (body, statusCode). The ADP signing headers come from cred; the
// canonical string format is the one thing that needs live verification (a
// mismatch surfaces as a non-2xx from Amazon, which the caller reports).
func SignedGet(ctx context.Context, cred *DeviceCredential, apiHost, pathAndQuery string) ([]byte, int, error) {
	if cred == nil {
		return nil, 0, ErrNotRegistered
	}
	// Semantic transport-outcome counter (amazon_calls_total{transport=signed}).
	// Every device-signed Amazon/Audible/Kindle GET flows through here, so this
	// one counter captures the full signed-call count. Cookie-based Cloud Reader
	// calls (readamazon.go) are counted separately with transport=cookie. The
	// generic per-host outbound metric also records these on the wire.
	metrics.AmazonCallsTotal.WithLabelValues("signed").Inc()
	h, err := Sign(cred, "GET", pathAndQuery, nil, time.Now())
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+apiHost+pathAndQuery, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("x-adp-token", h.AdpToken)
	req.Header.Set("x-adp-alg", h.AdpAlg)
	req.Header.Set("x-adp-signature", h.Signature)
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	// 64 MiB: a full Audible /1.0/library page (up to 1000 items × the wide
	// response_groups incl. product_extended_attrs) is tens of MB — the old 8 MiB
	// cap truncated it mid-JSON ("unexpected end of JSON input").
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	return body, resp.StatusCode, nil
}

// DownloadUserAgent is the User-Agent the Audible CDN expects on a content
// download. It is PAIRED WITH deviceType and must not drift from it.
//
// Verified against Libation's source (rmcrackan/AudibleApi,
// AudibleApi/Resources.cs), which keys the two together:
//
//	// iOS  — the block we match
//	Download_User_Agent = "Audible/671 CFNetwork/1240.0.4 Darwin/20.6.0"
//	DeviceType          = "A2CZJZGLK2JJVM"
//	OsVersion = "15.0.0"  SoftwareVersion = "35602678"  AppVersion = "3.56.2"
//
//	// Android — a DIFFERENT UA for a DIFFERENT device type
//	Download_User_Agent = "com.audible.playersdk.player/3.96.1 (Linux;Android 14) …"
//	DeviceType          = "A10KISP2GWF0E4"
//
// register.go registers as the iOS device type with those exact os/software/app
// versions, so the iOS UA is the correct one here. If deviceType is ever bumped,
// this must be bumped to the matching value — a UA that disagrees with the
// registered device type is precisely the sort of anomaly a CDN WAF blocks.
const DownloadUserAgent = "Audible/671 CFNetwork/1240.0.4 Darwin/20.6.0"

// DeviceType exposes the Audible-iOS device_type constant used at registration.
// It is one of the four inputs to the AAXC license-voucher key derivation
// (internal/books/liberate/voucher.go), which lives in a different package — so
// rather than duplicate the literal there (and risk the two drifting if Amazon
// ever forces a bump), the liberation path reads it from here.
func DeviceType() string { return deviceType }

// SignedPost performs a device-signed POST against an Amazon/Audible API host.
// It is the twin of SignedGet and shares its client, metrics, and read cap.
//
// The Audible content-licensing endpoint (POST /1.0/content/{asin}/licenserequest)
// is the only caller today. Two things about it are load-bearing:
//
//   - The body is signed AND sent as the SAME []byte. Sign() folds the body into
//     the canonical string, so re-marshalling between signing and sending — even
//     a semantically identical re-marshal with different key order or spacing —
//     produces a signature Amazon rejects. Callers pass bytes, not a struct, for
//     exactly this reason.
//   - Every prior caller of Sign() passed a nil body, so the body element of the
//     canonical string is exercised here for the first time. A format mismatch
//     surfaces as an Amazon-side auth error, never a local one; the admin
//     liberation probe checks it live.
func SignedPost(ctx context.Context, cred *DeviceCredential, apiHost, pathAndQuery string, body []byte) ([]byte, int, error) {
	if cred == nil {
		return nil, 0, ErrNotRegistered
	}
	metrics.AmazonCallsTotal.WithLabelValues("signed").Inc()
	h, err := Sign(cred, "POST", pathAndQuery, body, time.Now())
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+apiHost+pathAndQuery, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("x-adp-token", h.AdpToken)
	req.Header.Set("x-adp-alg", h.AdpAlg)
	req.Header.Set("x-adp-signature", h.Signature)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	// Same 64 MiB cap as SignedGet. A license response is a few KB; the cap is
	// here as a uniform safety bound, NOT as a content-sized limit — the AAXC
	// download deliberately does not go through this path (see fetch.go).
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	return respBody, resp.StatusCode, nil
}
