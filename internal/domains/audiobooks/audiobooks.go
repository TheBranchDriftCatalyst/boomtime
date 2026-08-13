// Package audiobooks is the catalyst-audiobooks ingestion domain: AUDIBLE
// listening. It maps the Audible library (fetched via the SHARED internal/amazon
// device credential) into the siloed reading_items table, records listening
// time into reading_activity, detects newly-finished books → notification
// events + a one-way Hardcover push, and drives its periodic sync job on the
// catalyst-go-jobs scheduler. It owns no auth (that's internal/amazon).
//
// Two idempotent sync modes (docs/design/catalyst-books-sync-architecture.md §2):
//
//	BackfillUser(ctx, owner) — one-shot, all-time. Sweeps every library page,
//	                           walks the finished-status continuation token to
//	                           exhaustion, and loops monthly listening aggregates
//	                           back to the account start. Seeds the cursors. Does
//	                           NOT emit finished events (would fire hundreds of
//	                           toasts for historical finishes).
//	SyncUser(ctx, owner)     — periodic forward delta. Uses the stored cursors to
//	                           fetch only what is new, detects the finished
//	                           false→true edge → BookFinished event + Hardcover
//	                           push, then advances the cursors.
package audiobooks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/hardcover"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/jobs"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/logctx"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/notify"
)

const (
	// AudibleSyncKind is the catalyst-go-jobs kind for the periodic forward sync.
	AudibleSyncKind = "audiobooks-audible-sync"
	// AudibleBackfillKind is the one-shot all-time backfill kind (enqueued on
	// demand from the connect flow / admin, never scheduled).
	AudibleBackfillKind = "audiobooks-audible-backfill"

	// HardcoverPushKind mirrors ONE finished book to Hardcover. Its own job kind (not inline in sync) so all Hardcover pushes across users share one concurrency-capped queue — Hardcover's rate limit is a global resource.
	HardcoverPushKind = "hardcover-push"

	source = "audible"

	// libraryResponseGroups selects the full field set we map (156 fields are
	// available; these are the useful ones — see the architecture doc §2.2a).
	libraryResponseGroups = "product_desc,contributors,product_attrs,product_extended_attrs," +
		"series,category_ladders,media,is_finished,percent_complete,listening_status"

	// 300 (Audible allows up to 1000) keeps each page's JSON to a few MB with the
	// wide response_groups; a short page still ends the sweep. Smaller pages =
	// safer parse + gentler on the API at the cost of a few more requests.
	libraryPageSize = 300

	// readingFinishedPct: at/above this percent_complete a title counts as finished
	// even without Audible's is_finished flag (gaka-vvij).
	readingFinishedPct = 95.0
)

// Service is the catalyst-audiobooks domain entrypoint.
type Service struct {
	DB     *db.DB
	Amazon *amazon.Store
	Logger *slog.Logger

	// Notify (nil-safe) publishes BookFinished events to the per-user hub so the
	// browser toasts. A nil hub = no push, exactly like the jobs notifier.
	Notify *notify.Hub
	// Hardcover (nil-safe) is the push connector; on a newly-finished book we
	// match + mirror the finish out when the user has connected Hardcover.
	Hardcover *hardcover.Store
	// Enqueuer (nil-safe) routes finished-book Hardcover pushes onto the
	// concurrency-capped HardcoverPushKind queue. nil => push inline.
	Enqueuer jobs.Enqueuer
}

// HardcoverPushPayload is the self-contained job payload for HardcoverPushKind:
// everything RunHardcoverPush needs to match + mirror one finished book, so the
// job carries no reference back into sweep state.
type HardcoverPushPayload struct {
	Owner      string    `json:"owner"`
	ASIN       string    `json:"asin"`
	AmazonASIN string    `json:"amazonAsin"`
	ISBN       string    `json:"isbn"`
	Title      string    `json:"title"`
	Author     string    `json:"author"`
	FinishedAt time.Time `json:"finishedAt"`
}

// New constructs the audiobooks (Audible) domain service. Notify/Hardcover are
// wired after construction (SetNotify/SetHardcover) so callers without them
// (tests, the diagnostics endpoint) keep working.
func New(database *db.DB, az *amazon.Store, logger *slog.Logger) *Service {
	return &Service{DB: database, Amazon: az, Logger: logger}
}

// SetNotify wires the notification hub (nil-safe).
func (s *Service) SetNotify(hub *notify.Hub) *Service { s.Notify = hub; return s }

// SetHardcover wires the Hardcover push store (nil-safe).
func (s *Service) SetHardcover(store *hardcover.Store) *Service { s.Hardcover = store; return s }

// SetEnqueuer wires the jobs enqueuer (nil-safe). nil => finished-book Hardcover
// pushes run inline; set => they enqueue onto the capped HardcoverPushKind queue.
func (s *Service) SetEnqueuer(e jobs.Enqueuer) *Service { s.Enqueuer = e; return s }

// ---------------------------------------------------------------------------
// Library item parsing
// ---------------------------------------------------------------------------

