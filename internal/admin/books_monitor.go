// books_monitor.go — the admin-only LIVE Kindle reading-monitor
// (gaka-books). A WebSocket that, while a client is connected, polls each
// in-progress Kindle book's last-page-read POSITION at a HIGH sample rate
// (default ~6s) via the Fiona CDE sidecar and streams every advance to the
// Admin › Books › Reading-monitor tab. The operator opens a book on their
// Kindle/iPad, hits Start, reads, and watches the furthest-page-read advance
// in real time — an empirical probe of the whispersync sync cadence.
//
// This is a READ-ONLY diagnostic: it NEVER writes kindle_reading_positions or
// reading_activity (that's the real books-kindle-reading-time job's path —
// reading_time.go). It only observes. Because it hits the sidecar frequently,
// it is admin-only, feature-gated (BOOM_FEATURE_BOOKS), capped to a small book
// set, and auto-stops after a safety window.
//
// Auth mirrors the other admin WS routes (CLIRunWS / AdminLabelImagesWS): the
// HttpOnly refresh_token cookie authenticates the handshake (a WS handshake
// can't carry an Authorization header) and the owner is admin-gated in-handler.
package admin

import (
	"context"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/amazon"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apierr"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/apihelpers"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/metrics"
)

// Reading-monitor tuning bounds. Defaults are deliberately aggressive (this is
// a live diagnostic), but every knob is clamped so a crafted query param can't
// turn it into a firehose or a resource leak.
const (
	monitorDefaultInterval = 6 * time.Second  // poll cadence when ?interval is unset
	monitorMinInterval     = 2 * time.Second  // fastest allowed (sidecar is a live GET/book)
	monitorMaxInterval     = 60 * time.Second // slowest allowed
	monitorDefaultLimit    = 12               // in-progress books polled per cycle by default
	monitorMaxLimit        = 50               // hard cap on the book set
	monitorMaxDuration     = 20 * time.Minute // safety auto-stop even if the client lingers
)

// monitorFrame is one frame of the reading-monitor stream. A single struct
// carries every frame type (discriminated by Type) so the FE parses one shape:
//
//	info      — sent once on connect: interval + the in-progress book count.
//	sample    — a book's position was first-seen or ADVANCED this cycle. Carries
//	            location + creationTime (Amazon's own event time) + sampledAt.
//	heartbeat — one per poll cycle, ALWAYS: proves the stream is alive even when
//	            nothing advanced. Carries how many books were polled/returned.
//	error     — a per-book fetch error or a listing error; the stream continues.
//
// Location/CreationTime use omitempty: only `sample` frames set them, and a real
// furthest-page-read location is a large positive offset (never 0), so dropping
// a zero value is harmless.
type monitorFrame struct {
	Type         string `json:"type"`
	ASIN         string `json:"asin,omitempty"`
	Title        string `json:"title,omitempty"`
	Location     int64  `json:"location,omitempty"`
	CreationTime string `json:"creationTime,omitempty"` // sidecar event time, RFC3339
	SampledAt    string `json:"sampledAt,omitempty"`    // server observation time, RFC3339
	Books        int    `json:"books,omitempty"`        // in-progress books this cycle
	Polled       int    `json:"polled,omitempty"`       // # that returned a position
	IntervalSec  int    `json:"intervalSec,omitempty"`  // effective poll cadence
	Message      string `json:"message,omitempty"`
	Error        string `json:"error,omitempty"`
}

