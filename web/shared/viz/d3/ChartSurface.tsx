import type { CSSProperties, ReactNode } from "react";
import type { D3Surface } from "./useD3Surface";

/**
 * Standard chart wrapper for a `useD3Surface` chart: a relative, full-width
 * div (the tooltip's positioning context) hosting the svg. `style` merges
 * over the defaults (ContributionCalendar's scroll/center wrapper);
 * `children` render after the svg (Punchcard's UTC note).
 */
export function ChartSurface({
  surface,
  style,
  children,
}: {
  surface: D3Surface;
  style?: CSSProperties;
  children?: ReactNode;
}) {
  return (
    <div
      ref={surface.ref}
      style={{
        position: "relative",
        width: "100%",
        height: surface.height,
        ...style,
      }}
    >
      <svg ref={surface.svgRef} />
      {children}
    </div>
  );
}
