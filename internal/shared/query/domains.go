package query

// domains.go registers boomtime's built-in query domains. Adding a new domain
// (health, media, …) is a matter of another Register call here — the compiler,
// grammar, and safety model are domain-agnostic.
//
// TABLE / OWNER-COLUMN map (the only per-table trusted facts the compiler needs):
//   - hb_rollup_daily          owner col = "sender"  date col = "day"
//   - reading_activity         owner col = "owner"   date col = "bucket_date"
//   - reading_items            owner col = "owner"   date col = "finished_at"
//   - reading_events_enriched  owner col = "owner"   date col = "finished_at"

func init() {
	registerCoding()
	registerReading()
	registerReadingEvents()
}

// registerCoding wires the coding domain over hb_rollup_daily. One measure —
// attributed seconds — grouped/filtered by the eight rollup axes.
func registerCoding() {
	const table = "hb_rollup_daily"
	axes := []string{"project", "language", "editor", "category", "branch", "plugin", "machine", "platform"}

	dims := map[string]Dimension{}
	for _, a := range axes {
		dims[a] = Dimension{Name: a, Table: table, Expr: a} // Expr = the raw column
	}

	Register(Domain{
		Name: "coding",
		Measures: map[string]Measure{
			"seconds": {
				Name:     "seconds",
				Table:    table,
				Expr:     "sum(total_seconds)",
				DateCol:  "day",
				OwnerCol: "sender",
				Dims:     axes,
			},
		},
		Dimensions: dims,
	})
}

