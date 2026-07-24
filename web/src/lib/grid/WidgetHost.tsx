// WidgetHost — forwardRef wrapper handling react-grid-layout's injection
// contract (style, className, onMouseDown/Up, onTouchEnd, ref, children).
// Mirrors hakboard's WidgetHost verbatim shape so the two implementations
// stay copy-pastable while the primitive graduates to catalyst-ui.
//
// Chrome (corner ticks, terminal header) is applied via CSS classes and is
// STYLE-agnostic: the primitive uses semantic class names and reads all
// colors/fonts from CSS custom properties (see grid.css). Consumers set
// `--grid-tile-*` vars in their own scope.
import { forwardRef, type CSSProperties, type ReactNode } from "react";
import { X } from "lucide-react";
import { useMeasuredSize } from "./useMeasuredSize";
import type { WidgetInstance } from "./types";

// The isolated ChartToggle lives alongside — a tiny segmented control.
// Keeping it inside the primitive folder is fine; it's a pure UI helper
// and doesn't leak boomtime types.
import { ChartToggle } from "./ChartToggle";

export interface WidgetHostProps {
  tileIndex: number;
  instance: WidgetInstance;
  view?: string;
  editable: boolean;
  onViewChange: (v: string) => void;
  onRemove?: () => void;
  // ⬇ react-grid-layout injection
  style?: CSSProperties;
  className?: string;
  onMouseDown?: React.MouseEventHandler<HTMLElement>;
  onMouseUp?: React.MouseEventHandler<HTMLElement>;
  onTouchEnd?: React.TouchEventHandler<HTMLElement>;
  children?: ReactNode;
}

export const WidgetHost = forwardRef<HTMLDivElement, WidgetHostProps>(function WidgetHost(
  { tileIndex, instance, view, editable, onViewChange, onRemove, children, ...gridProps },
  ref,
) {
  const [measuredRef, size] = useMeasuredSize<HTMLDivElement>();

  // Compose refs (react-grid-layout owns one, we need one for measurement).
  const setRefs = (node: HTMLDivElement | null) => {
    measuredRef.current = node;
    if (typeof ref === "function") ref(node);
    else if (ref) ref.current = node;
  };

  const hasViews = (instance.views?.length ?? 0) > 1;
  const currentView = view ?? instance.defaultView ?? instance.views?.[0]?.id;

  return (
    <div
      {...gridProps}
      ref={setRefs}
      className={`catalyst-grid-tile ${gridProps.className ?? ""}`}
      style={{
        ...(gridProps.style ?? {}),
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        ["--tile-index" as any]: tileIndex,
      }}
      data-widget-key={instance.key}
    >
      <span className="catalyst-grid-tile__corner catalyst-grid-tile__corner--tl" aria-hidden>┌</span>
      <span className="catalyst-grid-tile__corner catalyst-grid-tile__corner--tr" aria-hidden>┐</span>
      <span className="catalyst-grid-tile__corner catalyst-grid-tile__corner--bl" aria-hidden>└</span>
      <span className="catalyst-grid-tile__corner catalyst-grid-tile__corner--br" aria-hidden>┘</span>

      <header className="catalyst-grid-tile__header">
        <span className="catalyst-grid-tile__prompt" aria-hidden>▎</span>
        <span className="catalyst-grid-tile__title">
          {(instance.displayName ?? instance.key).toUpperCase()}
        </span>
        <span className="catalyst-grid-tile__kind" aria-hidden>· {instance.key}</span>
        <span className="catalyst-grid-tile__spacer" />
        {editable ? (
          <button
            type="button"
            onClick={onRemove}
            aria-label={`Remove ${instance.displayName ?? instance.key}`}
            className="catalyst-grid-tile__remove no-drag"
          >
            <X size={12} />
          </button>
        ) : (
          hasViews && (
            <ChartToggle
              views={instance.views!}
              value={currentView ?? instance.views![0].id}
              onChange={onViewChange}
            />
          )
        )}
      </header>

      <div className="catalyst-grid-tile__body">
        {instance.render({ view: currentView, width: size.width, height: size.height })}
      </div>

      <span className="catalyst-grid-tile__id" aria-hidden>#{instance.key}</span>
      {children /* RGL injects the resize handle here as its last child */}
    </div>
  );
});
