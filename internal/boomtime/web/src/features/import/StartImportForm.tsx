import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import type { DateRange } from "react-day-picker";
import { formatDistanceToNowStrict } from "date-fns";
import {
  CalendarDays,
  Eye,
  EyeOff,
  History,
  Loader2,
  Play,
  Save,
  Trash2,
  Wand2,
} from "lucide-react";
import { toast } from "sonner";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/tooltip";
import { Calendar } from "@/components/ui/calendar";
import { Card, CardContent, CardHeader, CardTitle } from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { Input } from "@thebranchdriftcatalyst/catalyst-ui/ui/input";
import { Label } from "@thebranchdriftcatalyst/catalyst-ui/ui/label";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/popover";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import { removeDays } from "@/lib/utils";
import type { ImportRequest } from "@/types/api";

interface StartImportFormProps {
  disabled?: boolean;
  onStarted: (jobId: number) => void;
}

// Age (ms) beyond which a "valid"-status key is considered stale — the FE
// downgrades the dot to yellow so the user knows the last successful check
// may no longer reflect wakatime.com's current view of the key.
const KEY_STATUS_STALE_MS = 7 * 24 * 60 * 60 * 1000;

// Derived dot color model (four states, three colors + neutral off).
type KeyDotState = "none" | "valid" | "unknown" | "invalid";

interface KeyDotInfo {
  state: KeyDotState;
  tooltip: string;
}

function deriveKeyDot(
  hasSavedKey: boolean,
  keyStatus: "valid" | "invalid" | "unknown" | null | undefined,
  checkedAt: string | null | undefined,
): KeyDotInfo {
  if (!hasSavedKey) return { state: "none", tooltip: "No key saved" };
  const rel = checkedAt ? formatDistanceToNowStrict(new Date(checkedAt), { addSuffix: true }) : null;
  if (keyStatus === "invalid") {
    return {
      state: "invalid",
      tooltip: rel ? `Saved key — last check failed ${rel}` : "Saved key — last check failed",
    };
  }
  if (keyStatus === "unknown" || !keyStatus) {
    return {
      state: "unknown",
      tooltip: "Saved key — status unknown, will re-check on next import",
    };
  }
  // keyStatus === "valid" — check staleness. A NULL checkedAt with 'valid'
  // is anomalous (would only happen if a status update raced with a clear);
  // treat conservatively as unknown.
  if (!checkedAt) {
    return {
      state: "unknown",
      tooltip: "Saved key — status unknown, will re-check on next import",
    };
  }
  const age = Date.now() - new Date(checkedAt).getTime();
  if (age > KEY_STATUS_STALE_MS) {
    return {
      state: "unknown",
      tooltip: `Saved key — last validated ${rel}, may be stale`,
    };
  }
  return {
    state: "valid",
    tooltip: rel ? `Saved key — validated ${rel}` : "Saved key — validated",
  };
}

// Static class map so the JIT compiler can see all the Tailwind classes at
// build time (dynamic template strings would silence purge).
const DOT_CLASSES: Record<KeyDotState, string> = {
  none: "bg-muted-foreground/40",
  valid: "bg-emerald-500 shadow-[0_0_8px_rgba(16,185,129,0.6)]",
  unknown: "bg-amber-400 shadow-[0_0_8px_rgba(251,191,36,0.6)]",
  invalid: "bg-destructive shadow-[0_0_8px_var(--destructive)]",
};

