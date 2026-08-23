// PrimitiveEditor — one big switch that renders the per-kind field group
// for every primitive Condition (boom-6uf). Ordered so a future kind
// addition mirrors formatCondition.ts's switch structure.
import { useState } from "react";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { Input } from "@thebranchdriftcatalyst/catalyst-ui/ui/input";
import { Label } from "@thebranchdriftcatalyst/catalyst-ui/ui/label";
import { X } from "lucide-react";
import type { Axis, Condition, CmpOp } from "@shared/features/publicprofile/labels/types";
import type { HeartbeatAxis } from "@shared/types/heartbeats";
import { useAxisValues } from "@boomtime/features/rules/useAxisValues";
import { Combobox } from "@shared/components/ui/combobox";

// The labels DSL uses plural axis names; the heartbeats-group endpoint that
// backs autocomplete uses singular. Map before every lookup so the
// combobox lists real project/language/etc values without a backend change.
const LABEL_AXIS_TO_HEARTBEAT: Record<Axis, HeartbeatAxis> = {
  languages: "language",
  editors: "editor",
  projects: "project",
  categories: "category",
  platforms: "platform",
};

const AXES: Axis[] = [
  "languages",
  "editors",
  "projects",
  "categories",
  "platforms",
];

const OPS: CmpOp[] = [">=", "<="];

const DOW_LABELS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

interface Props {
  value: Condition;
  onChange: (next: Condition) => void;
}

/** Renders the field group for a single primitive kind. Assumes value.kind
 *  is one of the ten primitives — the parent ConditionBuilder dispatches
 *  composers to ComposerEditor. */
export function PrimitiveEditor({ value, onChange }: Props) {
  switch (value.kind) {
    case "axis-time":
      return (
        <div className="grid grid-cols-[1fr_auto_auto] gap-2">
          <AxisField
            axis={value.axis}
            onAxisChange={(axis) => onChange({ ...value, axis })}
            valueField={value.value}
            onValueChange={(v) => onChange({ ...value, value: v })}
          />
          <OpField op={value.op} onChange={(op) => onChange({ ...value, op })} />
          <NumberField
            label="Hours"
            value={value.hours}
            onChange={(hours) => onChange({ ...value, hours })}
            min={0}
            step={0.5}
          />
        </div>
      );

    case "axis-time-sum":
      return (
        <div className="space-y-2">
          <div className="grid grid-cols-[1fr_auto_auto] gap-2">
            <div>
              <Label className="text-xs">Axis</Label>
              <AxisPicker
                value={value.axis}
                onChange={(axis) => onChange({ ...value, axis })}
              />
            </div>
            <OpField op={value.op} onChange={(op) => onChange({ ...value, op })} />
            <NumberField
              label="Total hours"
              value={value.hours}
              onChange={(hours) => onChange({ ...value, hours })}
              min={0}
              step={0.5}
            />
          </div>
          <StringListField
            label="Values (summed, OR-matched)"
            axis={value.axis}
            values={value.values}
            onChange={(values) => onChange({ ...value, values })}
          />
        </div>
      );

    case "axis-pct":
      return (
        <div className="grid grid-cols-[1fr_auto_auto] gap-2">
          <AxisField
            axis={value.axis}
            onAxisChange={(axis) => onChange({ ...value, axis })}
            valueField={value.value}
            onValueChange={(v) => onChange({ ...value, value: v })}
          />
          <OpField op={value.op} onChange={(op) => onChange({ ...value, op })} />
          <PctField
            value={value.pct}
            onChange={(pct) => onChange({ ...value, pct })}
          />
        </div>
      );

    case "top-share":
      return (
        <div className="grid grid-cols-[1fr_auto_auto] gap-2">
          <div>
            <Label className="text-xs">Axis</Label>
            <AxisPicker
              value={value.axis}
              onChange={(axis) => onChange({ ...value, axis })}
            />
          </div>
          <OpField op={value.op} onChange={(op) => onChange({ ...value, op })} />
          <PctField
            value={value.pct}
            onChange={(pct) => onChange({ ...value, pct })}
          />
        </div>
      );

    case "distinct-count":
      return (
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
          <div>
            <Label className="text-xs">Axis</Label>
            <AxisPicker
              value={value.axis}
              onChange={(axis) => onChange({ ...value, axis })}
            />
          </div>
          <NumberField
            label="Min hours each"
            value={value.minHoursEach}
            onChange={(minHoursEach) => onChange({ ...value, minHoursEach })}
            min={0}
            step={0.5}
          />
          <OpField op={value.op} onChange={(op) => onChange({ ...value, op })} />
          <NumberField
            label="Count (N)"
            value={value.n}
            onChange={(n) => onChange({ ...value, n: Math.round(n) })}
            min={1}
            step={1}
          />
        </div>
      );

    case "punchcard-hour-pct":
      return (
        <div className="space-y-2">
          <HourOfDayPicker
            hours={value.hoursIn}
            onChange={(hoursIn) => onChange({ ...value, hoursIn })}
          />
          <div className="grid grid-cols-2 gap-2">
            <OpField op={value.op} onChange={(op) => onChange({ ...value, op })} />
            <PctField
              value={value.pct}
              onChange={(pct) => onChange({ ...value, pct })}
            />
          </div>
        </div>
      );

    case "punchcard-dow-pct":
      return (
        <div className="space-y-2">
          <DowPicker
            dow={value.dowIn}
            onChange={(dowIn) => onChange({ ...value, dowIn })}
          />
          <div className="grid grid-cols-2 gap-2">
            <OpField op={value.op} onChange={(op) => onChange({ ...value, op })} />
            <PctField
              value={value.pct}
              onChange={(pct) => onChange({ ...value, pct })}
            />
          </div>
        </div>
      );

    case "streak":
      return (
        <div className="grid grid-cols-3 gap-2">
          <div>
            <Label className="text-xs">Which</Label>
            <select
              className="mt-1 h-9 w-full rounded-md border border-input bg-background px-2 text-xs"
              value={value.which}
              onChange={(e) =>
                onChange({ ...value, which: e.target.value as "current" | "longest" })
              }
            >
              <option value="current">current</option>
              <option value="longest">longest</option>
            </select>
          </div>
          <OpField op={value.op} onChange={(op) => onChange({ ...value, op })} />
          <NumberField
            label="Days"
            value={value.days}
            onChange={(days) => onChange({ ...value, days: Math.round(days) })}
            min={1}
            step={1}
          />
        </div>
      );

    case "daily-avg":
      return (
        <div className="grid grid-cols-2 gap-2">
          <OpField op={value.op} onChange={(op) => onChange({ ...value, op })} />
          <NumberField
            label="Hours/day"
            value={value.hours}
            onChange={(hours) => onChange({ ...value, hours })}
            min={0}
            step={0.5}
          />
        </div>
      );

    case "trend":
      return (
        <div className="grid grid-cols-3 gap-2">
          <div>
            <Label className="text-xs">Window</Label>
            <div className="mt-1 flex h-9 items-center rounded-md border bg-muted px-2 text-xs text-muted-foreground">
              last7-vs-prior7
            </div>
          </div>
          <OpField op={value.op} onChange={(op) => onChange({ ...value, op })} />
          <NumberField
            label="Ratio"
            value={value.ratio}
            onChange={(ratio) => onChange({ ...value, ratio })}
            min={0}
            step={0.1}
          />
        </div>
      );

    default:
      // Composers are dispatched by the parent — this branch would only
      // fire on an unknown/future kind. Render nothing rather than
      // exploding so a stale FE can survive a new server-side primitive.
      return (
        <div className="text-xs text-muted-foreground">
          Unknown primitive kind. Fall back to the Raw JSON pane.
        </div>
      );
  }
}

