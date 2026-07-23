import { useMemo, useState } from "react";
import {
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { toast } from "sonner";
import { EraserIcon } from "lucide-react";
import { api, ApiError } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import { Spinner } from "@/components/Spinner";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { Card, CardContent } from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { Input } from "@thebranchdriftcatalyst/catalyst-ui/ui/input";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/table";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/dialog";
import type { EntitySummary, EntityType } from "@/types/api";

// The four entity-type buckets stored in heartbeats.ty. Order chosen so
// file+app (the noisiest desktop plugins) sit before the browser-plugin
// buckets (domain/url) where "caught website" scrubs typically happen.
const TYPES: { value: EntityType; label: string }[] = [
  { value: "file", label: "Files" },
  { value: "app", label: "Apps" },
  { value: "domain", label: "Domains" },
  { value: "url", label: "URLs" },
];

function relative(iso: string): string {
  const then = new Date(iso).getTime();
  if (!Number.isFinite(then)) return "";
  const s = Math.max(0, (Date.now() - then) / 1000);
  if (s < 60) return `${Math.round(s)}s ago`;
  if (s < 3600) return `${Math.round(s / 60)}m ago`;
  if (s < 86400) return `${Math.round(s / 3600)}h ago`;
  return `${Math.round(s / 86400)}d ago`;
}

/**
 * Entity Explorer (gaka-90x): pick an entity ty (file/app/domain/url), see
 * the flat list of every distinct non-empty entity the owner has under that
 * type with heartbeat counts + first/last seen, then REDACT individual or
 * bulk-selected entities. Redact blanks the entity column on matching rows
 * — the heartbeats stay, contributing to project / language / machine
 * totals; only the specific entity value is scrubbed from audit views.
 *
 * Because entity isn't a rollup axis, redact does NOT drift the rollup
 * (unlike a row-delete would). No Resync prompt after redact.
 */
export function EntityExplorer() {
  const qc = useQueryClient();
  const [ty, setTy] = useState<EntityType>("file");
  const [filter, setFilter] = useState("");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [pending, setPending] = useState<string[] | null>(null);

  const { data, isLoading, isError, refetch } = useQuery({
    queryKey: qk.entitiesByType(ty),
    queryFn: () => api.listEntitiesByType(ty, 500),
    staleTime: 30_000,
  });

  const redact = useMutation({
    mutationFn: (entities: string[]) => api.redactEntities(ty, entities),
    onSuccess: (res, entities) => {
      toast.success(
        `Redacted ${res.redacted.toLocaleString()} heartbeats across ${entities.length} ${entities.length === 1 ? "entity" : "entities"}`,
      );
      setSelected(new Set());
      setPending(null);
      // Refresh entity list + audit-side aggregations grouped by entity.
      // Stats/timeline/rollup are unaffected (entity isn't a rollup axis),
      // and derived-status doesn't drift, so those keys stay untouched.
      qc.invalidateQueries({ queryKey: qk.prefix.entitiesByType });
      qc.invalidateQueries({ queryKey: qk.prefix.hbExploreGroup });
      qc.invalidateQueries({ queryKey: qk.prefix.hbExploreList });
    },
    onError: (e) =>
      toast.error(
        e instanceof ApiError
          ? `Redact failed: ${e.message}`
          : "Redact failed",
      ),
  });

  const rows = useMemo<EntitySummary[]>(() => {
    if (!data) return [];
    const needle = filter.trim().toLowerCase();
    if (!needle) return data.entities;
    return data.entities.filter((e) =>
      e.entity.toLowerCase().includes(needle),
    );
  }, [data, filter]);

  const allChecked = rows.length > 0 && rows.every((r) => selected.has(r.entity));
  const anyChecked = selected.size > 0;

  function toggleOne(entity: string) {
    setSelected((s) => {
      const next = new Set(s);
      if (next.has(entity)) next.delete(entity);
      else next.add(entity);
      return next;
    });
  }
  function toggleAll() {
    if (allChecked) {
      setSelected(new Set());
    } else {
      setSelected(new Set(rows.map((r) => r.entity)));
    }
  }

  function armRedact(entities: string[]) {
    if (entities.length === 0) return;
    setPending(entities);
  }
  function confirmRedact() {
    if (!pending) return;
    redact.mutate(pending);
  }

  const selectedCount = rows.filter((r) => selected.has(r.entity)).length;
  const selectedTotal = rows
    .filter((r) => selected.has(r.entity))
    .reduce((s, r) => s + r.count, 0);

  return (
    <div className="space-y-4">
      <Card>
        <CardContent className="flex flex-wrap items-center gap-2 py-3">
          <div className="flex items-center rounded-md border p-0.5">
            {TYPES.map((t) => (
              <Button
                key={t.value}
                variant={ty === t.value ? "secondary" : "ghost"}
                size="sm"
                className="h-7"
                onClick={() => {
                  setTy(t.value);
                  setSelected(new Set());
                  setFilter("");
                }}
              >
                {t.label}
              </Button>
            ))}
          </div>
          <Input
            className="h-8 max-w-xs"
            placeholder={`Filter ${ty} entities…`}
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
          <div className="ml-auto flex items-center gap-3 text-xs text-muted-foreground">
            {isLoading ? (
              <span>Loading…</span>
            ) : data ? (
              <span>
                {rows.length.toLocaleString()} shown
                {data.truncated && " (server-capped at 500)"}
              </span>
            ) : null}
            {anyChecked && (
              <Button
                variant="destructive"
                size="sm"
                className="h-7"
                onClick={() =>
                  armRedact(rows.filter((r) => selected.has(r.entity)).map((r) => r.entity))
                }
              >
                <EraserIcon className="mr-1 h-3.5 w-3.5" />
                Redact {selectedCount} ({selectedTotal.toLocaleString()} rows)
              </Button>
            )}
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardContent className="py-3">
          {isLoading ? (
            <Spinner />
          ) : isError ? (
            <div className="space-y-2 py-6 text-center">
              <p className="text-sm text-destructive">
                Failed to load entities.
              </p>
              <Button variant="outline" size="sm" onClick={() => void refetch()}>
                Retry
              </Button>
            </div>
          ) : rows.length === 0 ? (
            <p className="py-6 text-center text-sm text-muted-foreground">
              {data && data.entities.length > 0
                ? "No entities match this filter."
                : `No ${ty} entities in your heartbeats yet.`}
            </p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-8">
                    <input
                      type="checkbox"
                      aria-label="Select all"
                      checked={allChecked}
                      onChange={toggleAll}
                    />
                  </TableHead>
                  <TableHead>Entity</TableHead>
                  <TableHead className="w-24 text-right">Count</TableHead>
                  <TableHead className="w-40">Last seen</TableHead>
                  <TableHead className="w-10" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((r) => (
                  <TableRow
                    key={r.entity}
                    className={selected.has(r.entity) ? "bg-muted/40" : ""}
                  >
                    <TableCell>
                      <input
                        type="checkbox"
                        aria-label={`Select ${r.entity}`}
                        checked={selected.has(r.entity)}
                        onChange={() => toggleOne(r.entity)}
                      />
                    </TableCell>
                    <TableCell
                      className="max-w-0 truncate font-mono text-xs"
                      title={r.entity}
                    >
                      {r.entity}
                    </TableCell>
                    <TableCell className="text-right tabular-nums">
                      {r.count.toLocaleString()}
                    </TableCell>
                    <TableCell
                      className="text-xs text-muted-foreground"
                      title={r.lastSeen}
                    >
                      {relative(r.lastSeen)}
                    </TableCell>
                    <TableCell>
                      <Button
                        variant="ghost"
                        size="icon"
                        className="h-7 w-7"
                        title="Redact this entity (scrubs the value; keeps the heartbeats)"
                        onClick={() => armRedact([r.entity])}
                      >
                        <EraserIcon className="h-3.5 w-3.5" />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <Dialog
        open={pending !== null}
        onOpenChange={(o) => !o && !redact.isPending && setPending(null)}
      >
        <DialogContent>
          {pending && (
            <>
              <DialogHeader>
                <DialogTitle>
                  Redact {pending.length}{" "}
                  {pending.length === 1 ? "entity" : "entities"}?
                </DialogTitle>
                <DialogDescription>
                  This blanks the <code className="font-mono">entity</code>{" "}
                  value on every matching heartbeat — the rows themselves
                  stay, so per-project/language/machine totals are
                  unchanged. The specific value simply disappears from the
                  audit views (Entity Explorer, Heartbeats Explorer
                  group-by=entity, top-files). This cannot be undone
                  without a backup restore.
                </DialogDescription>
              </DialogHeader>

              <div className="max-h-40 overflow-auto rounded border border-border/60 bg-muted/20 p-2 font-mono text-xs">
                {pending.slice(0, 20).map((e) => (
                  <div key={e} className="truncate" title={e}>
                    {e}
                  </div>
                ))}
                {pending.length > 20 && (
                  <div className="mt-1 text-muted-foreground">
                    …and {pending.length - 20} more
                  </div>
                )}
              </div>

              <DialogFooter>
                <Button
                  variant="outline"
                  onClick={() => setPending(null)}
                  disabled={redact.isPending}
                >
                  Cancel
                </Button>
                <Button
                  variant="destructive"
                  onClick={confirmRedact}
                  disabled={redact.isPending}
                >
                  {redact.isPending ? "Redacting…" : "Redact permanently"}
                </Button>
              </DialogFooter>
            </>
          )}
        </DialogContent>
      </Dialog>
    </div>
  );
}