// LibraryItem is the subset of an Audible /1.0/library item we read. Unmapped
// fields still round-trip through raw_meta, so new attributes need no migration.
type LibraryItem struct {
	ASIN     string `json:"asin"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"`
	Authors  []struct {
		Name string `json:"name"`
	} `json:"authors"`
	Narrators []struct {
		Name string `json:"name"`
	} `json:"narrators"`
	Series []struct {
		Title    string `json:"title"`
		Sequence string `json:"sequence"`
	} `json:"series"`
	IsFinished      bool    `json:"is_finished"`
	PercentComplete float64 `json:"percent_complete"`
	// NOTE: listening_status is intentionally NOT mapped — Audible returns it as
	// an OBJECT ({finished_at_timestamp,is_finished,percent_complete,...}), and a
	// prior `string` typing made json.Unmarshal fail the ENTIRE library page.
	// The top-level is_finished + percent_complete carry what we need. Verified
	// live against the real library (all 300 page-1 items) 2026-08-12.
	RuntimeLengthMin int             `json:"runtime_length_min"`
	PurchaseDate     string          `json:"purchase_date"`
	ISBN             string          `json:"isbn"`
	AmazonASIN       string          `json:"amazon_asin"`
	ProductImages    json.RawMessage `json:"product_images"`
	CategoryLadders  []struct {
		Ladder []struct {
			Name string `json:"name"`
		} `json:"ladder"`
	} `json:"category_ladders"`
	GoodreadsRatings *struct {
		Rating json.Number `json:"rating"`
	} `json:"goodreads_ratings"`

	// raw is the original, unmodified item JSON — captured during per-item parse
	// so raw_meta preserves ALL source fields (not just the mapped subset).
	// Unexported + untagged: json ignores it on both marshal and unmarshal.
	raw json.RawMessage
}

func namesCSV[T any](items []T, get func(T) string) string {
	names := make([]string, 0, len(items))
	for _, it := range items {
		if n := strings.TrimSpace(get(it)); n != "" {
			names = append(names, n)
		}
	}
	return strings.Join(names, ", ")
}

func (li LibraryItem) authorsCSV() string {
	return namesCSV(li.Authors, func(a struct {
		Name string `json:"name"`
	}) string {
		return a.Name
	})
}

func (li LibraryItem) narratorsCSV() string {
	return namesCSV(li.Narrators, func(n struct {
		Name string `json:"name"`
	}) string {
		return n.Name
	})
}

func (li LibraryItem) seriesTitle() string {
	if len(li.Series) > 0 {
		return li.Series[0].Title
	}
	return ""
}

// coverURL picks the largest product image (keys are pixel widths as strings).
func (li LibraryItem) coverURL() string {
	if len(li.ProductImages) == 0 {
		return ""
	}
	var imgs map[string]string
	if err := json.Unmarshal(li.ProductImages, &imgs); err != nil || len(imgs) == 0 {
		return ""
	}
	bestW, bestURL := -1, ""
	for k, v := range imgs {
		w, _ := strconv.Atoi(k)
		if w > bestW && v != "" {
			bestW, bestURL = w, v
		}
	}
	return bestURL
}

// genresJSON flattens category_ladders into a JSON array of distinct names.
func (li LibraryItem) genresJSON() []byte {
	seen := map[string]struct{}{}
	var out []string
	for _, cl := range li.CategoryLadders {
		for _, node := range cl.Ladder {
			n := strings.TrimSpace(node.Name)
			if n == "" {
				continue
			}
			if _, ok := seen[n]; ok {
				continue
			}
			seen[n] = struct{}{}
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return nil
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil
	}
	return b
}

func (li LibraryItem) goodreadsRating() *float64 {
	if li.GoodreadsRatings == nil || li.GoodreadsRatings.Rating == "" {
		return nil
	}
	f, err := li.GoodreadsRatings.Rating.Float64()
	if err != nil || f <= 0 {
		return nil
	}
	return &f
}

func (li LibraryItem) purchaseDate() *time.Time {
	return parseAudibleTime([]byte(strconv.Quote(li.PurchaseDate)))
}

func (li LibraryItem) runtimeMin() *int {
	if li.RuntimeLengthMin <= 0 {
		return nil
	}
	v := li.RuntimeLengthMin
	return &v
}

// toReadingItem maps a parsed library item into a reading_items row.
func (li LibraryItem) toReadingItem(owner string) db.ReadingItem {
	// Prefer the original source JSON (captured at parse) so raw_meta is complete;
	// fall back to re-marshaling the mapped subset (e.g. FetchLibrary's diag path).
	raw := li.raw
	if len(raw) == 0 {
		raw, _ = json.Marshal(li)
	}
	// Treat >=95% listened as completed even when Audible's is_finished flag never
	// flipped — users routinely stop at 99% and never mark a title "finished", so
	// near-done books were wrongly showing as in-progress. gaka-vvij.
	finished := li.IsFinished || li.PercentComplete >= readingFinishedPct
	status := "reading"
	switch {
	case finished:
		status = "read"
	case li.PercentComplete <= 0:
		status = "want"
	}
	return db.ReadingItem{
		Owner:           owner,
		Source:          source,
		ExternalID:      li.ASIN,
		Title:           li.Title,
		Authors:         li.authorsCSV(),
		CoverURL:        li.coverURL(),
		Status:          status,
		ProgressPercent: int(li.PercentComplete),
		Finished:        finished,
		RawMeta:         raw,
		Subtitle:        li.Subtitle,
		Narrators:       li.narratorsCSV(),
		Series:          li.seriesTitle(),
		RuntimeMin:      li.runtimeMin(),
		PurchaseDate:    li.purchaseDate(),
		ISBN:            strings.TrimSpace(li.ISBN),
		AmazonASIN:      strings.TrimSpace(li.AmazonASIN),
		Genres:          li.genresJSON(),
		GoodreadsRating: li.goodreadsRating(),
	}
}

// ---------------------------------------------------------------------------
// Library sweep
// ---------------------------------------------------------------------------

// fetchLibraryPage GETs one /1.0/library page. purchasedAfter (non-nil) adds the
// forward-delta filter (&purchased_after=<RFC3339>&sort_by=-PurchaseDate).
func (s *Service) fetchLibraryPage(ctx context.Context, cred *amazon.DeviceCredential, page int, purchasedAfter *time.Time) ([]LibraryItem, error) {
	host := amazon.AudibleAPIHost(cred.Marketplace)
	path := fmt.Sprintf("/1.0/library?response_groups=%s&num_results=%d&page=%d",
		libraryResponseGroups, libraryPageSize, page)
	if purchasedAfter != nil {
		path += "&purchased_after=" + purchasedAfter.UTC().Format(time.RFC3339) + "&sort_by=-PurchaseDate"
	}
	body, status, err := amazon.SignedGet(ctx, cred, host, path)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("audible /1.0/library returned HTTP %d: %s", status, snippet(body))
	}
	// Parse per-item, not the whole page at once: a single item with an
	// unexpected field shape (Audible's schema is wide + occasionally surprising)
	// must NOT fail the entire sweep — skip + warn it and keep going. Each item's
	// original JSON is retained for a truthful raw_meta.
	var lr struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(body, &lr); err != nil {
		return nil, fmt.Errorf("audible library parse failed: %w (body: %s)", err, snippet(body))
	}
	items := make([]LibraryItem, 0, len(lr.Items))
	for _, raw := range lr.Items {
		var li LibraryItem
		if err := json.Unmarshal(raw, &li); err != nil {
			s.logWarn(ctx, "audible library: skipping unparseable item", "err", err, "snippet", snippet(raw))
			continue
		}
		li.raw = raw
		items = append(items, li)
	}
	return items, nil
}

