package amazon

// readamazon.go — the read.amazon.com Cloud Reader wire surface. This is the
// PRIMARY Kindle library source the catalyst-books domain drives: it returns the
// user's FULL library (title + authors + percentageRead + cover) in a paginated
// JSON feed, unlike the whispersync/CloudCollections path in kindle.go which only
// surfaces books the user manually filed onto a shelf (collection-organized) and
// carries no titles.
//
// Auth is DIFFERENT from the rest of this package. Audible/Kindle-whispersync use
// X-ADP-Request-Digest device signing (signing.go / SignedGet). The Cloud Reader
// is a plain website surface authenticated by *website cookies*, so we:
//
//  1. exchange the device refresh_token for a set of .amazon.com auth cookies
//     (POST /ap/exchangetoken/cookies), then
//  2. call read.amazon.com/kindle-library/search with those cookies.
//
// It therefore uses the stdlib http client with a Cookie header directly — it is
// NOT routed through SignedGet.
//
// Endpoint map (reverse-engineered live 2026-08-13):
//
//	cookie exchange   host www.amazon.com
//	  POST /ap/exchangetoken/cookies  (application/x-www-form-urlencoded)
//	  -> {"response":{"tokens":{"cookies":{".amazon.com":[{Name,Value},...]}}}}
//	library search    host read.amazon.com
//	  GET /kindle-library/search?libraryType=BOOKS&sortType=acquisition_desc
//	      &querySize=200[&paginationToken=<tok>]
//	  -> {"itemsList":[{asin,title,authors,percentageRead,productUrl,
//	      webReaderUrl,resourceType,...}], "paginationToken": <str|null>}
//
// The parse functions (parseExchangeCookies / parseCloudLibraryPage) are pure
// (body -> typed) so they unit-test against captured fixtures with no network.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// Cloud Reader hosts. Both are marketplace-independent for the US account we
// authenticate; the exchange lives on the auth-portal host, the library on the
// reader host.
const (
	// CookieExchangeHost serves the refresh_token -> website-cookies exchange.
	CookieExchangeHost = "www.amazon.com"
	// CloudReaderHost serves the Cloud Reader library search API.
	CloudReaderHost = "read.amazon.com"

	// cloudLibraryPageSize is the querySize per page; 200 is what the web client
	// requests and what was verified live.
	cloudLibraryPageSize = 200

	// cloudLibraryMaxPages bounds pagination so a malformed/looping
	// paginationToken can never spin forever. 2512 books / 200 ≈ 13 pages live;
	// 200 pages (40k books) is a generous ceiling.
	cloudLibraryMaxPages = 200

	// cloudReaderUserAgent is a normal browser UA. read.amazon.com rejects an
	// empty/obvious-bot UA, so a realistic one is sent with the cookie.
	cloudReaderUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
		"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
)

// CloudLibraryItem is one book from the Cloud Reader library feed. Unlike the
// whispersync CollectionRecord (ASIN only), this carries display metadata
// directly from Amazon, so a row can be ingested with a real title/author/cover
// even when Hardcover is not connected.
type CloudLibraryItem struct {
	ASIN           string
	Title          string
	Authors        []string // raw "Last, First:" strings — use AuthorsCSV to render
	PercentageRead int      // 0..100
	CoverURL       string   // productUrl (cover image)
	WebReaderURL   string
	ResourceType   string // e.g. "EBOOK", "EBOOK_SAMPLE"
}

// AuthorsCSV renders the raw authors list into a display string. Each entry
// arrives as "Last, First:" (a trailing ':' the feed appends); the ':' is
// stripped and the cleaned names are comma-joined. Empty entries are dropped.
func (it CloudLibraryItem) AuthorsCSV() string {
	parts := make([]string, 0, len(it.Authors))
	for _, a := range it.Authors {
		a = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(a), ":"))
		if a != "" {
			parts = append(parts, a)
		}
	}
	return strings.Join(parts, ", ")
}

// IsSample reports whether the item is a Kindle sample rather than an owned
// book, inferred from resourceType. Samples are excluded from the library sweep
// so they don't pollute the reading list.
func (it CloudLibraryItem) IsSample() bool {
	return strings.Contains(strings.ToUpper(it.ResourceType), "SAMPLE")
}

