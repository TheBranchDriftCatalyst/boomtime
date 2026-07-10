package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v5"
)

func ctxWithQuery(rawQuery string) *echo.Context {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/?"+rawQuery, nil)
	rec := httptest.NewRecorder()
	return e.NewContext(req, rec)
}

func TestCollectExploreFiltersRejectsUnknown(t *testing.T) {
	// A non-whitelisted filter axis is a 400.
	c := ctxWithQuery("language=Go&sender=evil")
	_, aerr := collectExploreFilters(c)
	if aerr == nil {
		t.Fatal("expected 400 for non-whitelisted filter axis 'sender'")
	}
	if aerr.Status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", aerr.Status)
	}

	// A raw DB column name (not an FE axis) is also rejected — the whitelist maps
	// FE names to columns, so passing the underlying column name is not allowed.
	c = ctxWithQuery("is_write=true")
	if _, aerr := collectExploreFilters(c); aerr == nil {
		t.Fatal("expected 400 for raw column name 'is_write' (FE axis is 'isWrite')")
	}
}

func TestCollectExploreFiltersAcceptsWhitelisted(t *testing.T) {
	// Reserved params are ignored; whitelisted axes become equality filters;
	// an empty value becomes an IS NULL filter.
	c := ctxWithQuery("groupBy=day&start=x&end=y&page=2&limit=50&entity=foo&language=Go&project=")
	filters, aerr := collectExploreFilters(c)
	if aerr != nil {
		t.Fatalf("unexpected error: %v", aerr)
	}
	if len(filters) != 2 {
		t.Fatalf("filters = %d, want 2 (language, project); got %+v", len(filters), filters)
	}
	var sawGoValue, sawNull bool
	for _, f := range filters {
		if f.Column == "language" && f.Value != nil && *f.Value == "Go" {
			sawGoValue = true
		}
		if f.Column == "project" && f.Value == nil {
			sawNull = true
		}
	}
	if !sawGoValue {
		t.Fatal("expected language=Go equality filter")
	}
	if !sawNull {
		t.Fatal("expected project (empty) => IS NULL filter")
	}
}
