package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

func TestStatusClass(t *testing.T) {
	cases := map[int]string{
		0:   "error", // transport failure, no response
		100: "1xx",
		204: "2xx",
		302: "3xx",
		404: "4xx",
		500: "5xx",
		700: "error", // out of range
	}
	for code, want := range cases {
		if got := StatusClass(code); got != want {
			t.Errorf("StatusClass(%d) = %q, want %q", code, got, want)
		}
	}
}

// TestInstrumentTransportRecordsClientMetric drives real requests through the
// instrumented transport against a stub upstream and asserts the outbound RED
// counter advances — and, crucially, that TWO DIFFERENT PATHS on the SAME host
// collapse onto ONE series (label cardinality bounded by host, not path).
func TestInstrumentTransportRecordsClientMetric(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent) // 204 → 2xx
	}))
	defer srv.Close()

	host := mustHost(t, srv.URL)
	client := &http.Client{Transport: InstrumentTransport(nil)}

	ctr := HTTPClientRequestsTotal.WithLabelValues(host, http.MethodGet, "2xx")
	before := testutil.ToFloat64(ctr)

	// Two distinct paths on the same host.
	for _, p := range []string{"/a/1", "/b/2"} {
		resp, err := client.Get(srv.URL + p)
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}

	if got := testutil.ToFloat64(ctr) - before; got != 2 {
		t.Errorf("http_client_requests_total{host=%s,GET,2xx} delta = %v, want 2 (both paths collapse to one series)", host, got)
	}

	// The duration histogram observed both calls (SampleCount advanced by 2).
	if got := histCount(t, HTTPClientRequestDuration.WithLabelValues(host)); got < 2 {
		t.Errorf("http_client_request_duration_seconds{host=%s} sample count = %d, want >= 2", host, got)
	}
}

// TestInstrumentTransportRecordsErrorClass verifies a transport-level failure
// (no response) is recorded with status class "error" and the base's error is
// returned verbatim.
func TestInstrumentTransportRecordsErrorClass(t *testing.T) {
	rt := InstrumentTransport(errRT{})
	ctr := HTTPClientRequestsTotal.WithLabelValues("err.example", http.MethodGet, "error")
	before := testutil.ToFloat64(ctr)

	req := httptest.NewRequest(http.MethodGet, "http://err.example/x", nil)
	resp, err := rt.RoundTrip(req)
	if err == nil || resp != nil {
		t.Fatalf("RoundTrip = (%v, %v), want (nil, error)", resp, err)
	}
	if got := testutil.ToFloat64(ctr) - before; got != 1 {
		t.Errorf("error-class counter delta = %v, want 1", got)
	}
}

// TestHandlerServesPrometheusText asserts /metrics-style output is served and
// contains a known series after it is touched.
func TestHandlerServesPrometheusText(t *testing.T) {
	HardcoverCallsTotal.WithLabelValues("executed").Inc()

	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	// A CounterVec with no observed label combinations emits no lines, so we
	// assert only families we actually touched here (hardcover_calls_total,
	// incremented above) plus the always-present runtime collectors.
	for _, want := range []string{
		"# TYPE hardcover_calls_total counter",
		`hardcover_calls_total{outcome="executed"}`,
		"go_goroutines",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics body missing %q", want)
		}
	}
}

// --- helpers ----------------------------------------------------------------

type errRT struct{}

func (errRT) RoundTrip(*http.Request) (*http.Response, error) { return nil, io.EOF }

func mustHost(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u.Hostname()
}

// histCount reads a histogram observer's SampleCount by writing the underlying
// metric to a dto.Metric.
func histCount(t *testing.T, o prometheus.Observer) uint64 {
	t.Helper()
	m, ok := o.(prometheus.Metric)
	if !ok {
		t.Fatalf("observer %T is not a prometheus.Metric", o)
	}
	var dm dto.Metric
	if err := m.Write(&dm); err != nil {
		t.Fatalf("write metric: %v", err)
	}
	return dm.GetHistogram().GetSampleCount()
}