// registerReading wires the reading domain. It spans TWO tables and the split is
// enforced by each measure's Dims:
//   - seconds  → reading_activity (real listening time; NO per-book attribution,
//     so it groups only by source/date).
//   - books    → reading_items count (carries the book dimensions).
//   - runtime  → reading_items sum(runtime_min) (book dimensions).
//
// genre is a jsonb dimension: reading_items.genres is a JSON array; v1 exposes
// the FIRST element (genres->>0) as the single-genre axis. (Full per-row
// multi-genre fan-out would need an unnest/join — deferred past v1.)
func registerReading() {
	const (
		activity = "reading_activity"
		items    = "reading_items"
	)

	// NOTE: "source" is a column on BOTH reading_activity and reading_items with
	// identical semantics, so it is ONE shared dimension. The same-table guard
	// is enforced by each measure's Dims whitelist (a measure only lists dims
	// whose Expr is valid on its own table) — NOT by comparing Dimension.Table,
	// which is descriptive only. Table below marks each dim's primary home.
	dims := map[string]Dimension{
		"source": {Name: "source", Table: activity, Expr: "source"},
		// status is the EFFECTIVE status = COALESCE(status_override, status): the
		// default group-by axis, the filter vocabulary, and what reading goals +
		// rollups read (migration 00069). DNF/Paused overrides move a book out of the
		// "reading" set here without losing the Amazon-derived value.
		"status": {Name: "status", Table: items, Expr: "COALESCE(status_override, status)"},
		// statusDerived is the RAW Amazon-computed status (un-overridden): the
		// >95%audio/100%kindle=read heuristic lives in ingest.statusFromPercent, so
		// grouping by this axis shows the source's want/reading/read buckets while
		// `status` shows them with curation overrides applied.
		"statusDerived": {Name: "statusDerived", Table: items, Expr: "status"},
		"series":        {Name: "series", Table: items, Expr: "series"},
		"author":        {Name: "author", Table: items, Expr: "authors"},
		"genre":         {Name: "genre", Table: items, Expr: "genres->>0"},
		// list is the Hardcover LIST-membership axis (migration 00077) — a book
		// property, jsonb array of list names. v1 exposes the FIRST list (like genre)
		// as the single-value group/filter axis; the panel shows the full chip set.
		"list": {Name: "list", Table: items, Expr: "hardcover_lists->>0"},
		// isMatched is the Hardcover MATCH-STATE axis (matched vs unmatched) —
		// distinct from a shelf status. It's a derived boolean rendered as a label
		// so a group-by splits the library into "linked to Hardcover" vs "not yet
		// matched" buckets you can act on (e.g. find everything the match sweep
		// still needs to resolve). matched = hardcover_book_id IS NOT NULL.
		"isMatched": {Name: "isMatched", Table: items, Expr: "case when hardcover_book_id is not null then 'matched' else 'unmatched' end"},
		// syncState is the finer Hardcover reconcile facet (a meta-status): a matched
		// book whose EFFECTIVE status diverges from the last-seen Hardcover shelf
		// (hardcover_status) is 'diverged' — a pending sync change, exactly what the
		// amber diff badge shows — vs 'synced' (matched + agreeing) vs 'unmatched'.
		// Lets a user filter/group to "everything out of sync with Hardcover".
		"syncState": {Name: "syncState", Table: items, Expr: "case when hardcover_book_id is null then 'unmatched' when hardcover_status is not null and hardcover_status is distinct from COALESCE(status_override, status) then 'diverged' else 'synced' end"},
		// title is a filter-oriented dimension (books SEARCH folds an ILIKE on it);
		// it is in the reading_items measures' Dims whitelist so it can filter, but
		// the FE offers no title group axis (grouping by a near-unique key is moot).
		"title": {Name: "title", Table: items, Expr: "title"},
		// liberationStatus is the catalyst-books LIBERATION axis (boom-w20s,
		// migration 00082): has this title been downloaded + DRM-stripped into the
		// local library, and if not, why. Rows never attempted are NULL in the
		// column, which would silently vanish from a group-by, so they are folded
		// into an explicit 'none' bucket — "how many have I not liberated" is the
		// question this axis mostly gets asked.
		"liberationStatus": {Name: "liberationStatus", Table: items, Expr: "COALESCE(liberation_status, 'none')"},
	}

	Register(Domain{
		Name: "reading",
		Measures: map[string]Measure{
			"seconds": {
				Name:     "seconds",
				Table:    activity,
				Expr:     "sum(listening_seconds)",
				DateCol:  "bucket_date",
				OwnerCol: "owner",
				Dims:     []string{"source"},
			},
			"books": {
				Name:     "books",
				Table:    items,
				Expr:     "count(*)",
				DateCol:  "finished_at",
				OwnerCol: "owner",
				Dims:     []string{"source", "status", "statusDerived", "isMatched", "syncState", "series", "author", "genre", "list", "title", "liberationStatus"},
			},
			"runtime": {
				Name:     "runtime",
				Table:    items,
				Expr:     "sum(runtime_min)",
				DateCol:  "finished_at",
				OwnerCol: "owner",
				Dims:     []string{"source", "status", "statusDerived", "isMatched", "syncState", "series", "author", "genre", "list", "title", "liberationStatus"},
			},
			// finished is a rollup-oriented measure: how many rows in a group are
			// finished — counted off EFFECTIVE status='read' (migration 00069), so a
			// DNF/Paused override drops out of the finished tally and an Amazon-finish
			// promotion counts. Same table/date/owner as books+runtime so it can ride
			// as a rollup alongside either.
			"finished": {
				Name:     "finished",
				Table:    items,
				Expr:     "sum(case when COALESCE(status_override, status) = 'read' then 1 else 0 end)",
				DateCol:  "finished_at",
				OwnerCol: "owner",
				Dims:     []string{"source", "status", "statusDerived", "isMatched", "syncState", "series", "author", "genre", "list", "title", "liberationStatus"},
			},
		},
		Dimensions: dims,

		// Leaf-rows source: the reading_items projection the groupable explorer
		// drills down to. Each column Name is the JSON key the FE ReadingItemDTO
		// reads (web/shared/types/meta.ts) so a rows response is directly castable to
		// ReadingItemDTO; Expr is the reading_items source column.
		Rows: &RowSource{
			Table:       items,
			OwnerCol:    "owner",
			DateCol:     "finished_at",
			DefaultSort: "finished_at DESC NULLS LAST",
			Columns: []RowColumn{
				{Name: "source", Expr: "source"},
				{Name: "externalId", Expr: "external_id"},
				{Name: "title", Expr: "title"},
				{Name: "subtitle", Expr: "subtitle"},
				{Name: "authors", Expr: "authors"},
				{Name: "narrators", Expr: "narrators"},
				{Name: "series", Expr: "series"},
				// status is EFFECTIVE (override ?? derived); the curation axes below
				// expose the layers separately so the FE can render a curated-vs-auto
				// indicator (migration 00069).
				{Name: "status", Expr: "COALESCE(status_override, status)"},
				{Name: "statusDerived", Expr: "status"},
				{Name: "statusOverride", Expr: "status_override"},
				{Name: "statusIsOverride", Expr: "(status_override IS NOT NULL)"},
				{Name: "finished", Expr: "finished"},
				{Name: "progressPercent", Expr: "progress_percent"},
				// finishedAt / rating are EFFECTIVE too (override ?? derived), with the
				// raw override exposed alongside for the indicator.
				{Name: "finishedAt", Expr: "COALESCE(finished_at_override, finished_at)"},
				{Name: "finishedAtOverride", Expr: "finished_at_override"},
				{Name: "rating", Expr: "COALESCE(rating_override, rating)"},
				{Name: "ratingOverride", Expr: "rating_override"},
				{Name: "goodreadsRating", Expr: "goodreads_rating"},
				{Name: "coverUrl", Expr: "cover_url"},
				{Name: "runtimeMin", Expr: "runtime_min"},
				{Name: "isbn", Expr: "isbn"},
				{Name: "amazonAsin", Expr: "amazon_asin"},
				{Name: "hardcoverBookId", Expr: "hardcover_book_id"},
				{Name: "hardcoverStatus", Expr: "hardcover_status"},
				{Name: "hardcoverSlug", Expr: "hardcover_slug"},
				// Hardcover list names (jsonb array) — a book property (migration 00077).
				{Name: "hardcoverLists", Expr: "hardcover_lists"},
				{Name: "syncedAt", Expr: "synced_at"},
				// Liberation state (boom-w20s, migration 00082). Carried on the leaf
				// projection so the Books explorer can show + facet it and the detail
				// sheet can render live per-book state, rather than the FE having to
				// fetch it per row. liberationStatus is COALESCEd to 'none' to match
				// the dimension above — the column and the axis must not disagree
				// about what an un-attempted book is called.
				{Name: "liberationStatus", Expr: "COALESCE(liberation_status, 'none')"},
				{Name: "liberationError", Expr: "liberation_error"},
				{Name: "liberatedAt", Expr: "liberated_at"},
				{Name: "audioPath", Expr: "audio_path"},
				{Name: "audioBytes", Expr: "audio_bytes"},
				{Name: "contentFormat", Expr: "content_format"},
			},
		},
	})
}