// FetchLibrary fetches page 1 of the library (back-compat: the diagnostics probe
// + a quick sync). Full sweeps use sweepLibrary.
func (s *Service) FetchLibrary(ctx context.Context, cred *amazon.DeviceCredential) ([]LibraryItem, error) {
	return s.fetchLibraryPage(ctx, cred, 1, nil)
}

// sweepLibrary loops library pages until a short page, upserting each item.
// purchasedAfter scopes it to the forward delta (nil = full sweep). Returns the
// number of items upserted and the newest purchase_date observed (the next
// library cursor), or nil when nothing was seen.
func (s *Service) sweepLibrary(ctx context.Context, cred *amazon.DeviceCredential, owner string, purchasedAfter *time.Time) (int, *time.Time, error) {
	var (
		count  int
		pages  int
		newest *time.Time
	)
	for page := 1; ; page++ {
		// Honor cancellation (admin cancel / shutdown) between pages so a long
		// multi-page sweep stops promptly instead of fetching the next page.
		if err := ctx.Err(); err != nil {
			return count, newest, err
		}
		items, err := s.fetchLibraryPage(ctx, cred, page, purchasedAfter)
		if err != nil {
			return count, newest, err
		}
		pages++
		for _, it := range items {
			if it.ASIN == "" {
				continue
			}
			ri := it.toReadingItem(owner)
			if err := s.DB.UpsertReadingItem(ctx, ri); err != nil {
				return count, newest, fmt.Errorf("upsert %q: %w", it.ASIN, err)
			}
			count++
			if pd := ri.PurchaseDate; pd != nil && (newest == nil || pd.After(*newest)) {
				newest = pd
			}
		}
		// Progress: one line per fetched page so a running multi-page sweep shows
		// live activity in the Admin log viewer instead of a single line at the end.
		s.logInfo(ctx, "audible: library page swept", "user", owner, "page", page, "pageItems", len(items), "upsertedSoFar", count)
		if len(items) < libraryPageSize {
			break
		}
	}
	s.logInfo(ctx, "audible: library sweep complete", "user", owner, "pages", pages, "upserted", count)
	return count, newest, nil
}

// ---------------------------------------------------------------------------
// Finished sweep → finished-detection
// ---------------------------------------------------------------------------

// finishedEntry is one row of /1.0/stats/status/finished's list.
type finishedEntry struct {
	ASIN               string          `json:"asin"`
	EventTimestamp     json.RawMessage `json:"event_timestamp"`
	IsMarkedAsFinished bool            `json:"is_marked_as_finished"`
}

// finishedEvent captures a newly-finished book (the false→true edge) so the
// caller can toast + push it after the sweep commits.
type finishedEvent struct {
	Meta       db.FinishedReadingItem
	FinishedAt time.Time
}

