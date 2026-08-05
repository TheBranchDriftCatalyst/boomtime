import { useQuery } from "@tanstack/react-query";
import { Spinner } from "@thebranchdriftcatalyst/catalyst-ui/ui/spinner";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import { cn } from "@/lib/utils";

// Admin caps dashboard (gaka-93f.6): who's on which tier, what each tier
// grants, and every user's effective capabilities. Read-only v1 — set-role /
// disable stay in the `boomtime user` CLI. Admin-gated server-side (403 for
// non-admins); the tab is also hidden from the sidebar for non-admins.

function prettyCap(c: string): string {
  const s = c.replace(/_/g, " ");
  return s.charAt(0).toUpperCase() + s.slice(1);
}

// Short column header from a capability key (first letters of each word),
// with the full name on hover.
function capAbbrev(c: string): string {
  return c
    .split("_")
    .map((w) => w[0]?.toUpperCase() ?? "")
    .join("");
}

const ROLE_STYLES: Record<string, string> = {
  admin: "border-primary/40 bg-primary/15 text-primary",
  full: "border-emerald-500/40 bg-emerald-500/15 text-emerald-400",
  service: "border-sky-500/40 bg-sky-500/15 text-sky-400",
  light: "border-border bg-muted text-muted-foreground",
};

function RoleBadge({ role }: { role: string }) {
  return (
    <span
      className={cn(
        "inline-block rounded border px-2 py-0.5 text-[11px] font-semibold uppercase tracking-wider",
        ROLE_STYLES[role] ?? ROLE_STYLES.light,
      )}
    >
      {role}
    </span>
  );
}

function Cell({ on }: { on: boolean }) {
  return on ? (
    <span className="text-emerald-400" aria-label="granted">
      ✓
    </span>
  ) : (
    <span className="text-muted-foreground/25" aria-label="denied">
      ·
    </span>
  );
}

export function UsersTab() {
  const { data, isLoading, error } = useQuery({
    queryKey: qk.adminUsers(),
    queryFn: () => api.getAdminUsers(),
    staleTime: 30_000,
  });

  if (isLoading) {
    return (
      <div className="flex h-[40vh] items-center justify-center">
        <Spinner />
      </div>
    );
  }
  if (error || !data) {
    return <p className="text-sm text-destructive">Failed to load users.</p>;
  }

  const caps = data.capabilities;
  const roleOrder = ["admin", "full", "service", "light"].filter(
    (r) => r in data.roles,
  );
  // Any roles the server has that aren't in our preferred order, appended.
  const extraRoles = Object.keys(data.roles).filter((r) => !roleOrder.includes(r));
  const roles = [...roleOrder, ...extraRoles];

  return (
    <div className="max-w-6xl space-y-10">
      {/* ── Tier legend ─────────────────────────────────────────────── */}
      <section>
        <h2 className="mb-1 font-mono text-xs font-semibold uppercase tracking-widest text-muted-foreground">
          Tiers &amp; capabilities
        </h2>
        <p className="mb-3 text-sm text-muted-foreground">
          What each role grants by default. Per-user overrides can flip individual
          grants; a disabled user is denied everything.
        </p>
        <div className="overflow-x-auto rounded-lg border border-border">
          <table className="w-full border-collapse text-sm">
            <thead>
              <tr className="border-b border-border bg-muted/30">
                <th className="px-3 py-2 text-left font-medium">Tier</th>
                {caps.map((c) => (
                  <th
                    key={c}
                    title={prettyCap(c)}
                    className="px-2 py-2 text-center font-mono text-[11px] font-medium text-muted-foreground"
                  >
                    {capAbbrev(c)}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {roles.map((role) => (
                <tr key={role} className="border-b border-border/60 last:border-0">
                  <td className="px-3 py-2">
                    <RoleBadge role={role} />
                  </td>
                  {caps.map((c) => (
                    <td key={c} className="px-2 py-2 text-center">
                      <Cell on={!!data.roles[role]?.[c]} />
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <p className="mt-2 text-[11px] text-muted-foreground/70">
          {caps.map((c) => `${capAbbrev(c)} = ${prettyCap(c)}`).join("  ·  ")}
        </p>
      </section>

      {/* ── Users ───────────────────────────────────────────────────── */}
      <section>
        <h2 className="mb-3 font-mono text-xs font-semibold uppercase tracking-widest text-muted-foreground">
          Users ({data.users.length})
        </h2>
        <div className="overflow-x-auto rounded-lg border border-border">
          <table className="w-full border-collapse text-sm">
            <thead>
              <tr className="border-b border-border bg-muted/30">
                <th className="px-3 py-2 text-left font-medium">User</th>
                <th className="px-3 py-2 text-left font-medium">Tier</th>
                <th className="px-3 py-2 text-left font-medium">Status</th>
                {caps.map((c) => (
                  <th
                    key={c}
                    title={prettyCap(c)}
                    className="px-2 py-2 text-center font-mono text-[11px] font-medium text-muted-foreground"
                  >
                    {capAbbrev(c)}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {data.users.length === 0 && (
                <tr>
                  <td colSpan={3 + caps.length} className="px-3 py-6 text-center text-muted-foreground">
                    No users.
                  </td>
                </tr>
              )}
              {data.users.map((u) => (
                <tr
                  key={u.username}
                  className={cn(
                    "border-b border-border/60 last:border-0",
                    u.disabled && "opacity-50",
                  )}
                >
                  <td className="px-3 py-2 font-medium">{u.username}</td>
                  <td className="px-3 py-2">
                    <RoleBadge role={u.role} />
                  </td>
                  <td className="px-3 py-2">
                    {u.disabled ? (
                      <span className="text-xs font-medium text-destructive">disabled</span>
                    ) : (
                      <span className="text-xs text-emerald-400">active</span>
                    )}
                  </td>
                  {caps.map((c) => (
                    <td key={c} className="px-2 py-2 text-center">
                      <Cell on={!!u.capabilities[c]} />
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <p className="mt-3 text-[11px] text-muted-foreground/70">
          Read-only. Change a tier with{" "}
          <code className="rounded bg-muted/60 px-1">boomtime user set-role &lt;user&gt; &lt;tier&gt;</code>{" "}
          or disable with{" "}
          <code className="rounded bg-muted/60 px-1">boomtime user disable &lt;user&gt;</code>.
        </p>
      </section>
    </div>
  );
}
