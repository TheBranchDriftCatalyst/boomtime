import { useState } from "react";
import type { DateRange } from "react-day-picker";
import { CalendarDays } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Calendar } from "@/components/ui/calendar";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { DATE_RANGE_PRESETS } from "@/lib/config";

// Far-back start for the "All time" preset; the backend honors the explicit
// range (no 1-year clamp), so only rows that actually exist are returned.
const ALL_TIME_START = new Date(Date.UTC(2000, 0, 1));
// Ranges this wide are labeled "All time" rather than a day count.
const ALL_TIME_THRESHOLD_DAYS = 3650;

interface DateRangePickerProps {
  numDays: number;
  onPreset: (days: number) => void;
  onRange: (start: Date, end: Date) => void;
}

export function DateRangePicker({
  numDays,
  onPreset,
  onRange,
}: DateRangePickerProps) {
  const [open, setOpen] = useState(false);
  const [range, setRange] = useState<DateRange | undefined>();

  const label =
    numDays >= ALL_TIME_THRESHOLD_DAYS ? "All time" : `${numDays} days`;

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button variant="outline" size="sm">
          <CalendarDays className="h-4 w-4" />
          Date range ({label})
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-auto p-3">
        <div className="mb-3 flex flex-wrap gap-1.5">
          {DATE_RANGE_PRESETS.map((d) => (
            <Button
              key={d}
              variant="secondary"
              size="sm"
              onClick={() => {
                onPreset(d);
                setOpen(false);
              }}
            >
              Last {d} days
            </Button>
          ))}
          <Button
            variant="secondary"
            size="sm"
            onClick={() => {
              onRange(ALL_TIME_START, new Date());
              setOpen(false);
            }}
          >
            All time
          </Button>
        </div>
        <Calendar
          mode="range"
          numberOfMonths={2}
          selected={range}
          onSelect={(r) => {
            setRange(r);
            if (r?.from && r?.to) {
              onRange(r.from, r.to);
              setOpen(false);
            }
          }}
          disabled={{ after: new Date() }}
        />
      </PopoverContent>
    </Popover>
  );
}
