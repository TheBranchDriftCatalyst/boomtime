// AvatarTab (gaka-9v4) — the "PROFILE SYNTHESIS · BIOMETRIC RENDER"
// console in Settings. Three columns:
//
//   LEFT  (INPUT CONTEXT):  top-3 dominant traits + one-line activity
//                           synopsis, deterministically derived from the
//                           caller's stats. Read-only.
//   MIDDLE (PROMPT SYNTHESIS): textarea seeded by an LLM SSE stream via
//                           useAvatarPromptStream. Editable — the user
//                           can tweak the tag list before rendering.
//   RIGHT (OUTPUT/BIOMETRIC): 320px preview with amber corner brackets
//                           and an ID-silhouette empty state. Shows the
//                           chibi once the async render lands.
//
// The bottom `[> RENDER]` button fires the async regen and starts a 5s
// poll on /avatar/status. Server-side generation is ~15-30s (SDXL) up
// to ~25min (chroma-hd), so the UI stays responsive throughout.
import { useCallback, useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import {
  Card,
  CardContent,
  CardHeader,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { Textarea } from "@thebranchdriftcatalyst/catalyst-ui/ui/textarea";
import { api, ApiError } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import { useAvatarPromptStream } from "./useAvatarPromptStream";
import { UserAvatarImage } from "@/features/publicprofile/UserAvatarImage";

// AvatarSummary is the compact profile digest we hand to the LLM's
// prompt-authoring call. Derived deterministically from StatsPayload so
// the same stats always produce the same summary — no LLM prompt drift
// across identical inputs.
interface AvatarSummary {
  topLabels: string[];
  synopsis: string;
}

// deriveSummary: pure over the last 30d stats. Produces:
//   topLabels: up to 3 short traits ("PYTHON HEAVY", "VIM USER", ...)
//   synopsis: one-line free-form context for the LLM
// This is intentionally NOT the full label evaluator — the evaluator
// requires a PublicDashboardPayload which auth'd Settings doesn't have
// on hand. A dumber summary is fine here: the LLM's job is to translate
// vibes into diffusion tags, not to produce a formal profile.
//
// `topLanguage.pct` is a 0..1 share (matches how the rest of the app
// carries pct in label DSL; see labels/types.ts `pct: number; // 0..1`).
// Multiply by 100 at the render site — reading the raw
// ResourceStats.totalPct directly would render "PYTHON 0%" because the
// backend also emits totalPct as a 0..1 decimal (same bug as the public-
// profile chip list, see WidgetRenderer's ChipList note).
export function deriveSummary(input: {
  topLanguage?: { name: string; pct: number };
  topEditor?: { name: string };
  topPlatform?: { name: string };
  dailyAvgHours?: number;
}): AvatarSummary {
  const labels: string[] = [];
  if (input.topLanguage) {
    labels.push(
      `${input.topLanguage.name.toUpperCase()} ${Math.round(input.topLanguage.pct * 100)}%`,
    );
  }
  if (input.topEditor) labels.push(`${input.topEditor.name.toUpperCase()} USER`);
  if (input.topPlatform) labels.push(`${input.topPlatform.name.toUpperCase()} NATIVE`);
  if (labels.length === 0) labels.push("NEW OPERATOR");

  const parts: string[] = [];
  if (input.topLanguage)
    parts.push(`${input.topLanguage.name.toLowerCase()}-heavy`);
  if (input.dailyAvgHours != null && input.dailyAvgHours > 0) {
    parts.push(`${input.dailyAvgHours.toFixed(1)}h/day`);
  }
  if (input.topEditor) parts.push(input.topEditor.name.toLowerCase());
  const synopsis = parts.length > 0 ? parts.join(" · ") : "no dominant traits yet";

  return { topLabels: labels, synopsis };
}

// last30dRange builds the {start, end} the ambient stats endpoints expect.
// We recompute inline (not via a shared helper) because the Settings pages
// don't share the Overview page's stats-range context.
function last30dRange(): { start: string; end: string } {
  const end = new Date();
  const start = new Date();
  start.setDate(start.getDate() - 30);
  return { start: start.toISOString(), end: end.toISOString() };
}

export function AvatarTab() {
  const queryClient = useQueryClient();
  const range = useMemo(() => last30dRange(), []);

  // Current user (for the public-avatar preview URL + the bust hint).
  // The backend crams the acting username into `full_name` (see
  // handler/auth.go CurrentUser) — this endpoint is the only auth'd
  // source of the caller's username short of parsing the JWT ourselves.
  const { data: current } = useQuery({
    queryKey: ["auth", "current-user"],
    queryFn: () => api.currentUser(),
    staleTime: 60_000,
  });
  const username = current?.data?.full_name ?? "";
  // gaka-9v4: SYNTHESIZE endpoint is under /api/v1/admin/... so it's
  // gated on the admin allowlist. Non-admin users can still type their
  // own prompt in the textarea and hit RENDER — only the "auto-author
  // from labels" convenience is admin-gated (see the design note about
  // opening this up once per-user LLM cost caps land).
  const isAdmin = Boolean(current?.data?.is_admin);

  // Stats for the LEFT panel + synopsis derivation. 30d window — enough
  // to capture a dominant trait even for weekend hackers, not so long
  // that a career shift is invisible.
  const { data: stats } = useQuery({
    queryKey: qk.stats(range.start, range.end),
    queryFn: () => api.getStats({ ...range, timeLimit: 15 }),
    staleTime: 60_000,
  });

  const summary = useMemo<AvatarSummary>(() => {
    if (!stats) return { topLabels: ["LOADING"], synopsis: "…" };
    const topLanguage = stats.languages?.[0]
      ? { name: stats.languages[0].name, pct: stats.languages[0].totalPct }
      : undefined;
    const topEditor = stats.editors?.[0]
      ? { name: stats.editors[0].name }
      : undefined;
    const topPlatform = stats.platforms?.[0]
      ? { name: stats.platforms[0].name }
      : undefined;
    const dailyAvgHours =
      stats.dailyAvg != null ? stats.dailyAvg / 3600 : undefined;
    return deriveSummary({ topLanguage, topEditor, topPlatform, dailyAvgHours });
  }, [stats]);

  // LLM prompt stream (SSE proxy).
  const { text: streamedPrompt, isStreaming, error: streamError, synthesize, setText } =
    useAvatarPromptStream();

  const handleSynthesize = useCallback(() => {
    void synthesize({ topLabels: summary.topLabels, synopsis: summary.synopsis });
  }, [synthesize, summary]);

  // Status poll — cheap enough (single row read) that a 5s refetch during
  // a render is unnoticeable server-side. Only enabled once we've kicked
  // off a render, so idle users don't pay for polling.
  const [renderStartedAt, setRenderStartedAt] = useState<number | null>(null);
  const shouldPoll = renderStartedAt != null;
  const { data: status } = useQuery({
    queryKey: qk.avatarStatus(),
    queryFn: () => api.getAvatarStatus(),
    enabled: true, // always fetch at least once so we know if there's an existing avatar
    refetchInterval: shouldPoll ? 5000 : false,
    staleTime: 0,
  });

  // When status transitions to ready or error, stop polling + notify.
  useEffect(() => {
    if (!shouldPoll) return;
    if (status?.status === "ready") {
      setRenderStartedAt(null);
      toast.success("Chibi avatar rendered");
      // Bust the <img> cache so the RIGHT panel picks up the new bytes.
      queryClient.invalidateQueries({ queryKey: qk.avatarStatus() });
    } else if (status?.status === "error") {
      setRenderStartedAt(null);
      toast.error(status.error ?? "Avatar render failed");
    }
  }, [status, shouldPoll, queryClient]);

  const regenerate = useMutation({
    mutationFn: () =>
      api.regenerateAvatar({
        prompt: streamedPrompt.trim(),
      }),
    onSuccess: () => {
      setRenderStartedAt(Date.now());
      toast("Render queued — this can take 15s to 25min depending on model");
      queryClient.invalidateQueries({ queryKey: qk.avatarStatus() });
    },
    onError: (err) => {
      if (err instanceof ApiError && err.status === 503) {
        toast.error("Avatar rendering not available on this server");
        return;
      }
      if (err instanceof ApiError && err.status === 409) {
        toast.error("A render is already in flight — wait for it to finish");
        return;
      }
      toast.error((err as Error).message || "Regenerate failed");
    },
  });

  const canRender = streamedPrompt.trim().length > 0 && !regenerate.isPending;
  const isRendering =
    status?.status === "running" || status?.status === "pending" || regenerate.isPending;

  const bustHint = status?.generatedAt
    ? new Date(status.generatedAt).valueOf()
    : undefined;

  return (
    <Card>
      <CardHeader>
        <div
          className="font-mono text-[11px] uppercase tracking-[0.24em]"
          style={{
            color: "color-mix(in oklab, var(--primary) 90%, transparent)",
            fontFamily: '"Chakra Petch", "JetBrains Mono", ui-monospace, monospace',
            fontWeight: 700,
          }}
        >
          &gt; PROFILE SYNTHESIS · BIOMETRIC RENDER
        </div>
        <div className="mt-1 text-xs text-muted-foreground">
          Author a chibi portrait prompt from your dominant activity traits,
          then render it via the on-prem image pipeline. Rendered avatars
          appear on your public profile hero.
        </div>
      </CardHeader>
      <CardContent>
        <div
          className="grid gap-4"
          style={{ gridTemplateColumns: "minmax(200px,1fr) minmax(280px,2fr) 340px" }}
        >
          {/* LEFT — INPUT CONTEXT */}
          <PanelSection title="INPUT CONTEXT">
            <div className="flex flex-wrap gap-1.5">
              {summary.topLabels.map((l) => (
                <span
                  key={l}
                  className="font-mono text-[10px] uppercase tracking-[0.14em]"
                  style={{
                    padding: "3px 8px",
                    border:
                      "1px solid color-mix(in oklab, var(--primary) 40%, transparent)",
                    background:
                      "color-mix(in oklab, var(--primary) 8%, transparent)",
                    color: "color-mix(in oklab, var(--primary) 90%, transparent)",
                  }}
                  data-testid="avatar-input-label"
                >
                  {l}
                </span>
              ))}
            </div>
            <div className="mt-3 text-[11px] text-muted-foreground">
              SYNOPSIS
            </div>
            <div
              className="font-mono text-[11px] uppercase tracking-[0.1em]"
              style={{
                color: "color-mix(in oklab, var(--primary) 85%, transparent)",
              }}
              data-testid="avatar-input-synopsis"
            >
              {summary.synopsis}
            </div>
          </PanelSection>

          {/* MIDDLE — PROMPT SYNTHESIS */}
          <PanelSection title="PROMPT SYNTHESIS">
            <div className="flex items-center gap-2">
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={handleSynthesize}
                disabled={isStreaming || !isAdmin}
                data-testid="avatar-synthesize-btn"
                title={
                  isAdmin
                    ? undefined
                    : "SYNTHESIZE is admin-gated for now. Type your own prompt below and hit RENDER."
                }
              >
                {isStreaming ? "▶ SYNTHESIZING…" : "▸ SYNTHESIZE"}
              </Button>
              {isStreaming && (
                <span
                  className="inline-block h-2 w-2 rounded-full"
                  style={{
                    background: "#f5a623",
                    boxShadow: "0 0 8px #f5a623",
                    animation: "pulse 1.2s ease-in-out infinite",
                  }}
                  aria-hidden
                />
              )}
              {streamError && (
                <span className="text-xs text-destructive">{streamError}</span>
              )}
            </div>
            <Textarea
              value={streamedPrompt}
              onChange={(e) => setText(e.target.value)}
              placeholder={
                isStreaming
                  ? "authoring…"
                  : "> click SYNTHESIZE to author a chibi prompt, or type your own"
              }
              rows={10}
              className="mt-2 font-mono text-xs"
              style={{
                background: "color-mix(in oklab, var(--background) 92%, black)",
                border:
                  "1px solid color-mix(in oklab, var(--primary) 45%, transparent)",
                borderRadius: 2,
                color: "color-mix(in oklab, var(--foreground) 92%, transparent)",
                resize: "vertical",
              }}
              data-testid="avatar-prompt-textarea"
            />
          </PanelSection>

          {/* RIGHT — OUTPUT / BIOMETRIC */}
          <PanelSection title="OUTPUT / BIOMETRIC">
            <div
              className="relative"
              style={{
                width: 320,
                height: 320,
                background: "color-mix(in oklab, var(--background) 92%, black)",
                border:
                  "1px solid color-mix(in oklab, var(--primary) 35%, transparent)",
                borderRadius: 2,
                overflow: "hidden",
              }}
              data-testid="avatar-preview-frame"
            >
              {/* Amber corner brackets — matches dossier aesthetic. */}
              <CornerBrackets />
              {/* Scanlines overlay while rendering. */}
              {isRendering && <RenderingScanlineOverlay />}
              {/* The image (or initials fallback). */}
              {username && (
                <div className="flex h-full items-center justify-center">
                  <UserAvatarImage
                    username={username}
                    size={288}
                    bustHint={bustHint}
                  />
                </div>
              )}
            </div>
            <div className="mt-2 text-[10px] uppercase tracking-[0.16em] text-muted-foreground">
              STATUS: {status?.status ?? "…"}
            </div>
            {status?.error && (
              <div className="mt-1 text-[11px] text-destructive">
                {status.error}
              </div>
            )}
          </PanelSection>
        </div>

        {/* Bottom action bar */}
        <div className="mt-4 flex items-center justify-end gap-3">
          <div className="text-xs text-muted-foreground">
            Renders can take 15s to 25min depending on the pipeline.
          </div>
          <Button
            type="button"
            size="lg"
            disabled={!canRender || isRendering}
            onClick={() => regenerate.mutate()}
            data-testid="avatar-render-btn"
            style={{
              minWidth: 180,
              fontFamily:
                '"Chakra Petch", "JetBrains Mono", ui-monospace, monospace',
              letterSpacing: "0.18em",
              fontWeight: 700,
              textTransform: "uppercase",
            }}
          >
            {isRendering ? "▶ RENDERING…" : "▸ RENDER"}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

function PanelSection({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <div>
      <div
        className="mb-2 font-mono text-[10px] uppercase tracking-[0.2em]"
        style={{
          color: "color-mix(in oklab, var(--primary) 70%, transparent)",
        }}
      >
        &gt; {title}
      </div>
      {children}
    </div>
  );
}

function CornerBrackets() {
  const color = "#f5a623";
  const size = 14;
  const thick = 2;
  const base: React.CSSProperties = {
    position: "absolute",
    width: size,
    height: size,
    pointerEvents: "none",
  };
  return (
    <>
      <span
        style={{
          ...base,
          top: 4,
          left: 4,
          borderTop: `${thick}px solid ${color}`,
          borderLeft: `${thick}px solid ${color}`,
        }}
      />
      <span
        style={{
          ...base,
          top: 4,
          right: 4,
          borderTop: `${thick}px solid ${color}`,
          borderRight: `${thick}px solid ${color}`,
        }}
      />
      <span
        style={{
          ...base,
          bottom: 4,
          left: 4,
          borderBottom: `${thick}px solid ${color}`,
          borderLeft: `${thick}px solid ${color}`,
        }}
      />
      <span
        style={{
          ...base,
          bottom: 4,
          right: 4,
          borderBottom: `${thick}px solid ${color}`,
          borderRight: `${thick}px solid ${color}`,
        }}
      />
    </>
  );
}

function RenderingScanlineOverlay() {
  return (
    <div
      aria-hidden
      style={{
        position: "absolute",
        inset: 0,
        pointerEvents: "none",
        backgroundImage:
          "linear-gradient(transparent 50%, rgba(0,0,0,0.35) 50%)",
        backgroundSize: "100% 3px",
        mixBlendMode: "multiply",
        opacity: 0.55,
      }}
    />
  );
}
