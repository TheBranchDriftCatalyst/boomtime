interface LabeledStatProps {
  label: string;
  value: string | number;
}

/** Small label-over-value stat cell used in panels and detail grids. */
export function LabeledStat({ label, value }: LabeledStatProps) {
  return (
    <div>
      <p className="text-xs uppercase tracking-wide text-muted-foreground">
        {label}
      </p>
      <p className="mt-0.5 font-mono text-sm font-medium">{value}</p>
    </div>
  );
}
