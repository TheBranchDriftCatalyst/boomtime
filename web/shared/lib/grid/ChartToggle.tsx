// ChartToggle — segmented mode-indicator for widget dual-view toggles.
// Belongs to the isolated grid primitive folder. Style is CSS-var driven
// (see grid.css) — no boomtime-domain tokens leak in.
import { BarChart3, Grid3x3, PieChart, Tag, type LucideIcon } from "lucide-react";

// Small icon resolver — keeps ChartToggle's props JSON-serializable in the
// catalog (icon: string). Add a lucide icon here as new views ship.
const ICONS: Record<string, LucideIcon> = {
  PieChart,
  BarChart3,
  Grid3x3,
  Tag,
};

export interface ChartToggleView {
  id: string;
  label: string;
  icon?: string;
}

export interface ChartToggleProps {
  views: ChartToggleView[];
  value: string;
  onChange: (next: string) => void;
}

export function ChartToggle({ views, value, onChange }: ChartToggleProps) {
  if (!views || views.length < 2) return null;
  return (
    <div className="catalyst-grid-toggle no-drag" role="group" aria-label="Chart view">
      {views.map((v) => {
        const Icon = v.icon ? ICONS[v.icon] : undefined;
        const active = v.id === value;
        return (
          <button
            key={v.id}
            type="button"
            className={`catalyst-grid-toggle__pill${active ? " catalyst-grid-toggle__pill--active" : ""}`}
            onClick={() => onChange(v.id)}
            aria-pressed={active}
            title={v.label}
            data-testid={`chart-toggle-${v.id}`}
          >
            {Icon && <Icon size={11} strokeWidth={2} />}
            <span className="catalyst-grid-toggle__label">{v.label}</span>
          </button>
        );
      })}
    </div>
  );
}
