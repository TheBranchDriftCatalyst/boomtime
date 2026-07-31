// PublicDashboard — /p/:slug read-only public dashboard (gaka-6jm.1 +
// gaka-keb).
//
// Composes the isolated grid primitive (@/lib/grid) with boomtime-specific
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
import { api, ApiError } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import {
  DraggableGridLayout,
  memoryAdapter,
  type WidgetInstance,
} from "@/lib/grid";
import { WIDGET_CATALOG } from "@/features/widgets/catalog";
import { WidgetRenderer } from "@/features/widgets/renderers/WidgetRenderer";
import { PUBLIC_PROFILE_DEFAULT_LAYOUT } from "./defaults";
import type { PublicDashboardPayload } from "@/types/stats";
import "./hacker.css";
// Arasaka overrides are scoped by .theme-arasaka .public-dashboard, so
// this import is a no-op for every other theme. See ./arasaka.css.
import "./arasaka.css";

export function PublicDashboard() {
  const { slug = "" } = useParams<{ slug: string }>();

  const { data, isLoading, error } = useQuery({
    queryKey: qk.publicDashboard(slug),
    queryFn: () => api.getPublicDashboard(slug),
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
          render: ({ view }) => <WidgetRenderer kind={e.kind} view={view} data={data} />,
        }),
      ),
    [data],
  );

  // In-memory storage: the public page is read-only. View toggles + drag
  // are all client-side state and evaporate on refresh. The owner uses
  // the inline ProfileEditor (rendered by EditableProfilePage when the
  // caller owns the profile) to persist changes; see gaka-ie3.
  const storage = useMemo(() => memoryAdapter(seed), [seed]);

  const version = window.location.host;

  return (
    <div className="public-dashboard">
      <div className="mx-auto max-w-7xl px-4">
        {/* gaka-k2p: dossier classification banner. Hidden by CSS under
         * every non-Arasaka theme so it doesn't leak into the boomtime
         * look. Placed OUTSIDE the hero so the border-top hairline reads
         * as a stripe across the top of the frame. */}
        <DossierClassLine username={data.username} slug={slug} />
        <header className="public-dashboard__hero">
          <div className="public-dashboard__hero-meta">
            &gt; PROFILE
            {/* Katakana signage: only visible under .theme-arasaka.
             * プロファイル = "profile". See ./arasaka.css .arasaka-katakana. */}
            <span className="arasaka-katakana" aria-hidden>プロファイル</span>
            {" · "}{data.username}@boomtime · {fmtRange(data.startDate, data.endDate)}
          </div>
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
  return (
    <div className="public-dashboard__classline" aria-hidden>
      <span className="public-dashboard__classline-block">▓</span>
      <span className="public-dashboard__classline-text">
        CLEARANCE: PUBLIC · SUBJECT: {subject} · FILE #{fileId}-ARASAKA-∞ ·{" "}
        <span className="public-dashboard__classline-stamp">
          CLASSIFIED: LVL-2
        </span>
        {" · REV "}
        {rev}
      </span>
      <span className="public-dashboard__classline-cursor" aria-hidden />
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
