import { useMemo, useState, useEffect } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowRight, Loader2, TriangleAlert, Trash2, Zap } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/dialog";
import { Input } from "@thebranchdriftcatalyst/catalyst-ui/ui/input";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/table";
import { api, ApiError } from "@shared/lib/api";
import { qk } from "@shared/lib/queryKeys";
import type { CurationRule } from "@shared/types/api";

/**
 * gaka-cr4 + gaka-due: shared destructive-confirm modal for BOTH curation
 * destructive actions:
 *   - variant="apply": rewrite raw heartbeat rows via a rename rule, then
 *     delete the rule. UPDATE + DELETE, one transaction.
 *   - variant="purge": DELETE raw heartbeat rows that a hide rule matches,
 *     then delete the rule. DELETE + DELETE, one transaction.
 *
 * Both variants share the modal chrome, the preview fetch, the SQL <pre>
 * block, the affected-rows table, and the mutation + query-invalidation
 * flow — the ONLY per-variant differences are:
 *   1. Header title + destructive tint (Zap for apply, Trash2 for purge)
 *   2. Diff table columns (before → after for apply, "will be deleted"
 *      column for purge)
 *   3. Confirm button label
 *   4. Success toast copy
 *   5. Purge REQUIRES typing the rule id to confirm — apply does not.
 *      Rationale: rewriting labels is reversible-ish (create an inverse
 *      rule), deleting rows is not.
 *
 * ONE preview endpoint (/curation/:id/preview) serves both variants — it
 * dispatches on rule.action server-side. The FE reads `preview.action` to
 * decide which sub-tree to render.
 */
export interface DestructiveActionDialogProps {
  /** Full rule — null closes the dialog. */
  rule: CurationRule | null;
  onClose: () => void;
  /**
   * Which destructive verb to run on confirm. Must match rule.action
   * (apply→rename, purge→hide) or the backend will reject with 400.
   */
  variant: "apply" | "purge";
}

// ---------- per-variant text + mutation config ------------------------------

// Copy + iconography per variant, kept in one place so a copy tweak or a
// new variant is a table edit, not a scattered grep.
const VARIANT_CONFIG = {
  apply: {
    Icon: Zap,
    titleVerb: "apply rename",
    // Rewriting labels doesn't destroy data; skip the typing gate.
    requiresTypingGate: false,
    confirmLabel: "Apply mapping (irreversible)",
    confirmLabelPending: "Applying…",
    successToast: (rows: number) =>
      `Mapping applied — ${rows.toLocaleString()} row${rows === 1 ? "" : "s"} rewritten`,
    noopMessage:
      "This mapping is a no-op (0 rows match). Applying will just remove the mapping row.",
  },
  purge: {
    Icon: Trash2,
    titleVerb: "purge hidden",
    // Deleting raw rows is unrecoverable — force the user to type the id.
    requiresTypingGate: true,
    confirmLabel: "Delete rows forever (irreversible)",
    confirmLabelPending: "Deleting…",
    successToast: (rows: number) =>
      `${rows.toLocaleString()} row${rows === 1 ? "" : "s"} deleted forever`,
    noopMessage:
      "This hide rule matches 0 rows. Purging will just remove the rule row.",
  },
} as const;

