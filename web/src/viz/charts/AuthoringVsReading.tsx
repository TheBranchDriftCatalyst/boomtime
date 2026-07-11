import { useMemo } from "react";
import * as d3 from "d3";
import { secondsToHms } from "@/lib/utils";
import { cssVar } from "@/viz/d3/useChartFrame";
import { useD3Surface } from "@/viz/d3/useD3Surface";
import { ChartSurface } from "@/viz/d3/ChartSurface";
import { tooltipHtml } from "@/viz/d3/tooltip";
import { formatDay, styleAxis } from "@/viz/d3/axes";
import { colorAt } from "@/viz/d3/color";
import { EmptyChart } from "@/viz/d3/EmptyChart";

interface AuthoringVsReadingProps {
  writeSeconds: number | undefined;
  readSeconds: number | undefined;
  // Bucketed write-ratio series (0..1) aligned to `dates`.
  dates: string[];
  ratio: number[];
  height?: number;
}

const WRITE_COLOR = colorAt(2); // green-ish
const READ_COLOR = colorAt(0); // blue

/** Donut of authoring vs reading time + a small write-ratio-over-time line. */
export function AuthoringVsReading({
  writeSeconds,
  readSeconds,
  dates,
  ratio,
  height = 300,
}: AuthoringVsReadingProps) {
  const write = writeSeconds ?? 0;
  const read = readSeconds ?? 0;
  const hasData =
    (writeSeconds !== undefined || readSeconds !== undefined) &&
    write + read > 0;

  const slices = useMemo(
    () => [
      { label: "Authoring", value: write, color: WRITE_COLOR },
      { label: "Reading", value: read, color: READ_COLOR },
    ],
    [write, read],
  );

  const surface = useD3Surface(
    { height },
    ({ g, width, showTip, hideTip }) => {
      if (!hasData) return;

      const fg = cssVar("--foreground");
      const card = cssVar("--card");
      const border = cssVar("--border");
      const muted = cssVar("--muted-foreground");

      // Donut occupies the left ~45%; the ratio line the right ~55%.
      const donutW = Math.min(width * 0.45, 220);
      const radius = Math.min(donutW, height) / 2 - 8;
      const cx = donutW / 2;
      const cy = height / 2;

      const donut = g.append("g").attr("transform", `translate(${cx},${cy})`);

      const total = write + read;
      const pie = d3
        .pie<{ label: string; value: number; color: string }>()
        .sort(null)
        .value((d) => d.value);
      const arc = d3
        .arc<d3.PieArcDatum<{ label: string; value: number; color: string }>>()
        .innerRadius(radius * 0.58)
        .outerRadius(radius);

      donut
        .selectAll("path")
        .data(pie(slices))
        .join("path")
        .attr("d", arc)
        .attr("fill", (d) => d.data.color)
        .attr("stroke", card)
        .attr("stroke-width", 1)
        .on("mousemove", (event, d) => {
          showTip(
            event,
            tooltipHtml(
              d.data.label,
              `${secondsToHms(d.data.value)} (${Math.round((d.data.value / total) * 100)}%)`,
            ),
          );
        })
        .on("mouseleave", hideTip);

      // Center label = authoring share.
      donut
        .append("text")
        .attr("text-anchor", "middle")
        .attr("dy", "-0.1em")
        .attr("fill", fg)
        .style("font-size", "20px")
        .style("font-weight", "700")
        .text(`${Math.round((write / total) * 100)}%`);
      donut
        .append("text")
        .attr("text-anchor", "middle")
        .attr("dy", "1.3em")
        .attr("fill", muted)
        .style("font-size", "10px")
        .text("authoring");

      // Right: write-ratio-over-time line (0..1).
      const lineData = dates.map((d, i) => ({ date: new Date(d), r: ratio[i] ?? 0 }));
      if (lineData.length >= 2) {
        const lm = { top: 12, right: 12, bottom: 22, left: 30 };
        const lx0 = donutW + 8;
        const innerW = width - lx0 - lm.right;
        const innerH = height - lm.top - lm.bottom;
        const lg = g.append("g").attr("transform", `translate(${lx0},${lm.top})`);

        const x = d3
          .scaleTime()
          .domain(d3.extent(lineData, (d) => d.date) as [Date, Date])
          .range([0, innerW]);
        const y = d3.scaleLinear().domain([0, 1]).range([innerH, 0]);

        styleAxis(
          lg
            .append("g")
            .call(
              d3
                .axisLeft(y)
                .ticks(3)
                .tickFormat((v) => `${Math.round(Number(v) * 100)}%`),
            ),
          { fg: muted },
          { fontSize: "10px" },
        );
        styleAxis(
          lg
            .append("g")
            .attr("transform", `translate(0,${innerH})`)
            .call(
              d3.axisBottom(x).ticks(4).tickFormat((d) => formatDay(d as Date)),
            ),
          { fg: muted, border },
          { domain: "line", fontSize: "10px" },
        );

        const line = d3
          .line<{ date: Date; r: number }>()
          .x((d) => x(d.date))
          .y((d) => y(d.r))
          .curve(d3.curveMonotoneX);
        lg.append("path")
          .datum(lineData)
          .attr("d", line)
          .attr("fill", "none")
          .attr("stroke", WRITE_COLOR)
          .attr("stroke-width", 2);
        lg.append("text")
          .attr("x", 0)
          .attr("y", -2)
          .attr("fill", muted)
          .style("font-size", "10px")
          .text("Authoring ratio over time");
      }
    },
    [hasData, slices, write, read, dates, ratio],
  );

  if (!hasData) return <EmptyChart height={height} />;

  return <ChartSurface surface={surface} />;
}
