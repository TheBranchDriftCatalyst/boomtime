// annotations.go — the ANNOTATION-SURFACE PROBE (boom-siwi.1).
//
// Question it answers: can the device credential we ALREADY hold read Kindle
// highlights and Audible clips directly, so the fusion engine owns the
// annotation corpus instead of renting it from Readwise?
//
// The lead came from two places in this very package:
//
//   - kindle.go:CloudCollectionsNamespace — "Other namespaces (device sync
//     state, ANNOTATIONS) are ignored." We list whispersync datasets and then
//     throw away every namespace that isn't shelves.
//   - sidecar.go:parseLastPagePosition — it walks payload.records[] and
//     `continue`s past every record whose type isn't "kindle.lpr". If Amazon
//     ships highlight records in that same envelope, we have been dropping them
//     on the floor since the day the sidecar landed.
//
// So this probe does not fetch anything new so much as it STOPS FILTERING: it
// re-runs the calls the domain already makes and reports a full census of what
// came back — every namespace, every record type, every field key — instead of
// the one shape the ingest cares about.
//
// Everything here is a GET. The probe never writes, never enqueues, and never
// mutates stored state; it is safe to point at a production credential.
//
// BODIES ARE PERSONAL. Highlights are the user's own prose, and the notebook
// HTML carries it inline. Bodies are captured only when opts.KeepBodies is set,
// and the CLI keeps that behind an explicit --dump path rather than printing to
// a terminal that scrolls into a log.

package amazon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/metrics"
)

// AnnotationVerdict is one probe's judgement. Unlike the raw dumps in the admin
// books diagnostics ("here is what came back, eyeball it"), each probe here
// answers a specific yes/no — does this surface carry annotations? — so it
// carries an explicit verdict and a human-readable Detail.
type AnnotationVerdict string

const (
	// AnnotationPass — this surface returned annotation data. The direct path works.
	AnnotationPass AnnotationVerdict = "pass"
	// AnnotationWarn — the call succeeded but carried no annotations. Inconclusive
	// on its own: an account with no highlights on that title looks identical to a
	// surface that does not serve them. Hence the two-ASIN method below.
	AnnotationWarn AnnotationVerdict = "warn"
	// AnnotationFail — the call itself failed (non-2xx, transport error, bad parse).
	AnnotationFail AnnotationVerdict = "fail"
	// AnnotationSkip — not attempted (missing input, e.g. no customer_id or no ASIN).
	AnnotationSkip AnnotationVerdict = "skip"
)