export function StartImportForm({ disabled, onStarted }: StartImportFormProps) {
  const [apiToken, setApiToken] = useState("");
  const [showToken, setShowToken] = useState(false);
  const [range, setRange] = useState<DateRange | undefined>({
    from: removeDays(new Date(), 10),
    to: new Date(),
  });
  const [submitting, setSubmitting] = useState(false);
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [detecting, setDetecting] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const queryClient = useQueryClient();

  const { data: config } = useQuery({
    queryKey: qk.importConfig(),
    queryFn: () => api.getImportConfig(),
    staleTime: 60_000,
  });
  const hasServerKey = config?.hasServerKey ?? false;

  // gaka-6jm.2: per-user encrypted Wakatime key state. Plaintext never comes
  // over the wire — only presence + last-known validity + timestamp so the
  // FE can render a status dot.
  const { data: savedKeyInfo } = useQuery({
    queryKey: qk.wakatimeKey(),
    queryFn: () => api.getWakatimeKey(),
    staleTime: 60_000,
  });
  const hasSavedKey = savedKeyInfo?.hasSavedKey ?? false;
  const dot = deriveKeyDot(hasSavedKey, savedKeyInfo?.keyStatus, savedKeyInfo?.checkedAt);

  // Most-recent heartbeat, for "backfill from last import". Null when the user
  // has no heartbeats yet (button disabled).
  const { data: latest } = useQuery({
    queryKey: qk.latestHeartbeat(),
    queryFn: () => api.getLatestHeartbeat(),
    staleTime: 30_000,
  });
  const lastHeartbeat = latest?.lastHeartbeat ?? null;

  // Effective set of "we have a key we can use" for enabling submit + Detect:
  // typed input OR previously-saved user key OR server env fallback.
  const hasAnyKey = apiToken.length > 0 || hasSavedKey || hasServerKey;

  function onBackfill() {
    if (!lastHeartbeat) {
      toast.info("No heartbeats yet — nothing to backfill from.");
      return;
    }
    // Start = the most recent heartbeat, End = now. Re-importing the last day
    // is safe because import is idempotent.
    setRange({ from: new Date(lastHeartbeat), to: new Date() });
    toast.success("Range set to top up from your most recent heartbeat.");
  }

  async function onDetect() {
    if (!hasAnyKey) {
      toast.error("Provide a Wakatime API token first, or configure one server-side");
      return;
    }
    setDetecting(true);
    try {
      // Send the raw wakatime api_key — the server does the single Basic
      // base64-encode into Authorization. Any client-side btoa() here would
      // double-encode and wakatime.com would reject with 401 (gaka-f2l).
      const body = apiToken ? { apiToken } : {};
      const res = await api.detectWakatimeRange(body);
      if (res.hasData) {
        // Parse as local dates (the response is a plain YYYY-MM-DD).
        setRange({
          from: new Date(`${res.startDate}T00:00:00`),
          to: new Date(`${res.endDate}T00:00:00`),
        });
        toast.success(`Found ${res.text} of history since ${res.startDate}.`);
      } else {
        toast.info("No Wakatime data found (or no key configured).");
      }
    } catch {
      toast.error("Failed to detect your Wakatime range");
    } finally {
      setDetecting(false);
    }
  }

  // Save the currently-typed key without kicking off an import. Server probes
  // wakatime.com first and returns 400 if the key is rejected — we surface
  // that as a compact inline error under the input row. On success the input
  // is cleared and the wakatimeKey query is invalidated so the dot updates.
  async function onSave() {
    if (!apiToken) {
      toast.error("Type a Wakatime API token first");
      return;
    }
    setSaving(true);
    setSaveError(null);
    try {
      await api.saveWakatimeKey(apiToken);
      toast.success("Wakatime key saved (encrypted at rest)");
      setApiToken("");
      await queryClient.invalidateQueries({ queryKey: qk.wakatimeKey() });
    } catch (err: unknown) {
      // ApiError from lib/api carries the server's 400 message; render it
      // inline so the user doesn't have to hunt for the toast.
      const msg =
        err && typeof err === "object" && "message" in err
          ? String((err as { message: string }).message)
          : "Failed to save Wakatime key";
      setSaveError(msg);
      toast.error(msg);
    } finally {
      setSaving(false);
    }
  }

  // Clear the server-saved encrypted key. Idempotent server-side so we don't
  // need to guard against a race with a manual DB edit.
  async function onDelete() {
    setDeleting(true);
    try {
      await api.deleteWakatimeKey();
      toast.success("Saved Wakatime key removed");
      await queryClient.invalidateQueries({ queryKey: qk.wakatimeKey() });
    } catch {
      toast.error("Failed to remove saved key");
    } finally {
      setDeleting(false);
    }
  }

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!range?.from || !range?.to) {
      toast.error("Please select a date range");
      return;
    }
    if (!hasAnyKey) {
      toast.error("Please provide a Wakatime API token");
      return;
    }

    const req: ImportRequest = {
      startDate: range.from.toISOString(),
      endDate: range.to.toISOString(),
    };
    // Only send a token when typed. Blank body means "use the saved
    // encrypted key if any, else the server env key" (server-side fallback
    // chain — plaintext never leaves the server for the saved path).
    // Send raw — server does the single Basic base64-encode (gaka-f2l).
    if (apiToken) req.apiToken = apiToken;

    setSubmitting(true);
    try {
      const res = await api.submitImport(req);
      toast.success("Import started");
      onStarted(res.jobId);
      if (apiToken) {
        setApiToken("");
        await queryClient.invalidateQueries({ queryKey: qk.wakatimeKey() });
      }
    } catch {
      toast.error("Failed to start the import job");
    } finally {
      setSubmitting(false);
    }
  }

  const rangeLabel =
    range?.from && range?.to
      ? `${range.from.toLocaleDateString()} - ${range.to.toLocaleDateString()}`
      : "Select a date range";

  const tokenOptional = hasSavedKey || hasServerKey;

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Start a new import</CardTitle>
      </CardHeader>
      <CardContent>
        <TooltipProvider delayDuration={200}>
          <form onSubmit={onSubmit} className="space-y-4">
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <div className="space-y-1.5">
                <Label htmlFor="wakatime-token">
                  Wakatime API token{tokenOptional ? " (optional)" : ""}
                </Label>
                <div className="flex items-center gap-2">
                  {/* gaka-6jm.2: status dot INSIDE the input's leading edge
                      so it reads as the input's own status LED. Tabbable so
                      keyboard users can focus the tooltip trigger. */}
                  <div className="relative flex-1">
                    <Tooltip>
                      <TooltipTrigger asChild>
                        <span
                          tabIndex={0}
                          role="status"
                          aria-label={dot.tooltip}
                          className="absolute left-3 top-1/2 -translate-y-1/2 z-10 outline-none focus-visible:ring-2 focus-visible:ring-ring rounded-full"
                        >
                          <span
                            className={`block h-2.5 w-2.5 rounded-full transition-colors ${DOT_CLASSES[dot.state]}`}
                          />
                        </span>
                      </TooltipTrigger>
                      <TooltipContent>{dot.tooltip}</TooltipContent>
                    </Tooltip>
                    <Input
                      id="wakatime-token"
                      type={showToken ? "text" : "password"}
                      required={!tokenOptional}
                      value={apiToken}
                      onChange={(e) => {
                        setApiToken(e.target.value);
                        if (saveError) setSaveError(null);
                      }}
                      className="pl-8 pr-9 font-mono"
                      autoComplete="off"
                      spellCheck={false}
                    />
                    <button
                      type="button"
                      onClick={() => setShowToken((v) => !v)}
                      className="absolute right-2 top-1/2 -translate-y-1/2 rounded p-1 text-muted-foreground hover:text-foreground focus:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                      aria-label={showToken ? "Hide token" : "Show token"}
                      aria-pressed={showToken}
                      tabIndex={-1}
                    >
                      {showToken ? (
                        <EyeOff className="h-4 w-4" />
                      ) : (
                        <Eye className="h-4 w-4" />
                      )}
                    </button>
                  </div>
                  {/* gaka-6jm.2: three inline icon actions in order save · delete · import.
                      - save (outline): probe wakatime.com then persist under AES-256-GCM.
                      - delete (outline): clear saved key; disabled when no key on file.
                      - import (primary): the existing Start import action, iconified. */}
                  <Button
                    type="button"
                    variant="outline"
                    size="icon-sm"
                    onClick={onSave}
                    disabled={saving || apiToken.length === 0}
                    title="Save Wakatime key (encrypted at rest)"
                    aria-label="Save Wakatime key"
                  >
                    {saving ? (
                      <Loader2 className="h-4 w-4 animate-spin" />
                    ) : (
                      <Save className="h-4 w-4" />
                    )}
                  </Button>
                  <Button
                    type="button"
                    variant="outline"
                    size="icon-sm"
                    onClick={onDelete}
                    disabled={deleting || !hasSavedKey}
                    title="Delete saved Wakatime key"
                    aria-label="Delete saved key"
                  >
                    {deleting ? (
                      <Loader2 className="h-4 w-4 animate-spin" />
                    ) : (
                      <Trash2 className="h-4 w-4" />
                    )}
                  </Button>
                  <Button
                    type="submit"
                    variant="default"
                    size="icon-sm"
                    disabled={submitting || disabled || !hasAnyKey}
                    title="Start import"
                    aria-label="Start import"
                  >
                    {submitting ? (
                      <Loader2 className="h-4 w-4 animate-spin" />
                    ) : (
                      <Play className="h-4 w-4" />
                    )}
                  </Button>
                </div>
                {saveError ? (
                  <p className="text-xs text-destructive" role="alert">
                    {saveError}
                  </p>
                ) : (
                  <p className="text-xs text-muted-foreground">
                    {hasSavedKey
                      ? "Saved key on file — leave the input blank to use it, or type to override."
                      : hasServerKey
                        ? "Using the server-configured Wakatime key — leave blank or override."
                        : "Paste your wakatime.com API key (looks like waka_… or a bare UUID)."}
                  </p>
                )}
              </div>
              <div className="space-y-1.5">
                <Label>Date range</Label>
                <div className="flex gap-2">
                  <Popover>
                    <PopoverTrigger asChild>
                      <Button
                        type="button"
                        variant="outline"
                        className="flex-1 justify-start font-normal"
                      >
                        <CalendarDays className="h-4 w-4" />
                        {rangeLabel}
                      </Button>
                    </PopoverTrigger>
                    <PopoverContent align="start" className="w-auto p-3">
                      <Calendar
                        mode="range"
                        numberOfMonths={2}
                        selected={range}
                        onSelect={setRange}
                        disabled={{ after: new Date() }}
                      />
                    </PopoverContent>
                  </Popover>
                  <Button
                    type="button"
                    variant="secondary"
                    onClick={onDetect}
                    disabled={detecting}
                    title="Auto-detect your earliest Wakatime data"
                  >
                    {detecting ? (
                      <Loader2 className="h-4 w-4 animate-spin" />
                    ) : (
                      <Wand2 className="h-4 w-4" />
                    )}
                    Detect range
                  </Button>
                </div>
                <div className="flex flex-wrap items-center gap-2">
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={onBackfill}
                    disabled={!lastHeartbeat}
                    title={
                      lastHeartbeat
                        ? "Set the range from your most recent heartbeat to now"
                        : "No heartbeats yet — nothing to backfill from"
                    }
                  >
                    <History className="h-4 w-4" />
                    Since last heartbeat
                  </Button>
                  <p className="text-xs text-muted-foreground">
                    {lastHeartbeat
                      ? `Top up from your most recent heartbeat (${new Date(
                          lastHeartbeat,
                        ).toLocaleDateString()}) to now.`
                      : "For which dates to fetch heartbeats."}
                  </p>
                </div>
              </div>
            </div>
          </form>
        </TooltipProvider>
      </CardContent>
    </Card>
  );
}