// sweepFinished walks /1.0/stats/status/finished from `since` (nil → all-time
// 2000-01-01), following continuation_token to exhaustion. Every entry marked
// finished is applied to reading_items; when emitEvents is true the false→true
// transitions are collected as finishedEvents. Returns the events + the newest
// event_timestamp seen (the next finished cursor).
func (s *Service) sweepFinished(ctx context.Context, cred *amazon.DeviceCredential, owner string, since *time.Time, emitEvents bool) ([]finishedEvent, *time.Time, error) {
	host := amazon.AudibleAPIHost(cred.Marketplace)
	start := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	if since != nil {
		start = *since
	}
	var (
		events  []finishedEvent
		newest  *time.Time
		token   string
		applied int // entries marked finished in reading_items this sweep
	)
	for {
		// Honor cancellation between continuation pages of the finished sweep.
		if err := ctx.Err(); err != nil {
			return events, newest, err
		}
		path := "/1.0/stats/status/finished?start_date=" + start.UTC().Format(time.RFC3339)
		if token != "" {
			path += "&continuation_token=" + token
		}
		body, status, err := amazon.SignedGet(ctx, cred, host, path)
		if err != nil {
			return events, newest, err
		}
		if status < 200 || status >= 300 {
			return events, newest, fmt.Errorf("audible finished-sweep returned HTTP %d: %s", status, snippet(body))
		}
		var fr struct {
			ContinuationToken string          `json:"continuation_token"`
			List              []finishedEntry `json:"mark_as_finished_status_list"`
		}
		if err := json.Unmarshal(body, &fr); err != nil {
			return events, newest, fmt.Errorf("audible finished-sweep parse failed: %w (body: %s)", err, snippet(body))
		}
		for _, e := range fr.List {
			if e.ASIN == "" || !e.IsMarkedAsFinished {
				continue
			}
			ts := parseAudibleTime(e.EventTimestamp)
			if ts == nil {
				continue
			}
			if newest == nil || ts.After(*newest) {
				newest = ts
			}
			transitioned, meta, found, err := s.DB.MarkReadingItemFinished(ctx, owner, source, e.ASIN, *ts)
			if err != nil {
				return events, newest, fmt.Errorf("mark finished %q: %w", e.ASIN, err)
			}
			applied++
			if emitEvents && transitioned && found {
				events = append(events, finishedEvent{Meta: meta, FinishedAt: *ts})
			}
		}
		if fr.ContinuationToken == "" || len(fr.List) == 0 {
			break
		}
		token = fr.ContinuationToken
	}
	// Phase summary: how many finished entries were applied and how many were the
	// newly-finished false→true edges (events) this run.
	s.logInfo(ctx, "audible: finished sweep complete", "user", owner, "applied", applied, "newlyFinished", len(events))
	return events, newest, nil
}

// ---------------------------------------------------------------------------
// Listening aggregates → reading_activity
// ---------------------------------------------------------------------------

// sweepAggregates fetches one aggregates window and upserts its buckets into
// reading_activity at the given granularity. daily=true uses the daily window
// args; false uses monthly. Returns the number of non-empty buckets written.
func (s *Service) sweepAggregates(ctx context.Context, cred *amazon.DeviceCredential, owner string, windowStart time.Time, duration int, daily bool) (int, error) {
	host := amazon.AudibleAPIHost(cred.Marketplace)
	path := "/1.0/stats/aggregates?response_groups=total_listening_stats&store=Audible"
	granularity := "month"
	if daily {
		granularity = "day"
		path += fmt.Sprintf("&daily_listening_interval_duration=%d&daily_listening_interval_start_date=%s",
			duration, windowStart.UTC().Format("2006-01-02"))
	} else {
		path += fmt.Sprintf("&monthly_listening_interval_duration=%d&monthly_listening_interval_start_date=%s",
			duration, windowStart.UTC().Format("2006-01"))
	}
	body, status, err := amazon.SignedGet(ctx, cred, host, path)
	if err != nil {
		return 0, err
	}
	if status < 200 || status >= 300 {
		return 0, fmt.Errorf("audible aggregates returned HTTP %d: %s", status, snippet(body))
	}
	buckets := parseAggregates(body)
	written := 0
	for _, b := range buckets {
		if b.seconds <= 0 || b.date.IsZero() {
			continue
		}
		if err := s.DB.UpsertReadingActivity(ctx, db.ReadingActivity{
			Owner:            owner,
			Source:           source,
			Granularity:      granularity,
			BucketDate:       b.date,
			ListeningSeconds: b.seconds,
		}); err != nil {
			return written, fmt.Errorf("upsert activity %s: %w", b.date.Format("2006-01-02"), err)
		}
		written++
	}
	return written, nil
}

// backfillAggregates loops monthly windows backward from the current month to
// the account start (a window with zero non-empty buckets = before the account
// existed → stop). No max lookback.
func (s *Service) backfillAggregates(ctx context.Context, cred *amazon.DeviceCredential, owner string) error {
	const windowMonths = 12
	// Start at the 1st of the current month, step back windowMonths at a time.
	cur := time.Now().UTC()
	cur = time.Date(cur.Year(), cur.Month(), 1, 0, 0, 0, 0, time.UTC)
	// Safety ceiling so a persistently-non-empty (or misparsed) response can't
	// loop forever: Audible launched in 1995; 40 years of windows is ample.
	var totalBuckets, windows int
	for i := 0; i < 40; i++ {
		// Honor cancellation between aggregate windows.
		if err := ctx.Err(); err != nil {
			return err
		}
		windowStart := cur.AddDate(0, -(windowMonths - 1), 0)
		written, err := s.sweepAggregates(ctx, cred, owner, windowStart, windowMonths, false)
		if err != nil {
			// Aggregates are best-effort — a shape/endpoint hiccup must not fail
			// the whole backfill (library + finished are the valuable parts).
			s.logWarn(ctx, "audible backfill: aggregates window failed", "user", owner,
				"window", windowStart.Format("2006-01"), "err", err)
			return nil
		}
		if written == 0 {
			break // no activity in this (older) window → account start reached.
		}
		windows++
		totalBuckets += written
		cur = windowStart.AddDate(0, -1, 0)
	}
	// Phase summary: how many monthly windows were walked and how many non-empty
	// listening buckets they wrote into reading_activity.
	s.logInfo(ctx, "audible: aggregates backfill complete", "user", owner, "windows", windows, "buckets", totalBuckets)
	return nil
}

// ---------------------------------------------------------------------------
// Sync modes
// ---------------------------------------------------------------------------