// AnnotationProbe is one endpoint's result.
type AnnotationProbe struct {
	Name      string `json:"name"`
	Endpoint  string `json:"endpoint"`
	Transport string `json:"transport"` // "signed" (ADP) | "cookie" (Cloud Reader)
	Status    int    `json:"status"`
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`

	Verdict AnnotationVerdict `json:"verdict"`
	Detail  string            `json:"detail"`

	// Census is the point of the exercise: what KINDS of thing came back and how
	// many of each. Record type -> count for the sidecar, namespace -> count for
	// whispersync, marker -> count for the notebook HTML.
	Census map[string]int `json:"census,omitempty"`
	// Fields is the union of field keys seen per census key, so a new record type
	// arrives with its shape already described and we can model it without a
	// second round trip.
	Fields map[string][]string `json:"fields,omitempty"`
	// Samples holds a few opaque identifiers (record keys, dataset ids) — safe to
	// print, unlike Body.
	Samples []string `json:"samples,omitempty"`

	// Body/BodyText are the verbatim response, captured only under KeepBodies.
	Body     json.RawMessage `json:"body,omitempty"`
	BodyText string          `json:"bodyText,omitempty"`
}

// AnnotationProbeOpts parameterizes a probe run.
type AnnotationProbeOpts struct {
	// EbookASINs are Kindle titles to probe. Supply TWO: one you know carries
	// highlights and one you know does not. A single ASIN cannot distinguish
	// "this surface has no highlights API" from "this book has no highlights" —
	// the Fiona empty-response red herring already cost us a day once.
	EbookASINs []string
	// AudiobookASINs are Audible titles to probe for clips/bookmarks.
	AudiobookASINs []string
	// Cookies enables the read.amazon.com notebook probes, which need a
	// refresh_token -> website-cookie exchange (US marketplace only).
	Cookies bool
	// MaxDatasets caps how many non-shelf whispersync datasets get their records
	// pulled. 0 defaults to 8.
	MaxDatasets int
	// KeepBodies captures verbatim response bodies. Off by default: highlight
	// text is personal.
	KeepBodies bool
}

// AnnotationReport is the whole run: every probe plus a rolled-up verdict that
// decides boom-siwi's direction (direct-from-Amazon vs Readwise-as-source).
type AnnotationReport struct {
	Marketplace string            `json:"marketplace"`
	Probes      []AnnotationProbe `json:"probes"`
	Verdict     AnnotationVerdict `json:"verdict"`
	Summary     string            `json:"summary"`
}

// annotationRecordTypes are the sidecar record types that would mean Amazon
// serves annotations through the envelope we already parse. kindle.lpr (the
// position record sidecar.go consumes) is deliberately NOT here — it is the
// known-present control, and a census containing only it is the warn case.
var annotationRecordTypes = []string{
	"kindle.highlight", "kindle.note", "kindle.bookmark",
	"audible.clip", "audible.bookmark", "audible.note",
}

// RunAnnotationProbes executes the whole sweep and returns a report. It is
// best-effort throughout: a failed probe is recorded and the sweep continues, so
// one dead surface never hides a live one.
func RunAnnotationProbes(ctx context.Context, cred *DeviceCredential, opts AnnotationProbeOpts) AnnotationReport {
	rep := AnnotationReport{Verdict: AnnotationSkip}
	if cred == nil {
		rep.Verdict = AnnotationFail
		rep.Summary = ErrNotRegistered.Error()
		return rep
	}
	rep.Marketplace = string(cred.Marketplace)
	if opts.MaxDatasets <= 0 {
		opts.MaxDatasets = 8
	}

	rep.Probes = append(rep.Probes, whispersyncNamespaceProbes(ctx, cred, opts)...)
	rep.Probes = append(rep.Probes, sidecarCensusProbes(ctx, cred, opts)...)
	rep.Probes = append(rep.Probes, audibleAnnotationProbes(ctx, cred, opts)...)
	if opts.Cookies {
		rep.Probes = append(rep.Probes, notebookProbes(ctx, cred, opts)...)
	}

	rep.Verdict, rep.Summary = rollUp(rep.Probes)
	return rep
}

// ---------------------------------------------------------------------------
// 1. whispersync — enumerate EVERY namespace, not just CloudCollections
// ---------------------------------------------------------------------------

func whispersyncNamespaceProbes(ctx context.Context, cred *DeviceCredential, opts AnnotationProbeOpts) []AnnotationProbe {
	if strings.TrimSpace(cred.CustomerID) == "" {
		return []AnnotationProbe{{
			Name:      "whispersync datasets (namespace census)",
			Transport: "signed",
			Verdict:   AnnotationSkip,
			Detail:    "the stored credential has no customer_id — reconnect Amazon to capture it; whispersync is keyed by it",
		}}
	}

	path := "/whispersync/v2/data/" + cred.CustomerID + "/datasets"
	p := signedProbe(ctx, cred, "whispersync datasets (namespace census)", WhispersyncHost, path, opts.KeepBodies)
	if !p.OK {
		return []AnnotationProbe{p}
	}

	datasets, err := ParseDatasets(rawOf(ctx, cred, WhispersyncHost, path))
	if err != nil {
		p.Verdict = AnnotationFail
		p.Detail = "datasets parsed as JSON but not as the expected shape: " + err.Error()
		return []AnnotationProbe{p}
	}

	census := map[string]int{}
	var others []Dataset
	for _, d := range datasets {
		census[d.Namespace]++
		if d.Namespace != CloudCollectionsNamespace {
			others = append(others, d)
		}
	}
	p.Census = census
	p.Samples = datasetSamples(others)
	// A non-shelf namespace is NOT automatically an annotation namespace. The
	// first live run returned Apps:Device:Settings (device config) and
	// LearningDecks (Vocabulary Builder flashcards) — both outside
	// CloudCollections, both irrelevant, and an earlier version of this probe
	// scored that a PASS. A pass here now requires a namespace that actually
	// names an annotation concept; anything else is a warn that says what it saw.
	annotationish := filterAnnotationish(others)
	switch {
	case len(others) == 0:
		p.Verdict = AnnotationWarn
		p.Detail = fmt.Sprintf("all %d datasets are %s (shelves). The comment in kindle.go promises other namespaces; this account exposes none, so whispersync is not the annotation carrier here.",
			len(datasets), CloudCollectionsNamespace)
	case len(annotationish) == 0:
		p.Verdict = AnnotationWarn
		p.Detail = fmt.Sprintf("%d of %d datasets sit outside %s, but none names an annotation concept (%s). The ingest discards these for good reason — whispersync is not the highlight store.",
			len(others), len(datasets), CloudCollectionsNamespace, namespaceList(others))
	default:
		p.Verdict = AnnotationPass
		p.Detail = fmt.Sprintf("%d namespace(s) name an annotation concept (%s) — the highlight store may be here. Records pulled below.",
			len(annotationish), namespaceList(annotationish))
	}

	out := []AnnotationProbe{p}
	for i, d := range others {
		if i >= opts.MaxDatasets {
			out = append(out, AnnotationProbe{
				Name:      "whispersync records (remaining datasets)",
				Transport: "signed",
				Verdict:   AnnotationSkip,
				Detail:    fmt.Sprintf("%d further non-shelf datasets not pulled (MaxDatasets=%d) — raise the cap to see them", len(others)-opts.MaxDatasets, opts.MaxDatasets),
			})
			break
		}
		out = append(out, whispersyncRecordsProbe(ctx, cred, d, opts))
	}
	return out
}

func whispersyncRecordsProbe(ctx context.Context, cred *DeviceCredential, d Dataset, opts AnnotationProbeOpts) AnnotationProbe {
	name := fmt.Sprintf("whispersync records: %s / %s", d.Namespace, firstNonBlank(d.Name, d.Identifier))
	path := "/whispersync/v2/data/" + cred.CustomerID + "/datasets/" + d.Identifier + "/records"
	p := signedProbe(ctx, cred, name, WhispersyncHost, path, opts.KeepBodies)
	if !p.OK {
		return p
	}
	body := rawOf(ctx, cred, WhispersyncHost, path)
	keys, count := recordKeyCensus(body)
	p.Census = keys
	p.Samples = mapKeysSorted(keys, 8)
	if count == 0 {
		p.Verdict = AnnotationWarn
		p.Detail = "dataset is empty — no records to shape a model from"
		return p
	}
	p.Verdict = AnnotationPass
	p.Detail = fmt.Sprintf("%d records under %d key prefixes. If a prefix keys by annotation rather than by amzn://<ASIN>/BOOK, this namespace is the highlight store.", count, len(keys))
	return p
}

// ---------------------------------------------------------------------------
// 2. Fiona CDE sidecar — the full record-type census, EBOK and AUDI
// ---------------------------------------------------------------------------

func sidecarCensusProbes(ctx context.Context, cred *DeviceCredential, opts AnnotationProbeOpts) []AnnotationProbe {
	var out []AnnotationProbe
	for _, asin := range opts.EbookASINs {
		out = append(out, sidecarProbe(ctx, cred, "EBOK", asin, opts.KeepBodies))
	}
	for _, asin := range opts.AudiobookASINs {
		out = append(out, sidecarProbe(ctx, cred, "AUDI", asin, opts.KeepBodies))
	}
	if len(out) == 0 {
		out = append(out, AnnotationProbe{
			Name:      "Fiona CDE sidecar (record-type census)",
			Transport: "signed",
			Verdict:   AnnotationSkip,
			Detail:    "no ASINs supplied — pass at least one ebook ASIN you know carries highlights AND one you know does not",
		})
	}
	return out
}

// sidecarProbe is the highest-value probe in the sweep: same URL sidecar.go
// already calls, with the kindle.lpr filter removed.
func sidecarProbe(ctx context.Context, cred *DeviceCredential, typ, asin string, keepBody bool) AnnotationProbe {
	asin = strings.TrimSpace(asin)
	path := "/FionaCDEServiceEngine/sidecar?type=" + typ + "&key=" + asin
	p := signedProbe(ctx, cred, fmt.Sprintf("Fiona sidecar %s %s (record-type census)", typ, asin), KindleCDEHost, path, keepBody)
	if p.Status == 404 {
		p.Verdict = AnnotationWarn
		p.Detail = "404 — no sidecar state for this title at all (never opened). Not evidence either way; probe a title you have actually read."
		return p
	}
	if !p.OK {
		return p
	}
	body := rawOf(ctx, cred, KindleCDEHost, path)
	census, fields := sidecarRecordCensus(body)
	p.Census = census
	p.Fields = fields
	// Field NAMES are not enough to build against. A clip is only useful if we
	// can read its offsets and know their units, because Amazon never serves the
	// clip AUDIO — only the range. The audio comes from our own liberated M4B, so
	// the offsets are the entire payload and their magnitude (ms vs s) decides
	// the ffmpeg call. Numeric/short scalar values are rendered verbatim; any
	// field that could hold the user's own words is reduced to a length.
	p.Samples = append(p.Samples, sidecarValueSamples(body)...)
	switch {
	case len(census) == 0:
		p.Verdict = AnnotationWarn
		p.Detail = "200 with an empty payload.records[] — the envelope exists but carries nothing for this title"
	case onlyPositionRecords(census):
		p.Verdict = AnnotationWarn
		p.Detail = "only kindle.lpr (the position record we already consume). This title's annotations are not in the sidecar envelope — if the control title WITH highlights says the same, the sidecar is not the highlight carrier."
	default:
		p.Verdict = AnnotationPass
		p.Detail = "record types BEYOND kindle.lpr are present — sidecar.go has been discarding these. Field keys are captured above; model them directly."
	}
	return p
}

func onlyPositionRecords(census map[string]int) bool {
	for k := range census {
		if k != "kindle.lpr" {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// 3. Audible annotations (device-signed, marketplace host)
// ---------------------------------------------------------------------------

func audibleAnnotationProbes(ctx context.Context, cred *DeviceCredential, opts AnnotationProbeOpts) []AnnotationProbe {
	host := AudibleAPIHost(cred.Marketplace)
	if len(opts.AudiobookASINs) == 0 {
		return []AnnotationProbe{{
			Name:      "Audible /1.0/annotations/lastpositions",
			Transport: "signed",
			Verdict:   AnnotationSkip,
			Detail:    "no audiobook ASIN supplied",
		}}
	}
	asins := strings.Join(opts.AudiobookASINs, ",")
	path := "/1.0/annotations/lastpositions?asins=" + asins
	p := signedProbe(ctx, cred, "Audible /1.0/annotations/lastpositions", host, path, opts.KeepBodies)
	if p.OK {
		body := rawOf(ctx, cred, host, path)
		p.Census, p.Fields = jsonShapeCensus(body)
		// Answering is not the same as carrying annotations. Live, this endpoint
		// returns only asin_last_position_heard_annots — the position we already
		// read from the sidecar — so "it responded" must not score as a pass.
		switch {
		case len(p.Census) == 0:
			p.Verdict = AnnotationWarn
			p.Detail = "reachable but empty for these ASINs"
		case !mentionsAnnotationConcept(mapKeysSorted(p.Census, 0)):
			p.Verdict = AnnotationWarn
			p.Detail = "answers on the device credential but carries LAST-POSITION only (" + strings.Join(mapKeysSorted(p.Census, 0), ", ") + ") — no clips or bookmarks here; the AUDI sidecar is the carrier"
		default:
			p.Verdict = AnnotationPass
			p.Detail = "the response carries a clips/bookmarks collection, not just a position"
		}
	}
	return []AnnotationProbe{p}
}

// ---------------------------------------------------------------------------
// 4. Cloud Reader notebook (cookie transport) — the highlights page itself
// ---------------------------------------------------------------------------

// notebookMarkers are DOM markers on read.amazon.com/notebook. Counting them is
// how we tell a real highlights page from a sign-in redirect that also returns
// HTTP 200.
var notebookMarkers = []string{
	"kp-notebook-highlight",
	"kp-notebook-note",
	"kp-notebook-annotation",
	"kp-notebook-metadata",
	"annotationHighlightHeader",
	"ap/signin", // negative marker: we were bounced to login
}

func notebookProbes(ctx context.Context, cred *DeviceCredential, opts AnnotationProbeOpts) []AnnotationProbe {
	cookies, err := ExchangeWebsiteCookies(ctx, cred)
	if err != nil {
		return []AnnotationProbe{{
			Name:      "read.amazon.com/notebook (cookie exchange)",
			Transport: "cookie",
			Verdict:   AnnotationFail,
			Error:     err.Error(),
			Detail:    "could not exchange the refresh_token for website cookies — the notebook surface is unreachable without them (US marketplace only)",
		}}
	}

	// The library view is the INDEX of annotated books, so it is fetched first
	// and its HTML drives the per-book probes. Probing "the book you read most"
	// is the wrong question — most-read and has-highlights are different sets,
	// and asking the wrong one produced a false negative on the first live run.
	libText, libProbe := notebookProbe(ctx, cookies,
		"read.amazon.com/notebook (library of annotated books)", "/notebook", opts.KeepBodies)
	out := []AnnotationProbe{libProbe}

	// Explicitly-named ASINs first (the operator's own control pair), then the
	// ones the library page says actually carry annotations.
	targets := append([]string{}, opts.EbookASINs...)
	annotated := annotatedASINsFromNotebook(libText)
	if len(annotated) > 0 {
		out = append(out, AnnotationProbe{
			Name:      "notebook library index (annotated ASINs)",
			Transport: "cookie",
			OK:        true,
			Verdict:   AnnotationPass,
			Detail: fmt.Sprintf("the library page lists %d book(s) with annotations — these, not the most-read title, are the ones to extract from",
				len(annotated)),
			Census:  map[string]int{"annotated books": len(annotated)},
			Samples: capStrings(annotated, 10),
		})
		for _, a := range annotated {
			if !containsString(targets, a) {
				targets = append(targets, a)
			}
		}
	}

	for i, asin := range targets {
		if i >= notebookPerBookCap {
			out = append(out, AnnotationProbe{
				Name:      "notebook per-book (remaining titles)",
				Transport: "cookie",
				Verdict:   AnnotationSkip,
				Detail:    fmt.Sprintf("%d further annotated title(s) not fetched (cap %d) — the shape is established by the ones above", len(targets)-notebookPerBookCap, notebookPerBookCap),
			})
			break
		}
		_, p := notebookProbe(ctx, cookies,
			"read.amazon.com/notebook?asin="+asin+" (per-book highlights)",
			"/notebook?asin="+strings.TrimSpace(asin)+"&contentLimitState=&", opts.KeepBodies)
		out = append(out, p)
	}
	return out
}

// notebookPerBookCap bounds the per-book fetches. Each is a full HTML page and
// the probe runs by hand; three is enough to establish the shape.
const notebookPerBookCap = 3

// notebookProbe fetches one notebook page and judges it. It returns the body
// text alongside the probe so the caller can mine the library index WITHOUT
// storing the body on the probe — KeepBodies governs what is persisted, not
// what is briefly held in memory.
func notebookProbe(ctx context.Context, cookies map[string]string, name, pathAndQuery string, keepBody bool) (string, AnnotationProbe) {
	p := AnnotationProbe{
		Name:      name,
		Endpoint:  "https://" + CloudReaderHost + pathAndQuery,
		Transport: "cookie",
	}
	body, status, err := cookieGet(ctx, cookies, CloudReaderHost, pathAndQuery)
	p.Status = status
	if err != nil {
		p.Verdict = AnnotationFail
		p.Error = err.Error()
		return "", p
	}
	p.OK = status >= 200 && status < 300
	if !p.OK {
		p.Verdict = AnnotationFail
		p.Detail = fmt.Sprintf("HTTP %d", status)
		return "", p
	}
	text := string(body)
	if keepBody {
		p.BodyText = truncate(text, 20000)
	}
	p.Census = markerCensus(text, notebookMarkers)
	switch {
	case p.Census["ap/signin"] > 0 && p.Census["kp-notebook-highlight"] == 0:
		p.Verdict = AnnotationFail
		p.Detail = "HTTP 200 but the page is a sign-in bounce — the exchanged cookies do not authenticate the notebook surface"
	case p.Census["kp-notebook-highlight"] > 0:
		p.Verdict = AnnotationPass
		p.Detail = fmt.Sprintf("%d highlight markers in the DOM — the notebook serves our highlights to these cookies. Scrapable, but HTML: prefer a signed API if one of the probes above passed.", p.Census["kp-notebook-highlight"])
	default:
		p.Verdict = AnnotationWarn
		p.Detail = "authenticated HTML with no highlight markers — this title has no annotations, or the DOM classes have moved"
	}
	return text, p
}

// asinIDPattern matches the ASIN-shaped element ids the notebook library view
// puts on each book row. Amazon ASINs for Kindle books are "B" + 9 uppercase
// alphanumerics, which is specific enough to pick the book rows out of a page
// whose other ids are word-shaped.
var asinIDPattern = regexp.MustCompile(`id="(B[0-9A-Z]{9})"`)

// annotatedASINsFromNotebook extracts, in document order, the ASINs the
// notebook library page lists — i.e. the books that HAVE annotations. This is
// the index the per-book probes should follow.
func annotatedASINsFromNotebook(html string) []string {
	if html == "" {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	for _, m := range asinIDPattern.FindAllStringSubmatch(html, -1) {
		asin := m[1]
		if _, ok := seen[asin]; ok {
			continue
		}
		seen[asin] = struct{}{}
		out = append(out, asin)
	}
	return out
}

// ---------------------------------------------------------------------------
// Transport helpers
// ---------------------------------------------------------------------------

// signedProbe performs one device-signed GET and records the outcome. It does
// NOT set Verdict/Census — each caller interprets its own surface.
func signedProbe(ctx context.Context, cred *DeviceCredential, name, host, pathAndQuery string, keepBody bool) AnnotationProbe {
	// The whispersync path embeds the account's customer id. Probe output gets
	// pasted into tickets and design docs in a PUBLIC repo, so the endpoint is
	// redacted before it is ever rendered. The real value still goes on the wire.
	endpoint := "https://" + host + pathAndQuery
	if cid := strings.TrimSpace(cred.CustomerID); cid != "" {
		endpoint = strings.ReplaceAll(endpoint, cid, "<customer-id>")
	}
	p := AnnotationProbe{Name: name, Endpoint: endpoint, Transport: "signed"}
	body, status, err := SignedGet(ctx, cred, host, pathAndQuery)
	p.Status = status
	if err != nil {
		p.Verdict = AnnotationFail
		p.Error = err.Error()
		return p
	}
	p.OK = status >= 200 && status < 300
	if !p.OK && status != 404 {
		p.Verdict = AnnotationFail
		p.Detail = fmt.Sprintf("HTTP %d: %s", status, kindleSnippet(body))
	}
	if keepBody {
		if json.Valid(body) {
			p.Body = json.RawMessage(body)
		} else {
			p.BodyText = truncate(string(body), 20000)
		}
	}
	return p
}

// rawOf re-fetches a body for census work. The extra round trip keeps
// signedProbe's contract narrow (it reports transport outcome, not payload) and
// costs one cheap GET on a probe path that runs by hand, never in a loop.
func rawOf(ctx context.Context, cred *DeviceCredential, host, pathAndQuery string) []byte {
	body, _, err := SignedGet(ctx, cred, host, pathAndQuery)
	if err != nil {
		return nil
	}
	return body
}

// cookieGet is the notebook's transport: the same cookie jar + browser UA the
// Cloud Reader library uses (readamazon.go), against an arbitrary path.
func cookieGet(ctx context.Context, cookies map[string]string, host, pathAndQuery string) ([]byte, int, error) {
	if len(cookies) == 0 {
		return nil, 0, fmt.Errorf("amazon notebook: no website cookies (call ExchangeWebsiteCookies first)")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+pathAndQuery, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Cookie", cookieHeader(cookies))
	req.Header.Set("Accept", "text/html,application/json")
	req.Header.Set("User-Agent", cloudReaderUserAgent)
	metrics.AmazonCallsTotal.WithLabelValues("cookie").Inc()
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	return body, resp.StatusCode, nil
}

// ---------------------------------------------------------------------------
// Pure census parsers (unit-tested against captured fixtures)
// ---------------------------------------------------------------------------

// sidecarRecordCensus counts payload.records[] by `type` and unions each type's
// field keys. This is deliberately the SAME envelope sidecar.go parses — the
// only difference is that nothing is filtered out.
func sidecarRecordCensus(body []byte) (map[string]int, map[string][]string) {
	var resp struct {
		Payload struct {
			Records []map[string]json.RawMessage `json:"records"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, nil
	}
	census := map[string]int{}
	fieldSet := map[string]map[string]struct{}{}
	for _, rec := range resp.Payload.Records {
		t := lprType(rec)
		if t == "" {
			t = "(untyped)"
		}
		census[t]++
		if fieldSet[t] == nil {
			fieldSet[t] = map[string]struct{}{}
		}
		for k := range rec {
			fieldSet[t][k] = struct{}{}
		}
	}
	if len(census) == 0 {
		return map[string]int{}, map[string][]string{}
	}
	fields := make(map[string][]string, len(fieldSet))
	for t, set := range fieldSet {
		keys := make([]string, 0, len(set))
		for k := range set {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fields[t] = keys
	}
	return census, fields
}

