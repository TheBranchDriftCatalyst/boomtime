// ReadingDashboard — the "Reading" sub-tab of the Overview. A grid of query-DSL
// tiles (listening time, trends, genres, series, finished-per-month) plus the
// two non-aggregate tiles (Now reading rows, Weekly listening goal ring). Owns
// its own <Page> chrome so it drops into the Overview tab switch exactly like
// the Coding tab (<OverviewDashboard/>) does.
//
// Visibility of the whole tab is gated upstream on books_enabled (see
// Overview.tsx); this component assumes the reading domain is live.
import { Page } from "@/layout/Page";
import {
  BooksByGenreTile,
  FinishedPerMonthTile,
  ListeningThisWeekTile,
  ListeningTrendTile,
  ProlificGenresTile,
  TopSeriesByRuntimeTile,
} from "./ReadingTiles";
import { NowReadingTile } from "./NowReading";
import { WeeklyListeningGoalTile } from "./WeeklyListeningGoal";

export function ReadingDashboard() {
  return (
    <Page>
      <Page.Header
        title="Reading"
        subtitle="Your listening & reading, fused from Kindle + Audible"
      />
      <Page.Body>
        <Page.Content>
          <div className="space-y-6">
            {/* Trend leads (2/3) with the weekly KPI + goal ring stacked beside it. */}
            <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
              <div className="lg:col-span-2">
                <ListeningTrendTile />
              </div>
              <div className="flex flex-col gap-6">
                <ListeningThisWeekTile />
                <WeeklyListeningGoalTile />
              </div>
            </div>

            {/* Composition: genres + what's in progress. */}
            <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
              <BooksByGenreTile />
              <NowReadingTile />
            </div>

            {/* Depth: series runtime + finishing cadence. */}
            <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
              <TopSeriesByRuntimeTile />
              <FinishedPerMonthTile />
            </div>

            <ProlificGenresTile />
          </div>
        </Page.Content>
      </Page.Body>
    </Page>
  );
}
