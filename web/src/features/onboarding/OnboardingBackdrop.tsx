import type { CSSProperties, ReactNode } from "react";
import "@/features/onboarding/onboarding.css";

// Decorative floating chart-widgets behind the onboarding card (gaka-93f.1.2).
//
// A first-time visitor should SEE what boomtime is before reading a word:
// askew, softly-drifting mini dashboards — bars, a momentum area, a punchcard,
// a language donut, a streak strip, a grade ring — scattered around the edges
// with parallax depth. Everything is:
//   • pure inline SVG (no data fetch, no d3 mount) → cheap + instant
//   • themed via the live --chart-1..12 / --primary tokens → matches whatever
//     theme the visitor has (arasaka, synthwave, dracula, …) automatically
//   • deterministic (seeded PRNG) → stable layout, no reflow jitter, SSR-safe
//   • pointer-events-none + reduced-motion aware (see onboarding.css)

// ── seeded PRNG (mulberry32) — deterministic demo data ───────────────────────
function mulberry32(seed: number) {
  return function () {
    seed |= 0;
    seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
// One generator per widget keeps each chart's shape stable + independent.
const seeded = (s: number) => {
  const r = mulberry32(s);
  return {
    r,
    series: (n: number, min: number, max: number) =>
      Array.from({ length: n }, () => min + r() * (max - min)),
  };
};

const chart = (i: number) => `var(--chart-${(i % 12) + 1})`;

// ── mini charts ──────────────────────────────────────────────────────────────

function Bars() {
  const { series } = seeded(7);
  const vals = series(7, 0.25, 1);
  const w = 132,
    h = 66,
    gap = 5;
  const bw = (w - gap * (vals.length - 1)) / vals.length;
  const max = Math.max(...vals);
  return (
    <svg viewBox={`0 0 ${w} ${h}`} width="100%" height="66">
      {vals.map((v, i) => {
        const bh = (v / max) * (h - 6);
        return (
          <rect
            key={i}
            x={i * (bw + gap)}
            y={h - bh}
            width={bw}
            height={bh}
            rx={2}
            fill={chart(i)}
          />
        );
      })}
    </svg>
  );
}

function AreaMomentum() {
  const { series } = seeded(23);
  const vals = series(16, 0.2, 1);
  const w = 148,
    h = 62;
  const max = Math.max(...vals);
  const pts = vals.map((v, i) => {
    const x = (i / (vals.length - 1)) * w;
    const y = h - 4 - (v / max) * (h - 12);
    return [x, y] as const;
  });
  const line = pts.map(([x, y], i) => `${i ? "L" : "M"}${x.toFixed(1)},${y.toFixed(1)}`).join(" ");
  const area = `${line} L${w},${h} L0,${h} Z`;
  return (
    <svg viewBox={`0 0 ${w} ${h}`} width="100%" height="62">
      <defs>
        <linearGradient id="ob-area" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor="var(--primary)" stopOpacity="0.35" />
          <stop offset="100%" stopColor="var(--primary)" stopOpacity="0" />
        </linearGradient>
      </defs>
      <path d={area} fill="url(#ob-area)" />
      <path d={line} fill="none" stroke="var(--primary)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
      {pts.map(([x, y], i) => (
        <circle key={i} cx={x} cy={y} r={i === pts.length - 1 ? 3 : 0} fill="var(--primary)" />
      ))}
    </svg>
  );
}

function Punchcard() {
  const { r } = seeded(41);
  const cols = 14,
    rows = 6,
    cell = 11,
    pad = 3;
  const nodes: ReactNode[] = [];
  for (let y = 0; y < rows; y++) {
    for (let x = 0; x < cols; x++) {
      const v = Math.pow(r(), 1.6); // skew toward small so a few pop
      nodes.push(
        <circle
          key={`${x}-${y}`}
          cx={pad + x * cell + cell / 2}
          cy={pad + y * cell + cell / 2}
          r={1 + v * 4}
          fill={chart(3)}
          opacity={0.35 + v * 0.65}
        />,
      );
    }
  }
  return (
    <svg viewBox={`0 0 ${pad * 2 + cols * cell} ${pad * 2 + rows * cell}`} width="100%" height="72">
      {nodes}
    </svg>
  );
}

function Donut() {
  const { series } = seeded(59);
  const raw = series(5, 0.4, 1);
  const total = raw.reduce((a, b) => a + b, 0);
  const R = 26,
    C = 2 * Math.PI * R;
  let off = 0;
  return (
    <svg viewBox="0 0 72 72" width="72" height="72">
      <g transform="translate(36 36) rotate(-90)">
        <circle r={R} fill="none" stroke="var(--muted)" strokeOpacity="0.25" strokeWidth="10" />
        {raw.map((v, i) => {
          const frac = v / total;
          const dash = frac * C;
          const el = (
            <circle
              key={i}
              r={R}
              fill="none"
              stroke={chart(i)}
              strokeWidth="10"
              strokeDasharray={`${dash} ${C - dash}`}
              strokeDashoffset={-off}
            />
          );
          off += dash;
          return el;
        })}
      </g>
    </svg>
  );
}

function StatSpark() {
  const { series } = seeded(83);
  const vals = series(20, 0.3, 1);
  const w = 140,
    h = 34;
  const max = Math.max(...vals);
  const line = vals
    .map((v, i) => {
      const x = (i / (vals.length - 1)) * w;
      const y = h - 2 - (v / max) * (h - 6);
      return `${i ? "L" : "M"}${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(" ");
  return (
    <div>
      <div className="flex items-baseline gap-1.5">
        <span className="text-2xl font-semibold tabular-nums text-foreground">42h</span>
        <span className="text-xs font-medium text-[color:var(--chart-4)]">▲ 18%</span>
      </div>
      <svg viewBox={`0 0 ${w} ${h}`} width="100%" height="30" className="mt-1">
        <path d={line} fill="none" stroke={chart(4)} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
      </svg>
    </div>
  );
}

function StreakStrip() {
  const { r } = seeded(101);
  const cols = 15,
    rows = 5,
    cell = 12,
    gap = 3;
  const nodes: ReactNode[] = [];
  for (let y = 0; y < rows; y++) {
    for (let x = 0; x < cols; x++) {
      const v = r();
      nodes.push(
        <rect
          key={`${x}-${y}`}
          x={x * (cell + gap)}
          y={y * (cell + gap)}
          width={cell}
          height={cell}
          rx={2.5}
          fill={v > 0.72 ? chart(1) : v > 0.45 ? chart(0) : "var(--muted)"}
          opacity={v > 0.45 ? 0.55 + v * 0.45 : 0.3}
        />,
      );
    }
  }
  const wpx = cols * (cell + gap) - gap;
  const hpx = rows * (cell + gap) - gap;
  return (
    <svg viewBox={`0 0 ${wpx} ${hpx}`} width="100%" height="72">
      {nodes}
    </svg>
  );
}

function GradeRing() {
  const R = 26,
    C = 2 * Math.PI * R,
    pct = 0.86;
  return (
    <svg viewBox="0 0 72 72" width="72" height="72">
      <g transform="translate(36 36) rotate(-90)">
        <circle r={R} fill="none" stroke="var(--muted)" strokeOpacity="0.25" strokeWidth="7" />
        <circle
          r={R}
          fill="none"
          stroke="var(--primary)"
          strokeWidth="7"
          strokeLinecap="round"
          strokeDasharray={`${pct * C} ${C}`}
        />
      </g>
      <text x="36" y="36" textAnchor="middle" dominantBaseline="central" className="fill-foreground" fontSize="20" fontWeight="700">
        A−
      </text>
    </svg>
  );
}

function HBars() {
  const { series } = seeded(127);
  const vals = series(4, 0.35, 1);
  const max = Math.max(...vals);
  return (
    <div className="space-y-2">
      {vals.map((v, i) => (
        <div key={i} className="flex items-center gap-2">
          <span className="h-1.5 w-1.5 rounded-full" style={{ background: chart(i) }} />
          <div className="h-2 flex-1 overflow-hidden rounded-full bg-[color:var(--muted)]/30">
            <div className="h-full rounded-full" style={{ width: `${(v / max) * 100}%`, background: chart(i) }} />
          </div>
        </div>
      ))}
    </div>
  );
}

// ── widget card + floating wrapper ──────────────────────────────────────────

function WidgetCard({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="rounded-xl border border-border/60 bg-card/70 p-3 shadow-xl shadow-black/20 backdrop-blur-md">
      <div className="mb-1.5 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground/80">
        {title}
      </div>
      {children}
    </div>
  );
}

interface FloatConf {
  /** position (any of top/left/right/bottom in %) */
  pos: CSSProperties;
  rot: number;
  scale: number;
  opacity: number;
  blur: number;
  dur: number;
  delay: number;
  reveal: number;
  /** hide below this breakpoint to avoid crowding small viewports */
  showAt?: "always" | "md" | "lg" | "xl";
  alt?: boolean;
  width: number;
}

function Floating({ conf, children }: { conf: FloatConf; children: ReactNode }) {
  const hide =
    conf.showAt === "md"
      ? "hidden md:block"
      : conf.showAt === "lg"
        ? "hidden lg:block"
        : conf.showAt === "xl"
          ? "hidden xl:block"
          : "";
  return (
    <div
      className={`ob-widget absolute ${hide}`}
      style={
        {
          ...conf.pos,
          width: conf.width,
          transform: `rotate(${conf.rot}deg) scale(${conf.scale})`,
          filter: conf.blur ? `blur(${conf.blur}px)` : undefined,
          "--ob-opacity": conf.opacity,
          "--ob-reveal-delay": `${conf.reveal}ms`,
        } as CSSProperties
      }
    >
      <div
        className={`ob-bob ${conf.alt ? "ob-bob--alt" : ""}`}
        style={{ ["--ob-dur" as string]: `${conf.dur}s`, ["--ob-delay" as string]: `${conf.delay}s` }}
      >
        {children}
      </div>
    </div>
  );
}

// Curated placement: hug the edges/corners, leave the vertical center lane
// clear for the onboarding card. Nearer cards are sharper + more opaque;
// farther ones blur + fade for depth.
const WIDGETS: Array<{ conf: FloatConf; title: string; chart: ReactNode }> = [
  {
    title: "Top Languages",
    chart: <Bars />,
    conf: { pos: { top: "8%", left: "6%" }, rot: -7, scale: 1, opacity: 0.85, blur: 0, dur: 9, delay: 0, reveal: 80, width: 168, showAt: "md" },
  },
  {
    title: "Languages",
    chart: <Donut />,
    conf: { pos: { top: "12%", right: "8%" }, rot: 8, scale: 1, opacity: 0.8, blur: 0, dur: 11, delay: 0.6, reveal: 220, width: 120, alt: true, showAt: "md" },
  },
  {
    title: "This Week",
    chart: <AreaMomentum />,
    conf: { pos: { top: "40%", left: "3%" }, rot: 5, scale: 1.05, opacity: 0.9, blur: 0, dur: 10, delay: 1.1, reveal: 360, width: 184, showAt: "lg" },
  },
  {
    title: "Punchcard",
    chart: <Punchcard />,
    conf: { pos: { top: "44%", right: "4%" }, rot: -6, scale: 1.05, opacity: 0.9, blur: 0, dur: 12, delay: 0.3, reveal: 300, width: 190, alt: true, showAt: "lg" },
  },
  {
    title: "Focus",
    chart: <StatSpark />,
    conf: { pos: { bottom: "10%", right: "10%" }, rot: 6, scale: 1, opacity: 0.82, blur: 0, dur: 9.5, delay: 1.4, reveal: 480, width: 176, showAt: "md" },
  },
  {
    title: "Streak",
    chart: <StreakStrip />,
    conf: { pos: { bottom: "8%", left: "7%" }, rot: -5, scale: 1, opacity: 0.8, blur: 0, dur: 11.5, delay: 0.9, reveal: 420, width: 190, alt: true, showAt: "md" },
  },
  {
    title: "Grade",
    chart: <GradeRing />,
    conf: { pos: { top: "68%", left: "22%" }, rot: 9, scale: 0.92, opacity: 0.55, blur: 1.2, dur: 13, delay: 1.8, reveal: 560, width: 116, showAt: "xl" },
  },
  {
    title: "Leaderboard",
    chart: <HBars />,
    conf: { pos: { top: "22%", left: "30%" }, rot: -8, scale: 0.9, opacity: 0.5, blur: 1.4, dur: 12.5, delay: 0.5, reveal: 640, width: 150, alt: true, showAt: "xl" },
  },
];

export function OnboardingBackdrop() {
  return (
    <div aria-hidden className="pointer-events-none absolute inset-0 overflow-hidden">
      {/* atmospheric wash: a soft radial bloom from the primary color + a
          vignette so the edges (and the widgets) sink behind the card. */}
      <div
        className="absolute inset-0"
        style={{
          background:
            "radial-gradient(120% 90% at 50% 30%, color-mix(in oklab, var(--primary) 14%, transparent), transparent 60%)",
        }}
      />
      {WIDGETS.map((w, i) => (
        <Floating key={i} conf={w.conf}>
          <WidgetCard title={w.title}>{w.chart}</WidgetCard>
        </Floating>
      ))}
      {/* top+bottom fade to keep the composition grounded */}
      <div
        className="absolute inset-0"
        style={{
          background:
            "linear-gradient(180deg, color-mix(in oklab, var(--background) 40%, transparent) 0%, transparent 22%, transparent 78%, color-mix(in oklab, var(--background) 55%, transparent) 100%)",
        }}
      />
    </div>
  );
}