// registerReadingEvents wires the reading-EVENTS domain over the
// reading_events_enriched VIEW (migration 00081 / books 00003): each discrete read
// (reading_events) LEFT JOIN LATERAL its book row (reading_items) for title/author/
// series/genre/status. It is a DISTINCT domain from "reading" — not another measure
// on it — because a query domain has exactly ONE leaf-rows RowSource, and "reading"
// already spends that on reading_items (the library, one row per BOOK). The events
// table needs its own leaf projection (one row per READ, with origin + per-event
// finished_at), so it gets its own domain sharing the same owner-scope + injection
// model.
//
// One measure — `reads` = count(*) over the view — grouped/filtered by the event
// axes. origin (hardcover|audible|kindle) is the events-only axis; source/series/
// author/genre/status/title reuse the reading_items metadata the view exposes (the
// same trusted Exprs as the reading domain, valid here because the view carries
// those columns). status reads EFFECTIVE status (COALESCE(status_override, status)),
// both columns exposed by the view.
func registerReadingEvents() {
	const view = "reading_events_enriched"

	dims := map[string]Dimension{
		// origin: who produced the read (hardcover | audible | kindle) — the
		// events-only provenance axis, distinct from source (the Amazon edition kind).
		"origin": {Name: "origin", Table: view, Expr: "origin"},
		"source": {Name: "source", Table: view, Expr: "source"},
		"series": {Name: "series", Table: view, Expr: "series"},
		"author": {Name: "author", Table: view, Expr: "authors"},
		"genre":  {Name: "genre", Table: view, Expr: "genres->>0"},
		// EFFECTIVE status (override ?? item status) — both columns are exposed by the
		// view so this shared Expr resolves exactly as it does on reading_items.
		"status": {Name: "status", Table: view, Expr: "COALESCE(status_override, status)"},
		// title: a filter-oriented dimension (folds an ILIKE for search); grouping by a
		// near-unique key is moot, but it is whitelisted so the FE search can filter.
		"title": {Name: "title", Table: view, Expr: "title"},
	}

	Register(Domain{
		Name: "readingEvents",
		Measures: map[string]Measure{
			// reads: how many discrete reads fall in a group/window. count(*) over the
			// view; a book read three times contributes three reads (unlike the reading
			// domain's `books`, which counts the ONE reading_items row).
			"reads": {
				Name:     "reads",
				Table:    view,
				Expr:     "count(*)",
				DateCol:  "finished_at",
				OwnerCol: "owner",
				Dims:     []string{"origin", "source", "series", "author", "genre", "status", "title"},
			},
		},
		Dimensions: dims,

		// Leaf-rows source: one row per READ. Each column Name is the JSON key the FE
		// ReadingEventDTO reads (internal/books/web/.../readingEventsExplorerConfig);
		// Expr is the view column. status is EFFECTIVE (status_effective column).
		Rows: &RowSource{
			Table:       view,
			OwnerCol:    "owner",
			DateCol:     "finished_at",
			DefaultSort: "finished_at DESC NULLS LAST",
			Columns: []RowColumn{
				{Name: "origin", Expr: "origin"},
				{Name: "source", Expr: "source"},
				{Name: "externalId", Expr: "external_id"},
				{Name: "hardcoverBookId", Expr: "hardcover_book_id"},
				{Name: "title", Expr: "title"},
				{Name: "authors", Expr: "authors"},
				{Name: "series", Expr: "series"},
				{Name: "status", Expr: "status_effective"},
				{Name: "startedAt", Expr: "started_at"},
				{Name: "finishedAt", Expr: "finished_at"},
				{Name: "progressPages", Expr: "progress_pages"},
				{Name: "progressSeconds", Expr: "progress_seconds"},
			},
		},
	})
}