// ---------------- field primitives ----------------

function AxisPicker({
  value,
  onChange,
}: {
  value: Axis;
  onChange: (axis: Axis) => void;
}) {
  return (
    <select
      className="mt-1 h-9 w-full rounded-md border border-input bg-background px-2 text-xs"
      value={value}
      onChange={(e) => onChange(e.target.value as Axis)}
    >
      {AXES.map((a) => (
        <option key={a} value={a}>
          {a}
        </option>
      ))}
    </select>
  );
}

function OpField({
  op,
  onChange,
}: {
  op: CmpOp;
  onChange: (op: CmpOp) => void;
}) {
  return (
    <div>
      <Label className="text-xs">Op</Label>
      <select
        className="mt-1 h-9 w-16 rounded-md border border-input bg-background px-2 font-mono text-xs"
        value={op}
        onChange={(e) => onChange(e.target.value as CmpOp)}
      >
        {OPS.map((o) => (
          <option key={o} value={o}>
            {o}
          </option>
        ))}
      </select>
    </div>
  );
}

function NumberField({
  label,
  value,
  onChange,
  min,
  step,
}: {
  label: string;
  value: number;
  onChange: (n: number) => void;
  min?: number;
  step?: number;
}) {
  return (
    <div>
      <Label className="text-xs">{label}</Label>
      <Input
        type="number"
        className="mt-1 h-9 text-xs"
        value={Number.isFinite(value) ? value : 0}
        min={min}
        step={step}
        onChange={(e) => {
          const n = Number(e.target.value);
          onChange(Number.isFinite(n) ? n : 0);
        }}
      />
    </div>
  );
}

