import { Combobox } from "@/components/ui/combobox";
import { Input } from "@thebranchdriftcatalyst/catalyst-ui/ui/input";
import { Label } from "@thebranchdriftcatalyst/catalyst-ui/ui/label";
import { axisLabel } from "@/lib/axes";
import { useAxisValues } from "@/features/rules/useAxisValues";
import { cn } from "@/lib/utils";
import type { HeartbeatAxis } from "@/types/api";

interface AxisValueFieldProps {
  /** Axis whose real values feed the exact-mode autocomplete + search texts. */
  axis: HeartbeatAxis;
  /** true = exact mode (creatable Combobox of real values); false = regex Input. */
  exact: boolean;
  value: string;
  onChange: (value: string) => void;
  label: string;
  placeholder: string;
  /** Wrapper classes (layout variants). */
  className?: string;
}

/**
 * Shared pattern/value field for rule forms, switched on the match mode:
 * exact → a creatable Combobox wired to useAxisValues (real values + counts);
 * regex → a plain mono Input. Used by RemappingForm and SpaceRuleForm.
 */
export function AxisValueField({
  axis,
  exact,
  value,
  onChange,
  label,
  placeholder,
  className,
}: AxisValueFieldProps) {
  // React Query dedupes this against any sibling useAxisValues(axis) call
  // (e.g. RemappingForm's client-side preview), so owning it here is free.
  const { options, isLoading } = useAxisValues(axis);
  const axisName = axisLabel(axis).toLowerCase();

  return (
    <div className={cn("space-y-1", className)}>
      <Label className="text-xs">{label}</Label>
      {exact ? (
        <Combobox
          options={options}
          value={value || null}
          onSelect={onChange}
          loading={isLoading}
          creatable
          placeholder={placeholder}
          searchPlaceholder={`Search ${axisName}s…`}
          emptyText={`No ${axisName} values found.`}
          className="h-8 font-mono"
        />
      ) : (
        <Input
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={placeholder}
          className="h-8 font-mono"
        />
      )}
    </div>
  );
}