// BackfillUser runs the one-shot, all-time sweep: full library, all-time
// finished sweep (no events), and monthly listening aggregates back to the
// account start. Seeds the forward cursors + stamps last_backfill_at. Idempotent.
func (s *Service) BackfillUser(ctx context.Context, owner string) error {
	cred, err := s.Amazon.Load(ctx, owner)
	if err != nil {
		return err
	}

	libCount, newestPurchase, err := s.sweepLibrary(ctx, cred, owner, nil)
	if err != nil {
		_ = s.Amazon.DB.UpdateAmazonDeviceStatus(ctx, owner, db.AmazonDeviceStatusInvalid)
		return err
	}
	_ = s.Amazon.DB.UpdateAmazonDeviceStatus(ctx, owner, db.AmazonDeviceStatusValid)

	_, newestFinished, err := s.sweepFinished(ctx, cred, owner, nil, false)
	if err != nil {
		return err
	}

	if err := s.backfillAggregates(ctx, cred, owner); err != nil {
		return err
	}

	now := time.Now().UTC()
	st, _ := s.DB.GetBookSyncState(ctx, owner, source)
	st.Owner, st.Source = owner, source
	if newestPurchase != nil {
		st.LastLibraryCursor = newestPurchase
	}
	if newestFinished != nil {
		st.LastFinishedCursor = newestFinished
	}
	today := now.Truncate(24 * time.Hour)
	st.LastActivityCursor = &today
	st.LastBackfillAt = &now
	st.LastForwardAt = &now
	if err := s.DB.SetBookSyncState(ctx, st); err != nil {
		return err
	}
	s.logInfo(ctx, "audible backfill complete", "user", owner, "libraryItems", libCount)
	return nil
}

// SyncUser runs the periodic forward delta: library items purchased since the
// cursor, finished-status changes since the cursor (emitting BookFinished events
// + Hardcover pushes for the false→true edges), and the current daily listening
// window. Advances all three cursors. Returns the number of library items
// upserted this run. Idempotent — a re-run with unchanged cursors emits nothing.
func (s *Service) SyncUser(ctx context.Context, owner string) (int, error) {
	cred, err := s.Amazon.Load(ctx, owner)
	if err != nil {
		return 0, err
	}
	st, err := s.DB.GetBookSyncState(ctx, owner, source)
	if err != nil {
		return 0, err
	}

	libCount, newestPurchase, err := s.sweepLibrary(ctx, cred, owner, st.LastLibraryCursor)
	if err != nil {
		_ = s.Amazon.DB.UpdateAmazonDeviceStatus(ctx, owner, db.AmazonDeviceStatusInvalid)
		return 0, err
	}
	_ = s.Amazon.DB.UpdateAmazonDeviceStatus(ctx, owner, db.AmazonDeviceStatusValid)

	events, newestFinished, err := s.sweepFinished(ctx, cred, owner, st.LastFinishedCursor, true)
	if err != nil {
		return libCount, err
	}

	// Current daily window (last 30 days) → granularity='day'. Best-effort.
	if _, aerr := s.sweepAggregates(ctx, cred, owner, time.Now().UTC().AddDate(0, 0, -29), 30, true); aerr != nil {
		s.logWarn(ctx, "audible forward: daily aggregates failed", "user", owner, "err", aerr)
	}

	// Publish + push the newly-finished books AFTER the sweep committed.
	for _, ev := range events {
		s.announceFinished(ctx, owner, ev)
	}

	// Mirror each currently-reading title's listening % to Hardcover so an
	// in-progress book's progress tracks there too (complements the finished-edge
	// push above). Best-effort + nil-safe on s.Hardcover.
	s.syncInProgressToHardcover(ctx, owner)

	now := time.Now().UTC()
	st.Owner, st.Source = owner, source
	if newestPurchase != nil {
		st.LastLibraryCursor = newestPurchase
	}
	if newestFinished != nil {
		st.LastFinishedCursor = newestFinished
	}
	today := now.Truncate(24 * time.Hour)
	st.LastActivityCursor = &today
	st.LastForwardAt = &now
	if err := s.DB.SetBookSyncState(ctx, st); err != nil {
		return libCount, err
	}
	if len(events) > 0 {
		s.logInfo(ctx, "audible forward: newly finished", "user", owner, "count", len(events))
	}
	return libCount, nil
}

// announceFinished publishes the BookFinished notification and mirrors the
// finish to Hardcover (both nil-safe / best-effort — neither can fail the sync).
func (s *Service) announceFinished(ctx context.Context, owner string, ev finishedEvent) {
	title := ev.Meta.Title
	if title == "" {
		title = ev.Meta.ExternalID
	}
	if s.Notify != nil {
		s.Notify.Publish(notify.Event{
			Type:  "book.finished",
			Owner: owner,
			Title: "Finished: " + title,
			Body:  ev.Meta.Authors,
			Data: map[string]any{
				"asin":       ev.Meta.ExternalID,
				"finishedAt": ev.FinishedAt.UTC().Format(time.RFC3339),
				"source":     source,
			},
		})
	}
	s.mirrorFinishedToHardcover(ctx, owner, ev)
}

// mirrorFinishedToHardcover routes the finished-book push. With an Enqueuer wired
// it marshals a HardcoverPushPayload and enqueues onto the capped
// HardcoverPushKind queue so all users' pushes share Hardcover's rate limit; an
// enqueue failure falls back to the inline push. Without one it pushes inline.
func (s *Service) mirrorFinishedToHardcover(ctx context.Context, owner string, ev finishedEvent) {
	if s.Enqueuer == nil {
		s.pushFinishedToHardcover(ctx, owner, ev)
		return
	}
	p := payloadFromEvent(owner, ev)
	body, err := json.Marshal(p)
	if err != nil {
		s.logWarn(ctx, "hardcover-push: marshal payload failed — pushing inline", "user", owner, "err", err)
		s.pushFinishedToHardcover(ctx, owner, ev)
		return
	}
	if _, err := s.Enqueuer.Enqueue(ctx, HardcoverPushKind, body, jobs.Owner(owner), jobs.MaxAttempts(3)); err != nil {
		s.logWarn(ctx, "hardcover-push: enqueue failed — pushing inline", "user", owner, "err", err)
		s.pushFinishedToHardcover(ctx, owner, ev)
	}
}

