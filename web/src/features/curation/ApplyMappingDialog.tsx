import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowRight, Loader2, TriangleAlert } from "lucide-react";
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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/table";
import { api, ApiError } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import type { CurationRule } from "@/types/api";

interface ApplyMappingDialogProps {
  /**
   * The remap rule to apply — null closes the dialog. Kept as the full rule
   * (not just an id) so the modal header can render the human-readable
   * "old → new" without waiting for the preview to load.
   */
  rule: CurationRule | null;
  onClose: () => void;
}

/**
 * gaka-cr4: Destructive-confirm modal for applying a rename mapping.
 *
 * A "rename" curation rule normally lives forever as a query-time translation
 * layer — dashboards read a CASE remap of the raw column, so raw heartbeats
 * keep their original values. This modal is the on-ramp to the DESTRUCTIVE
 * alternative: collapse the mapping into the raw data (rewrite matching rows,
 * remove the mapping row), so the mapping table stays lean and the raw data
 * is canonical.
 *
 * Contract (matches the spec):
 *   1. Exact SQL that will be executed is shown verbatim in a monospace <pre>
 *      block. The backend regression TestApplyRenamePreviewMatchesRun asserts
 *      preview.sqlPlanned === apply.sqlRun so the modal isn't lying.
 *   2. Affected-rows diff table (heartbeat_id | before → after), capped at
 *      100 rows shown in the modal; an "and N more…" footer surfaces the
 *      true total (the actual apply still touches all N rows).
 *   3. Destructive-styled confirm ("Apply mapping (irreversible)") and a
 *      cancel button.
 */
export function ApplyMappingDialog({ rule, onClose }: ApplyMappingDialogProps) {
  const qc = useQueryClient();
  const open = rule !== null;

  // Fetch the preview when the modal opens. `enabled: open` keeps us from
  // firing preview calls in the background for unrelated rules.
  const preview = useQuery({
    queryKey: qk.remappingApplyPreview(rule?.id ?? -1),
    queryFn: () => api.previewApplyRemapping(rule!.id),
    enabled: open,
    // The preview is a snapshot — keep it fresh per open (the raw data may
    // have changed since the last modal open for this rule).
    staleTime: 0,
    refetchOnWindowFocus: false,
    retry: false,
  });

  const apply = useMutation({
    mutationFn: () => api.applyRemapping(rule!.id),
    onSuccess: (res) => {
      toast.success(
        `Mapping applied — ${res.rowsAffected.toLocaleString()} row${res.rowsAffected === 1 ? "" : "s"} rewritten`,
      );
      // The apply mutated raw heartbeats AND removed a curation rule:
      // (1) every dashboard/aggregation is stale (raw column values changed)
      // (2) the curation rules list is stale (mapping removed)
      // (3) the axis-values query for the affected axis needs to refetch
      // Invalidating curationDependents covers all three (see queryKeys.ts).
      qc.invalidateQueries({ queryKey: qk.curation() });
      for (const key of qk.curationDependents) {
        qc.invalidateQueries({ queryKey: key });
      }
      qc.invalidateQueries({ queryKey: qk.prefix.curationAffected });
      qc.invalidateQueries({ queryKey: qk.prefix.remappingApplyPreview });
      onClose();
    },
    onError: (e) => {
      toast.error(
        e instanceof ApiError
          ? `Apply failed: ${e.message}`
          : "Apply failed",
      );
    },
  });

  const close = () => {
    if (apply.isPending) return; // don't dismiss mid-apply
    onClose();
  };

  const data = preview.data;
  const total = data?.totalAffected ?? 0;
  const shown = data?.affectedRows.length ?? 0;
  const overflow = total > shown ? total - shown : 0;
  const isNoop = data !== undefined && total === 0;

  return (
    <Dialog open={open} onOpenChange={(o) => !o && close()}>
      <DialogContent className="max-w-3xl">
        {rule !== null && (
          <>
            <DialogHeader>
              <DialogTitle className="font-mono text-base">
                {"> apply mapping — "}
                <span className="text-primary">{rule.matchValue}</span>
                <ArrowRight className="inline h-3.5 w-3.5 mx-1.5 text-muted-foreground" />
                <span className="text-primary">{rule.newValue}</span>
              </DialogTitle>
              <DialogDescription className="flex items-start gap-2 pt-2">
                <TriangleAlert className="h-4 w-4 shrink-0 text-destructive mt-0.5" />
                <span>
                  This DESTRUCTIVELY rewrites raw heartbeat rows and removes
                  the mapping. Unlike a query-time remap, this cannot be
                  undone without a database restore.
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
                {/* Planned SQL block — the actual UPDATE + DELETE that will run,
                    displayed with concrete literals inlined (see backend
                    InlineParams). Postgres-flavor SQL; no syntax highlighter
                    dep is worth pulling in for two statements. */}
                <div>
                  <p className="mb-1.5 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                    Planned SQL
                  </p>
                  <pre className="max-h-48 overflow-auto rounded border border-destructive/40 bg-secondary/60 p-3 font-mono text-[11px] leading-relaxed text-foreground">
                    {data.sqlPlanned}
                  </pre>
                </div>

                {/* Diff table: before / after for each affected heartbeat row.
                    Capped at 100 rows (backend cap); overflow footer says how
                    many more will still be rewritten on confirm. */}
                <div>
                  <p className="mb-1.5 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                    Affected rows
                    <span className="ml-2 font-normal normal-case tracking-normal text-muted-foreground">
                      {total.toLocaleString()} row{total === 1 ? "" : "s"} will
                      be rewritten
                      {overflow > 0 && ` (showing first ${shown})`}
                    </span>
                  </p>
                  {isNoop ? (
                    <p className="rounded border border-dashed border-muted p-3 text-xs text-muted-foreground">
                      This mapping is a no-op (0 rows match). Applying will
                      just remove the mapping row.
                    </p>
                  ) : (
                    <div className="max-h-64 overflow-auto rounded border">
                      <Table>
                        <TableHeader>
                          <TableRow>
                            <TableHead className="w-24">heartbeat_id</TableHead>
                            <TableHead>before</TableHead>
                            <TableHead className="w-8" />
                            <TableHead>after</TableHead>
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                          {data.affectedRows.map((r) => (
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
                          ))}
                        </TableBody>
                      </Table>
                      {overflow > 0 && (
                        <p className="border-t bg-secondary/40 px-3 py-1.5 text-center font-mono text-[11px] text-muted-foreground">
                          … and {overflow.toLocaleString()} more row
                          {overflow === 1 ? "" : "s"} will also be rewritten
                        </p>
                      )}
                    </div>
                  )}
                </div>
              </div>
            ) : null}

            <DialogFooter>
              <Button
                variant="outline"
                onClick={close}
                disabled={apply.isPending}
              >
                Cancel
              </Button>
              <Button
                variant="destructive"
                disabled={preview.isLoading || preview.isError || apply.isPending}
                onClick={() => apply.mutate()}
              >
                {apply.isPending
                  ? "Applying…"
                  : "Apply mapping (irreversible)"}
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