// CloudReaderClient drives the read.amazon.com Cloud Reader library. It is the
// runtime implementation the books domain injects; a fake satisfies the same
// method set in tests. It holds no state — the credential/cookies flow through
// each call — so a zero value is usable.
type CloudReaderClient struct{}

// NewCloudReaderClient constructs the Cloud Reader library client.
func NewCloudReaderClient() *CloudReaderClient { return &CloudReaderClient{} }

// ExchangeWebsiteCookies exchanges the device credential's refresh_token for a
// set of .amazon.com website auth cookies (at-main, session-token, ubid-main,
// x-main, session-id, …) usable against read.amazon.com. The returned map is
// name -> value with any wrapping quotes stripped.
func (c *CloudReaderClient) ExchangeWebsiteCookies(ctx context.Context, cred *DeviceCredential) (map[string]string, error) {
	return ExchangeWebsiteCookies(ctx, cred)
}

// KindleCloudLibrary pulls the full paginated Cloud Reader library using the
// exchanged website cookies.
func (c *CloudReaderClient) KindleCloudLibrary(ctx context.Context, cookies map[string]string) ([]CloudLibraryItem, error) {
	return KindleCloudLibrary(ctx, cookies)
}

// ExchangeWebsiteCookies POSTs the refresh_token to /ap/exchangetoken/cookies
// and returns the resulting .amazon.com cookie jar (name -> value). It is a
// package-level function so it can be called without an instance; the method on
// CloudReaderClient delegates here.
func ExchangeWebsiteCookies(ctx context.Context, cred *DeviceCredential) (map[string]string, error) {
	if cred == nil {
		return nil, ErrNotRegistered
	}
	if strings.TrimSpace(cred.RefreshToken) == "" {
		return nil, fmt.Errorf("amazon cloud reader: device credential has no refresh_token")
	}

	form := url.Values{}
	form.Set("app_name", "Unknown")
	form.Set("requested_token_type", "auth_cookies")
	form.Set("domain", ".amazon.com")
	form.Set("source_token_type", "refresh_token")
	form.Set("source_token", cred.RefreshToken)

	endpoint := "https://" + CookieExchangeHost + "/ap/exchangetoken/cookies"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", cloudReaderUserAgent)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("amazon cloud reader: cookie exchange returned HTTP %d: %s", resp.StatusCode, kindleSnippet(body))
	}
	cookies, err := parseExchangeCookies(body)
	if err != nil {
		return nil, err
	}
	if len(cookies) == 0 {
		return nil, fmt.Errorf("amazon cloud reader: cookie exchange returned no cookies: %s", kindleSnippet(body))
	}
	return cookies, nil
}

// KindleCloudLibrary GETs the Cloud Reader library, following paginationToken
// until it is null/absent, and returns every item across pages. Samples are NOT
// filtered here (that's a domain policy decision) — the raw feed is returned.
func KindleCloudLibrary(ctx context.Context, cookies map[string]string) ([]CloudLibraryItem, error) {
	if len(cookies) == 0 {
		return nil, fmt.Errorf("amazon cloud reader: no website cookies (call ExchangeWebsiteCookies first)")
	}
	cookieHeader := cookieHeader(cookies)

	var all []CloudLibraryItem
	token := ""
	for page := 0; page < cloudLibraryMaxPages; page++ {
		body, status, err := cloudLibraryPage(ctx, cookieHeader, token)
		if err != nil {
			return nil, err
		}
		if status < 200 || status >= 300 {
			return nil, fmt.Errorf("amazon cloud reader: library search returned HTTP %d: %s", status, kindleSnippet(body))
		}
		items, next, err := parseCloudLibraryPage(body)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if strings.TrimSpace(next) == "" {
			return all, nil
		}
		token = next
	}
	return all, fmt.Errorf("amazon cloud reader: library pagination exceeded %d pages (possible token loop)", cloudLibraryMaxPages)
}

// cloudLibraryPage performs one library-search GET with the cookie header and an
// optional paginationToken, returning the raw body + status.
func cloudLibraryPage(ctx context.Context, cookieHeader, paginationToken string) ([]byte, int, error) {
	q := url.Values{}
	q.Set("libraryType", "BOOKS")
	q.Set("sortType", "acquisition_desc")
	q.Set("querySize", fmt.Sprintf("%d", cloudLibraryPageSize))
	if strings.TrimSpace(paginationToken) != "" {
		q.Set("paginationToken", paginationToken)
	}
	endpoint := "https://" + CloudReaderHost + "/kindle-library/search?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Cookie", cookieHeader)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", cloudReaderUserAgent)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	// 64 MiB: a 200-item page with covers + web-reader URLs is comfortably under
	// this, but a full library dumped in one response should never truncate.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	return body, resp.StatusCode, nil
}

