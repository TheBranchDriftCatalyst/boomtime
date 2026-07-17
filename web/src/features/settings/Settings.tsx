import { useSearchParams } from "react-router";
import { PageToolbar } from "@/components/toolbar/PageToolbar";
import { cn } from "@/lib/utils";
import { CurationTab } from "@/features/curation/CurationTab";
import { RemappingsTab } from "@/features/curation/RemappingsTab";
import { WidgetLinksCard } from "@/features/widgets/WidgetLinksCard";
import { Changelog } from "@/features/changelog/Changelog";
import { Logs } from "@/features/logs/Logs";
import { PluginSetup } from "@/features/settings/PluginSetup";
import { TokensTab } from "@/features/tokens/TokensTab";

// Plugin Setup goes first — highest-value first-run info (how to actually
// stream heartbeats here). API tokens sits adjacent because Plugin Setup
// explains "how to send data" and Tokens explains "which credential to use".
const TABS = [
  { id: "plugin", label: "Plugin setup", render: () => <PluginSetup /> },
  { id: "tokens", label: "API tokens", render: () => <TokensTab /> },
  { id: "curation", label: "Hidden data", render: () => <CurationTab /> },
  { id: "remappings", label: "Remappings", render: () => <RemappingsTab /> },
  { id: "widgets", label: "Widgets", render: () => <WidgetLinksCard /> },
  { id: "changelog", label: "Changelog", render: () => <Changelog embedded /> },
  { id: "logs", label: "Logs", render: () => <Logs embedded /> },
] as const;

type TabID = (typeof TABS)[number]["id"];

// Settings: one page, horizontal top tab bar. The active tab lives in
// ?tab=<id> so tabs are linkable/bookmarkable (old /app/logs and
// /app/changelog routes redirect here). Default lands on Plugin Setup so a
// first-run user sees the ingest URL immediately.
export function Settings() {
  const [params, setParams] = useSearchParams();
  const raw = params.get("tab");
  const active: TabID = TABS.some((t) => t.id === raw)
    ? (raw as TabID)
    : "plugin";
  const tab = TABS.find((t) => t.id === active)!;

  return (
    <div>
      <PageToolbar title="Settings" />

      <div
        role="tablist"
        aria-label="Settings sections"
        className="mb-6 flex gap-1 border-b border-border"
      >
        {TABS.map((t) => (
          <button
            key={t.id}
            role="tab"
            aria-selected={t.id === active}
            onClick={() => setParams({ tab: t.id }, { replace: true })}
            className={cn(
              "-mb-px border-b-2 px-4 py-2 text-sm font-medium transition-colors",
              t.id === active
                ? "border-primary text-foreground"
                : "border-transparent text-muted-foreground hover:border-border hover:text-foreground",
            )}
          >
            {t.label}
          </button>
        ))}
      </div>

      <div role="tabpanel" className="max-w-4xl">
        {tab.render()}
      </div>
    </div>
  );
}
