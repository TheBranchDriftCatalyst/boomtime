package amazon

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/metrics"
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
	// External-API call-rate observability (admin Metrics dashboard). Every
	// device-signed Amazon/Audible/Kindle GET flows through here, so this one
	// counter captures the full signed-call rate. Cookie-based Cloud Reader
	// calls (readamazon.go) are counted separately at their own choke point.
	metrics.Inc(metrics.Name("amazon.calls", "transport", "signed"), 1)
	metrics.Inc("amazon.calls", 1)
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
