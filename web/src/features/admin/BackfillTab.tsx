// BackfillTab — Settings > Admin > Backfill (gaka-vh8).
//
// Three-panel layout:
//   1. Header stats: total backfilled rows, source breakdown, [oldest,
//      newest] range.
//   2. Config panel: per-user tunables for the CLI (cluster gap, lead/
//      tail, HB rate, source tag, author allowlist, lang overrides).
//   3. Live job queue: WS-driven table of currently-in-flight (or
//      recently-done) repo scans.
//   4. Danger zone (accordion): delete backfilled heartbeats.
//
// The CLI runs on the operator's laptop and streams heartbeats over the
// admin API. This tab is the observation + config surface; there is no
// server-side "run" button (the server never touches an operator's
// filesystem).

import { useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";
import {
  RefreshCw,
  Loader2,
  CheckCircle2,
  AlertTriangle,
  Wifi,
  WifiOff,
  Trash2,
  Copy,
  Plus,
  X,
  Info,
} from "lucide-react";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { Input } from "@thebranchdriftcatalyst/catalyst-ui/ui/input";
import { Label as UILabel } from "@thebranchdriftcatalyst/catalyst-ui/ui/label";
import { Textarea } from "@thebranchdriftcatalyst/catalyst-ui/ui/textarea";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/tooltip";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import { cn } from "@/lib/utils";
import {
  useBackfillJobQueue,
  type BackfillJobState,
} from "./useBackfillJobQueue";

// ---- helpers ---------------------------------------------------------------

function fmtDateShort(iso?: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (isNaN(d.getTime())) return "—";
  return d.toISOString().slice(0, 10);
}

function fmtElapsed(iso?: string): string {
  if (!iso) return "—";
  const then = new Date(iso).getTime();
  if (!Number.isFinite(then)) return "—";
  const diff = Math.max(0, Date.now() - then);
  const s = Math.floor(diff / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s`;
  const h = Math.floor(m / 60);
  return `${h}h ${m % 60}m`;
}

function StatusPill({ status }: { status: BackfillJobState["status"] }) {
  switch (status) {
    case "queued":
      return (
        <span className="inline-flex items-center gap-1 rounded-sm border border-border bg-muted/40 px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-wider text-muted-foreground">
          queued
        </span>
      );
    case "running":
      return (
        <span className="inline-flex items-center gap-1 rounded-sm border border-primary/40 bg-primary/10 px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-wider text-primary">
          <Loader2 size={10} className="animate-spin" />
          running
        </span>
      );
    case "done":
      return (
        <span className="inline-flex items-center gap-1 rounded-sm border border-emerald-500/40 bg-emerald-500/10 px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-wider text-emerald-500">
          <CheckCircle2 size={10} />
          done
        </span>
      );
    case "error":
      return (
        <span className="inline-flex items-center gap-1 rounded-sm border border-destructive/40 bg-destructive/10 px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-wider text-destructive">
          <AlertTriangle size={10} />
          error
        </span>
      );
  }
}

// ---- config panel ----------------------------------------------------------

type CfgDraft = {
  clusterGapSec: number;
  preCommitLeadSec: number;
  postCommitTailSec: number;
  heartbeatRateSec: number;
  authorEmails: string[];
  sourceTag: string;
  langMap: Record<string, string>;
};

function secToMin(v: number): number {
  return Math.round(v / 60);
}

function ConfigPanel() {
  const queryClient = useQueryClient();
  const { data, isLoading } = useQuery({
    queryKey: qk.backfillConfig(),
    queryFn: () => api.getBackfillConfig(),
    staleTime: 30_000,
  });

  const [draft, setDraft] = useState<CfgDraft | null>(null);
  const [emailInput, setEmailInput] = useState("");
  const [langMapRaw, setLangMapRaw] = useState("{}");
  const [langMapErr, setLangMapErr] = useState<string | null>(null);

  useEffect(() => {
    if (data) {
      setDraft({
        clusterGapSec: data.clusterGapSec,
        preCommitLeadSec: data.preCommitLeadSec,
        postCommitTailSec: data.postCommitTailSec,
        heartbeatRateSec: data.heartbeatRateSec,
        authorEmails: [...data.authorEmails],
        sourceTag: data.sourceTag,
        langMap: { ...(data.langMap ?? {}) },
      });
      setLangMapRaw(JSON.stringify(data.langMap ?? {}, null, 2));
      setLangMapErr(null);
    }
  }, [data]);

  const save = useMutation({
    mutationFn: async (d: CfgDraft) => api.patchBackfillConfig(d),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: qk.backfillConfig() });
    },
  });

  if (isLoading || !draft) {
    return (
      <div className="border border-border p-4 font-mono text-xs uppercase tracking-wider text-muted-foreground">
        loading config…
      </div>
    );
  }

  const addEmail = () => {
    const raw = emailInput.trim().toLowerCase();
    if (!raw || draft.authorEmails.includes(raw)) return;
    setDraft({ ...draft, authorEmails: [...draft.authorEmails, raw] });
    setEmailInput("");
  };
  const removeEmail = (e: string) =>
    setDraft({ ...draft, authorEmails: draft.authorEmails.filter((x) => x !== e) });

  const commitLangMap = () => {
    try {
      const obj = JSON.parse(langMapRaw);
      if (
        obj === null ||
        typeof obj !== "object" ||
        Array.isArray(obj) ||
        Object.values(obj).some((v) => typeof v !== "string")
      ) {
        setLangMapErr("must be an object of {ext: language} strings");
        return;
      }
      setDraft({ ...draft, langMap: obj as Record<string, string> });
      setLangMapErr(null);
    } catch (e) {
      setLangMapErr(String((e as Error).message ?? e));
    }
  };

  const handleSave = () => {
    // Parse langMap synchronously so we don't rely on the async setDraft
    // in commitLangMap having landed by the time we mutate. A parse
    // failure blocks the save and surfaces the error inline.
    let langMap = draft.langMap;
    try {
      const obj = JSON.parse(langMapRaw);
      if (
        obj !== null &&
        typeof obj === "object" &&
        !Array.isArray(obj) &&
        Object.values(obj).every((v) => typeof v === "string")
      ) {
        langMap = obj as Record<string, string>;
        setLangMapErr(null);
      } else {
        setLangMapErr("must be an object of {ext: language} strings");
        return;
      }
    } catch (e) {
      setLangMapErr(String((e as Error).message ?? e));
      return;
    }
    save.mutate({ ...draft, langMap });
  };

  return (
    <div className="space-y-4 border border-border bg-card/40 p-4">
      <h2 className="font-mono text-xs uppercase tracking-widest text-muted-foreground">
        Backfill Configuration
      </h2>

      {/* Sliders row */}
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
        <SliderRow
          label={`Cluster gap (${secToMin(draft.clusterGapSec)} min)`}
          value={draft.clusterGapSec}
          min={300}
          max={7200}
          step={300}
          onChange={(v) => setDraft({ ...draft, clusterGapSec: v })}
          tooltip={
            <>
              <strong>How commits get grouped into a coding session.</strong>{" "}
              Any two of your commits made within this window merge into a single
              session; a gap longer than this starts a new one.
              <br />
              <br />
              <em>Bigger</em> = fewer, longer sessions (may merge unrelated work).
              {" "}
              <em>Smaller</em> = more, shorter sessions (may fragment one real
              coding block into several).
              <br />
              <br />
              Default 30&nbsp;min matches Wakatime&apos;s own idle-timeout heuristic.
            </>
          }
        />
        <SliderRow
          label={`Pre-commit lead (${secToMin(draft.preCommitLeadSec)} min)`}
          value={draft.preCommitLeadSec}
          min={0}
          max={3600}
          step={300}
          onChange={(v) => setDraft({ ...draft, preCommitLeadSec: v })}
          tooltip={
            <>
              <strong>Time credited BEFORE the first commit of each session.</strong>{" "}
              Captures the &quot;you typed for a while before hitting save/commit&quot;
              behavior that Wakatime editor plugins pick up but git can&apos;t see.
              <br />
              <br />
              Set to <em>0</em> to only count from the first commit onward
              (strictest — undercounts thinking time). Default <em>15&nbsp;min</em> is
              conservative for the average code→commit gap.
            </>
          }
        />
        <SliderRow
          label={`Post-commit tail (${secToMin(draft.postCommitTailSec)} min)`}
          value={draft.postCommitTailSec}
          min={0}
          max={1800}
          step={60}
          onChange={(v) => setDraft({ ...draft, postCommitTailSec: v })}
          tooltip={
            <>
              <strong>Time credited AFTER the last commit of each session.</strong>{" "}
              Covers commit-message writing, cleanup, and small tweaks that happen
              in the tail of a work block.
              <br />
              <br />
              Usually small — default <em>5&nbsp;min</em> is enough for a well-written
              commit message. Bump higher if you frequently do post-commit review /
              docs updates without a new commit.
            </>
          }
        />
        <SliderRow
          label={`Heartbeat rate (${secToMin(draft.heartbeatRateSec)} min)`}
          value={draft.heartbeatRateSec}
          min={60}
          max={600}
          step={60}
          onChange={(v) => setDraft({ ...draft, heartbeatRateSec: v })}
          tooltip={
            <>
              <strong>How often a synthetic heartbeat is emitted inside each session.</strong>{" "}
              Matches Wakatime&apos;s ~2&nbsp;min real cadence by default so backfilled
              time rolls up the same way as live-tracked time.
              <br />
              <br />
              <em>Larger</em> = fewer DB rows, less granular per-file attribution.{" "}
              <em>Smaller</em> = more rows, higher DB pressure, tighter file-level
              detail. A 30-min session at 2&nbsp;min rate = 16 heartbeats spread
              across the files you actually edited.
            </>
          }
        />
      </div>

      {/* Emails chip input */}
      <div className="space-y-2">
        <div className="flex items-center gap-1.5">
          <UILabel className="font-mono text-xs uppercase tracking-widest text-muted-foreground">
            Author emails
          </UILabel>
          <InfoTip
            content={
              <>
                <strong>Which commits count as yours.</strong> Only commits whose{" "}
                <code className="bg-muted/40 px-1">author.email</code> exactly
                matches one of these are turned into heartbeats.
                <br />
                <br />
                Add <em>every email</em> you&apos;ve ever committed under —
                personal, past work, projects that used a different alias.
                Missing an email means those commits are silently skipped and
                contribute zero backfill time.
                <br />
                <br />
                Case-sensitive, exact match. No wildcards.
              </>
            }
          />
        </div>
        <div className="flex flex-wrap gap-1">
          {draft.authorEmails.length === 0 && (
            <span className="text-xs text-muted-foreground">
              (empty — CLI will refuse to run without at least one)
            </span>
          )}
          {draft.authorEmails.map((e) => (
            <span
              key={e}
              className="inline-flex items-center gap-1 rounded-sm border border-border bg-muted/40 px-2 py-0.5 font-mono text-xs text-foreground"
            >
              {e}
              <button
                type="button"
                aria-label={`remove ${e}`}
                onClick={() => removeEmail(e)}
                className="text-muted-foreground hover:text-destructive"
              >
                <X size={10} />
              </button>
            </span>
          ))}
        </div>
        <div className="flex gap-2">
          <Input
            placeholder="me@example.com"
            value={emailInput}
            onChange={(e) => setEmailInput(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                addEmail();
              }
            }}
            className="max-w-xs font-mono text-xs"
          />
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={addEmail}
            disabled={!emailInput.trim()}
          >
            <Plus size={12} /> add
          </Button>
        </div>
      </div>

      {/* Source tag */}
      <div className="space-y-1">
        <div className="flex items-center gap-1.5">
          <UILabel className="font-mono text-xs uppercase tracking-widest text-muted-foreground">
            Source tag
          </UILabel>
          <InfoTip
            content={
              <>
                <strong>How backfilled rows are labeled in the DB.</strong>{" "}
                Written to <code className="bg-muted/40 px-1">heartbeats.source</code>{" "}
                on every synthetic heartbeat.
                <br />
                <br />
                Server enforces a <code className="bg-muted/40 px-1">backfill:</code>{" "}
                prefix so real Wakatime rows (<code>source IS NULL</code>) can never
                be overwritten by mistake.
                <br />
                <br />
                Use different tags to segment runs — e.g.{" "}
                <code>backfill:git-2024</code> then <code>backfill:git-2025</code>{" "}
                lets you selectively purge one range from the Danger Zone without
                losing the other.
              </>
            }
          />
        </div>
        <Input
          value={draft.sourceTag}
          onChange={(e) => setDraft({ ...draft, sourceTag: e.target.value })}
          className="max-w-md font-mono text-xs"
        />
        <p className="text-xs text-muted-foreground">
          Written to <code>heartbeats.source</code>; must start with{" "}
          <code>backfill:</code> (server-side clamp adds the prefix if you drop it).
        </p>
      </div>

      {/* Lang map (advanced accordion — always visible for now) */}
      <details className="space-y-1">
        <summary className="cursor-pointer font-mono text-xs uppercase tracking-widest text-muted-foreground">
          Language map overrides
        </summary>
        <Textarea
          value={langMapRaw}
          onChange={(e) => setLangMapRaw(e.target.value)}
          onBlur={commitLangMap}
          rows={6}
          className="mt-2 font-mono text-xs"
        />
        {langMapErr && (
          <p className="text-xs text-destructive">JSON error: {langMapErr}</p>
        )}
        <p className="text-xs text-muted-foreground">
          Map of file extension (without dot) → language string. Merged with the
          compiled default table on the CLI. Example:{" "}
          <code>{"{ \"ts\": \"TypeScript\" }"}</code>
        </p>
      </details>

      <div className="flex items-center gap-2">
        <Button
          type="button"
          onClick={handleSave}
          disabled={save.isPending || !!langMapErr}
          size="sm"
        >
          {save.isPending && <Loader2 size={12} className="animate-spin" />}
          Save
        </Button>
        {save.isSuccess && (
          <span className="text-xs text-emerald-500">saved</span>
        )}
        {save.isError && (
          <span className="text-xs text-destructive">save failed</span>
        )}
      </div>
    </div>
  );
}

interface SliderRowProps {
  label: string;
  value: number;
  min: number;
  max: number;
  step: number;
  onChange: (v: number) => void;
  /** Rich-text explanation shown on ℹ hover. Rendered inside a Radix tooltip. */
  tooltip?: ReactNode;
}

function SliderRow({ label, value, min, max, step, onChange, tooltip }: SliderRowProps) {
  return (
    <div className="space-y-1">
      <div className="flex items-center gap-1.5">
        <UILabel className="font-mono text-xs uppercase tracking-widest text-muted-foreground">
          {label}
        </UILabel>
        {tooltip && <InfoTip content={tooltip} />}
      </div>
      <input
        type="range"
        min={min}
        max={max}
        step={step}
        value={value}
        onChange={(e) => onChange(Number(e.target.value))}
        className="w-full accent-[color:var(--primary)]"
      />
    </div>
  );
}

// InfoTip — small ℹ icon that pops a hover tooltip with rich explanation.
// Wrap in its own TooltipProvider so this stays self-contained; app root
// already has one but nesting is a no-op in Radix.
function InfoTip({ content }: { content: ReactNode }) {
  return (
    <TooltipProvider delayDuration={150}>
      <Tooltip>
        <TooltipTrigger asChild>
          <button
            type="button"
            className="text-muted-foreground/60 hover:text-[color:var(--primary)] focus-visible:text-[color:var(--primary)] outline-none"
            aria-label="What is this?"
          >
            <Info size={12} />
          </button>
        </TooltipTrigger>
        <TooltipContent
          side="top"
          align="start"
          sideOffset={6}
          className="max-w-[340px] border border-[color:var(--primary)]/50 bg-[color:var(--card)] p-3 text-[11px] leading-relaxed text-[color:var(--foreground)]"
        >
          {content}
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}

// ---- CLI hint --------------------------------------------------------------

function CLIHint({ emails }: { emails: string[] }) {
  const apiBase = typeof window !== "undefined" ? window.location.origin : "https://boomtime.example.com";
  const emailArg = emails.length > 0 ? `--emails ${emails.join(",")} ` : "";
  const cmd = `boomtime backfill git --root ~/code ${emailArg}--api ${apiBase} --token $BOOM_ADMIN_TOKEN`;
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(cmd);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* ignore */
    }
  };
  return (
    <div className="space-y-2 border border-border bg-card/40 p-4">
      <h2 className="font-mono text-xs uppercase tracking-widest text-muted-foreground">
        CLI Command
      </h2>
      <div className="relative">
        <pre className="overflow-x-auto rounded-sm border border-border bg-background p-3 font-mono text-xs">
          {cmd}
        </pre>
        <Button
          type="button"
          size="sm"
          variant="outline"
          onClick={copy}
          className="absolute right-2 top-2"
        >
          <Copy size={12} /> {copied ? "copied" : "copy"}
        </Button>
      </div>
      <p className="text-xs text-muted-foreground">
        Generate a token via <code>boomtime create-api-token</code> and export it as{" "}
        <code>BOOM_ADMIN_TOKEN</code>.
      </p>
    </div>
  );
}

// ---- stats row -------------------------------------------------------------

function StatsRow() {
  const { data, isLoading } = useQuery({
    queryKey: qk.backfillStats(),
    queryFn: () => api.getBackfillStats(),
    refetchInterval: 15_000,
  });
  const sources = useMemo(
    () => Object.entries(data?.sources ?? {}).sort((a, b) => b[1] - a[1]),
    [data],
  );
  return (
    <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
      <StatCard label="Backfilled rows" value={isLoading ? "—" : String(data?.totalRows ?? 0)} />
      <StatCard
        label="Sources"
        value={
          sources.length === 0 ? (
            <span className="text-muted-foreground">none</span>
          ) : (
            <div className="flex flex-wrap gap-1">
              {sources.map(([src, n]) => (
                <span
                  key={src}
                  className="rounded-sm border border-border bg-muted/40 px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-wider"
                >
                  {src}={n}
                </span>
              ))}
            </div>
          )
        }
      />
      <StatCard
        label="Range"
        value={
          data?.totalRows && data?.oldest && data?.newest ? (
            <span className="font-mono text-xs">
              {fmtDateShort(data.oldest)} → {fmtDateShort(data.newest)}
            </span>
          ) : (
            <span className="text-muted-foreground">—</span>
          )
        }
      />
    </div>
  );
}

function StatCard({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="border border-border bg-card/40 p-3">
      <div className="font-mono text-[10px] uppercase tracking-widest text-muted-foreground">
        {label}
      </div>
      <div className="mt-1 tabular-nums text-lg text-foreground">{value}</div>
    </div>
  );
}

// ---- job queue table -------------------------------------------------------

function JobQueue() {
  const queue = useBackfillJobQueue();
  const rows = useMemo(
    () =>
      Array.from(queue.jobs.values()).sort((a, b) =>
        a.enqueuedAt < b.enqueuedAt ? -1 : 1,
      ),
    [queue.jobs],
  );
  return (
    <div className="space-y-2 border border-border bg-card/40 p-4">
      <div className="flex items-center justify-between">
        <h2 className="font-mono text-xs uppercase tracking-widest text-muted-foreground">
          Live queue
        </h2>
        <span
          className={cn(
            "inline-flex items-center gap-1 font-mono text-[10px] uppercase tracking-wider",
            queue.connected ? "text-emerald-500" : "text-muted-foreground",
          )}
          title={queue.connected ? "connected" : `reconnecting… (${queue.reconnectAttempt})`}
        >
          {queue.connected ? <Wifi size={10} /> : <WifiOff size={10} />}
          {queue.connected ? "live" : "reconnecting"}
        </span>
      </div>
      {rows.length === 0 ? (
        <div className="py-8 text-center font-mono text-xs uppercase tracking-widest text-muted-foreground">
          no active runs — start the CLI to see jobs land
        </div>
      ) : (
        <table className="w-full border-collapse text-xs">
          <thead>
            <tr className="border-b border-border font-mono uppercase tracking-widest text-muted-foreground">
              <th className="py-2 text-left">Repo</th>
              <th className="py-2 text-left">Path</th>
              <th className="py-2 text-left">Status</th>
              <th className="py-2 text-right tabular-nums">Commits</th>
              <th className="py-2 text-right tabular-nums">Written</th>
              <th className="py-2 text-right tabular-nums">Skipped</th>
              <th className="py-2 text-right tabular-nums">Elapsed</th>
              <th className="py-2 text-left">Error</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((j) => (
              <tr key={j.id} className="border-b border-border/40 last:border-none">
                <td className="py-2 font-mono">{j.repoName}</td>
                <td className="py-2 font-mono text-muted-foreground">{j.repoPath}</td>
                <td className="py-2">
                  <StatusPill status={j.status} />
                </td>
                <td className="py-2 text-right tabular-nums">
                  {j.processed}/{j.total || "?"}
                </td>
                <td className="py-2 text-right tabular-nums">{j.written}</td>
                <td className="py-2 text-right tabular-nums">{j.skipped}</td>
                <td className="py-2 text-right tabular-nums">{fmtElapsed(j.startedAt)}</td>
                <td className="py-2 font-mono text-destructive" title={j.error}>
                  {j.error ? j.error.slice(0, 32) : ""}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

// ---- danger zone -----------------------------------------------------------

function DangerZone() {
  const queryClient = useQueryClient();
  const stats = useQuery({
    queryKey: qk.backfillStats(),
    queryFn: () => api.getBackfillStats(),
  });
  const [confirm, setConfirm] = useState("");
  const [source, setSource] = useState<string>("");

  const sources = Object.keys(stats.data?.sources ?? {});
  const del = useMutation({
    mutationFn: async () => {
      if (source === "" || source === "__all__") {
        return api.deleteBackfillHeartbeats({ all: true });
      }
      return api.deleteBackfillHeartbeats({ source });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: qk.backfillStats() });
      setConfirm("");
    },
  });

  const totalForSource =
    source === "" || source === "__all__"
      ? stats.data?.totalRows ?? 0
      : stats.data?.sources?.[source] ?? 0;

  return (
    <details className="border border-destructive/50 bg-destructive/5 p-4">
      <summary className="cursor-pointer font-mono text-xs uppercase tracking-widest text-destructive">
        Danger zone
      </summary>
      <div className="mt-3 space-y-3">
        <p className="text-xs text-muted-foreground">
          This will permanently delete{" "}
          <span className="font-mono text-foreground tabular-nums">{totalForSource}</span>{" "}
          backfilled heartbeats. <strong>Real Wakatime data is NOT touched.</strong>
        </p>
        <div className="flex items-center gap-2">
          <UILabel className="font-mono text-xs uppercase tracking-widest text-muted-foreground">
            Source
          </UILabel>
          <select
            value={source}
            onChange={(e) => setSource(e.target.value)}
            className="border border-border bg-background px-2 py-1 font-mono text-xs"
          >
            <option value="__all__">all backfill:%</option>
            {sources.map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </select>
        </div>
        <div className="flex items-center gap-2">
          <Input
            placeholder='type "DELETE" to confirm'
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            className="max-w-xs font-mono text-xs"
          />
          <Button
            type="button"
            variant="destructive"
            size="sm"
            disabled={confirm !== "DELETE" || del.isPending}
            onClick={() => del.mutate()}
          >
            {del.isPending ? (
              <Loader2 size={12} className="animate-spin" />
            ) : (
              <Trash2 size={12} />
            )}
            Delete backfilled rows
          </Button>
        </div>
        {del.isSuccess && del.data && (
          <p className="font-mono text-xs text-emerald-500">
            deleted {del.data.deleted} rows
          </p>
        )}
        {del.isError && (
          <p className="font-mono text-xs text-destructive">delete failed</p>
        )}
      </div>
    </details>
  );
}

// ---- entry -----------------------------------------------------------------

export function BackfillTab() {
  const cfg = useQuery({
    queryKey: qk.backfillConfig(),
    queryFn: () => api.getBackfillConfig(),
    staleTime: 30_000,
  });
  return (
    <div className="space-y-6">
      <div className="flex items-center gap-2">
        <RefreshCw size={16} className="text-primary" />
        <h1 className="font-mono text-sm uppercase tracking-widest">
          Git-history backfill
        </h1>
      </div>
      <p className="text-xs text-muted-foreground">
        Point the CLI at a directory of git repos and it will materialize
        synthetic Wakatime-style heartbeats for every author-matched commit,
        skipping any session that overlaps existing real activity.
      </p>
      <StatsRow />
      <ConfigPanel />
      <CLIHint emails={cfg.data?.authorEmails ?? []} />
      <JobQueue />
      <DangerZone />
    </div>
  );
}