// contentFields are record fields that can carry the user's own prose. They are
// reported as a length, never rendered — the same rule the rest of this file
// applies to bodies. Everything else in an annotation record is an offset, an
// id or a timestamp, which is exactly what we need to see.
var contentFields = map[string]bool{
	"text": true, "note": true, "metadata": true, "title": true, "content": true,
}

// sidecarValueSamples renders the concrete values of every ANNOTATION record in
// a sidecar payload (kindle.lpr is skipped — it is the known control). This is
// what turns "the envelope has an audible.clip type" into "a clip is
// startPosition=%d endPosition=%d, which is a %s range we can hand to ffmpeg".
func sidecarValueSamples(body []byte) []string {
	var resp struct {
		Payload struct {
			Records []map[string]json.RawMessage `json:"records"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil
	}
	var out []string
	for _, rec := range resp.Payload.Records {
		t := lprType(rec)
		if t == "" || t == "kindle.lpr" {
			continue
		}
		keys := make([]string, 0, len(rec))
		for k := range rec {
			if k != "type" {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, k+"="+renderFieldValue(k, rec[k]))
		}
		line := t + "  " + strings.Join(parts, "  ")
		if span := clipSpan(rec); span != "" {
			line += "\n        " + span
		}
		out = append(out, line)
	}
	return out
}

// renderFieldValue prints a field safely: content-bearing fields collapse to a
// length, everything else prints as-is (offsets, ids, timestamps).
func renderFieldValue(key string, raw json.RawMessage) string {
	if contentFields[strings.ToLower(key)] {
		return fmt.Sprintf("<%d bytes, withheld>", len(raw))
	}
	v := strings.TrimSpace(string(raw))
	if len(v) > 80 {
		return v[:80] + "…"
	}
	return v
}

// clipSpan interprets startPosition/endPosition as MILLISECONDS and states the
// resulting range in human terms. Units are the one thing a field-name census
// cannot tell you, and getting them wrong means every transcribed clip is cut
// from the wrong place. The rendered span is the check: a clip that reads as a
// plausible few seconds/minutes confirms ms; one that reads as hours does not.
func clipSpan(rec map[string]json.RawMessage) string {
	start, sok := positionMillis(rec["startPosition"])
	end, eok := positionMillis(rec["endPosition"])
	if !sok || !eok || end <= start {
		return ""
	}
	dur := end - start
	return fmt.Sprintf("→ span %s .. %s (%s) — if that reads as a plausible clip length, positions are MILLISECONDS and ffmpeg -ss/-to can cut it straight from the liberated M4B",
		millisClock(start), millisClock(end), millisDuration(dur))
}

func positionMillis(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	n := parseInt(raw)
	if n <= 0 {
		return 0, false
	}
	return n, true
}

func millisClock(ms int64) string {
	s := ms / 1000
	return fmt.Sprintf("%02d:%02d:%02d.%03d", s/3600, (s%3600)/60, s%60, ms%1000)
}

func millisDuration(ms int64) string {
	if ms < 60000 {
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	}
	return fmt.Sprintf("%dm%02ds", ms/60000, (ms%60000)/1000)
}

// recordKeyCensus buckets a whispersync records map by key PREFIX (the scheme
// before "://", plus the trailing type segment) so a namespace keyed by
// annotation id is visibly different from one keyed by amzn://<ASIN>/BOOK.
// Returns (prefix census, total record count).
func recordKeyCensus(body []byte) (map[string]int, int) {
	var resp struct {
		Records map[string]json.RawMessage `json:"records"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return map[string]int{}, 0
	}
	census := map[string]int{}
	for key := range resp.Records {
		census[recordKeyShape(key)]++
	}
	return census, len(resp.Records)
}

// recordKeyShape reduces a concrete record key to its SHAPE, replacing the
// variable middle with "*": "amzn://B01ABC/BOOK" -> "amzn://*/BOOK". Shapes are
// safe to print; raw keys can encode content.
func recordKeyShape(key string) string {
	const scheme = "://"
	i := strings.Index(key, scheme)
	if i < 0 {
		if j := strings.IndexByte(key, '-'); j > 0 {
			return key[:j] + "-*"
		}
		return "(opaque)"
	}
	head := key[:i+len(scheme)]
	rest := key[i+len(scheme):]
	if k := strings.IndexByte(rest, '/'); k >= 0 {
		return head + "*" + rest[k:]
	}
	return head + "*"
}

// jsonShapeCensus describes an arbitrary JSON response one level deep: for each
// top-level key, how many entries it holds (arrays) or 1 (scalars/objects),
// plus the field keys of the first array element. Enough to tell "carries a
// clips collection" from "carries only a position".
func jsonShapeCensus(body []byte) (map[string]int, map[string][]string) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return map[string]int{}, map[string][]string{}
	}
	census := map[string]int{}
	fields := map[string][]string{}
	for k, raw := range top {
		var arr []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &arr); err == nil {
			census[k] = len(arr)
			if len(arr) > 0 {
				keys := make([]string, 0, len(arr[0]))
				for fk := range arr[0] {
					keys = append(keys, fk)
				}
				sort.Strings(keys)
				fields[k] = keys
			}
			continue
		}
		census[k] = 1
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err == nil {
			keys := make([]string, 0, len(obj))
			for fk := range obj {
				keys = append(keys, fk)
			}
			sort.Strings(keys)
			fields[k] = keys
		}
	}
	return census, fields
}

