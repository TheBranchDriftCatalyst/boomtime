// PublicDashboard — /p/:slug read-only public dashboard (gaka-6jm.1 +
// gaka-keb).
//
// Composes the isolated grid primitive (@shared/lib/grid) with boomtime-specific
// pieces: the catalog-derived widget instances, the dbStorageAdapter, and
// the catalyst-hacker aesthetic via ./hacker.css. Read-only by default —
// the DashboardEditor variant handles the settings-side editing flow.
//
// Design intent (from the design brief):
//   - Bootup animation: whole grid fades in briefly, tiles stagger 60ms.
//   - Hero identity: chunky monospace username, magenta glow, RPG-class
//     tagline built from top language + top editor.
//   - Grid: 12-col asymmetric magazine layout with a poster grade badge.
//   - Footer: `> END OF TRANSMISSION · boomtime vX · <url>` with cursor.
//
// 404 handling: intentionally-terse "not public" — no signup CTA, respect
// the owner's opt-out.
import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { useParams } from "react-router";
import { Spinner } from "@thebranchdriftcatalyst/catalyst-ui/ui/spinner";
import { api, ApiError } from "@shared/lib/api";
import { qk } from "@shared/lib/queryKeys";
import {
  DraggableGridLayout,
  memoryAdapter,
  type WidgetInstance,
} from "@shared/lib/grid";
import { WIDGET_CATALOG } from "@shared/features/widgets/catalog";
import { WidgetRenderer } from "@shared/features/widgets/renderers/WidgetRenderer";
import { DossierControls, ReclassifyOverlay } from "./ProfileChrome";
import { HeroBackdrop } from "./HeroBackdrop";
import { useProfileRange } from "./profileRange";
import { PUBLIC_PROFILE_DEFAULT_LAYOUT } from "./defaults";
import type { PublicDashboardPayload } from "@shared/types/stats";
import "./hacker.css";
// Arasaka overrides are scoped by .theme-arasaka .public-dashboard, so
// this import is a no-op for every other theme. See ./arasaka.css.
import "./arasaka.css";
// Dossier foundation (gaka-174.1). Loaded LAST so its clean-mode classline
// rule wins over arasaka.css's base `display:none`; arasaka's higher-
// specificity `.theme-arasaka …` rules still win under that theme.
import "./dossier.css";

export interface PublicDashboardProps {
  /** Explicit slug (gaka-4ng: the in-app owner view at /app/profile has no
   * :slug route param, so it injects the owner's resolved slug). Falls back to
   * the /p/:slug route param when omitted. */
  slug?: string;
}

export function PublicDashboard({ slug: slugProp }: PublicDashboardProps = {}) {
  const params = useParams<{ slug: string }>();
  const slug = slugProp ?? params.slug ?? "";

  // gaka-174.7: the selected stats window drives the payload query. The
  // query key carries `rangeDays` so switching windows refetches; the base
  // qk.publicDashboard(slug) stays a prefix so invalidations still match.
  const [rangeDays] = useProfileRange();
  const { data, isLoading, error } = useQuery({
    queryKey: [...qk.publicDashboard(slug), rangeDays],
    queryFn: () => api.getPublicDashboard(slug, rangeDays),
    enabled: !!slug,
    retry: (failureCount, err) => {
      if (err instanceof ApiError && err.status === 404) return false;
      return failureCount < 1;
    },
  });

  if (error instanceof ApiError && error.status === 404) {
    return (
      <PublicShell>
        <div className="mx-auto max-w-md py-24 text-center">
          <h1 className="text-2xl font-semibold">This profile isn't public</h1>
          <p className="mt-2 text-muted-foreground">
            The link may be mistyped, or the owner has disabled public visibility.
          </p>
        </div>
      </PublicShell>
    );
  }

  if (isLoading || !data) {
    return (
      <PublicShell>
        <div className="flex h-[60vh] items-center justify-center">
          <Spinner />
        </div>
      </PublicShell>
    );
  }

  if (error) {
    return (
      <PublicShell>
        <div className="mx-auto max-w-md py-24 text-center">
          <h1 className="text-2xl font-semibold">Something went wrong</h1>
          <p className="mt-2 text-muted-foreground">Please try again later.</p>
        </div>
      </PublicShell>
    );
  }

  return (
    <PublicShell>
      <DashboardBody data={data} slug={slug} />
    </PublicShell>
  );
}

