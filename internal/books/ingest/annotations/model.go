// model.go — the pure mapping layer: wire annotation -> db.BookAnnotation, and
// the idempotency key both sources share.
package annotations

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/connect/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/books/events"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
)

// parserVersion is bumped whenever extraction changes shape. It rides in
// raw_meta so a corpus produced by a parser we later find to be wrong can be
// identified and re-derived, rather than being indistinguishable from a corpus
// produced by the fixed one. For an HTML scraper this is the difference between
// a targeted backfill and a full re-sync.
const parserVersion = 1

// AnnotationKey builds the storage idempotency key.
//
// It is POSITIONAL rather than Amazon's own annotationId, because the one
// annotationId this repo has seen in full — documented in sidecar.go as
// "<deviceId>-<ASIN>-EBOK-furthest-page-read" — embeds the DEVICE SERIAL. This
// app supports re-registering the Amazon device, and a re-registration would
// mint a new serial and silently double the entire corpus.
//
// The `fallbackID` guard matters more than it looks. If position parsing ever
// fails, every annotation on a book collapses to position 0 and therefore to the
// SAME key — quietly reducing a hundred highlights to one row per kind, with no
// error anywhere. When there is no usable position we key on the DOM id instead
// and accept the weaker stability, because a duplicate row is recoverable and
// silent data loss is not.
func AnnotationKey(kind string, start int64, end *int64, fallbackID string) string {
	if start <= 0 {
		if fallbackID == "" {
			return ""
		}
		return kind + ":id:" + fallbackID
	}
	if end != nil {
		return fmt.Sprintf("%s:%d:%d", kind, start, *end)
	}
	return fmt.Sprintf("%s:%d", kind, start)
}

// kindleEventType maps an annotation kind onto its registered event type. The
// mapping is explicit rather than string-built ("annotation."+kind+".captured")
// so an unregistered kind is a compile-visible gap here instead of a runtime
// "unregistered event type" deep inside a sweep.
var kindleEventType = map[string]string{
	amazon.AnnotationKindHighlightStr: "annotation.highlight.captured",
	amazon.AnnotationKindNoteStr:      "annotation.note.captured",
}

// extractKindleAnnotation is this source's Extractor: one parsed notebook
// annotation becomes one registered Event. Plain Go rather than a declarative
// field-mapping, because the input shapes across sources have nothing in common
// — see the events package doc.
//
// ok=false means "not an event": an unkeyable annotation (it would duplicate on
// every sync) or a kind with no registered type.
//
// NOTE the absent timestamp. The notebook reports no capture time, and
// annotation.*.captured is declared TimeUnknown, so Event.At stays nil. This is
// not an oversight to be tidied up later: events.Validate REFUSES a timestamp on
// these types precisely so nobody "helpfully" stamps time.Now() and dates the
// entire corpus to the first sync.
var extractKindleAnnotation events.Extractor[amazon.KindleAnnotation] = func(owner, asin string, a amazon.KindleAnnotation) (events.Event, bool) {
	typeName, known := kindleEventType[a.Kind]
	if !known {
		return events.Event{}, false
	}
	key := AnnotationKey(a.Kind, a.Position, nil, a.AnnotationID)
	if key == "" {
		return events.Event{}, false
	}
	unit := a.PositionUnit
	if unit == "" {
		unit = db.PositionUnitLocation
	}
	attrs := map[string]string{
		"kind":          a.Kind,
		"positionUnit":  unit,
		"positionStart": strconv.FormatInt(a.Position, 10),
	}
	if a.Kind == amazon.AnnotationKindHighlightStr && a.Note != "" {
		attrs["hasNote"] = "true"
	}
	return events.Event{
		Type:  typeName,
		Owner: owner,
		Scope: asin,
		Key:   key,
		At:    nil, // TimeUnknown — enforced by events.Validate
		Attrs: attrs,
	}, true
}

// rowFromEvent builds the storage row from an ALREADY-VALIDATED event plus the
// source record it came from.
//
// The ordering is the point: the event is validated first, so the declaration in
// the registry gates what reaches the database rather than merely describing it.
// CapturedAt is taken from the event, which for a TimeUnknown type can only ever
// be nil.
func rowFromEvent(ev events.Event, a amazon.KindleAnnotation) db.BookAnnotation {
	return db.BookAnnotation{
		Owner:         ev.Owner,
		Source:        source,
		ExternalID:    ev.Scope,
		Kind:          ev.Attrs["kind"],
		AnnotationKey: ev.Key,
		PositionUnit:  ev.Attrs["positionUnit"],
		PositionStart: a.Position,
		Body:          a.Body,
		Note:          a.Note,
		CapturedAt:    ev.At,
		RawMeta:       kindleRawMeta(a),
	}
}

// buildFromKindle runs the full extract → validate → row chain for one
// annotation. Returns ok=false when the record is not an event, and an error
// only when a produced event VIOLATES its own declaration — which is a bug in
// this package, not bad input, and is surfaced rather than swallowed.
func buildFromKindle(owner, asin string, a amazon.KindleAnnotation, _ time.Time) (db.BookAnnotation, bool, error) {
	ev, ok := extractKindleAnnotation(owner, asin, a)
	if !ok {
		return db.BookAnnotation{}, false, nil
	}
	if err := events.Validate(ev); err != nil {
		return db.BookAnnotation{}, false, err
	}
	return rowFromEvent(ev, a), true, nil
}

// kindleRawMeta snapshots the source record for debugging and re-derivation. It
// is NEVER read by business logic — the same contract reading_items.raw_meta
// carries. The DOM id and the metadata prose are kept precisely because they are
// what a future parser fix would need to re-interpret an existing corpus.
func kindleRawMeta(a amazon.KindleAnnotation) []byte {
	blob, err := json.Marshal(struct {
		ParserVersion int    `json:"parserVersion"`
		AnnotationID  string `json:"annotationId,omitempty"`
		MetadataRaw   string `json:"metadataRaw,omitempty"`
		Color         string `json:"color,omitempty"`
		PositionUnit  string `json:"positionUnit,omitempty"`
	}{parserVersion, a.AnnotationID, a.MetadataRaw, a.Color, a.PositionUnit})
	if err != nil {
		return nil
	}
	return blob
}
