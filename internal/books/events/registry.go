// registry.go — the books domain's event catalog.
//
// Only events that a producer ACTUALLY EMITS today are registered. A registry
// that also lists aspirational events teaches consumers to code against things
// that never arrive, so phase-2/3 types (annotation.clip.captured,
// annotation.bookmark.captured from the Audible sidecar; annotation.transcribed
// from the whisper pass) land here when their producer does.
//
// NOTE WHY HIGHLIGHT AND CLIP ARE SEPARATE TYPES rather than one
// "annotation.captured" carrying a kind field. They disagree on the one thing a
// type must be able to state unambiguously: a Kindle notebook highlight has NO
// source timestamp (TimeUnknown), while an Audible clip carries a real
// creationTime (TimeSourceReported). A single type could not declare both, and
// collapsing them would mean either fabricating a time for highlights or
// discarding a real one for clips. The `kind` axis still exists for querying —
// it is a dimension on the annotations query domain — but the EVENT identity is
// finer-grained than the storage row's kind column, on purpose.
package events

func init() {
	Register(Type{
		Name:       "annotation.highlight.captured",
		Source:     "kindle",
		Extraction: SetReconcile,
		Time:       TimeUnknown,
		Confidence: Observed,
		KeyDoc: "kind:location (positional). NOT Amazon's annotationId: the only " +
			"annotationId this codebase has seen in full embeds the DEVICE SERIAL, so " +
			"re-registering the Amazon device would mint new ids and silently double the " +
			"corpus. Falls back to kind:id:<domId> when the location cannot be parsed, " +
			"because a position-less key of kind:0 would collapse every highlight on a " +
			"book into one row.",
		Doc: "A passage the user highlighted in a Kindle book, read from " +
			"read.amazon.com/notebook. The notebook exposes no capture time — hence " +
			"TimeUnknown, which Validate enforces by refusing any timestamp on this type.",
		Fields: []Field{
			{Name: "kind", Required: true, Enum: []string{"highlight"}, Doc: "the annotation kind"},
			{Name: "positionUnit", Required: true, Enum: []string{"location", "page"},
				Doc: "declared per row, never inferred from source: a reflowable Kindle book reports a location, a print replica reports a page"},
			{Name: "positionStart", Required: true, Doc: "where the highlight begins, in positionUnit"},
			{Name: "hasNote", Doc: "true when the user attached a margin note to this highlight"},
		},
	})

	Register(Type{
		Name:       "annotation.note.captured",
		Source:     "kindle",
		Extraction: SetReconcile,
		Time:       TimeUnknown,
		Confidence: Observed,
		KeyDoc:     "kind:location, same rule as annotation.highlight.captured.",
		Doc: "A standalone note — one the user wrote without highlighting a passage. " +
			"A note attached TO a highlight is folded into that highlight instead of " +
			"emitting separately, or every annotated passage would count twice.",
		Fields: []Field{
			{Name: "kind", Required: true, Enum: []string{"note"}},
			{Name: "positionUnit", Required: true, Enum: []string{"location", "page"}},
			{Name: "positionStart", Required: true},
		},
	})
}
