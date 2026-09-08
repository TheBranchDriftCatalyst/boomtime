package amazon

import (
	"reflect"
	"strings"
	"testing"
)

// sidecarWithHighlights is the shape the probe exists to detect: the SAME
// envelope sidecar.go already parses, carrying annotation records alongside the
// kindle.lpr position record it keeps. parseLastPagePosition `continue`s past
// every non-lpr record, so if Amazon ships this, the ingest silently drops it.
const sidecarWithHighlights = `{
  "md5": "deadbeef",
  "payload": {"records": [
    {"type": "kindle.lpr", "location": "9283", "creationTime": "2026-08-07 03:03:02.0"},
    {"type": "kindle.highlight", "startLocation": "1201", "endLocation": "1240",
     "text": "the highlighted prose", "creationTime": "2026-08-01 10:00:00.0"},
    {"type": "kindle.highlight", "startLocation": "3300", "endLocation": "3312",
     "text": "another one", "note": "a margin note", "creationTime": "2026-08-02 11:00:00.0"},
    {"location": "77"}
  ]}
}`

const sidecarPositionOnly = `{"payload":{"records":[
  {"type":"kindle.lpr","location":"9283","creationTime":"2026-08-07 03:03:02.0"}
]}}`

func TestSidecarRecordCensusCountsEveryTypeNotJustLPR(t *testing.T) {
	census, fields := sidecarRecordCensus([]byte(sidecarWithHighlights))

	want := map[string]int{"kindle.lpr": 1, "kindle.highlight": 2, "(untyped)": 1}
	if !reflect.DeepEqual(census, want) {
		t.Fatalf("census = %v, want %v", census, want)
	}

	// The point of the probe: a highlight record must survive the parse. The
	// production position parser drops it, which is the defect this whole spike
	// is testing for.
	if census["kindle.highlight"] == 0 {
		t.Fatal("highlight records were dropped — the census is filtering like parseLastPagePosition")
	}

	// Field keys are UNIONED across records of a type, so a note that appears on
	// only the second highlight is still described.
	wantFields := []string{"creationTime", "endLocation", "note", "startLocation", "text", "type"}
	if !reflect.DeepEqual(fields["kindle.highlight"], wantFields) {
		t.Fatalf("highlight fields = %v, want %v", fields["kindle.highlight"], wantFields)
	}
}

// The control: a title whose sidecar carries only the position record is the
// WARN case, not a pass. Getting this wrong would report "annotations found" for
// every book that has ever been opened.
func TestSidecarRecordCensusPositionOnlyIsNotAPass(t *testing.T) {
	census, _ := sidecarRecordCensus([]byte(sidecarPositionOnly))
	if !onlyPositionRecords(census) {
		t.Fatalf("census %v should be position-only", census)
	}
	if onlyPositionRecords(map[string]int{"kindle.lpr": 1, "kindle.highlight": 1}) {
		t.Fatal("a census containing a highlight must not read as position-only")
	}
}

func TestSidecarRecordCensusEmptyAndMalformed(t *testing.T) {
	census, _ := sidecarRecordCensus([]byte(`{"payload":{"records":[]}}`))
	if len(census) != 0 {
		t.Fatalf("empty records should census to nothing, got %v", census)
	}
	if c, _ := sidecarRecordCensus([]byte(`not json`)); c != nil {
		t.Fatalf("malformed body should census to nil, got %v", c)
	}
}

func TestRecordKeyCensusShapesKeysWithoutLeakingThem(t *testing.T) {
	body := `{"records":{
	  "amzn://B01ABCDEFG/BOOK": {"isDeleted": false},
	  "amzn://B09ZZZZZZZ/BOOK": {"isDeleted": false},
	  "annot://B01ABCDEFG/HIGHLIGHT": {"isDeleted": false}
	}}`
	census, total := recordKeyCensus([]byte(body))
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
	want := map[string]int{"amzn://*/BOOK": 2, "annot://*/HIGHLIGHT": 1}
	if !reflect.DeepEqual(census, want) {
		t.Fatalf("census = %v, want %v", census, want)
	}
	// A shape must never carry the concrete ASIN — these get printed to a
	// terminal and pasted into tickets.
	for k := range census {
		if strings.Contains(k, "B01ABCDEFG") {
			t.Fatalf("shape %q leaked a concrete identifier", k)
		}
	}
}

func TestMarkerCensusDistinguishesSigninBounceFromHighlights(t *testing.T) {
	bounce := markerCensus(`<html><a href="/ap/signin?openid">Sign in</a></html>`, notebookMarkers)
	if bounce["ap/signin"] == 0 {
		t.Fatal("a sign-in bounce must be detectable — it returns HTTP 200 like a real page")
	}
	if bounce["kp-notebook-highlight"] != 0 {
		t.Fatal("bounce page should carry no highlight markers")
	}

	real := markerCensus(
		`<div id="kp-notebook-annotations"><span class="kp-notebook-highlight">a</span>`+
			`<span class="kp-notebook-highlight">b</span><div class="kp-notebook-note">n</div></div>`,
		notebookMarkers)
	if real["kp-notebook-highlight"] != 2 {
		t.Fatalf("highlight markers = %d, want 2", real["kp-notebook-highlight"])
	}
}

func TestJSONShapeCensusDescribesCollectionsAndScalars(t *testing.T) {
	body := `{"asin_last_position_heard_annots":[{"asin":"B0X","last_position_heard":{"status":"OK"}}],
	          "clips":[],"total":1}`
	census, fields := jsonShapeCensus([]byte(body))
	if census["asin_last_position_heard_annots"] != 1 {
		t.Fatalf("annots count = %d, want 1", census["asin_last_position_heard_annots"])
	}
	if census["clips"] != 0 {
		t.Fatalf("empty clips array should census to 0, got %d", census["clips"])
	}
	if got := fields["asin_last_position_heard_annots"]; !reflect.DeepEqual(got, []string{"asin", "last_position_heard"}) {
		t.Fatalf("fields = %v", got)
	}
}

// The roll-up is what turns a pile of probes into boom-siwi's direction, so its
// three branches are asserted directly.
func TestRollUpDirection(t *testing.T) {
	pass, summary := rollUp([]AnnotationProbe{
		{Name: "sidecar EBOK", Verdict: AnnotationPass},
		{Name: "notebook", Verdict: AnnotationWarn},
	})
	if pass != AnnotationPass {
		t.Fatalf("one pass should carry the run, got %s", pass)
	}
	if !strings.Contains(summary, "sidecar EBOK") {
		t.Fatalf("summary must name the passing surface: %q", summary)
	}

	warn, _ := rollUp([]AnnotationProbe{{Verdict: AnnotationWarn}, {Verdict: AnnotationSkip}})
	if warn != AnnotationWarn {
		t.Fatalf("all-warn should be inconclusive, got %s", warn)
	}

	fail, _ := rollUp([]AnnotationProbe{{Verdict: AnnotationFail}, {Verdict: AnnotationWarn}})
	if fail != AnnotationFail {
		t.Fatalf("a failure with no pass should close the direct path, got %s", fail)
	}

	none, _ := rollUp(nil)
	if none != AnnotationSkip {
		t.Fatalf("no probes should be skip, got %s", none)
	}
}