// AdminBooksReadingMonitorWS: GET
// /api/v1/admin/books/reading-monitor/ws?interval=<sec>&limit=<n> — admin-only
// live Kindle reading-position monitor. Registered ONLY when BOOM_FEATURE_BOOKS
// is on (see routes.go), so it 404s like any unknown path when the feature is
// off. Cookie-authed + admin-gated in-handler.
func (h *Handler) AdminBooksReadingMonitorWS(c *echo.Context) error {
	owner, aerr := apihelpers.IdentifyOwnerFromCookie(h.DB, h.Logger, c, apierr.ExpiredRefreshToken())
	if aerr != nil {
		return apihelpers.RespondErr(c, aerr)
	}
	if !h.Cfg.IsAdmin(owner) {
		return apihelpers.RespondErr(c, apierr.Forbidden("admin only"))
	}

	interval := clampInterval(time.Duration(apihelpers.QueryInt64(c, "interval", int64(monitorDefaultInterval/time.Second))) * time.Second)
	limit := clampLimit(int(apihelpers.QueryInt64(c, "limit", monitorDefaultLimit)))

	conn, err := websocket.Accept(c.Response(), c.Request(), &websocket.AcceptOptions{
		InsecureSkipVerify: true, // same-origin; CORS handled elsewhere
	})
	if err != nil {
		return nil // handshake failed; nothing more to do
	}
	defer conn.CloseNow()
	metrics.WSActiveConnections.WithLabelValues("books-monitor").Inc()
	defer metrics.WSActiveConnections.WithLabelValues("books-monitor").Dec()

	// Background context so streaming outlives the HTTP handler return, bounded
	// by a safety auto-stop so a forgotten open tab can't poll the sidecar
	// forever.
	ctx, cancel := context.WithTimeout(context.Background(), monitorMaxDuration)
	defer cancel()

	// A reader goroutine cancels the stream the moment the client disconnects.
	go func() {
		for {
			if _, _, rerr := conn.Read(ctx); rerr != nil {
				cancel()
				return
			}
		}
	}()

	// The monitor reuses the admin's own connected Amazon device credential (the
	// same one the real reading-time job and the diagnostics dump sign with).
	cred, lerr := amazon.NewStore(h.DB).Load(ctx, owner)
	if lerr != nil {
		_ = wsjson.Write(ctx, conn, monitorFrame{
			Type:  "error",
			Error: "no Amazon credential — connect Amazon in Settings first (" + lerr.Error() + ")",
		})
		conn.Close(websocket.StatusNormalClosure, "no credential")
		return nil
	}

	sc := amazon.NewKindleSidecarClient()
	lister := func(lctx context.Context) ([]db.ReadingItem, error) {
		return h.listInProgressKindle(lctx, owner, limit)
	}

	// Opening `info` frame: immediate proof the stream is live + the effective
	// cadence the FE should display.
	_ = wsjson.Write(ctx, conn, monitorFrame{
		Type:        "info",
		IntervalSec: int(interval / time.Second),
		Message:     "reading monitor live — open + read a Kindle book to watch the position advance",
		SampledAt:   time.Now().UTC().Format(time.RFC3339),
	})

	emit := func(f monitorFrame) error { return wsjson.Write(ctx, conn, f) }
	_ = streamReadingMonitor(ctx, sc, cred, lister, interval, emit)

	conn.Close(websocket.StatusNormalClosure, "monitor closed")
	h.Logger.Info("admin books reading-monitor closed", "actor", owner, "intervalSec", int(interval/time.Second), "limit", limit)
	return nil
}

// listInProgressKindle returns the owner's in-progress Kindle books — the poll
// set — capped to the `limit` most-recently-synced (the best available proxy for
// "most-recently-active"). A book with no ASIN (external_id) can't be polled and
// is dropped. reading_items rows arrive ORDER BY finished,title, so we re-sort by
// SyncedAt desc before capping.
func (h *Handler) listInProgressKindle(ctx context.Context, owner string, limit int) ([]db.ReadingItem, error) {
	items, err := h.DB.ListReadingItems(ctx, owner, "kindle")
	if err != nil {
		return nil, err
	}
	inProgress := make([]db.ReadingItem, 0, len(items))
	for _, it := range items {
		if it.Status == "reading" && it.ExternalID != "" {
			inProgress = append(inProgress, it)
		}
	}
	// Most-recently-synced first, then cap. Stable so equal SyncedAt keeps the
	// DB's title order.
	sortBySyncedDesc(inProgress)
	if len(inProgress) > limit {
		inProgress = inProgress[:limit]
	}
	return inProgress, nil
}