function DashboardBody({ data, slug }: { data: PublicDashboardPayload; slug: string }) {
  // Seed the initial layout from the payload if present, else the default.
  const seed = useMemo(() => {
    const persisted = (data.layout as { widgets?: unknown } | undefined)?.widgets;
    if (Array.isArray(persisted) && persisted.length > 0) {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      return persisted as any;
    }
    return PUBLIC_PROFILE_DEFAULT_LAYOUT;
  }, [data.layout]);

  const instances: WidgetInstance[] = useMemo(
    () =>
      WIDGET_CATALOG.filter((e) => (e.dashboardScopes ?? []).includes("profile")).map(
        (e) => ({
          key: e.kind,
          displayName: e.title,
          defaultLayout: e.defaultLayout,
          views: e.views,
          defaultView: e.defaultView,
          render: ({ view }) => (
            <WidgetRenderer kind={e.kind} view={view} data={data} slug={slug} />
          ),
        }),
      ),
    [data, slug],
  );

  // In-memory storage: the public page is read-only. View toggles + drag
  // are all client-side state and evaporate on refresh. The owner uses
  // the inline ProfileEditor (rendered by EditableProfilePage when the
  // caller owns the profile) to persist changes; see gaka-ie3.
  const storage = useMemo(() => memoryAdapter(seed), [seed]);

  const version = window.location.host;

  return (
    <div className="public-dashboard">
      {/* gaka-174.1: the whole page reads as one dossier "file". The frame
       * carries the spine hairlines + corner registration marks; the inner
       * column holds the classification strip, hero, grid, and footer. */}
      <div className="public-dashboard__frame mx-auto max-w-7xl">
        <span className="dossier-corner dossier-corner--tl" aria-hidden />
        <span className="dossier-corner dossier-corner--tr" aria-hidden />
        <span className="dossier-corner dossier-corner--bl" aria-hidden />
        <span className="dossier-corner dossier-corner--br" aria-hidden />

        {/* gaka-k2p + gaka-174.1: dossier classification strip. Clean mode
         * shows a restrained neutral line; the arasaka theme restores the
         * loud CLEARANCE/CLASSIFIED/katakana embellishments (dossier.css). */}
        <DossierClassLine username={data.username} slug={slug} />
        <header className="public-dashboard__hero">
          {/* gaka-174.3: lazy WebGL ambient field behind the hero. Renders
           * nothing on no-WebGL / reduced-motion clients — the hero stays
           * fully legible without it. */}
          <HeroBackdrop />
          {/* gaka-174: the "> PROFILE · <user>@boomtime" meta line was
           * redundant with the big title + the SERVICE RECORD strip and just
           * ate vertical space — removed. */}
          <h1 className="public-dashboard__hero-title" data-testid="public-username">
            {data.username}
          </h1>
          <div className="public-dashboard__hero-tagline">
            <span className="public-dashboard__hero-underline" aria-hidden />
            <span>
              KEYSTROKE-HACKER · CATALYST-∞
              {/* オペレーター = "operator". */}
              <span className="arasaka-katakana" aria-hidden>オペレーター</span>
            </span>
          </div>

          {/* gaka-174.1: labeled dossier field rail — the element that turns
           * the header into a service record instead of a page title. */}
          <dl className="public-dashboard__dossier-fields">
            <DossierField label="Subject" value={data.username} />
            <DossierField label="Designation" value="Keystroke-Hacker" />
            <DossierField
              label="Service Period"
              value={fmtRange(data.startDate, data.endDate)}
            />
            <DossierField label="Status" value="Active" accent />
          </dl>
        </header>

        <div className="public-dashboard__grid">
          <DraggableGridLayout
            instances={instances}
            storage={storage}
            editable={false}
            cols={12}
            seedLayout={seed}
          />
        </div>

        <footer className="public-dashboard__footer">
          &gt; END OF TRANSMISSION
          {/* 通信終了 = "end of transmission". */}
          <span className="arasaka-katakana" aria-hidden>通信終了</span>
          {" · boomtime · "}{version}{" "}
          <span className="public-dashboard__footer-cursor" aria-hidden>▎</span>
        </footer>
      </div>

      {/* gaka-174.2: floating dossier controls + the reclassify sweep that
       * plays when the theme (dossier skin) changes. Available to any viewer;
       * owner-canonical persistence is the follow-up half of gaka-174.2. */}
      <DossierControls />
      <ReclassifyOverlay />
    </div>
  );
}