// payloadFromEvent builds a HardcoverPushPayload from a finishedEvent.
func payloadFromEvent(owner string, ev finishedEvent) HardcoverPushPayload {
	return HardcoverPushPayload{
		Owner:      owner,
		ASIN:       ev.Meta.ExternalID,
		AmazonASIN: ev.Meta.AmazonASIN,
		ISBN:       ev.Meta.ISBN,
		Title:      ev.Meta.Title,
		Author:     ev.Meta.Authors,
		FinishedAt: ev.FinishedAt,
	}
}

// pushFinishedToHardcover is the best-effort inline path: it builds the payload
// from ev and runs the push, discarding the error (unchanged behavior — a miss /
// rate limit / transport error is logged inside, never propagated).
func (s *Service) pushFinishedToHardcover(ctx context.Context, owner string, ev finishedEvent) {
	_ = s.RunHardcoverPush(ctx, payloadFromEvent(owner, ev))
}

// RunHardcoverPush matches the finished book and mirrors status='read' + the
// finish date out via the Hardcover connector. Returns nil when there is nothing
// to do (no Hardcover configured, the user hasn't connected, or no confident
// match) and the underlying error on a Match / UpsertUserBook / UpsertRead
// failure (after routing it through hardcoverError so a bad token still flips the
// stored status). This is the job-handler body for HardcoverPushKind.
func (s *Service) RunHardcoverPush(ctx context.Context, p HardcoverPushPayload) error {
	if s.Hardcover == nil {
		return nil
	}
	client, ok, err := s.Hardcover.ClientForUser(ctx, p.Owner)
	if err != nil || !ok {
		if err != nil {
			s.logWarn(ctx, "hardcover: client load failed", "user", p.Owner, "err", err)
		}
		return nil
	}
	match, err := client.Match(ctx, hardcover.MatchInput{
		ASIN:   firstNonEmpty(p.ASIN, p.AmazonASIN),
		ISBN13: p.ISBN,
		Title:  p.Title,
		Author: p.Author,
	})
	if err != nil {
		s.hardcoverError(ctx, p.Owner, "match", err)
		return err
	}
	if match.BookID <= 0 {
		s.logInfo(ctx, "hardcover: no confident match — left for review", "user", p.Owner, "asin", p.ASIN)
		return nil
	}
	// Dry-run: surface WHAT WOULD be written (toast + log) and stop before any
	// mutation. The client's graphql() gate would block the writes anyway; doing
	// it here gives the user a clear, per-book preview of the intended push.
	if client.DryRun() {
		title := firstNonEmpty(p.Title, p.ASIN)
		s.logInfo(ctx, "hardcover DRYRUN: would push finished book",
			"user", p.Owner, "title", title, "bookId", match.BookID, "editionId", match.EditionID)
		if s.Notify != nil {
			s.Notify.Publish(notify.Event{
				Type:  "hardcover.dryrun",
				Owner: p.Owner,
				Title: "Hardcover (dry-run) — would push",
				Body:  fmt.Sprintf("Would mark %q as read", title),
				Data: map[string]any{
					"dryRun":     true,
					"title":      p.Title,
					"asin":       firstNonEmpty(p.ASIN, p.AmazonASIN),
					"bookId":     match.BookID,
					"editionId":  match.EditionID,
					"status":     "read",
					"format":     "audio",
					"finishedAt": p.FinishedAt.UTC().Format(time.RFC3339),
				},
			})
		}
		return nil
	}
	userBookID, err := client.UpsertUserBook(ctx, match.BookID, match.EditionID, hardcover.StatusRead, hardcover.FormatAudio)
	if err != nil {
		s.hardcoverError(ctx, p.Owner, "upsert user_book", err)
		return err
	}
	finishedAt := p.FinishedAt
	if _, err := client.UpsertRead(ctx, userBookID, hardcover.ReadInput{
		FinishedAt:      &finishedAt,
		EditionID:       match.EditionID,
		ReadingFormatID: hardcover.FormatAudio,
	}); err != nil {
		s.hardcoverError(ctx, p.Owner, "upsert user_book_read", err)
		return err
	}
	s.logInfo(ctx, "hardcover: pushed finished book", "user", p.Owner, "asin", p.ASIN, "bookId", match.BookID)
	return nil
}