export function DestructiveActionDialog({
  rule,
  onClose,
  variant,
}: DestructiveActionDialogProps) {
  const qc = useQueryClient();
  const open = rule !== null;
  const cfg = VARIANT_CONFIG[variant];

  // Preview fetch — same endpoint for both variants, backend dispatches.
  const preview = useQuery({
    queryKey: qk.curationActionPreview(rule?.id ?? -1),
    queryFn: () => api.previewCurationAction(rule!.id),
    enabled: open,
    staleTime: 0,
    refetchOnWindowFocus: false,
    retry: false,
  });

  // Typing gate for the purge variant: user must type the numeric rule id.
  // Reset whenever the modal reopens for a different rule.
  const [typedId, setTypedId] = useState("");
  useEffect(() => {
    if (rule) setTypedId("");
  }, [rule?.id]);
  const typingGatePassed =
    !cfg.requiresTypingGate || (rule !== null && typedId === String(rule.id));

  // Mutation dispatches on variant. Kept as one useMutation (not two) so the
  // render tree doesn't have to conditionalize on isPending across two mutations
  // — same lifecycle either way.
  const mutation = useMutation({
    mutationFn: async () => {
      if (variant === "apply") return api.applyCurationRule(rule!.id);
      return api.purgeCurationRule(rule!.id);
    },
    onSuccess: (res) => {
      toast.success(cfg.successToast(res.rowsAffected));
      // Same invalidation surface either way — both variants mutate raw
      // heartbeats AND remove a curation rule, so every dashboard/aggregation/
      // per-axis-values query is stale. curationDependents covers all of them.
      qc.invalidateQueries({ queryKey: qk.curation() });
      for (const key of qk.curationDependents) {
        qc.invalidateQueries({ queryKey: key });
      }
      qc.invalidateQueries({ queryKey: qk.prefix.curationAffected });
      qc.invalidateQueries({ queryKey: qk.prefix.curationActionPreview });
      onClose();
    },
    onError: (e) => {
      toast.error(
        e instanceof ApiError
          ? `${variant === "apply" ? "Apply" : "Purge"} failed: ${e.message}`
          : `${variant === "apply" ? "Apply" : "Purge"} failed`,
      );
    },
  });

  const close = () => {
    if (mutation.isPending) return; // don't dismiss mid-mutation
    onClose();
  };

  const data = preview.data;
  const total = data?.totalAffected ?? 0;
  const shown = data?.affectedRows.length ?? 0;
  const overflow = total > shown ? total - shown : 0;
  const isNoop = data !== undefined && total === 0;

  // Header target string — for apply we show "old → new", for purge we show
  // just the pattern (there's no target — it's being deleted).
  const headerTarget = useMemo(() => {
    if (!rule) return null;
    if (variant === "apply") {
      return (
        <>
          <span className="text-primary">{rule.matchValue}</span>
          <ArrowRight className="inline h-3.5 w-3.5 mx-1.5 text-muted-foreground" />
          <span className="text-primary">{rule.newValue}</span>
        </>
      );
    }
    return <span className="text-destructive">{rule.matchValue}</span>;
  }, [rule?.id, variant]);

  const { Icon } = cfg;

  return (
    <Dialog open={open} onOpenChange={(o) => !o && close()}>
      <DialogContent className="max-w-3xl">
        {rule !== null && (
          <>
            <DialogHeader>
              <DialogTitle className="font-mono text-base">
                <Icon
                  className={
                    variant === "purge"
                      ? "inline h-4 w-4 mr-2 text-destructive"
                      : "inline h-4 w-4 mr-2"
                  }
                />
                {"> "}
                {cfg.titleVerb} — {headerTarget}
              </DialogTitle>
              <DialogDescription className="flex items-start gap-2 pt-2">
                <TriangleAlert className="h-4 w-4 shrink-0 text-destructive mt-0.5" />
                <span>
                  {variant === "apply"
                    ? "This DESTRUCTIVELY rewrites raw heartbeat rows and removes the mapping. Unlike a query-time remap, this cannot be undone without a database restore."
                    : "This DESTRUCTIVELY deletes raw heartbeat rows — they cease to exist. Unlike a hide rule (which just filters at query time), a purge cannot be undone without a database restore."}
                </span>
              </DialogDescription>
            </DialogHeader>

            {preview.isLoading ? (
              <div className="flex items-center gap-2 py-6 text-sm text-muted-foreground">
                <Loader2 className="h-4 w-4 animate-spin" />
                Loading planned SQL and affected-row diff…
              </div>
            ) : preview.isError ? (
              <p className="py-4 text-sm text-destructive">
                Failed to load preview: {(preview.error as Error).message}
              </p>
            ) : data ? (
              <div className="space-y-4">
                {/* Planned SQL — verbatim what will run. Regression-guarded
                    by TestApplyRenamePreviewMatchesRun / TestPurgeHidden-
                    PreviewMatchesRun. */}
                <div>
                  <p className="mb-1.5 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                    Planned SQL
                  </p>
                  <pre className="max-h-48 overflow-auto rounded border border-destructive/40 bg-secondary/60 p-3 font-mono text-[11px] leading-relaxed text-foreground">
                    {data.sqlPlanned}
                  </pre>
                </div>

                {/* Affected rows. Column layout differs per variant — apply
                    shows before→after, purge shows a single "deleted" column
                    with the raw values that will cease to exist. */}
                <div>
                  <p className="mb-1.5 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                    Affected rows
                    <span className="ml-2 font-normal normal-case tracking-normal text-muted-foreground">
                      {total.toLocaleString()} row{total === 1 ? "" : "s"} will
                      be{" "}
                      {variant === "apply" ? "rewritten" : "deleted forever"}
                      {overflow > 0 && ` (showing first ${shown})`}
                    </span>
                  </p>
                  {isNoop ? (
                    <p className="rounded border border-dashed border-muted p-3 text-xs text-muted-foreground">
                      {cfg.noopMessage}
                    </p>
                  ) : (
                    <div className="max-h-64 overflow-auto rounded border">
                      <Table>
                        <TableHeader>
                          <TableRow>
                            <TableHead className="w-24">
                              heartbeat_id
                            </TableHead>
                            {data.action === "rename" ? (
                              <>
                                <TableHead>before</TableHead>
                                <TableHead className="w-8" />
                                <TableHead>after</TableHead>
                              </>
                            ) : (
                              <TableHead>will be deleted</TableHead>
                            )}
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                          {data.action === "rename"
                            ? data.affectedRows.map((r) => (
                                <TableRow key={r.id}>
                                  <TableCell className="font-mono text-xs text-muted-foreground">
                                    {r.id}
                                  </TableCell>
                                  <TableCell className="font-mono text-xs">
                                    {r.before}
                                  </TableCell>
                                  <TableCell className="text-muted-foreground">
                                    <ArrowRight className="h-3 w-3" />
                                  </TableCell>
                                  <TableCell className="font-mono text-xs font-medium text-primary">
                                    {r.after}
                                  </TableCell>
                                </TableRow>
                              ))
                            : data.affectedRows.map((r) => (
                                <TableRow key={r.id}>
                                  <TableCell className="font-mono text-xs text-muted-foreground">
                                    {r.id}
                                  </TableCell>
                                  <TableCell className="font-mono text-xs text-destructive">
                                    {Object.entries(r.deleted)
                                      .map(([col, val]) => `${col} = ${val}`)
                                      .join(", ")}
                                  </TableCell>
                                </TableRow>
                              ))}
                        </TableBody>
                      </Table>
                      {overflow > 0 && (
                        <p className="border-t bg-secondary/40 px-3 py-1.5 text-center font-mono text-[11px] text-muted-foreground">
                          … and {overflow.toLocaleString()} more row
                          {overflow === 1 ? "" : "s"} will also be{" "}
                          {variant === "apply" ? "rewritten" : "deleted"}
                        </p>
                      )}
                    </div>
                  )}
                </div>

                {/* Purge-only typing gate — muscle-memory defense. Rendered
                    below the diff so the user has already seen exactly what
                    they're about to obliterate. */}
                {cfg.requiresTypingGate && (
                  <div className="space-y-1.5 rounded border border-destructive/40 bg-destructive/5 p-3">
                    <label
                      htmlFor="purge-confirm-id"
                      className="text-xs font-semibold uppercase tracking-wide text-destructive"
                    >
                      Type rule id{" "}
                      <span className="font-mono text-sm">{rule.id}</span> to
                      confirm
                    </label>
                    <Input
                      id="purge-confirm-id"
                      value={typedId}
                      onChange={(e) => setTypedId(e.target.value)}
                      placeholder={String(rule.id)}
                      disabled={mutation.isPending}
                      autoComplete="off"
                      inputMode="numeric"
                      aria-label={`Type rule id ${rule.id} to confirm purge`}
                    />
                  </div>
                )}
              </div>
            ) : null}

            <DialogFooter>
              <Button
                variant="outline"
                onClick={close}
                disabled={mutation.isPending}
              >
                Cancel
              </Button>
              <Button
                variant="destructive"
                disabled={
                  preview.isLoading ||
                  preview.isError ||
                  mutation.isPending ||
                  !typingGatePassed
                }
                onClick={() => mutation.mutate()}
              >
                {mutation.isPending ? cfg.confirmLabelPending : cfg.confirmLabel}
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
