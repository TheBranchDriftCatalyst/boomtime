package query

// domains.go registers boomtime's built-in query domains. Adding a new domain
// (health, media, …) is a matter of another Register call here — the compiler,
// grammar, and safety model are domain-agnostic.
//
// TABLE / OWNER-COLUMN map (the only per-table trusted facts the compiler needs):
//   - hb_rollup_daily   owner col = "sender"  date col = "day"
//   - reading_activity  owner col = "owner"   date col = "bucket_date"
//   - reading_items     owner col = "owner"   date col = "finished_at"

func init() {
	registerCoding()
	registerReading()
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
		// title is a filter-oriented dimension (books SEARCH folds an ILIKE on it);
		// it is in the reading_items measures' Dims whitelist so it can filter, but
		// the FE offers no title group axis (grouping by a near-unique key is moot).
		"title": {Name: "title", Table: items, Expr: "title"},
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
				Dims:     []string{"source", "status", "statusDerived", "series", "author", "genre", "title"},
			},
			"runtime": {
				Name:     "runtime",
				Table:    items,
				Expr:     "sum(runtime_min)",
				DateCol:  "finished_at",
				OwnerCol: "owner",
				Dims:     []string{"source", "status", "statusDerived", "series", "author", "genre", "title"},
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
				Dims:     []string{"source", "status", "statusDerived", "series", "author", "genre", "title"},
			},
		},
		Dimensions: dims,

		// Leaf-rows source: the reading_items projection the groupable explorer
		// drills down to. Each column Name is the JSON key the FE ReadingItemDTO
		// reads (web/src/types/meta.ts) so a rows response is directly castable to
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
				{Name: "syncedAt", Expr: "synced_at"},
			},
		},
	})
}
