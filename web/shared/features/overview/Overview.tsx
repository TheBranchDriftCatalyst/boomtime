import { useMemo } from "react";
import { useSearchParams } from "react-router";
import { Activity, BookOpen, Code2 } from "lucide-react";
import { Page } from "@shared/layout/Page";
import { TabNav, tabClass } from "@shared/layout/PageTabs";
import { useHeaderSlot } from "@shared/layout/HeaderSlot";
import { usePublicConfig } from "@shared/lib/usePublicConfig";
import { OverviewDashboard } from "@shared/features/overview/OverviewDashboard";
import { ReadingDashboard } from "@shared/features/overview/reading/ReadingDashboard";

// The Overview is now a small tab host: Coding | Reading | Health. Each tab
// renders a full <Page> (so the Coding tab is the EXISTING OverviewDashboard,
// verbatim); the tab strip is hoisted into the app HeaderBar via useHeaderSlot,
// exactly like Settings. The active tab lives in ?view= so it's linkable.
type ViewID = "coding" | "reading" | "health";

interface ViewTab {
  id: ViewID;
  label: string;
  icon: typeof Code2;
}

/** Coming-soon placeholder for the not-yet-built Health dashboard. */
function HealthComingSoon() {
  return (
    <Page>
      <Page.Header
        title="Health"
        subtitle="Apple Watch & HealthKit signal, fused into your dashboard"
      />
      <Page.Body>
        <Page.Content>
          <div className="flex min-h-[60vh] items-center justify-center">
            <div className="relative w-full max-w-md overflow-hidden rounded-xl border border-primary/20 bg-gradient-to-br from-primary/10 via-background to-background p-8 text-center">
              {/* neon bloom + faint grid — the house synthwave chrome */}
              <div className="pointer-events-none absolute -right-16 -top-20 h-48 w-48 rounded-full bg-primary/20 blur-3xl" />
              <div
                className="pointer-events-none absolute inset-0 opacity-[0.06]"
                style={{
                  backgroundImage:
                    "linear-gradient(hsl(var(--primary)) 1px, transparent 1px), linear-gradient(90deg, hsl(var(--primary)) 1px, transparent 1px)",
                  backgroundSize: "28px 28px",
                }}
              />
              <div className="relative">
                <span className="mx-auto flex h-14 w-14 items-center justify-center rounded-full bg-primary/10 ring-1 ring-primary/20">
                  <Activity className="h-6 w-6 text-primary" />
                </span>
                <div className="mt-4 flex items-center justify-center gap-1.5 font-mono text-[11px] uppercase tracking-[0.2em] text-primary/80">
                  Health
                </div>
                <h2 className="mt-2 text-2xl font-semibold tracking-tight">Coming soon</h2>
                <p className="mt-1.5 text-sm text-muted-foreground">
                  Steps, heart rate, sleep and workouts from your paired Apple Watch
                  will land here — fused alongside your coding and reading.
                </p>
              </div>
            </div>
          </div>
        </Page.Content>
      </Page.Body>
    </Page>
  );
}

export function Overview() {
  const [params, setParams] = useSearchParams();
  const { config } = usePublicConfig();

  // Reading tab is only offered when the books feature is on (same gate as the
  // Books nav entry). Coding + Health are always present.
  const tabs = useMemo<ViewTab[]>(() => {
    const base: ViewTab[] = [{ id: "coding", label: "Coding", icon: Code2 }];
    if (config.books_enabled) {
      base.push({ id: "reading", label: "Reading", icon: BookOpen });
    }
    base.push({ id: "health", label: "Health", icon: Activity });
    return base;
  }, [config.books_enabled]);

  const raw = params.get("view") ?? "";
  const active: ViewID = tabs.some((t) => t.id === raw)
    ? (raw as ViewID)
    : "coding";

  const headerTabs = useMemo(
    () => (
      <TabNav ariaLabel="Overview sections" variant="header" label="Overview">
        {tabs.map((t) => {
          const Icon = t.icon;
          return (
            <button
              key={t.id}
              role="tab"
              aria-selected={t.id === active}
              onClick={() => setParams({ view: t.id }, { replace: true })}
              className={tabClass(t.id === active, "gap-1.5")}
            >
              <Icon className="h-3.5 w-3.5" />
              {t.label}
            </button>
          );
        })}
      </TabNav>
    ),
    [tabs, active, setParams],
  );
  useHeaderSlot(headerTabs);

  if (active === "reading") return <ReadingDashboard />;
  if (active === "health") return <HealthComingSoon />;
  // Coding — the existing global Overview dashboard, unchanged.
  return <OverviewDashboard />;
}
