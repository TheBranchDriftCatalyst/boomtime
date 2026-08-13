import {
  useLayoutEffect,
  useRef,
  useState,
  type ComponentType,
  type ReactNode,
} from "react";
import * as Dialog from "@radix-ui/react-dialog";
import {
  BookOpen,
  Github,
  Info,
  KeyRound,
  Library,
  Lock,
  ShieldCheck,
  Sparkles,
  Wrench,
  X,
} from "lucide-react";
import { cn } from "@/lib/utils";

// Settings › Connections detail drawer (CX enhancement).
//
// Each connection card in ConnectionsTab is wrapped in a <ConnectionCardShell>
// that (a) surfaces a discoverable ⓘ affordance and (b) makes the card body a
// click target that opens a right-side detail drawer explaining the provider —
// what it is, what boomtime uses it for, how to set it up, and a security note.
//
// The action controls already inside each card (Connect / Disconnect / Unlink /
// Sync / Backfill / paste-token inputs) keep working: the shell's body click
// bails out whenever the click originates on an interactive element, so we never
// have to reach into the individual card components to add stopPropagation.
//
// The drawer itself is a Radix Dialog rendered as a right-aligned sheet — we
// get focus-trap, Esc-to-close, overlay-click dismiss, focus restore, and the
// aria-modal wiring for free. Slide-in + overlay fade live in index.css
// (.boom-drawer / .boom-overlay) and respect prefers-reduced-motion.

export type Provider = "authentik" | "github" | "amazon" | "hardcover";

interface ConnectionDetail {
  /** Provider display name — the drawer title. */
  label: string;
  /** One-line role, shown under the title (the Dialog description). */
  subtitle: string;
  icon: ComponentType<{ className?: string }>;
  whatItIs: ReactNode;
  whatBoomtimeUses: ReactNode;
  /** Numbered "how to connect / get your token" steps. */
  steps: ReactNode[];
  security: ReactNode;
  /** Optional extra callout (e.g. Hardcover token rotation / dry-run note). */
  note?: ReactNode;
}

const HC = (
  <a
    href="https://hardcover.app/account/api"
    target="_blank"
    rel="noopener noreferrer"
    className="text-primary underline-offset-2 hover:underline"
  >
    hardcover.app/account/api
  </a>
);

export const CONNECTION_DETAILS: Record<Provider, ConnectionDetail> = {
  authentik: {
    label: "Authentik",
    subtitle: "Linked sign-in identity",
    icon: ShieldCheck,
    whatItIs: "Your single-sign-on identity — the account you use to sign in through Authentik (OIDC).",
    whatBoomtimeUses:
      "Linking lets you sign in to boomtime through Authentik. Do it before an admin switches this server to OIDC-only login, so you never get locked out.",
    steps: [
      <>There is no token to copy. Click <strong>Link Authentik</strong> on the card.</>,
      <>Sign in through Authentik once when prompted.</>,
      <>You land back here and your account is now linked — that's it.</>,
    ],
    security:
      "We store only your OIDC subject id and email — never a password. Linking binds that identity to your current boomtime account.",
  },
  github: {
    label: "GitHub",
    subtitle: "Developer activity",
    icon: Github,
    whatItIs: "Your GitHub account, connected over GitHub's OAuth.",
    whatBoomtimeUses:
      "Surfaces your GitHub activity — commits and contribution stats — alongside your coding stats, so your dashboard tells the whole story.",
    steps: [
      <>Click <strong>Connect GitHub</strong> on the card.</>,
      <>Authorize boomtime on GitHub's OAuth screen.</>,
      <>You're redirected back here already connected — no manual token to copy or paste.</>,
    ],
    security:
      "We store only an encrypted OAuth access token — never your password. You can disconnect at any time from the card.",
  },
  amazon: {
    label: "Amazon",
    subtitle: "Kindle + Audible",
    icon: BookOpen,
    whatItIs: "A single Amazon device credential that covers both Kindle and Audible.",
    whatBoomtimeUses:
      "One link tracks BOTH your Kindle reading and your Audible listening — library, finish dates, and listening time — all from the same connection.",
    steps: [
      <>Click <strong>Connect Amazon</strong> and choose your marketplace.</>,
      <>Sign in on Amazon's page in the tab that opens.</>,
      <>
        Amazon then shows a "couldn't find that page" screen — that's expected, the URL still carries
        the code.
      </>,
      <>
        Paste the resulting <code className="rounded bg-muted px-1 py-0.5 text-xs">maplanding</code>{" "}
        URL back here (or use the one-click capture bookmarklet).
      </>,
    ],
    security:
      "We register a device and store only an encrypted credential (a device token + RSA key) — never your Amazon password.",
  },
  hardcover: {
    label: "Hardcover",
    subtitle: "Reading-state sync target",
    icon: Library,
    whatItIs: "Your Hardcover API bearer token — the key boomtime uses to write to your shelf.",
    whatBoomtimeUses:
      "Pushes your reading state (finished books, with dates) out to your Hardcover shelf, reconciling against books already there so it never creates duplicates.",
    steps: [
      <>
        Sign in at{" "}
        <a
          href="https://hardcover.app"
          target="_blank"
          rel="noopener noreferrer"
          className="text-primary underline-offset-2 hover:underline"
        >
          hardcover.app
        </a>
        .
      </>,
      <>Open your account API settings ({HC}).</>,
      <>Copy your GraphQL API bearer token.</>,
      <>Paste it into the <strong>Connect Hardcover</strong> field on the card.</>,
    ],
    security:
      "We store only an encrypted copy of the token — never your Hardcover password.",
    note: (
      <>
        Hardcover rotates tokens periodically (e.g. a Jan-1 reset), so if the status ever goes{" "}
        <strong>invalid</strong>, just re-paste a fresh one. Writes are currently in a safe{" "}
        <strong>dry-run</strong> mode while the sync is still being built out.
      </>
    ),
  },
};