// markerCensus counts literal substrings in a document. Used on notebook HTML,
// where a class-name count is the honest signal and a full parse is overkill.
func markerCensus(text string, markers []string) map[string]int {
	out := map[string]int{}
	for _, m := range markers {
		if n := strings.Count(text, m); n > 0 {
			out[m] = n
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Roll-up
// ---------------------------------------------------------------------------

// rollUp turns the probe list into the one thing boom-siwi.1 exists to produce:
// a direction. Any single pass means the direct path is viable; all-warn means
// reachable-but-empty (re-run with a title that definitely has highlights before
// concluding); all-fail means Readwise stays the source.
func rollUp(probes []AnnotationProbe) (AnnotationVerdict, string) {
	var pass, warn, fail, skip int
	var passing []string
	for _, p := range probes {
		switch p.Verdict {
		case AnnotationPass:
			pass++
			passing = append(passing, p.Name)
		case AnnotationWarn:
			warn++
		case AnnotationFail:
			fail++
		default:
			skip++
		}
	}
	switch {
	case pass > 0:
		return AnnotationPass, fmt.Sprintf(
			"%d surface(s) returned annotation data: %s. The device credential reaches the annotation layer — boom-siwi.2 (Readwise ingest) drops to optional and boom-siwi.3/.4 proceed against our own source.",
			pass, strings.Join(passing, "; "))
	case warn > 0 && fail == 0:
		return AnnotationWarn, fmt.Sprintf(
			"every surface answered but none carried annotations (%d warn, %d skip). Inconclusive: re-run naming an ebook ASIN you KNOW has highlights before concluding Amazon withholds them.",
			warn, skip)
	case fail > 0 && pass == 0:
		return AnnotationFail, fmt.Sprintf(
			"%d surface(s) failed and none returned annotations. On this evidence the direct path is closed and Readwise stays the source (boom-siwi.2 becomes required).",
			fail)
	default:
		return AnnotationSkip, "nothing was probed — supply ASINs and a credential with a customer_id"
	}
}

// ---------------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------------

// annotationConcepts are the words a namespace, dataset or response key uses
// when it actually holds annotations. Matching on concept rather than on
// "is not the one namespace we know" is what stops device settings and
// vocabulary decks from reading as a highlight store.
var annotationConcepts = []string{"annot", "highlight", "note", "clip", "bookmark", "markup"}

// mentionsAnnotationConcept reports whether any of the strings names one.
func mentionsAnnotationConcept(ss []string) bool {
	for _, s := range ss {
		low := strings.ToLower(s)
		for _, c := range annotationConcepts {
			if strings.Contains(low, c) {
				return true
			}
		}
	}
	return false
}

// filterAnnotationish keeps the datasets whose namespace or name names an
// annotation concept.
func filterAnnotationish(ds []Dataset) []Dataset {
	out := make([]Dataset, 0, len(ds))
	for _, d := range ds {
		if mentionsAnnotationConcept([]string{d.Namespace, d.Name}) {
			out = append(out, d)
		}
	}
	return out
}

// namespaceList renders the distinct namespaces in a dataset slice, for a
// detail string that says what was actually found instead of just a count.
func namespaceList(ds []Dataset) string {
	seen := map[string]struct{}{}
	var out []string
	for _, d := range ds {
		if _, ok := seen[d.Namespace]; ok {
			continue
		}
		seen[d.Namespace] = struct{}{}
		out = append(out, d.Namespace)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

func capStrings(ss []string, n int) []string {
	if n > 0 && len(ss) > n {
		return ss[:n]
	}
	return ss
}

func containsString(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func datasetSamples(ds []Dataset) []string {
	out := make([]string, 0, len(ds))
	for i, d := range ds {
		if i >= 8 {
			break
		}
		out = append(out, d.Namespace+" / "+firstNonBlank(d.Name, d.Identifier))
	}
	return out
}

func mapKeysSorted(m map[string]int, limit int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if limit > 0 && len(keys) > limit {
		keys = keys[:limit]
	}
	return keys
}

func firstNonBlank(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…(truncated)"
}