// cookieHeader renders a cookie map into a "k=v; k=v" Cookie header value with a
// stable (sorted) order so the request is deterministic.
func cookieHeader(cookies map[string]string) string {
	names := make([]string, 0, len(cookies))
	for k := range cookies {
		names = append(names, k)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, k := range names {
		parts = append(parts, k+"="+cookies[k])
	}
	return strings.Join(parts, "; ")
}

// ---------------------------------------------------------------------------
// Pure parsers (unit-tested against captured fixtures)
// ---------------------------------------------------------------------------

// parseExchangeCookies extracts the .amazon.com cookie jar from an
// /ap/exchangetoken/cookies response:
//
//	{"response":{"tokens":{"cookies":{".amazon.com":[{"Name","Value"},...]}}}}
//
// Values may arrive wrapped in double quotes (e.g. "\"abc\"") — the quotes are
// stripped. Cookies from any domain key present are merged; in practice only
// ".amazon.com" is returned.
func parseExchangeCookies(body []byte) (map[string]string, error) {
	var resp struct {
		Response struct {
			Tokens struct {
				Cookies map[string][]struct {
					Name  string `json:"Name"`
					Value string `json:"Value"`
				} `json:"cookies"`
			} `json:"tokens"`
		} `json:"response"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("amazon cloud reader: cookie exchange parse failed: %w (body: %s)", err, kindleSnippet(body))
	}
	out := map[string]string{}
	for _, list := range resp.Response.Tokens.Cookies {
		for _, c := range list {
			name := strings.TrimSpace(c.Name)
			if name == "" {
				continue
			}
			out[name] = stripQuotes(c.Value)
		}
	}
	return out, nil
}

// stripQuotes removes a single layer of wrapping double quotes from a cookie
// value if present. Amazon sometimes returns values as "\"...\"".
func stripQuotes(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		return v[1 : len(v)-1]
	}
	return v
}

// cloudLibraryResponse is the wire shape of one library-search page.
type cloudLibraryResponse struct {
	ItemsList []struct {
		ASIN           string   `json:"asin"`
		Title          string   `json:"title"`
		Authors        []string `json:"authors"`
		PercentageRead float64  `json:"percentageRead"`
		ProductURL     string   `json:"productUrl"`
		WebReaderURL   string   `json:"webReaderUrl"`
		ResourceType   string   `json:"resourceType"`
	} `json:"itemsList"`
	PaginationToken *string `json:"paginationToken"`
}

// parseCloudLibraryPage maps one library-search page into typed items and the
// next paginationToken (empty string when null/absent). Items without an ASIN
// are skipped (they cannot be keyed). PercentageRead is clamped to 0..100.
func parseCloudLibraryPage(body []byte) ([]CloudLibraryItem, string, error) {
	var resp cloudLibraryResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, "", fmt.Errorf("amazon cloud reader: library page parse failed: %w (body: %s)", err, kindleSnippet(body))
	}
	items := make([]CloudLibraryItem, 0, len(resp.ItemsList))
	for _, it := range resp.ItemsList {
		asin := strings.TrimSpace(it.ASIN)
		if asin == "" {
			continue
		}
		items = append(items, CloudLibraryItem{
			ASIN:           asin,
			Title:          strings.TrimSpace(it.Title),
			Authors:        it.Authors,
			PercentageRead: clampPercentInt(it.PercentageRead),
			CoverURL:       strings.TrimSpace(it.ProductURL),
			WebReaderURL:   strings.TrimSpace(it.WebReaderURL),
			ResourceType:   strings.TrimSpace(it.ResourceType),
		})
	}
	next := ""
	if resp.PaginationToken != nil {
		next = strings.TrimSpace(*resp.PaginationToken)
	}
	return items, next, nil
}

// clampPercentInt rounds+clamps a percentage into 0..100.
func clampPercentInt(f float64) int {
	if f <= 0 {
		return 0
	}
	if f >= 100 {
		return 100
	}
	return int(f + 0.5)
}
