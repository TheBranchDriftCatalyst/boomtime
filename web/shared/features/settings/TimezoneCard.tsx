// TimezoneCard (boom-dg7) — Settings > Profile card that owns the per-user
// IANA timezone pick.
//
// Contract:
//   - Reads via GET /api/v1/users/current/timezone on mount. Returns
//     {timezone, effectiveTimezone} where `timezone` is '' if the user has
//     never explicitly picked and `effectiveTimezone` is the resolver output
//     (user > BOOM_DEFAULT_TIMEZONE > "UTC") — NEVER "".
//   - Saves via PATCH /api/v1/users/current/timezone. Server validates the
//     IANA name (time.LoadLocation), rebuilds hb_rollup_daily under the new
//     TZ, and invalidates the owner's aggregation cache so the Overview fast
//     path serves user-local buckets on next paint.
//   - On success invalidates qk.timezone() AND the ["auth", "current-user"]
//     key so the Sidebar/other consumers pick up the new value atomically.
//
// Auto-detect (first login only): if `timezone === ''` (never picked) AND
// the browser reports a zone that differs from `effectiveTimezone`, silently
// PATCH the browser value + emit a subtle toast. This means a fresh account
// on a laptop set to America/New_York doesn't inherit the operator default
// (typically UTC or America/Los_Angeles) — the FE volunteers the correct
// zone on first paint. NEVER fires when the user has already picked, and
// NEVER fires when browser TZ matches the effective TZ (would be a no-op).
import { useEffect, useMemo, useRef, useState } from "react";
import {
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { toast } from "sonner";
import { api, ApiError } from "@shared/lib/api";
import { qk } from "@shared/lib/queryKeys";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import {
  Card,
  CardContent,
  CardHeader,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { Label } from "@thebranchdriftcatalyst/catalyst-ui/ui/label";

// FALLBACK_ZONES: a curated list for browsers that don't implement
// Intl.supportedValuesOf('timeZone') (Chrome <99, Safari <15.4). Kept short
// on purpose — anyone off-list can type into the input via the "Other zone"
// text field escape hatch. Ordered roughly by user density.
const FALLBACK_ZONES: readonly string[] = [
  "UTC",
  "America/Los_Angeles",
  "America/Denver",
  "America/Chicago",
  "America/New_York",
  "America/Sao_Paulo",
  "Europe/London",
  "Europe/Paris",
  "Europe/Berlin",
  "Europe/Moscow",
  "Africa/Johannesburg",
  "Asia/Dubai",
  "Asia/Kolkata",
  "Asia/Singapore",
  "Asia/Shanghai",
  "Asia/Tokyo",
  "Australia/Sydney",
  "Pacific/Auckland",
];

// listSupportedZones: returns the full IANA list when the runtime supports
// Intl.supportedValuesOf. Otherwise falls back to FALLBACK_ZONES. Memoized
// per-render so mounting is cheap.
function listSupportedZones(): readonly string[] {
  const supported = (Intl as unknown as {
    supportedValuesOf?: (key: string) => string[];
  }).supportedValuesOf;
  if (typeof supported === "function") {
    try {
      const list = supported.call(Intl, "timeZone");
      if (Array.isArray(list) && list.length > 0) return list;
    } catch {
      // fall through
    }
  }
  return FALLBACK_ZONES;
}

// browserZone: what the browser thinks it's in. Returns '' if the runtime
// refuses to answer (unlikely; JSDOM does).
function browserZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone ?? "";
  } catch {
    return "";
  }
}

// hintFor: renders the small "(your choice)" / "(server default)" hint next
// to the effective timezone. Distinguishes the three resolver outcomes so
// the user can tell WHY the current effective TZ is what it is.
function hintFor(raw: string, effective: string): string {
  if (raw && raw === effective) return "your choice";
  if (raw && raw !== effective) {
    // Should be impossible when the resolver+FE are in sync, but guard.
    return "your choice (server override)";
  }
  if (effective === "UTC") return "fallback UTC";
  return "server default";
}

