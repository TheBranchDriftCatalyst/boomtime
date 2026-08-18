// reading_curation_test.go — pins the effective-status DSL contract (migration
// 00069): the `status` dimension + `finished` measure read EFFECTIVE status
// (COALESCE(status_override, status)), the new `statusDerived` axis reads the raw
// Amazon value, and the leaf-rows projection exposes both layers + the override
// flags. Because reading goals run through query.Q("reading"), the same
// grouped/scalar assertions here are exactly what a reading goal inherits.
package query_test

import (
	"context"
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/query"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

// seedCurated inserts one reading_item with an optional status_override. A nil
// override leaves the row on its Amazon-derived status.
func seedCurated(t *testing.T, hz *testutil.Harness, owner, asin, derived string, override *string) {
	t.Helper()
	_, err := hz.DB.Pool.Exec(context.Background(), `
		INSERT INTO reading_items (owner, source, external_id, title, status, status_override)
		VALUES ($1,'kindle',$2,$3,$4,$5)
		ON CONFLICT (owner, source, external_id) DO UPDATE
		   SET status=EXCLUDED.status, status_override=EXCLUDED.status_override`,
		owner, asin, "Title "+asin, derived, override)
	if err != nil {
		t.Fatalf("seed curated %s: %v", asin, err)
	}
	t.Cleanup(func() {
		_, _ = hz.DB.Pool.Exec(context.Background(), `DELETE FROM reading_items WHERE owner=$1`, owner)
	})
}

func TestReading_EffectiveStatus_GroupingAndFinished(t *testing.T) {
	hz := testutil.NewHarness(t)
	owner, _ := hz.MintUser("q_reading_curation")

	// Three rows, all Amazon-derived 'reading'; overrides move two of them.
	seedCurated(t, hz, owner, "A", "reading", nil)        // effective reading
	seedCurated(t, hz, owner, "B", "reading", sp("dnf"))  // effective dnf
	seedCurated(t, hz, owner, "C", "reading", sp("read")) // effective read (promoted)

	// Group by EFFECTIVE status: one row in each of reading/dnf/read.
	res, err := query.Run(context.Background(), hz.DB.Pool, owner,
		query.Q("reading").Measure("books").Group("status"))
	if err != nil {
		t.Fatalf("group by status: %v", err)
	}
	eff := groupMap(res)
	if eff["reading"] != 1 || eff["dnf"] != 1 || eff["read"] != 1 {
		t.Fatalf("effective status grouping = %v, want reading/dnf/read = 1 each", eff)
	}

	// Group by RAW derived status: all three are 'reading'.
	res, err = query.Run(context.Background(), hz.DB.Pool, owner,
		query.Q("reading").Measure("books").Group("statusDerived"))
	if err != nil {
		t.Fatalf("group by statusDerived: %v", err)
	}
	raw := groupMap(res)
	if raw["reading"] != 3 {
		t.Fatalf("statusDerived grouping = %v, want reading = 3 (raw, un-overridden)", raw)
	}

	// finished measure counts EFFECTIVE 'read' — only C (the promoted override),
	// NOT B (dnf) and NOT A (reading).
	res, err = query.Run(context.Background(), hz.DB.Pool, owner,
		query.Q("reading").Measure("finished"))
	if err != nil {
		t.Fatalf("finished measure: %v", err)
	}
	if res.Scalar != 1 {
		t.Fatalf("effective finished = %v, want 1 (only the read override)", res.Scalar)
	}
}

func TestReading_Rows_ExposeCurationLayers(t *testing.T) {
	hz := testutil.NewHarness(t)
	owner, _ := hz.MintUser("q_reading_rows_cur")

	seedCurated(t, hz, owner, "B0ROWDNF", "reading", sp("dnf"))
	seedCurated(t, hz, owner, "B0ROWRAW", "reading", nil)

	res, err := query.Run(context.Background(), hz.DB.Pool, owner,
		query.Q("reading").Rows().Page(1, 50))
	if err != nil {
		t.Fatalf("rows: %v", err)
	}
	if res.Kind != query.ResultRows {
		t.Fatalf("kind = %v, want rows", res.Kind)
	}
	byExt := map[string]map[string]any{}
	for _, r := range res.Rows {
		byExt[r["externalId"].(string)] = r
	}

	dnf := byExt["B0ROWDNF"]
	if dnf["status"] != "dnf" {
		t.Fatalf("row status (effective) = %v, want dnf", dnf["status"])
	}
	if dnf["statusDerived"] != "reading" {
		t.Fatalf("row statusDerived = %v, want reading", dnf["statusDerived"])
	}
	if dnf["statusOverride"] != "dnf" {
		t.Fatalf("row statusOverride = %v, want dnf", dnf["statusOverride"])
	}
	if dnf["statusIsOverride"] != true {
		t.Fatalf("row statusIsOverride = %v, want true", dnf["statusIsOverride"])
	}

	raw := byExt["B0ROWRAW"]
	if raw["status"] != "reading" || raw["statusOverride"] != nil || raw["statusIsOverride"] != false {
		t.Fatalf("un-overridden row = status:%v override:%v isOverride:%v, want reading/nil/false",
			raw["status"], raw["statusOverride"], raw["statusIsOverride"])
	}
	// The Rows projection must carry hardcoverSlug (gaka-qic0) so the Books table
	// can deep-link. A never-matched seed leaves it NULL, but the KEY must be
	// present (the projection is wired), else the FE column silently vanishes.
	if _, ok := raw["hardcoverSlug"]; !ok {
		t.Fatalf("Rows projection missing hardcoverSlug key: %v", raw)
	}
}

// sp is a local *string helper.
func sp(s string) *string { return &s }

// groupMap folds a groups result into key→value.
func groupMap(res query.Result) map[string]float64 {
	m := map[string]float64{}
	for _, g := range res.Groups {
		m[g.Key] = g.Value
	}
	return m
}