// syncInProgressToHardcover mirrors every currently-reading Audible title's
// listening % to Hardcover (status=reading + progress). Best-effort + nil-safe:
// no Hardcover connection or a per-book error never fails the sync. Dry-run is
// honored by the client's mutation gate (PushProgressMatched reports applied=false
// under it). It reads the freshly-swept reading_items so the pushed percent
// reflects this run.
//
// Anti-flood (gaka): it pushes ONLY matched rows and ONLY when the percent moved
// since the last real push — it never re-runs the rate-limited match ladder in
// this loop (that per-book, per-sync re-match was the flood). Unmatched rows are
// left for the hardcover-match step; unchanged rows are skipped with no client
// call at all. See inProgressPushMatched.
func (s *Service) syncInProgressToHardcover(ctx context.Context, owner string) {
	if s.Hardcover == nil {
		return
	}
	client, ok, err := s.Hardcover.ClientForUser(ctx, owner)
	if err != nil || !ok {
		if err != nil {
			s.logWarn(ctx, "hardcover: client load failed (in-progress push)", "user", owner, "err", err)
		}
		return
	}
	items, err := s.DB.ListReadingItems(ctx, owner, source)
	if err != nil {
		s.logWarn(ctx, "hardcover: list reading items failed (in-progress push)", "user", owner, "err", err)
		return
	}
	var pushed, skipped int
	for _, it := range items {
		// Stop before each per-book Hardcover call on cancellation (this loop can
		// make one rate-limited push per in-progress title).
		if ctx.Err() != nil {
			return
		}
		bookID, editionID, pct, lenSeconds, do := inProgressPushMatched(it)
		if !do {
			skipped++ // not in-progress, unmatched, or unchanged since last push
			continue
		}
		// Push against the STORED match — never re-run the match ladder here (that
		// per-book, per-sync re-match was the flood). On a first-ever run nothing
		// is matched yet so this loop no-ops for that row; the match step resolves
		// its link and the NEXT cycle pushes. That's the intended handoff.
		applied, err := hardcover.PushProgressMatched(ctx, client, bookID, editionID, float64(pct), 0, lenSeconds, hardcover.FormatAudio)
		if err != nil {
			s.hardcoverError(ctx, owner, "push in-progress", err)
			continue
		}
		// Record the pushed percent ONLY after a real write (applied) so an
		// unchanged next run skips it. A dry-run no-op leaves it unrecorded on
		// purpose: dry-run keeps showing full intent each run, and flipping dry-run
		// off then flushes the backlog once.
		if applied {
			pushed++
			if err := s.DB.SetReadingItemPushedProgress(ctx, owner, it.Source, it.ExternalID, pct); err != nil {
				s.logWarn(ctx, "hardcover: record pushed progress failed", "user", owner, "asin", it.ExternalID, "err", err)
			}
		}
	}
	// Phase summary: progress pushes actually applied vs rows skipped (not
	// in-progress, unmatched, or unchanged since the last push).
	s.logInfo(ctx, "audible: in-progress hardcover push complete", "user", owner, "pushed", pushed, "skipped", skipped)
}

// inProgressPushMatched is the pure skip/push predicate for one reading_item in
// the continuous-progress loop. It returns the STORED Hardcover book/edition ids
// + the percent (int) + audio length to push, and do=true ONLY when the row is
// worth pushing THIS run. do is false when:
//   - the row is not actively in progress (delegated to inProgressPush:
//     status!="reading", pct<=0, or pct>=95), OR
//   - the row is not yet matched (HardcoverBookID == nil) — resolving a match is
//     the hardcover-match step's job; re-matching in the push loop is exactly the
//     flood this change removes, OR
//   - the percent is unchanged since the last REAL push
//     (HardcoverPushedProgress != nil && == pct) — nothing moved, so skip.
//
// Pure — unit-testable without a client or DB.
func inProgressPushMatched(it db.ReadingItem) (bookID, editionID int64, pct, lenSeconds int, do bool) {
	_, pctF, secs, ok := inProgressPush(it)
	if !ok {
		return 0, 0, 0, 0, false
	}
	if it.HardcoverBookID == nil {
		return 0, 0, 0, 0, false // unmatched — leave it for the match step, don't re-fuzz here
	}
	p := int(pctF)
	if it.HardcoverPushedProgress != nil && *it.HardcoverPushedProgress == p {
		return 0, 0, 0, 0, false // unchanged since the last real push
	}
	edition := int64(0)
	if it.HardcoverEditionID != nil {
		edition = *it.HardcoverEditionID
	}
	return *it.HardcoverBookID, edition, p, secs, true
}

// inProgressPush decides whether a reading_item is an in-progress book worth
// mirroring and, if so, builds its Hardcover MatchInput + the percent + the
// audio length in seconds. ok is false for anything not actively in progress
// (want, finished/read, 0% or >=95%). Pure — unit-testable without a client.
func inProgressPush(it db.ReadingItem) (hardcover.MatchInput, float64, int, bool) {
	if it.Status != "reading" {
		return hardcover.MatchInput{}, 0, 0, false
	}
	pct := float64(it.ProgressPercent)
	if pct <= 0 || pct >= readingFinishedPct {
		return hardcover.MatchInput{}, 0, 0, false
	}
	lenSeconds := 0
	if it.RuntimeMin != nil && *it.RuntimeMin > 0 {
		lenSeconds = *it.RuntimeMin * 60
	}
	in := hardcover.MatchInput{
		ASIN:   firstNonEmpty(it.ExternalID, it.AmazonASIN),
		ISBN13: it.ISBN,
		Title:  it.Title,
		Author: it.Authors,
	}
	return in, pct, lenSeconds, true
}

