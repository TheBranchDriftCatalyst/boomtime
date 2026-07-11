import { Label } from "@/components/ui/label";
import { cn } from "@/lib/utils";

interface MatchTypeToggleProps<M extends string> {
  /** Ordered modes to offer (e.g. ["exact", "regex"] or with "template"). */
  modes: readonly M[];
  /** Display label per mode. */
  labels: Record<M, string>;
  value: M;
  onChange: (mode: M) => void;
  label?: string;
}

/**
 * Shared segmented match-strategy toggle ("Exact | Regex | …"), generic over
 * the mode union so RemappingForm (3 modes) and SpaceRuleForm (2 modes) render
 * the byte-identical control from one implementation.
 */
export function MatchTypeToggle<M extends string>({
  modes,
  labels,
  value,
  onChange,
  label = "Match",
}: MatchTypeToggleProps<M>) {
  return (
    <div className="space-y-1">
      <Label className="text-xs">{label}</Label>
      <div className="inline-flex h-8 items-center rounded-md border p-0.5">
        {modes.map((m) => (
          <button
            key={m}
            type="button"
            aria-pressed={value === m}
            onClick={() => onChange(m)}
            className={cn(
              "h-full rounded px-2 text-xs font-medium transition-colors",
              value === m
                ? "bg-primary text-primary-foreground"
                : "text-muted-foreground hover:text-foreground",
            )}
          >
            {labels[m]}
          </button>
        ))}
      </div>
    </div>
  );
}
