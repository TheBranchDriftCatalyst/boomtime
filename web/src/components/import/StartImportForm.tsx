import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import type { DateRange } from "react-day-picker";
import { CalendarDays, Loader2, Play, Wand2 } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Calendar } from "@/components/ui/calendar";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { api } from "@/lib/api";
import { removeDays } from "@/lib/utils";
import type { ImportRequest } from "@/types/api";

interface StartImportFormProps {
  disabled?: boolean;
  onStarted: (jobId: number) => void;
}

export function StartImportForm({ disabled, onStarted }: StartImportFormProps) {
  const [apiToken, setApiToken] = useState("");
  const [range, setRange] = useState<DateRange | undefined>({
    from: removeDays(new Date(), 10),
    to: new Date(),
  });
  const [submitting, setSubmitting] = useState(false);
  const [detecting, setDetecting] = useState(false);

  const { data: config } = useQuery({
    queryKey: ["import-config"],
    queryFn: () => api.getImportConfig(),
    staleTime: 60_000,
  });
  const hasServerKey = config?.hasServerKey ?? false;

  async function onDetect() {
    if (!apiToken && !hasServerKey) {
      toast.error("Provide a Wakatime API token first, or configure one server-side");
      return;
    }
    setDetecting(true);
    try {
      const body = apiToken ? { apiToken: btoa(apiToken) } : {};
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

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!range?.from || !range?.to) {
      toast.error("Please select a date range");
      return;
    }
    if (!apiToken && !hasServerKey) {
      toast.error("Please provide a Wakatime API token");
      return;
    }

    const req: ImportRequest = {
      startDate: range.from.toISOString(),
      endDate: range.to.toISOString(),
    };
    // Only send a token when typed; blank falls back to the server's env key.
    if (apiToken) req.apiToken = btoa(apiToken);

    setSubmitting(true);
    try {
      const res = await api.submitImport(req);
      toast.success("Import started");
      onStarted(res.jobId);
      setApiToken("");
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

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Start a new import</CardTitle>
      </CardHeader>
      <CardContent>
        <form onSubmit={onSubmit} className="space-y-4">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label htmlFor="wakatime-token">
                Wakatime API token{hasServerKey ? " (optional)" : ""}
              </Label>
              <Input
                id="wakatime-token"
                type="password"
                required={!hasServerKey}
                value={apiToken}
                onChange={(e) => setApiToken(e.target.value)}
              />
              <p className="text-xs text-muted-foreground">
                {hasServerKey
                  ? "Using the server-configured Wakatime key — leave blank or override."
                  : "Used to authenticate with wakatime.com."}
              </p>
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
              <p className="text-xs text-muted-foreground">
                For which dates to fetch heartbeats. Auto-detect your earliest
                Wakatime data.
              </p>
            </div>
          </div>
          <Button type="submit" disabled={submitting || disabled}>
            <Play className="h-4 w-4" />
            {submitting ? "Starting..." : "Start import"}
          </Button>
        </form>
      </CardContent>
    </Card>
  );
}