// hardcoverError logs a Hardcover failure and, on a bad token, flips the stored
// key status so the UI prompts a re-paste (the Jan-1 reset makes this routine).
func (s *Service) hardcoverError(ctx context.Context, owner, op string, err error) {
	if s.Hardcover != nil && err == hardcover.ErrBadToken {
		_ = s.Hardcover.MarkInvalid(ctx, owner)
	}
	s.logWarn(ctx, "hardcover: "+op+" failed", "user", owner, "err", err)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// logInfo/logWarn resolve the job-scoped logger from ctx (logctx.FromContext),
// falling back to s.Logger off a job. Threading ctx means every handler line
// inherits the running job's job_id/kind/owner so the Admin viewer can filter to
// one job's run (gaka-f0is).
func (s *Service) logInfo(ctx context.Context, msg string, args ...any) {
	if l := logctx.FromContext(ctx, s.Logger); l != nil {
		l.Info(msg, args...)
	}
}

func (s *Service) logWarn(ctx context.Context, msg string, args ...any) {
	if l := logctx.FromContext(ctx, s.Logger); l != nil {
		l.Warn(msg, args...)
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// parseAudibleTime parses an Audible timestamp that may arrive as an RFC3339
// string, a plain date, or an epoch number (seconds or milliseconds). Returns
// nil on anything it can't interpret.
func parseAudibleTime(raw json.RawMessage) *time.Time {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" || string(raw) == `""` {
		return nil
	}
	// String form.
	if raw[0] == '"' {
		var str string
		if err := json.Unmarshal(raw, &str); err != nil {
			return nil
		}
		str = strings.TrimSpace(str)
		if str == "" {
			return nil
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
			if t, err := time.Parse(layout, str); err == nil {
				u := t.UTC()
				return &u
			}
		}
		// Maybe an epoch encoded as a string.
		if n, err := strconv.ParseInt(str, 10, 64); err == nil {
			return epochToTime(n)
		}
		return nil
	}
	// Numeric epoch.
	if n, err := strconv.ParseInt(string(raw), 10, 64); err == nil {
		return epochToTime(n)
	}
	return nil
}

func epochToTime(n int64) *time.Time {
	if n <= 0 {
		return nil
	}
	// Heuristic: > 1e12 ⇒ milliseconds.
	var t time.Time
	if n > 1_000_000_000_000 {
		t = time.UnixMilli(n).UTC()
	} else {
		t = time.Unix(n, 0).UTC()
	}
	return &t
}

// aggregateBucket is a normalized listening-time bucket parsed from an Audible
// aggregates response.
type aggregateBucket struct {
	date    time.Time
	seconds int64
}

// parseAggregates extracts listening-time buckets from an aggregates response.
// The exact response shape varies by response_group/marketplace, so the parser
// is defensive: it walks every top-level array whose key names listening stats
// and, for each element, pulls a date-ish field + a duration-ish numeric field
// (seconds; milliseconds are downscaled). Unknown shapes yield no buckets rather
// than an error — reading_activity is best-effort.
func parseAggregates(body []byte) []aggregateBucket {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return nil
	}
	var out []aggregateBucket
	for key, raw := range top {
		lk := strings.ToLower(key)
		if !strings.Contains(lk, "listening") && !strings.Contains(lk, "stats") {
			continue
		}
		var arr []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &arr); err != nil {
			continue
		}
		for _, el := range arr {
			b := parseAggregateElement(el)
			if b.seconds > 0 && !b.date.IsZero() {
				out = append(out, b)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].date.Before(out[j].date) })
	return out
}

func parseAggregateElement(el map[string]json.RawMessage) aggregateBucket {
	var b aggregateBucket
	for k, v := range el {
		lk := strings.ToLower(k)
		switch {
		case b.date.IsZero() && (strings.Contains(lk, "date") || strings.Contains(lk, "interval") ||
			strings.Contains(lk, "month") || strings.Contains(lk, "day") || strings.Contains(lk, "time")):
			if t := parseAudibleTime(v); t != nil {
				b.date = *t
			} else if t := parseYearMonth(v); t != nil {
				b.date = *t
			}
		case b.seconds == 0 && (strings.Contains(lk, "seconds") || strings.Contains(lk, "aggregate") ||
			strings.Contains(lk, "value") || strings.Contains(lk, "sum") || strings.Contains(lk, "total") ||
			strings.Contains(lk, "listening")):
			b.seconds = parseDurationSeconds(v)
		}
	}
	return b
}

// parseYearMonth handles an interval encoded as {"year":2023,"month":1} or a
// "2023-01" string.
func parseYearMonth(raw json.RawMessage) *time.Time {
	var obj struct {
		Year  int `json:"year"`
		Month int `json:"month"`
		Day   int `json:"day"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && obj.Year > 0 {
		day := obj.Day
		if day <= 0 {
			day = 1
		}
		month := obj.Month
		if month <= 0 {
			month = 1
		}
		t := time.Date(obj.Year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
		return &t
	}
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		for _, layout := range []string{"2006-01", "2006-01-02"} {
			if t, err := time.Parse(layout, strings.TrimSpace(str)); err == nil {
				u := t.UTC()
				return &u
			}
		}
	}
	return nil
}

// parseDurationSeconds reads a listening duration as a number (seconds; values
// large enough to be milliseconds are downscaled) or a nested {value/unit}-ish
// object's first numeric field.
func parseDurationSeconds(raw json.RawMessage) int64 {
	var num json.Number
	if err := json.Unmarshal(raw, &num); err == nil {
		// Audible's aggregated_sum is a FLOAT (e.g. 49012010.0), so Int64() fails
		// on the decimal — parse as float then truncate. normalizeSeconds then
		// converts the milliseconds magnitude (unit:"Milliseconds") to seconds.
		if f, err := num.Float64(); err == nil {
			return normalizeSeconds(int64(f))
		}
	}
	var obj map[string]json.Number
	if err := json.Unmarshal(raw, &obj); err == nil {
		for _, v := range obj {
			if f, err := v.Float64(); err == nil && f > 0 {
				return normalizeSeconds(int64(f))
			}
		}
	}
	return 0
}

func normalizeSeconds(n int64) int64 {
	if n <= 0 {
		return 0
	}
	// A single day/month of listening in MILLISECONDS is >= ~1e6; treat very
	// large values as ms. 10 days of listening ≈ 864000 s, so the 1e7 ceiling
	// keeps plausible second-counts intact.
	if n > 10_000_000 {
		return n / 1000
	}
	return n
}

// snippet returns a short, safe preview of a response body for error messages.
func snippet(b []byte) string {
	const max = 300
	s := strings.TrimSpace(string(b))
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}
