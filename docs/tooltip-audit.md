# Tooltip Audit — gaka-9pt

Per-chart audit of every hover tooltip under `web/src/viz/charts/`. This doc
pairs with the implementation that lands alongside it and is the source of
truth for the "primary" hover on every viz. The "Other (N more)" segment
breakdown is owned by sibling bead **gaka-7m4** and rendered through
`otherBreakdownContent()` in `web/src/viz/d3/tooltipContent.ts`; this doc
covers only the primary (non-Other) hover.

Every tooltip already routes through `tooltipHtml(spec: TooltipSpec)` in
`web/src/viz/d3/tooltip.ts`. All user-controlled strings are HTML-escaped;
`titleSwatch` renders a coloured square before the title on multi-series
charts.

## Design principles

Consistent set of desirable fields, applied per-chart where the data supports it:

- **Value** — always. Time via `secondsToHms`; counts as plain integers.
- **% of visible total** — where a total is meaningful (share of range, share
  of day, share of bucket, share of row peak).
- **Rank** — top-N charts (`#3 of 14`).
- **Delta vs prior period** — where the payload carries prior-period signal
  in-series (Momentum's week-over-week, in-series).
- **Date / date range** — for time-series charts. Bucketed columns/rows
  render as `12–18 Jan 2026` via `fmtDateRange`.

Skipped fields: heartbeat count / distinct entities. `ResourceStats` does not
carry a heartbeat count today — see followups.

## Per-chart audit

| # | Chart | Current tooltip | Proposed / Landed | Rationale |
|---|---|---|---|---|
| 1 | **PieChart** (`PieChart.tsx`) | Title+swatch, Time, Share, rank `#N of M`. `Other` renders member breakdown (gaka-7m4). | KEEP as-is. Already: title+swatch, Time, Share, rank, and gaka-7m4 breakdown for Other. | The Pie is the reference implementation. Rank tells "how does this slice stack against neighbors"; share is over the *rendered* set so it sums to 100%. |
| 2 | **FileBarChart** (`FileBarChart.tsx`) | Title=basename+swatch, subtitle=full path (when different), Time, Share (of ALL files), rank `#N of filesCount`. | KEEP as-is. | Share is honest (grand total across ALL files, not top-10). Rank uses true file count when available. |
| 3 | **ColumnChart — single** (`ColumnChart.tsx`) | Title=day, subtitle=range (when bucketed), single row `seriesName: Hms`. | KEEP as-is. | The single-series column is a simple "day → time" — a rank of days isn't decision-useful for the primary hover. Delta vs prior period requires a second `getStats` fetch (see followups). |
| 3b | **ColumnChart — stacked** | Title=segment name+swatch, subtitle=day/range, rows: Time, Share of day, Day total (muted). Other segment appends member breakdown (gaka-7m4). | KEEP as-is. | Segment share + day total gives both intra-day and absolute context. |
| 4 | **HeatmapChart** (`HeatmapChart.tsx`) | Title=row name+swatch, subtitle=day/range, rows: Time, Share of row peak (muted). Other row appends member breakdown (gaka-7m4). | KEEP as-is. | "Share of row peak" reads as "was this a peak day for THIS series?" — more useful than "share of whole grid" (which would be near-zero for most cells and useless for peak-detection). |
| 5 | **CategoryStreamgraph** (`CategoryStreamgraph.tsx`) | Title=layer name+swatch, subtitle=bucket/range, rows: Time, Share of bucket. Other appends breakdown (gaka-7m4). | KEEP as-is. | Bucket total is the honest denominator on a stacked stream; % of whole range would double-count over time. |
| 6 | **CategoryBreakdown** (`CategoryBreakdown.tsx`) | No hover tooltip. Bar list shows time + % inline; native `title` attribute is the name only. | SKIP — no hover tooltip. Inline text already carries value + %. | This is an HTML bar list, not a D3 chart. Adding a D3 tooltip would duplicate the inline text. |
| 7 | **MomentumGrid** (`MomentumGrid.tsx`) | Title=project+swatch, subtitle=`Week of DD Mon YYYY`, rows: Time, Share of peak (muted), footer=WoW delta via `fmtDelta`. | KEEP as-is. | Rich, in-series delta already present. WoW delta is the flagship signal — "is this project heating up or cooling down?" |
| 8 | **RadarChart** (`RadarChart.tsx`) | Title=weekday+swatch, rows: Activity, Share of week, footer=rank across active days. | KEEP as-is. | Rank across *active* days (not all 7) — otherwise all-week rank is degenerate on quiet ranges. |
| 9 | **BranchActivity** (`BranchActivity.tsx`) | Title=branch+swatch, rows: Time, Share, footer=rank `#N of branchesCount`. | KEEP as-is. | Uses true `branchesCount` when available so rank is honest even when the top-12 is a small slice. |
| 10 | **HourBarChart** (`HourBarChart.tsx`) | Title=`HH:00–HH:00`+swatch, rows: Activity, Share of day, footer=`Local time`. | ADD rank across active hours (`#3 of 12 active hours`). Retain `Local time` context via subtitle. | Ranking hours tells "when do I actually work?" — peaked-at-14:00 is the useful signal, not a bare share. |
| 11 | **BreadthVsDepth** (`BreadthVsDepth.tsx`) | Title=day, subtitle=range (when bucketed), rows: Time (with swatch), Files (with swatch), Time/file (muted). | KEEP as-is. | The two-swatch layout is exactly what a dual-axis chart needs — Time/file is the derived "depth" signal. |
| 12 | **AuthoringVsReading** (`AuthoringVsReading.tsx`) — donut | Title=Authoring/Reading+swatch, rows: Time, Share. | KEEP donut as-is. | 2-slice pie: rank would be either 1st or 2nd — not useful. |
| 12b | **AuthoringVsReading** — ratio line | NO tooltip on the line. | ADD hover: Title=day, rows: Authoring ratio (%), footer optional context. | The line's the whole story in the right pane — hovering should reveal each day's ratio. |
| 13 | **CumulativeArea** (`CumulativeArea.tsx`) | Title=day/range+swatch, rows: Total so far, This bucket, Progress (muted). | KEEP as-is. | Cumulative-total + bucket-contribution + `Progress` (% of the range's final cumulative) is the honest three-way readout. |
| 14 | **Punchcard** (`Punchcard.tsx`) | Title=`Day HH:00–HH:00`+swatch, rows: Time, Share of week, footer=`UTC`. | ADD rank across active cells (`#3 of N active cells`). Retain `UTC` context. | On a 7×24 grid the interesting cells are the top few "when do I actually work?" cells — rank is exactly that signal. |
| 15 | **ContributionCalendar** (`ContributionCalendar.tsx`) | Title=`Weekday DD Mon YYYY`+swatch, rows: Time, Share of window, footer=`Peak day in this window` on the top day only. | ADD rank across active days (`#3 of N active days`). | Rank complements the "peak day" flag — you can see "this was your 3rd most active day" even on non-peak cells. |
| 16 | **DeepWorkSessions** (`DeepWorkSessions.tsx`) — histogram | Title=bin label+swatch, subtitle=`Session length bin`, rows: Sessions, Share. | KEEP as-is. | Share of total sessions is the useful denominator for a distribution. Time-in-bin needs a backend field (`SessionsHistogramBin.totalSeconds`) — see followups. |
| 16b | **DeepWorkSessions** — daily strip | Title=day/range+swatch, rows: Sessions, Share. | KEEP as-is. | Share of total sessions across the range gives "was this a busy day for focus sessions?". |
| 17 | **StreakBanner** (`StreakBanner.tsx`) | No tooltip. Sparkline is decorative. | SKIP — sparkline is intentionally decorative under a stat card. | The stat cards ARE the readout; a sparkline tooltip would compete with them without adding data. |
| 18 | **TimelineChart** (`TimelineChart.tsx`) | Title=lang+swatch, subtitle=`DD Mon, HH:MM → HH:MM`, rows: Duration. | ADD Share of lane (%), footer=rank in lane (`#3 of N segments`). | On a range-bar timeline the useful question is "how much of this lane's day was this segment, and is it a big or small one?" — share + rank answers both. |

## Cross-cutting

- **Escaping**: every user-controlled string (project / branch / file /
  language name) flows through `escapeHtml` inside `tooltipHtml`. Never
  hand-concatenate HTML in a chart.
- **`titleSwatch`**: use on every multi-series chart; skip on single-series
  time-of-day/date bars where colour carries no series semantics.
- **Bucketed dates**: prefer `fmtDateRange(start, end)` in the subtitle over
  a bucket's first-day. Callers pass a `ranges?: {start,end}[]` prop.
- **Rank semantics**: `fmtRank(rank, total)` — `#3 of 14`. When the base is
  degenerate (0), returns `""`, so guards are unnecessary at call sites.
- **`otherBreakdownContent()`** (gaka-7m4 owned): renders the collapsed tail
  as extra `rows` on the Other segment tooltip. Do NOT touch — the primary
  hover on non-Other segments is this ticket's territory.

## Followups (data not currently on the payload)

Tracked here rather than in a fresh bead until DJ decides whether any is
worth the backend churn:

- **Heartbeat count per ResourceStats** — would let every top-N tooltip
  show "N heartbeats" as a signal-density hint. Requires
  `ResourceStats.heartbeatCount` in `internal/stats/stats.go` (incl.
  `capWithOther` summing counts) + `web/src/types/stats.ts`. Skipped for
  the ticket per constraint "no payload changes".
- **Prior-period delta on the Pie / Column tooltips** — Momentum already
  computes an in-series WoW delta, but per-slice delta on the Pie
  ("Coding down 12% vs prior 30d") would need a second `getStats` fetch
  for the preceding equal-length window and a join by resource name. No
  backend change; batched under gaka-9pt phase 4.
- **Session-length bin time totals** — `SessionsHistogramBin.totalSeconds`
  on the sessions histogram would let the tooltip read "12 sessions ·
  4h 30m in this bin" instead of just "12 sessions". Requires
  `internal/db/sessions.go` histogram query + `internal/model/model.go`
  + `web/src/types/api.ts`.
- **CategoryBreakdown / StreakBanner** — intentionally no D3 hover
  tooltip. If cross-chart consistency ever demands one, use the same
  `tooltipHtml(spec)` API — the inline HTML lists have all the data.