// streamReadingMonitor is the poll loop, factored out of the WS handler so it is
// testable with a fake amazon.KindleSidecar + a scripted lister + a capturing
// emit sink (no network, no socket). Each cycle it lists the in-progress books,
// polls each book's current last-page-read position, emits a `sample` frame for
// every book whose position was FIRST-SEEN or ADVANCED since the last cycle
// (unchanged positions are deduped — the heartbeat covers liveness), then emits
// exactly one `heartbeat` frame. It polls once immediately, then every interval.
//
// It returns when ctx is cancelled (client disconnect or the safety timeout) or
// when emit fails (client gone). A per-book fetch error or a listing error emits
// an `error` frame and the loop continues — one bad book never kills the stream.
func streamReadingMonitor(
	ctx context.Context,
	sc amazon.KindleSidecar,
	cred *amazon.DeviceCredential,
	lister func(context.Context) ([]db.ReadingItem, error),
	interval time.Duration,
	emit func(monitorFrame) error,
) error {
	lastLoc := map[string]int64{} // asin -> last emitted position (dedup key)

	poll := func() error {
		now := time.Now().UTC()
		items, lerr := lister(ctx)
		if lerr != nil {
			// A listing error is surfaced but non-fatal: still heartbeat so the
			// UI shows the monitor is alive and retrying.
			if eerr := emit(monitorFrame{Type: "error", Error: "list in-progress books: " + lerr.Error(), SampledAt: now.Format(time.RFC3339)}); eerr != nil {
				return eerr
			}
			items = nil
		}
		polled := 0
		for _, it := range items {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			pos, at, ok, ferr := sc.FetchLastPagePosition(ctx, cred, it.ExternalID)
			if ferr != nil {
				if eerr := emit(monitorFrame{Type: "error", ASIN: it.ExternalID, Title: it.Title, Error: ferr.Error(), SampledAt: now.Format(time.RFC3339)}); eerr != nil {
					return eerr
				}
				continue
			}
			if !ok {
				continue // clean miss — no recorded position (a stateless book 404s)
			}
			polled++
			if prev, had := lastLoc[it.ExternalID]; had && prev == pos {
				continue // no advance since last cycle → heartbeat covers liveness
			}
			lastLoc[it.ExternalID] = pos
			creation := ""
			if !at.IsZero() {
				creation = at.UTC().Format(time.RFC3339)
			}
			if eerr := emit(monitorFrame{
				Type:         "sample",
				ASIN:         it.ExternalID,
				Title:        it.Title,
				Location:     pos,
				CreationTime: creation,
				SampledAt:    now.Format(time.RFC3339),
			}); eerr != nil {
				return eerr
			}
		}
		return emit(monitorFrame{Type: "heartbeat", Books: len(items), Polled: polled, SampledAt: now.Format(time.RFC3339)})
	}

	// Poll once immediately so the UI shows current positions without waiting a
	// full interval.
	if err := poll(); err != nil {
		return err
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := poll(); err != nil {
				return err
			}
		}
	}
}

// sortBySyncedDesc stable-sorts reading items by SyncedAt descending in place.
func sortBySyncedDesc(items []db.ReadingItem) {
	// insertion sort keeps it dependency-free + stable; the slice is tiny (<= the
	// full kindle library, and we only ever emit `limit` of it).
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].SyncedAt.After(items[j-1].SyncedAt); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

func clampInterval(d time.Duration) time.Duration {
	if d < monitorMinInterval {
		return monitorMinInterval
	}
	if d > monitorMaxInterval {
		return monitorMaxInterval
	}
	return d
}

func clampLimit(n int) int {
	if n < 1 {
		return 1
	}
	if n > monitorMaxLimit {
		return monitorMaxLimit
	}
	return n
}
