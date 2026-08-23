// HoloGradeBadge — the RANK grade badge with a holographic-foil sheen
// (boom-174.6). Pointer-reactive: the badge tilts toward the cursor and an
// iridescent foil + glare track the pointer. A faint idle shimmer keeps it
// alive without a cursor (skipped under prefers-reduced-motion via CSS).
//
// Deliberately NOT WebGL — a pointer-driven CSS treatment is lighter (zero
// bundle cost), works on every device, and reads better at this small size
// than a 3D scene would. Preserves data-testid="grade-badge-letter".
import { useCallback, useRef } from "react";
import { computeGrade } from "@shared/features/publicprofile/grade";
import type { PublicDashboardPayload } from "@shared/types/stats";
import "./HoloGradeBadge.css";

export function HoloGradeBadge({ data }: { data: PublicDashboardPayload }) {
  const grade = computeGrade(data);
  const pctStr = `${Math.round(grade.percentile)}th`;
  const ref = useRef<HTMLDivElement>(null);

  const onMove = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    const el = ref.current;
    if (!el) return;
    const r = el.getBoundingClientRect();
    const x = Math.min(1, Math.max(0, (e.clientX - r.left) / r.width));
    const y = Math.min(1, Math.max(0, (e.clientY - r.top) / r.height));
    el.style.setProperty("--mx", x.toFixed(3));
    el.style.setProperty("--my", y.toFixed(3));
    // Tilt toward the cursor (top = tilt back, right = tilt right).
    el.style.setProperty("--rx", ((0.5 - y) * 14).toFixed(2) + "deg");
    el.style.setProperty("--ry", ((x - 0.5) * 16).toFixed(2) + "deg");
  }, []);

  const reset = useCallback(() => {
    const el = ref.current;
    if (!el) return;
    el.style.setProperty("--rx", "0deg");
    el.style.setProperty("--ry", "0deg");
    el.style.setProperty("--mx", "0.5");
    el.style.setProperty("--my", "0.5");
  }, []);

  return (
    <div
      ref={ref}
      className="holo-badge"
      onPointerMove={onMove}
      onPointerLeave={reset}
      data-testid="grade-badge"
    >
      <div className="holo-badge__inner">
        <div className="holo-badge__rank">&gt; RANK</div>
        <div
          className="holo-badge__letter"
          data-letter={grade.level}
          data-testid="grade-badge-letter"
        >
          {grade.level}
        </div>
        <div className="holo-badge__pct">{pctStr} PERCENTILE</div>
        <span className="holo-badge__foil" aria-hidden />
        <span className="holo-badge__glare" aria-hidden />
      </div>
    </div>
  );
}
