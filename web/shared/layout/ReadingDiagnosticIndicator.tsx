// ReadingDiagnosticIndicator — rm2. A PERMANENT beacon in the top nav that
// lights ONLY while the reading monitor's high-fidelity diagnostic (calibration)
// window is running. It lives in the shared HeaderBar, so it's visible on EVERY
// page: the user always knows a ~10s-polling window is burning Amazon calls in
// the background. Driven by the lightweight, user-scoped status endpoint (polled
// ~15s), NOT the admin state — so it costs almost nothing app-wide. Clicking it
// jumps to the Books admin reading-monitor tab where the window can be stopped.
import { useNavigate } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { Radar } from "lucide-react";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/tooltip";
import { api } from "@shared/lib/api";
import { qk } from "@shared/lib/queryKeys";

// App-wide poll cadence for the beacon. Deliberately gentle — this fires on
// every page for the whole session, and it only needs to catch the window
// starting/expiring within a few seconds of it happening.
const STATUS_POLL_MS = 15_000;

export function ReadingDiagnosticIndicator() {
  const navigate = useNavigate();

  const { data } = useQuery({
    queryKey: qk.readingMonitorStatus(),
    queryFn: () => api.getReadingMonitorStatus(),
    refetchInterval: STATUS_POLL_MS,
    // A transient error just means the beacon stays dark this tick — don't
    // retry-storm the header on every page.
    retry: false,
    staleTime: STATUS_POLL_MS,
  });

  // The beacon is invisible unless a calibration window is actively running.
  if (!data?.calibrating) return null;

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          data-testid="reading-diagnostic-indicator"
          aria-label="Reading diagnostic mode active — high-fidelity calibration running"
          onClick={() => navigate("/app/admin/books?view=monitor")}
          className="relative inline-flex h-9 w-9 items-center justify-center rounded-lg border border-amber-400/50 bg-amber-500/10 text-amber-300 transition-colors hover:bg-amber-500/20 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-amber-400/60"
          style={{
            boxShadow:
              "0 0 10px color-mix(in oklab, oklch(0.82 0.16 78) 35%, transparent)",
          }}
        >
          {/* Pulsing halo behind a steady radar glyph — reads as "live". */}
          <span
            aria-hidden
            className="absolute inline-flex h-5 w-5 animate-ping rounded-full bg-amber-400/40"
          />
          <Radar className="relative h-[18px] w-[18px]" />
        </button>
      </TooltipTrigger>
      <TooltipContent side="bottom">
        Reading diagnostic mode active — high-fidelity calibration running.
      </TooltipContent>
    </Tooltip>
  );
}