// ── ConnectionCardShell ────────────────────────────────────────────────────
// Wraps an existing connection card without editing it. Adds the ⓘ affordance
// and a body click-to-open, while leaving every interactive control inside the
// card fully functional.

const INTERACTIVE_SELECTOR =
  'button, a, input, textarea, select, label, [role="button"], [contenteditable="true"]';

export function ConnectionCardShell({
  provider,
  onShowDetails,
  children,
}: {
  provider: Provider;
  onShowDetails: (p: Provider) => void;
  children: ReactNode;
}) {
  const innerRef = useRef<HTMLDivElement>(null);
  // Cards self-gate (render null when their feature flag is off / OIDC absent).
  // Detect whether the wrapped card actually rendered anything so the shell
  // doesn't leave a floating ⓘ over an empty box. Runs every render; setState
  // bails out when the value is unchanged, so no render loop.
  const [hasContent, setHasContent] = useState(true);
  // Intentionally no dep array: re-measure after every commit, since the
  // wrapped card can flip to null asynchronously (feature flag / OIDC probe
  // resolving). setState bails out on an unchanged value, so this can't loop.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useLayoutEffect(() => {
    setHasContent((innerRef.current?.childElementCount ?? 0) > 0);
  });

  const detail = CONNECTION_DETAILS[provider];

  const onBodyClick = (e: React.MouseEvent<HTMLDivElement>) => {
    // Ignore clicks that land on (or inside) an interactive control — those are
    // the card's own actions and must behave normally.
    if ((e.target as HTMLElement).closest(INTERACTIVE_SELECTOR)) return;
    onShowDetails(provider);
  };

  return (
    <div className={cn("group relative", !hasContent && "hidden")}>
      <div
        onClick={onBodyClick}
        className="cursor-pointer rounded-xl outline-none transition-shadow duration-200 focus-within:ring-1 focus-within:ring-primary/30 group-hover:shadow-[0_0_0_1px_hsl(var(--primary)/0.25),0_0_28px_-6px_hsl(var(--primary)/0.35)]"
      >
        <div ref={innerRef}>{children}</div>
      </div>

      {hasContent && (
        <button
          type="button"
          onClick={() => onShowDetails(provider)}
          aria-label={`About the ${detail.label} connection`}
          title={`About the ${detail.label} connection`}
          className="absolute right-3 top-3 z-10 inline-flex items-center gap-1 rounded-full border border-primary/25 bg-primary/5 px-2 py-1 font-mono text-[10px] uppercase tracking-[0.15em] text-primary/80 opacity-70 outline-none transition-all hover:border-primary/50 hover:bg-primary/10 hover:text-primary hover:opacity-100 focus-visible:ring-2 focus-visible:ring-primary/50 group-hover:opacity-100"
        >
          <Info className="h-3.5 w-3.5" />
          <span className="hidden sm:inline">Details</span>
        </button>
      )}
    </div>
  );
}

// ── The drawer ─────────────────────────────────────────────────────────────

function SectionLabel({
  icon: Icon,
  children,
}: {
  icon: ComponentType<{ className?: string }>;
  children: ReactNode;
}) {
  return (
    <div className="flex items-center gap-1.5 font-mono text-[11px] uppercase tracking-[0.2em] text-primary/80">
      <Icon className="h-3.5 w-3.5" />
      {children}
    </div>
  );
}