// gaka-174.1: one labeled cell in the dossier field rail. Rendered as a
// <div> inside a <dl> is fine for our purposes — this is decorative chrome,
// not a semantic definition list consumers depend on.
function DossierField({
  label,
  value,
  accent = false,
}: {
  label: string;
  value: string;
  accent?: boolean;
}) {
  return (
    <div className="dossier-field">
      <dt className="dossier-field__label">{label}</dt>
      <dd
        className={
          "dossier-field__value" +
          (accent ? " dossier-field__value--accent" : "")
        }
      >
        {value}
      </dd>
    </div>
  );
}

function PublicShell({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex min-h-screen flex-col bg-background text-foreground">
      <main className="flex-1">{children}</main>
    </div>
  );
}

function fmtRange(startISO: string, endISO: string): string {
  const start = new Date(startISO);
  const end = new Date(endISO);
  const opts: Intl.DateTimeFormatOptions = { year: "numeric", month: "short", day: "numeric" };
  return `${start.toLocaleDateString(undefined, opts)} — ${end.toLocaleDateString(undefined, opts)}`.toUpperCase();
}

// gaka-k2p: dossier classification banner.
//
// Only styled under the Arasaka theme (see arasaka.css
// `.public-dashboard__classline`); every other theme keeps the element in
// the DOM but display:none — no branching in JS needed. The typing effect
// is CSS-only (width steps() + a resolving cursor keyframe), so no state.
function DossierClassLine({ username, slug }: { username: string; slug: string }) {
  // File # = first 5 hex chars of the slug's hash. Deterministic,
  // stable across renders, and doesn't require any network dependency.
  const fileId = shortHash(slug || username);
  const rev = new Date().toISOString().slice(0, 10).replace(/-/g, ".");
  const subject = username.toUpperCase();
  // gaka-174.1: the strip renders a RESTRAINED neutral line in clean mode
  // ("SERVICE RECORD · SUBJECT: … · FILE #… · REV …"). The arasaka-only spans
  // (hidden by dossier.css outside .theme-arasaka) restore the loud
  // CLEARANCE/CLASSIFIED/-ARASAKA-∞ embellishments — keeping the exact strings
  // the public-profile-dossier e2e asserts under the arasaka theme.
  return (
    <div className="public-dashboard__classline" aria-hidden>
      <span className="public-dashboard__classline-block dossier-arasaka-only">
        ▓
      </span>
      <span className="public-dashboard__classline-text">
        <span className="dossier-arasaka-only">CLEARANCE: PUBLIC · </span>
        SERVICE RECORD · SUBJECT: {subject} · FILE #{fileId}
        <span className="dossier-arasaka-only">-ARASAKA-∞</span>
        {" · "}
        <span className="public-dashboard__classline-stamp dossier-arasaka-only">
          CLASSIFIED: LVL-2
        </span>
        <span className="dossier-arasaka-only">{" · "}</span>
        REV {rev}
      </span>
      <span className="public-dashboard__classline-cursor dossier-arasaka-only" aria-hidden />
    </div>
  );
}

// Cheap deterministic 5-char id derived from a string. NOT a cryptographic
// hash — purely for the dossier's decorative "FILE #" field. Result is
// 5 lowercase hex chars (e.g. "b4c92"). Same input → same output across
// sessions, which lets a subject's profile URL feel like a stable dossier
// reference.
function shortHash(s: string): string {
  let h = 2166136261 >>> 0; // FNV-1a offset basis
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 16777619) >>> 0;
  }
  return h.toString(16).padStart(8, "0").slice(0, 5);
}