function PctField({
  value,
  onChange,
}: {
  value: number;
  onChange: (pct: number) => void;
}) {
  // Users think in 0..100 but the DSL is 0..1. Show percent, store fraction.
  const pctInt = Math.round((value ?? 0) * 100);
  return (
    <div>
      <Label className="text-xs">%</Label>
      <Input
        type="number"
        className="mt-1 h-9 w-20 text-xs"
        value={pctInt}
        min={0}
        max={100}
        step={1}
        onChange={(e) => {
          const n = Number(e.target.value);
          const clamped = Math.max(0, Math.min(100, Number.isFinite(n) ? n : 0));
          onChange(clamped / 100);
        }}
      />
    </div>
  );
}

/** Axis dropdown + a value combobox that autocompletes from the caller's
 *  real activity on that axis. */
function AxisField({
  axis,
  onAxisChange,
  valueField,
  onValueChange,
}: {
  axis: Axis;
  onAxisChange: (a: Axis) => void;
  valueField: string;
  onValueChange: (v: string) => void;
}) {
  const { options, isLoading } = useAxisValues(
    LABEL_AXIS_TO_HEARTBEAT[axis],
    true,
  );
  return (
    <div className="grid grid-cols-[auto_1fr] gap-2">
      <div>
        <Label className="text-xs">Axis</Label>
        <AxisPicker value={axis} onChange={onAxisChange} />
      </div>
      <div>
        <Label className="text-xs">Value</Label>
        <div className="mt-1">
          <Combobox
            options={options}
            value={valueField || null}
            onSelect={onValueChange}
            placeholder="Pick or type a value"
            creatable
            loading={isLoading}
            fullWidth
          />
        </div>
      </div>
    </div>
  );
}

/** Editable list of strings — chip-style with an add-row combobox that
 *  autocompletes off the same axis. */
function StringListField({
  label,
  axis,
  values,
  onChange,
}: {
  label: string;
  axis: Axis;
  values: string[];
  onChange: (v: string[]) => void;
}) {
  const { options, isLoading } = useAxisValues(
    LABEL_AXIS_TO_HEARTBEAT[axis],
    true,
  );
  const [pending, setPending] = useState<string>("");
  return (
    <div>
      <Label className="text-xs">{label}</Label>
      <div className="mt-1 flex flex-wrap gap-1">
        {values.map((v, i) => (
          <span
            key={i}
            className="inline-flex items-center gap-1 rounded border border-border bg-muted px-2 py-0.5 text-[11px]"
          >
            <span className="font-mono">{v || "(empty)"}</span>
            <button
              type="button"
              className="text-muted-foreground hover:text-destructive"
              onClick={() =>
                onChange(values.filter((_, idx) => idx !== i))
              }
              aria-label={`Remove ${v}`}
            >
              <X className="h-3 w-3" />
            </button>
          </span>
        ))}
      </div>
      <div className="mt-2 flex items-center gap-2">
        <div className="min-w-[220px]">
          <Combobox
            options={options}
            value={pending || null}
            onSelect={(v) => {
              setPending("");
              if (v && !values.includes(v)) onChange([...values, v]);
            }}
            placeholder="Add a value"
            creatable
            loading={isLoading}
            fullWidth
          />
        </div>
      </div>
    </div>
  );
}

/** 24-cell hour-of-day toggle grid. Compact, keyboard-usable. */
function HourOfDayPicker({
  hours,
  onChange,
}: {
  hours: number[];
  onChange: (h: number[]) => void;
}) {
  const set = new Set(hours);
  const toggle = (h: number) => {
    const next = new Set(set);
    if (next.has(h)) next.delete(h);
    else next.add(h);
    onChange(Array.from(next).sort((a, b) => a - b));
  };
  return (
    <div>
      <Label className="text-xs">Hours of day (multi-select)</Label>
      <div className="mt-1 grid grid-cols-8 gap-1 sm:grid-cols-12">
        {Array.from({ length: 24 }, (_, h) => (
          <Button
            key={h}
            type="button"
            size="sm"
            variant={set.has(h) ? "default" : "outline"}
            className="h-7 px-1 font-mono text-[10px]"
            onClick={() => toggle(h)}
          >
            {String(h).padStart(2, "0")}
          </Button>
        ))}
      </div>
    </div>
  );
}

/** 7-cell day-of-week toggle. Sun..Sat mapped to 0..6 matching the DSL. */
function DowPicker({
  dow,
  onChange,
}: {
  dow: number[];
  onChange: (d: number[]) => void;
}) {
  const set = new Set(dow);
  const toggle = (d: number) => {
    const next = new Set(set);
    if (next.has(d)) next.delete(d);
    else next.add(d);
    onChange(Array.from(next).sort((a, b) => a - b));
  };
  return (
    <div>
      <Label className="text-xs">Days of week (multi-select)</Label>
      <div className="mt-1 flex gap-1">
        {DOW_LABELS.map((name, d) => (
          <Button
            key={d}
            type="button"
            size="sm"
            variant={set.has(d) ? "default" : "outline"}
            className="h-7 px-2 font-mono text-[10px]"
            onClick={() => toggle(d)}
          >
            {name}
          </Button>
        ))}
      </div>
    </div>
  );
}
