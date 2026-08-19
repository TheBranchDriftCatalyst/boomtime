// AdminTabShell — the formal shared base every admin tab renders through
// (the "AdminABC"). It owns the three things every admin tab was hand-rolling:
//
//   1. HEADER  — an optional mono/uppercase title + description + right-aligned
//                actions slot, matching the existing admin visual language.
//   2. STATE   — one consistent loading spinner and one consistent error card
//                (pass `isLoading` / `error`), so tabs stop each inventing their
//                own "Loading…" / "Admin access required" copy.
//   3. GATING  — an optional RBAC/capability gate (`denied`): when true the tab
//                renders the standard access-required card instead of its body.
//                Route-level AdminRoute + server 403s remain the real boundary;
//                this is the per-tab UX seam a domain can opt into.
//
// DOMAIN-FREE: pure presentation + catalyst-ui primitives, no feature imports,
// so it travels to `web/shared/` with the rest of the shell.
import type { ReactNode } from "react";
import { Spinner } from "@thebranchdriftcatalyst/catalyst-ui/ui/spinner";
import { cn } from "@shared/lib/utils";

export interface AdminTabShellProps {
  /** mono/uppercase section title. */
  title?: ReactNode;
  /** supporting copy under the title. */
  description?: ReactNode;
  /** right-aligned header controls (filters, refresh, download…). */
  actions?: ReactNode;
  /** show the standard centered spinner in place of the body. */
  isLoading?: boolean;
  /** render the standard admin-error card in place of the body. Any truthy
   *  value works; an Error's message is surfaced. */
  error?: unknown;
  /** render the access-required card in place of the body (RBAC gate). */
  denied?: boolean;
  /** heading for the error/denied card. */
  errorTitle?: string;
  /** extra classes on the outer wrapper. */
  className?: string;
  /** classes on the body region (below the header). */
  bodyClassName?: string;
  children?: ReactNode;
}

/** Standardized admin error / access-required card. Exported so a tab with its
 *  own bespoke layout can still render the same card without the full shell. */
export function AdminAccessCard({
  title = "Admin access required.",
  error,
}: {
  title?: string;
  error?: unknown;
}) {
  const message =
    error instanceof Error
      ? error.message
      : typeof error === "string"
        ? error
        : "You are not on the admin allowlist.";
  return (
    <div className="rounded-md border border-destructive/40 bg-destructive/10 p-4 text-sm">
      <p className="font-semibold">{title}</p>
      <p className="mt-2 text-muted-foreground">{message}</p>
    </div>
  );
}

export function AdminTabShell({
  title,
  description,
  actions,
  isLoading,
  error,
  denied,
  errorTitle,
  className,
  bodyClassName,
  children,
}: AdminTabShellProps) {
  const hasHeader = title != null || description != null || actions != null;

  let body: ReactNode;
  if (isLoading) {
    body = (
      <div className="flex h-[40vh] items-center justify-center">
        <Spinner />
      </div>
    );
  } else if (denied || error) {
    body = <AdminAccessCard title={errorTitle} error={error} />;
  } else {
    body = children;
  }

  return (
    <section className={cn("space-y-6", className)}>
      {hasHeader && (
        <header className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            {title != null && (
              <h2 className="font-mono text-sm font-semibold uppercase tracking-wider">
                {title}
              </h2>
            )}
            {description != null && (
              <p className="mt-1 max-w-2xl text-xs text-muted-foreground">
                {description}
              </p>
            )}
          </div>
          {actions != null && (
            <div className="flex shrink-0 items-center gap-2">{actions}</div>
          )}
        </header>
      )}
      <div className={bodyClassName}>{body}</div>
    </section>
  );
}
