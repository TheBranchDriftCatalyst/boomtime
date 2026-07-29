package testutil

import (
	"fmt"
	"net/http/httptest"

	"github.com/onsi/gomega/format"
	"github.com/onsi/gomega/types"
)

// HaveStatus asserts on the Code of an *httptest.ResponseRecorder. On
// failure the message ALWAYS includes the response body (truncated to
// 1 KiB) — the single most useful diagnostic when a request returns an
// unexpected 4xx/5xx.
//
// Replaces the pervasive:
//
//	Expect(rec.Code).To(Equal(http.StatusOK), "body=%s", rec.Body.String())
//
// with the cleaner:
//
//	Expect(rec).To(HaveStatus(http.StatusOK))
func HaveStatus(want int) types.GomegaMatcher {
	return &haveStatus{want: want}
}

type haveStatus struct{ want int }

func (m *haveStatus) Match(actual any) (bool, error) {
	rec, ok := actual.(*httptest.ResponseRecorder)
	if !ok {
		return false, fmt.Errorf("HaveStatus expects *httptest.ResponseRecorder, got %T", actual)
	}
	return rec.Code == m.want, nil
}

func (m *haveStatus) FailureMessage(actual any) string {
	rec := actual.(*httptest.ResponseRecorder)
	return fmt.Sprintf("expected response status\n\t%d\ngot\n\t%d\nbody:\n%s",
		m.want, rec.Code, previewBody(rec))
}

func (m *haveStatus) NegatedFailureMessage(actual any) string {
	rec := actual.(*httptest.ResponseRecorder)
	return fmt.Sprintf("expected response NOT to have status\n\t%d\nbut it did (body:\n%s)",
		m.want, previewBody(rec))
}

// HaveHeader asserts an *httptest.ResponseRecorder has a header equal
// to want. Case-insensitive on the header name (http.Header.Get already
// canonicalises). Replaces:
//
//	Expect(rec.Header().Get("Cache-Control")).To(Equal("private,max-age=30"))
//
// with:
//
//	Expect(rec).To(HaveHeader("Cache-Control", "private,max-age=30"))
func HaveHeader(name, want string) types.GomegaMatcher {
	return &haveHeader{name: name, want: want}
}

type haveHeader struct{ name, want string }

func (m *haveHeader) Match(actual any) (bool, error) {
	rec, ok := actual.(*httptest.ResponseRecorder)
	if !ok {
		return false, fmt.Errorf("HaveHeader expects *httptest.ResponseRecorder, got %T", actual)
	}
	return rec.Header().Get(m.name) == m.want, nil
}

func (m *haveHeader) FailureMessage(actual any) string {
	rec := actual.(*httptest.ResponseRecorder)
	return fmt.Sprintf("expected header %q\n\t%q\ngot\n\t%q",
		m.name, m.want, rec.Header().Get(m.name))
}

func (m *haveHeader) NegatedFailureMessage(actual any) string {
	rec := actual.(*httptest.ResponseRecorder)
	return fmt.Sprintf("expected header %q NOT to equal %q, but it did (=%q)",
		m.name, m.want, rec.Header().Get(m.name))
}

// previewBody returns rec.Body.String() truncated to 1 KiB. gomega's
// format package handles longer inputs gracefully already, but we clip
// here so the failure message stays skimmable — anything longer than a
// screen of JSON is almost never the payload that pinpoints the bug.
func previewBody(rec *httptest.ResponseRecorder) string {
	const cap = 1024
	body := rec.Body.String()
	if len(body) <= cap {
		return format.Object(body, 1)
	}
	return format.Object(body[:cap]+"…(truncated)", 1)
}
