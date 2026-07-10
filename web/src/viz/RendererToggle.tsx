import { useRenderer, type Renderer } from "@/viz/rendererContext";
import { cn } from "@/lib/utils";

/**
 * Segmented control that flips the whole dashboard between the ApexCharts and
 * raw-D3 renderers. Mounted in the AppShell topbar next to the ThemeToggle.
 */
export function RendererToggle() {
  const { renderer, setRenderer } = useRenderer();

  const options: { value: Renderer; label: string }[] = [
    { value: "apex", label: "Apex" },
    { value: "d3", label: "D3" },
  ];

  return (
    <div
      role="group"
      aria-label="Chart renderer"
      className="inline-flex h-9 items-center rounded-md border border-input bg-background p-0.5 shadow-sm"
    >
      {options.map((opt) => {
        const active = renderer === opt.value;
        return (
          <button
            key={opt.value}
            type="button"
            onClick={() => setRenderer(opt.value)}
            aria-pressed={active}
            title={`Render charts with ${opt.label}`}
            className={cn(
              "inline-flex h-8 items-center rounded px-3 text-xs font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
              active
                ? "bg-primary text-primary-foreground shadow-sm"
                : "text-muted-foreground hover:text-foreground",
            )}
          >
            {opt.label}
          </button>
        );
      })}
    </div>
  );
}
