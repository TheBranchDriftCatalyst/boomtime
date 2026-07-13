import { useMemo } from "react";
import { Bot, Sparkles, User } from "lucide-react";
import { ChartCard } from "@/components/ChartCard";
import { Card, CardContent } from "@/components/ui/card";
import type { AIActivityPayload } from "@/types/api";

interface AIAssistanceCardProps {
  data: AIActivityPayload | undefined;
}

// gaka-1l9: Overview surface for wakatime.com's AI-assistance metrics
// (heartbeats.ai_*). Renders nothing when the range holds no AI-tagged
// heartbeats — the AIActivityPayload.hasData short-circuits this without
// consuming a viz slot for users on non-AI editor plugins.
//
// Design intent: a compact four-tile stat strip + a horizontal AI-vs-Human
// line-changes ratio bar. Deep-dive charts (per-day time-series, per-language
// AI split) are a follow-up — the strip alone gives the operator the answer
// to "how much of my coding this week was AI-assisted".
export function AIAssistanceCard({ data }: AIAssistanceCardProps) {
  const ratio = useMemo(() => {
    if (!data) return null;
    const total = data.totalAILineChanges + data.totalHumanLineChanges;
    if (total === 0) return null;
    return {
      aiPct: (data.totalAILineChanges / total) * 100,
      humanPct: (data.totalHumanLineChanges / total) * 100,
    };
  }, [data]);

  if (!data || !data.hasData) return null;

  const avgOutPerSession =
    data.totalSessions > 0
      ? Math.round(data.totalOutputTokens / data.totalSessions)
      : 0;

  return (
    <ChartCard
      title="AI assistance"
      action={
        data.latestPlan ? (
          <span className="text-xs text-muted-foreground">
            plan: <span className="font-medium">{data.latestPlan}</span>
          </span>
        ) : undefined
      }
    >
      <div className="space-y-4">
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          <MiniStat
            label="Sessions"
            value={fmt(data.totalSessions)}
            icon={Sparkles}
            hint={`${fmt(data.heartbeatsWithAI)} AI-tagged heartbeats`}
          />
          <MiniStat
            label="AI tokens in"
            value={fmt(data.totalInputTokens)}
            icon={User}
            hint="prompt tokens sent"
          />
          <MiniStat
            label="AI tokens out"
            value={fmt(data.totalOutputTokens)}
            icon={Bot}
            hint={
              avgOutPerSession > 0
                ? `~${fmt(avgOutPerSession)} / session`
                : "response tokens"
            }
          />
          <MiniStat
            label="AI lines"
            value={fmt(data.totalAILineChanges)}
            icon={Bot}
            hint={`${fmt(data.totalHumanLineChanges)} human`}
          />
        </div>

        {ratio && (
          <div className="space-y-1.5">
            <div className="flex justify-between text-xs text-muted-foreground">
              <span>
                <Bot className="mr-1 inline h-3 w-3" />
                AI {ratio.aiPct.toFixed(1)}%
              </span>
              <span>
                Human {ratio.humanPct.toFixed(1)}%
                <User className="ml-1 inline h-3 w-3" />
              </span>
            </div>
            <div className="flex h-2 overflow-hidden rounded-full bg-muted">
              <div
                className="bg-primary"
                style={{ width: `${ratio.aiPct}%` }}
                title={`AI: ${fmt(data.totalAILineChanges)} lines`}
              />
              <div
                className="bg-emerald-500"
                style={{ width: `${ratio.humanPct}%` }}
                title={`Human: ${fmt(data.totalHumanLineChanges)} lines`}
              />
            </div>
          </div>
        )}
      </div>
    </ChartCard>
  );
}

interface MiniStatProps {
  label: string;
  value: string;
  icon: typeof Bot;
  hint?: string;
}

function MiniStat({ label, value, icon: Icon, hint }: MiniStatProps) {
  return (
    <Card className="bg-muted/20">
      <CardContent className="p-3">
        <div className="flex items-center gap-1.5 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
          <Icon className="h-3 w-3" />
          {label}
        </div>
        <div
          className="mt-1 truncate text-lg font-semibold tabular-nums"
          title={value}
        >
          {value}
        </div>
        {hint && (
          <div className="truncate text-[11px] text-muted-foreground" title={hint}>
            {hint}
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function fmt(n: number): string {
  if (n < 1000) return n.toLocaleString();
  if (n < 1_000_000) return `${(n / 1000).toFixed(n < 10_000 ? 1 : 0)}k`;
  return `${(n / 1_000_000).toFixed(n < 10_000_000 ? 1 : 0)}M`;
}
