package api

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/apiroute"
)

// Register mounts the catalyst-books HTTP surface (boom-zp2s Phase 2). Extracted
// verbatim from internal/identity/routes.go — same routes, same order, same
// BooksEnabled() gating — so the API is byte-identical. Called by the composition
// root after the identity routes (book paths never overlap non-book paths, so the
// relative call order is immaterial for matching). Owner resolution inside the
// handlers still goes through apihelpers.IdentifyOwner.
//
// Every registration carries its own .Doc(summary, description): the prose lives
// at the call site for the same reason the response TYPE does — a description
// next to the route cannot outlive it, and deleting the route deletes its
// documentation. There is no second file to forget.
func Register(e *echo.Echo, h *Handler) {
	// Amazon device connect (catalyst-books + catalyst-audiobooks share ONE Amazon
	// link). GET/DELETE status are ALWAYS registered (GET reports {connected:false},
	// DELETE is a no-op when nothing is stored, credential NEVER returned). The
	// connect + import MUTATION routes gate on Cfg.BooksEnabled() — inert on default boot.
	apiroute.GET(e, "/api/v1/amazon", h.GetAmazonConnection).
		Doc("Amazon link status",
			"Whether the caller has a stored Amazon device credential, its last-known "+
				"validity and when that was last checked (RFC3339). ONE Amazon link feeds "+
				"both Kindle and Audible. The credential itself is never returned — not a "+
				"prefix, not a length. Registered even when the books feature is off, where "+
				"it simply answers connected:false.")
	apiroute.NoContent(e, http.MethodDelete, "/api/v1/amazon", h.DisconnectAmazon).
		Doc("Disconnect Amazon",
			"Deletes the caller's stored Amazon device credential and answers 204. "+
				"Idempotent — the same 204 whether or not anything was stored. Registered "+
				"even when the books feature is off. This removes the LINK only; already "+
				"synced Kindle/Audible rows survive, delete those with "+
				"DELETE /api/v1/books/items.")
	if h != nil && h.Cfg != nil && h.Cfg.BooksEnabled() {
		apiroute.POST(e, "/api/v1/amazon/connect/start", h.ConnectAmazonStart).
			Doc("Begin Amazon device registration",
				"Step 1 of 2. Takes an optional marketplace (empty means US) and returns the "+
					"Amazon authorizeUrl to open plus an opaque session. Amazon redirects to its "+
					"OWN maplanding page after login rather than back to boomtime, so the user "+
					"copies that URL and posts it to the complete step. The session is the "+
					"encrypted PKCE registration state — the code_verifier never leaves the "+
					"server in the clear — so hand it back verbatim. Body cap 4 KiB.")
		// complete + import bind their OWN bodies (16 KiB / 64 KiB caps the seam's
		// binding registrars cannot express), so they register as 204-no-body.
		apiroute.NoContent(e, http.MethodPost, "/api/v1/amazon/connect/complete", h.ConnectAmazonComplete).
			Doc("Finish Amazon device registration",
				"Step 2 of 2. Body {session, redirectUrl}: the session from the start step "+
					"plus the maplanding URL Amazon redirected to. Exchanges them for a device "+
					"credential, stores it encrypted, and answers 204 with no body. 400 when "+
					"either field is missing, when the session is invalid or expired (restart "+
					"the flow), or when Amazon rejects the exchange. The body is bound "+
					"in-handler at 16 KiB — maplanding URLs are long — rather than by the "+
					"seam, so no request schema is generated for it.")
		apiroute.NoContent(e, http.MethodPost, "/api/v1/amazon/connect/import", h.ImportAmazonAuth).
			Doc("Import a .audible auth file",
				"The non-interactive alternative to the connect flow. Body {authFile} carries "+
					"the JSON of an existing .audible auth file (PEM device key + adp_token + "+
					"website cookies); it is parsed into a device credential and stored "+
					"encrypted. Answers 204; 400 when authFile is absent or unparseable. Bound "+
					"in-handler at 64 KiB because these files routinely exceed the seam's "+
					"4 KiB cap, so no request schema is generated for it.")
		// Ingest + the siloed view/delete surface (data-deletion on request).
		apiroute.POSTNoBody(e, "/api/v1/amazon/audible/sync", h.SyncAudible).
			Doc("Audible library sync",
				"Runs an Audible library sweep INLINE for the caller and upserts the results "+
					"into the siloed reading_items table, answering {synced, source:\"audible\"}. "+
					"Takes no request body. An Amazon-side failure comes back as 400 carrying the "+
					"upstream status and message, so a request-signing or response-format "+
					"mismatch is diagnosable from the UI rather than only from the logs.")
		apiroute.Accepted(e, http.MethodPost, "/api/v1/amazon/audible/backfill", h.BackfillAudible).
			Doc("Audible all-time backfill",
				"Enqueues the one-shot, all-time Audible backfill (full library sweep + "+
					"finished sweep + monthly listening aggregates) and answers 202 with the job "+
					"id to poll — it does NOT wait for the sweep. Takes no request body. 400 when "+
					"background jobs are unavailable on this server or no Amazon credential is "+
					"stored, so a missing link fails here rather than minutes later in a job log. "+
					"Safe to call twice: the backfill upserts.")
		// catalyst-books (Kindle) ingest triggers — the ebook mirror of audible/*.
		apiroute.POSTNoBody(e, "/api/v1/kindle/sync", h.SyncKindle).
			Doc("Kindle library sync",
				"The ebook mirror of the Audible sync: pulls the Kindle library and upserts "+
					"into reading_items, answering {synced, source:\"kindle\"}. Runs INLINE "+
					"because the Kindle sweep is a single whispersync pull rather than a "+
					"paginated crawl. Takes no request body; an Amazon-side failure is surfaced "+
					"verbatim as a 400.")
		apiroute.Accepted(e, http.MethodPost, "/api/v1/kindle/backfill", h.BackfillKindle).
			Doc("Kindle all-time backfill",
				"Enqueues the one-shot Kindle backfill on the jobs worker and answers 202 with "+
					"the job id. Takes no request body. 400 when background jobs are unavailable "+
					"or no Amazon credential is stored. Idempotent to enqueue — the backfill "+
					"upserts, so a duplicate run is harmless.")
		apiroute.POSTNoBody(e, "/api/v1/kindle/insights", h.SyncKindleInsights).
			Doc("Kindle Reading Insights ingest",
				"Fetches the caller's Kindle reading history (finish DATES + streaks), stores "+
					"the raw snapshot, and backfills finished_at onto their existing Kindle "+
					"reading_items — answering {datesBackfilled, source:\"kindle\"}. Runs inline "+
					"(one GET). Run it AFTER a library sync: it only dates rows that already "+
					"exist, so on an empty library it reports 0.")
		apiroute.Accepted(e, http.MethodPost, "/api/v1/kindle/reconcile", h.ReconcileKindle).
			Doc("Kindle status reconcile",
				"Enqueues the status-reconcile sweep and answers 202 with the job id. Unlike "+
					"sync and insights this is roughly one CDE sidecar call per not-yet-read "+
					"book — thousands for a large library — which is why it runs on the worker "+
					"rather than inline. It promotes a book to 'reading' when it has a "+
					"last-page-read record, leaves un-opened books as 'want', and never "+
					"clobbers a read/finished row. 400 without background jobs or an Amazon "+
					"credential. Idempotent to enqueue.")
		apiroute.GET(e, "/api/v1/books/items", h.GetReadingItems).
			Doc("Synced reading items",
				"Every book and audiobook synced for the caller, as the view DTO — never the "+
					"raw source blob. items is ALWAYS an array, never null. Optional ?source= "+
					"filters to kindle or audible; omit it for both. status/rating/finishedAt "+
					"are the EFFECTIVE values (curation override falling back to the "+
					"Amazon-derived value); statusDerived plus the *Override fields expose both "+
					"layers so the UI can flag a row as curated. Not paginated — the whole "+
					"library comes back in one response.")
		apiroute.DELETE(e, "/api/v1/books/items", h.DeleteReadingItemsHandler).
			Doc("Wipe synced reading items",
				"DESTRUCTIVE data-deletion-on-request: removes the caller's synced book rows "+
					"and answers {deleted:<rows>}. Optional ?source= scopes the wipe to kindle "+
					"or audible; omit it and ALL sources are removed. When the wipe covers "+
					"Kindle it also drops the Kindle Reading-Insights snapshot (best effort — a "+
					"failure there is logged, not surfaced). Idempotent; the Amazon link itself "+
					"is untouched, so a later sync repopulates.")
		// Book detail side panel: all editions of one canonical Work (rows sharing a
		// hardcover_book_id, or amazon_asin for unmatched siblings).
		apiroute.GET(e, "/api/v1/books/work", h.GetBookWork).
			Doc("One Work, every edition",
				"Backs the book detail side panel: every edition of ONE canonical Work the "+
					"caller owns (rows sharing a Hardcover book id, or — for still-unmatched "+
					"books — an amazon_asin), plus that Work's read history so re-reads show up "+
					"as separate entries. Identify the Work with ?bookId=<hardcover id> or "+
					"?asin=<amazon asin>; at least one is required or the call is a 400, and "+
					"bookId must be a positive integer. reads is best effort — a history load "+
					"miss returns the editions with an empty array rather than failing.")
		// User-scoped reading-monitor status for the global nav indicator.
		apiroute.GET(e, "/api/v1/books/reading-monitor/status", h.ReadingMonitorStatus).
			Doc("Reading-monitor indicator state",
				"The thin SELF-only read behind the global nav indicator: is the persistent "+
					"Kindle reading monitor enabled for me, am I inside a calibration burst, and "+
					"when does that burst expire (RFC3339, or null). Read-only — it never "+
					"mutates and never starts the engine. Deliberately separate from the "+
					"admin-gated GET /api/v1/admin/books/reading-monitor so the nav can poll it "+
					"without granting the caller the operator surface.")
		// Delete one read from the history (reading_events) + propagate to Hardcover.
		apiroute.DELETE(e, "/api/v1/books/reads/:id", h.DeleteReadingEvent).
			Doc("Delete one read",
				"Removes a single read from the caller's local reading_events history — how "+
					"you prune a junk read or a finish you undid. When that read ORIGINATED on "+
					"Hardcover the matching user_book_read is deleted there too and "+
					"hardcoverDeleted comes back true; the remote delete is best effort, so a "+
					"miss is logged and reported as false rather than failing the call. 400 on "+
					"a non-numeric or non-positive id, 404 when no such read belongs to the "+
					"caller.")
		// Curation override: set the effective status/rating/finish for one row
		// (boom-books, migration 00069) + enqueue the Hardcover push.
		apiroute.PATCH(e, "/api/v1/books/items/:externalId/curation", h.SetBookCuration).
			Doc("Curate one book",
				"Writes the user OVERRIDE layer for one row — status, rating and/or finish "+
					"date — and returns the updated item with its new effective values. The "+
					"Amazon-derived layer is never touched. The row is keyed by the caller plus "+
					"the REQUIRED ?source= (kindle|audible) plus :externalId (the ASIN). Each "+
					"body field is optional and tri-state: absent leaves the override alone, an "+
					"explicit null CLEARS it back to the derived value, a value sets it. status "+
					"must be one of want, reading, read, paused, dnf. A Hardcover push is "+
					"enqueued afterwards, best effort — the override is already persisted, so an "+
					"enqueue miss only delays the mirror. 400 on a missing source or an invalid "+
					"field, 404 when the row does not exist. Body cap 4 KiB.")
		// Push-only: re-mirror one row's CURRENT effective state to Hardcover on demand.
		// On the typed seam via WritesJSON: the handler owns the write because it has
		// TWO success branches (200 + readingItemDTO inline, 202 {enqueued} on the queue
		// fallback) that one (Resp, status) registration cannot both state. Declaring
		// the 200 shape and naming the fallback in prose is strictly better than the
		// generic stub this used to emit.
		apiroute.WritesJSON[readingItemDTO](e, http.MethodPost, "/api/v1/books/items/:externalId/push", h.PushBookToHardcover).
			Doc("Push one book to Hardcover",
				"The per-row \"sync this book now\" button. Changes NOTHING locally — it "+
					"re-mirrors the row's CURRENT effective status, finish date and rating out "+
					"to Hardcover. Keyed by the caller plus the REQUIRED ?source= (kindle|"+
					"audible) plus :externalId. Normally the push runs INLINE and the response "+
					"is 200 with the re-read item, so the advanced hardcoverStatus clears the "+
					"out-of-sync badge without a reload — that is the documented 200 schema "+
					"below. When the inline push service is not wired the work is queued "+
					"instead and the response is 202 {\"enqueued\": true}; that branch is not "+
					"in the schema. 400 on a missing source, 404 when the row does not exist, "+
					"409 when the book has no Hardcover match yet (match it first). The push "+
					"itself is suppressed while BOOM_HARDCOVER_DRYRUN is on.")
		// Manual match-fixer: apply a user-chosen Hardcover book to a reading_item.
		// Also WritesJSON: BOTH branches are 200 but carry different shapes, so the
		// normal one is declared and the degraded ack is described.
		apiroute.WritesJSON[readingItemDTO](e, http.MethodPost, "/api/v1/books/items/:externalId/match", h.SetBookManualMatch).
			Doc("Apply a manual Hardcover match",
				"Links one reading item to a user-chosen Hardcover book, for the books the "+
					"automated match ladder cannot resolve. Body {hardcoverBookId (required, "+
					"positive), editionId?, slug?}; a missing edition or slug is resolved "+
					"server-side, and a failure there still links on the book id alone. Keyed "+
					"by the caller plus the REQUIRED ?source= plus :externalId. The linkage is "+
					"stored with confidence \"manual\" on THIS row only — a human's pick is "+
					"authoritative for their library, not for the shared match cache. Answers "+
					"200 with the updated item (the schema below). In the rare case the "+
					"post-write read-back fails the link is still persisted and the response is "+
					"instead 200 {\"matched\": true, \"hardcoverBookId\": <id>}. 400 on a "+
					"missing source, unparseable body or absent hardcoverBookId; 404 when the "+
					"row does not exist. The body is decoded in-handler and uncapped, so no "+
					"request schema is generated for it.")
		// Interactive Hardcover catalog search (autocomplete for the match-fixer).
		apiroute.GET(e, "/api/v1/hardcover/search", h.HardcoverSearch).
			Doc("Hardcover catalog search",
				"Live autocomplete over Hardcover's catalog, feeding the manual match-fixer. "+
					"Pass ?q=; candidates is ALWAYS an array, never null. A query shorter than "+
					"2 characters returns an EMPTY set rather than an error, so the autocomplete "+
					"stays quiet while the user types, and the result count is fixed at 8. "+
					"Requires a connected Hardcover token: 412 when Hardcover is not connected, "+
					"502 when Hardcover rejects the token (reconnect), 429 when Hardcover "+
					"rate-limits.")
		// Orchestrator: chain the whole reading-sync pipeline behind ONE enqueue.
		apiroute.Accepted(e, http.MethodPost, "/api/v1/books/sync-all", h.SyncAllBooks).
			Doc("Full reading-sync pipeline",
				"ONE enqueue for the whole pipeline — Audible ingest, then Kindle ingest, then "+
					"Hardcover match, then Hardcover pull — instead of the UI firing four kinds "+
					"in order. Answers 202 with the orchestrator job id; the stages run on the "+
					"worker. Takes no request body. 400 without background jobs or an Amazon "+
					"credential; a missing Hardcover token is NOT an error, the match and pull "+
					"stages simply no-op while the ingests still run. Idempotent to enqueue — "+
					"every stage is individually re-runnable.")

		// ── Liberation (boom-w20s.15) — the Libation rebuild ─────────────────
		// Nested under BooksEnabled AND gated on LiberationEnabled() (which also
		// requires a configured library path), so with the feature off these
		// paths 404 rather than existing and erroring. Same 404-when-off
		// convention the e2e specs rely on elsewhere.
		if h.Cfg.LiberationEnabled() {
			apiroute.Accepted(e, http.MethodPost, "/api/v1/books/items/:externalId/liberate", h.LiberateBook).
				Doc("Liberate one title",
					"Queues ONE Audible title (:externalId is the ASIN) for download and remux "+
						"into the configured library path, answering 202 {enqueued, jobId, asin}. "+
						"It is always enqueued, never inline: a liberation is minutes of download "+
						"plus minutes of remux, which would outlive any sane proxy timeout. "+
						"Ownership is confirmed FIRST, so an unknown ASIN is an immediate 404 "+
						"rather than a job that fails later. 409 when a run is already in flight "+
						"for that title — pass ?force=true to override, which is also how you "+
						"re-liberate. The job runs with maxAttempts=1 because an automatic retry "+
						"re-downloads the whole file. 400 when background jobs or liberation are "+
						"not configured on this server.")
			apiroute.DELETE(e, "/api/v1/books/items/:externalId/liberate", h.ForgetLiberation).
				Doc("Forget one liberation",
					"Clears the stored liberation state for one title so it can be re-run, "+
						"answering {forgotten, fileDeleted}. The downloaded audio is removed ONLY "+
						"when ?deleteFile=true — the default keeps it, so a mis-click costs a "+
						"database row rather than a multi-hundred-megabyte re-download. "+
						"fileDeleted reports which of the two happened. 404 when the caller owns "+
						"no title with that ASIN, 400 when liberation is not configured.")
			// Accepted (not AcceptedBody): the sweep's optional body must keep its
			// tolerate-anything binding — see SweepLiberation's comment.
			apiroute.Accepted(e, http.MethodPost, "/api/v1/books/liberate/sweep", h.SweepLiberation).
				Doc("Sweep the whole library",
					"Queues the whole-library liberation sweep, answering 202 {enqueued, jobId, "+
						"pending} — pending is how many titles the sweep is about to take on, so "+
						"the UI can state roughly how many gigabytes the user just committed to. "+
						"The optional body {limit, force} is bound leniently IN the handler and a "+
						"malformed or absent body deliberately means \"everything, unforced\" "+
						"rather than a 400 (the web client double-encodes it, so a strict bind "+
						"would reject every sweep); no request schema is generated for it. 400 "+
						"when background jobs or liberation are not configured.")
			// Typed seam (internal/shared/apiroute): the response TYPE is
			// captured here, so the OpenAPI schema is generated from Go rather
			// than hand-written. Registering these with plain e.GET no longer
			// compiles — which is the point.
			apiroute.GET(e, "/api/v1/books/liberation/status", h.LiberationStatus).
				Doc("Liberation rollup",
					"Per-status counts across the owner's Audible library, how many titles a sweep "+
						"would queue right now, how many the sweep has given up on, and the library "+
						"path files are written to.")
			apiroute.GET(e, "/api/v1/books/liberation/excluded", h.LiberationExcluded).
				Doc("Titles the sweep skips",
					"The rows behind the excluded COUNT on the liberation rollup: every title the "+
						"sweep will not pick up on its own, with the status and error that got it "+
						"there. Counts alone cannot answer \"should it really have given up on that "+
						"one?\" — before this existed, excluded titles were correctly skipped and "+
						"completely invisible. 400 when liberation is not configured.")
		}
	}

	// Hardcover connect (catalyst-books PUSH target). GET/DELETE status ALWAYS
	// registered; the connect + pull + match MUTATION routes gate on BooksEnabled().
	apiroute.GET(e, "/api/v1/hardcover", h.GetHardcoverConnection).
		Doc("Hardcover link status",
			"Whether the caller has a stored Hardcover bearer token, its last-known "+
				"validity and when that was last checked (RFC3339). The token is NEVER "+
				"returned — no prefix, no length, no hint. status flips to invalid after "+
				"Hardcover answers 401, which is the cue for the UI to prompt a re-paste; "+
				"Hardcover tokens expire yearly and reset every January 1, so that is "+
				"routine. Registered even when the books feature is off.")
	apiroute.NoContent(e, http.MethodDelete, "/api/v1/hardcover", h.DisconnectHardcover).
		Doc("Disconnect Hardcover",
			"Clears the caller's stored Hardcover token and answers 204. Idempotent — the "+
				"same 204 whether or not a token was stored. Existing Hardcover match "+
				"linkage on reading_items is left in place; only the credential goes.")
	if h != nil && h.Cfg != nil && h.Cfg.BooksEnabled() {
		apiroute.NoContentBody(e, http.MethodPost, "/api/v1/hardcover/connect", h.ConnectHardcover).
			Doc("Connect Hardcover",
				"Body {token}: a bearer token copied from Hardcover account settings. "+
					"Validate-THEN-persist — the token is checked with a me{} query (15 s "+
					"timeout) before anything is written, so an obviously bad token never "+
					"survives in the database. Answers 204 on success. 400 when the token is "+
					"blank, when Hardcover rejects it, or when Hardcover is rate-limiting; a "+
					"network or timeout failure is a 500 and stores nothing. The plaintext "+
					"token is never logged and never returned by any endpoint. Body cap 4 KiB.")
		// Inbound sync (PULL half): read the shelf + reconcile linkage.
		apiroute.Accepted(e, http.MethodPost, "/api/v1/hardcover/pull", h.PullHardcover).
			Doc("Pull from Hardcover",
				"The INBOUND half of the bidirectional sync: reads the caller's Hardcover "+
					"shelf and reconciles each entry's status and updated_at onto the matching "+
					"local row's linkage columns. Enqueued, answering 202 with the job id, "+
					"because the shelf sweep is paginated. Takes no request body. 400 when "+
					"background jobs are unavailable or Hardcover is not connected — checked "+
					"up front so the UI gets a clear error instead of a job that silently "+
					"no-ops. Idempotent to enqueue.")
		// Explicit MATCH stage: resolve unmatched reading_items to a Hardcover book.
		apiroute.Accepted(e, http.MethodPost, "/api/v1/hardcover/match", h.MatchHardcover).
			Doc("Match unmatched books",
				"The middle stage of backfill → match → sync: resolves every still-unmatched "+
					"reading item to a Hardcover book and edition through the read-only match "+
					"ladder and caches the linkage. Enqueued, answering 202 with the job id. "+
					"Takes no request body. Pass ?force=1 to ignore the 30-day negative-cache "+
					"window and retry rows the ladder previously proved unmatchable; without "+
					"it, the normal windowed sweep runs. 400 when background jobs are "+
					"unavailable or Hardcover is not connected. Idempotent to enqueue — an "+
					"already-matched row drops out of the worklist.")
	}
}
