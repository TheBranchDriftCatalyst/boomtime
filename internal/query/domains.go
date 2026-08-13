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
		"status": {Name: "status", Table: items, Expr: "status"},
		"series": {Name: "series", Table: items, Expr: "series"},
		"author": {Name: "author", Table: items, Expr: "authors"},
		"genre":  {Name: "genre", Table: items, Expr: "genres->>0"},
		"title":  {Name: "title", Table: items, Expr: "title"},
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
				Dims:     []string{"source", "status", "series", "author", "genre"},
			},
			"runtime": {
				Name:     "runtime",
				Table:    items,
				Expr:     "sum(runtime_min)",
				DateCol:  "finished_at",
				OwnerCol: "owner",
				Dims:     []string{"source", "status", "series", "author", "genre"},
			},
		},
		Dimensions: dims,
	})
}