export function ConnectionDetailsDrawer({
  provider,
  onClose,
}: {
  provider: Provider | null;
  onClose: () => void;
}) {
  const open = provider !== null;
  // Keep the last provider around during the close animation so content doesn't
  // blank out as the sheet slides away.
  const lastRef = useRef<Provider | null>(provider);
  if (provider) lastRef.current = provider;
  const active = provider ?? lastRef.current;
  const detail = active ? CONNECTION_DETAILS[active] : null;

  return (
    <Dialog.Root open={open} onOpenChange={(o) => !o && onClose()}>
      <Dialog.Portal>
        <Dialog.Overlay className="boom-overlay fixed inset-0 z-50 bg-background/70 backdrop-blur-sm" />
        <Dialog.Content
          className="boom-drawer fixed inset-y-0 right-0 z-50 flex w-full max-w-[460px] flex-col overflow-hidden border-l border-primary/25 bg-background shadow-[0_0_60px_-12px_hsl(var(--primary)/0.4)] outline-none"
          aria-describedby={detail ? "conn-drawer-desc" : undefined}
        >
          {detail && active && (
            <>
              {/* Header — synthwave hero chrome: neon bloom + faint grid. */}
              <div className="relative shrink-0 overflow-hidden border-b border-primary/15 bg-gradient-to-br from-primary/10 via-background to-background p-6">
                <div className="pointer-events-none absolute -right-16 -top-20 h-48 w-48 rounded-full bg-primary/20 blur-3xl" />
                <div
                  className="pointer-events-none absolute inset-0 opacity-[0.06]"
                  style={{
                    backgroundImage:
                      "linear-gradient(hsl(var(--primary)) 1px, transparent 1px), linear-gradient(90deg, hsl(var(--primary)) 1px, transparent 1px)",
                    backgroundSize: "24px 24px",
                  }}
                />
                <Dialog.Close
                  aria-label="Close"
                  className="absolute right-4 top-4 z-10 inline-flex h-8 w-8 items-center justify-center rounded-md border border-border/60 bg-background/60 text-muted-foreground outline-none transition-colors hover:border-primary/40 hover:text-foreground focus-visible:ring-2 focus-visible:ring-primary/50"
                >
                  <X className="h-4 w-4" />
                </Dialog.Close>
                <div className="relative">
                  <div className="flex items-center gap-1.5 font-mono text-[11px] uppercase tracking-[0.2em] text-primary/80">
                    <detail.icon className="h-3.5 w-3.5" />
                    Connection
                  </div>
                  <Dialog.Title className="mt-2 flex items-center gap-2.5 text-2xl font-semibold tracking-tight">
                    <span className="inline-flex h-10 w-10 items-center justify-center rounded-lg border border-primary/25 bg-primary/10">
                      <detail.icon className="h-5 w-5 text-primary" />
                    </span>
                    {detail.label}
                  </Dialog.Title>
                  <Dialog.Description
                    id="conn-drawer-desc"
                    className="mt-1.5 text-sm text-muted-foreground"
                  >
                    {detail.subtitle}
                  </Dialog.Description>
                </div>
              </div>

              {/* Body — scrollable sections. */}
              <div className="flex-1 space-y-6 overflow-y-auto p-6">
                <section className="space-y-2">
                  <SectionLabel icon={Sparkles}>What it is</SectionLabel>
                  <p className="text-sm leading-relaxed text-foreground/90">{detail.whatItIs}</p>
                </section>

                <section className="space-y-2">
                  <SectionLabel icon={Info}>What boomtime uses it for</SectionLabel>
                  <p className="text-sm leading-relaxed text-foreground/90">
                    {detail.whatBoomtimeUses}
                  </p>
                </section>

                <section className="space-y-3">
                  <SectionLabel icon={Wrench}>How to connect</SectionLabel>
                  <ol className="space-y-3">
                    {detail.steps.map((step, i) => (
                      <li key={i} className="flex gap-3">
                        <span
                          aria-hidden
                          className="mt-0.5 inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-full border border-primary/30 bg-primary/10 font-mono text-xs font-semibold text-primary"
                        >
                          {i + 1}
                        </span>
                        <span className="text-sm leading-relaxed text-foreground/90">{step}</span>
                      </li>
                    ))}
                  </ol>
                </section>

                <section className="space-y-2 rounded-lg border border-primary/15 bg-primary/[0.04] p-4">
                  <SectionLabel icon={Lock}>Security</SectionLabel>
                  <p className="text-sm leading-relaxed text-foreground/90">{detail.security}</p>
                </section>

                {detail.note && (
                  <section className="space-y-2 rounded-lg border border-amber-500/30 bg-amber-500/5 p-4">
                    <div className="flex items-center gap-1.5 font-mono text-[11px] uppercase tracking-[0.2em] text-amber-500/90">
                      <KeyRound className="h-3.5 w-3.5" />
                      Good to know
                    </div>
                    <p className="text-sm leading-relaxed text-foreground/90">{detail.note}</p>
                  </section>
                )}
              </div>

              <div className="shrink-0 border-t border-border/60 px-6 py-4">
                <Dialog.Close className="inline-flex items-center gap-1.5 text-xs font-medium text-muted-foreground outline-none transition-colors hover:text-foreground focus-visible:ring-2 focus-visible:ring-primary/50">
                  <X className="h-3.5 w-3.5" />
                  Close
                </Dialog.Close>
              </div>
            </>
          )}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