export function TimezoneCard() {
  const qc = useQueryClient();
  const zones = useMemo(listSupportedZones, []);

  const { data, isLoading } = useQuery({
    queryKey: qk.timezone(),
    queryFn: () => api.getTimezone(),
    staleTime: 30_000,
  });

  // Local state for the picker. Separate from the query so an unsaved pick
  // doesn't get stomped by a background refetch — Save button submits the
  // current local value.
  const [pending, setPending] = useState<string>("");

  // Seed local state whenever the server payload arrives / changes. If the
  // user has no explicit pick, leave `pending` empty so the "select a zone"
  // placeholder shows and the Save button is disabled.
  useEffect(() => {
    if (data) setPending(data.timezone ?? "");
  }, [data]);

  const mutate = useMutation({
    mutationFn: (tz: string) => api.updateTimezone(tz),
    onSuccess: (resp) => {
      // Invalidate both the timezone key AND the current-user key so the
      // Sidebar/other current-user consumers pick up the new value.
      qc.invalidateQueries({ queryKey: qk.timezone() });
      qc.invalidateQueries({ queryKey: ["auth", "current-user"] });
      setPending(resp.timezone ?? "");
    },
    onError: (e) => {
      toast.error(e instanceof ApiError ? e.message : "Timezone save failed");
    },
  });

  // Auto-detect (first login only). Runs once when the server payload lands
  // AND the user has never picked AND the browser's zone differs from what
  // the server currently resolves to. Ref guards against double-fire during
  // Strict Mode double-mount + subsequent data refetches.
  const autoDetectFired = useRef(false);
  useEffect(() => {
    if (!data || autoDetectFired.current) return;
    if (data.timezone !== "") return; // user already picked
    const browser = browserZone();
    if (!browser) return; // runtime doesn't tell us
    if (browser === data.effectiveTimezone) return; // no-op
    // Only fire if the detected zone is in the supported list — otherwise
    // the PATCH would 400 and we'd surface a toast for no user gain.
    if (!zones.includes(browser)) return;
    autoDetectFired.current = true;
    mutate.mutate(browser);
    toast(`Detected timezone: ${browser}`);
  }, [data, mutate, zones]);

  const explicitlyPicked = (data?.timezone ?? "") !== "";
  const effective = data?.effectiveTimezone ?? "UTC";
  const currentHint = hintFor(data?.timezone ?? "", effective);
  const canSave =
    !isLoading &&
    !mutate.isPending &&
    pending !== (data?.timezone ?? "") &&
    (pending === "" || zones.includes(pending));

  function onSave() {
    mutate.mutate(pending);
  }

  function onUseServerDefault() {
    // Explicit "revert to server default" — PATCH with empty string clears
    // the stored pick and the resolver falls back to the operator default.
    mutate.mutate("");
  }

  return (
    <Card data-testid="timezone-card">
      <CardHeader className="p-4 pb-0">
        <h2 className="font-mono text-lg font-semibold uppercase tracking-wide">
          Timezone
        </h2>
        <p className="text-sm text-muted-foreground">
          Controls how days, hours, and streaks are bucketed in your dashboard.
          Currently using{" "}
          <span
            className="font-mono font-medium text-foreground"
            data-testid="timezone-effective"
          >
            {effective}
          </span>{" "}
          <span
            className="text-muted-foreground"
            data-testid="timezone-hint"
          >
            ({currentHint})
          </span>
          .
        </p>
      </CardHeader>
      <CardContent className="p-4">
        <div className="max-w-lg space-y-4">
          <div className="space-y-1.5">
            <Label
              htmlFor="timezone-select"
              className="font-mono text-xs uppercase tracking-wide"
            >
              Timezone
            </Label>
            {/* Native <select> so we don't have to reimplement Radix's
                type-ahead search across 400+ options — the browser does it
                for free, and it stays keyboard-navigable in JSDOM. */}
            <select
              id="timezone-select"
              data-testid="timezone-select"
              value={pending}
              onChange={(e) => setPending(e.target.value)}
              disabled={isLoading || mutate.isPending}
              className="flex h-9 w-full items-center rounded-md border border-input bg-background px-3 py-1 text-sm shadow-sm focus:outline-none focus:ring-1 focus:ring-ring disabled:cursor-not-allowed disabled:opacity-50"
            >
              <option value="">
                — Use server default —
              </option>
              {zones.map((z) => (
                <option key={z} value={z}>
                  {z}
                </option>
              ))}
            </select>
            <p className="text-xs text-muted-foreground">
              Pick your IANA zone (e.g. <code>America/Los_Angeles</code>). Leave
              blank to fall back to the server default.
            </p>
          </div>

          <div className="flex flex-wrap items-center gap-3">
            <Button
              type="button"
              onClick={onSave}
              disabled={!canSave}
              data-testid="timezone-save"
            >
              {mutate.isPending ? "Saving..." : "Save"}
            </Button>
            {explicitlyPicked && (
              <Button
                type="button"
                variant="outline"
                onClick={onUseServerDefault}
                disabled={mutate.isPending}
                data-testid="timezone-reset"
              >
                Use server default
              </Button>
            )}
          </div>
        </div>
      </CardContent>
    </Card>
  );
}
