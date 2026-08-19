// CatalystBooks domain — the Connections settings tab body. Split out from
// register.tsx so registration can lazy-import it (keeps the provider cards out
// of the app-entry chunk and keeps registration side-effect free for tests).
import { useState, type ComponentType } from "react";
import { BookOpen, Github, Library, Plug, ShieldCheck } from "lucide-react";

import { GithubConnectCard } from "@shared/features/settings/GithubConnectCard";
import { AmazonConnectCard } from "@shared/features/settings/AmazonConnectCard";
import { HardcoverConnectCard } from "@shared/features/settings/HardcoverConnectCard";
import { LinkedIdentitiesCard } from "@shared/features/settings/LinkedIdentitiesCard";
import {
  ConnectionCardShell,
  ConnectionDetailsDrawer,
  type Provider,
} from "@shared/features/settings/ConnectionDetailsDrawer";

// The hero chips double as a shortcut into each provider's detail drawer, so
// they're real focusable buttons; onSelect lifts the click up to ConnectionsTab.
function ProviderChip({
  icon: Icon,
  label,
  provider,
  onSelect,
}: {
  icon: ComponentType<{ className?: string }>;
  label: string;
  provider: Provider;
  onSelect: (p: Provider) => void;
}) {
  return (
    <button
      type="button"
      onClick={() => onSelect(provider)}
      className="inline-flex items-center gap-1.5 rounded-full border border-primary/25 bg-primary/5 px-2.5 py-1 text-xs text-foreground/80 outline-none transition-colors hover:border-primary/50 hover:bg-primary/10 hover:text-foreground focus-visible:ring-2 focus-visible:ring-primary/50"
    >
      <Icon className="h-3.5 w-3.5 text-primary" />
      {label}
    </button>
  );
}

// ConnectionsTab: every external-account link in one place. Each card self-gates
// (renders nothing when its feature is off), so the tab stays tidy per
// deployment. Moved verbatim out of Settings.tsx into the books domain — the
// user's reorg groups connectors under CatalystBooks.
export function ConnectionsTab() {
  const [openProvider, setOpenProvider] = useState<Provider | null>(null);

  return (
    <div className="space-y-6">
      <div className="relative overflow-hidden rounded-xl border border-primary/20 bg-gradient-to-br from-primary/10 via-background to-background p-6">
        {/* neon bloom + faint grid — synthwave chrome, purely decorative */}
        <div className="pointer-events-none absolute -right-20 -top-24 h-56 w-56 rounded-full bg-primary/20 blur-3xl" />
        <div
          className="pointer-events-none absolute inset-0 opacity-[0.06]"
          style={{
            backgroundImage:
              "linear-gradient(hsl(var(--primary)) 1px, transparent 1px), linear-gradient(90deg, hsl(var(--primary)) 1px, transparent 1px)",
            backgroundSize: "28px 28px",
          }}
        />
        <div className="relative">
          <div className="flex items-center gap-1.5 font-mono text-[11px] uppercase tracking-[0.2em] text-primary/80">
            <Plug className="h-3.5 w-3.5" />
            Data fusion
          </div>
          <h2 className="mt-2 text-2xl font-semibold tracking-tight">Connections</h2>
          <p className="mt-1.5 max-w-xl text-sm text-muted-foreground">
            Link an external account and boomtime fuses its signal into your dashboard — your
            sign-in identity, your GitHub activity, and your Kindle&nbsp;+&nbsp;Audible reading, all
            in one place. Click any card for a walkthrough.
          </p>
          <div className="mt-4 flex flex-wrap gap-2">
            <ProviderChip icon={ShieldCheck} label="Authentik" provider="authentik" onSelect={setOpenProvider} />
            <ProviderChip icon={Github} label="GitHub" provider="github" onSelect={setOpenProvider} />
            <ProviderChip icon={BookOpen} label="Kindle + Audible" provider="amazon" onSelect={setOpenProvider} />
            <ProviderChip icon={Library} label="Hardcover" provider="hardcover" onSelect={setOpenProvider} />
          </div>
        </div>
      </div>

      {/* Each card is wrapped in a shell that adds the ⓘ affordance + body
          click-to-open, without touching the card component itself. Cards
          self-gate (render null when their flag is off); the shell hides
          itself in that case so no empty affordance is left behind. */}
      <div className="space-y-6">
        <ConnectionCardShell provider="authentik" onShowDetails={setOpenProvider}>
          <LinkedIdentitiesCard />
        </ConnectionCardShell>
        <ConnectionCardShell provider="github" onShowDetails={setOpenProvider}>
          <GithubConnectCard />
        </ConnectionCardShell>
        <ConnectionCardShell provider="amazon" onShowDetails={setOpenProvider}>
          <AmazonConnectCard />
        </ConnectionCardShell>
        <ConnectionCardShell provider="hardcover" onShowDetails={setOpenProvider}>
          <HardcoverConnectCard />
        </ConnectionCardShell>
      </div>

      <ConnectionDetailsDrawer
        provider={openProvider}
        onClose={() => setOpenProvider(null)}
      />
    </div>
  );
}
