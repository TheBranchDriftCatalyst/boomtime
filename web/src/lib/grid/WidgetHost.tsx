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
  /** Opaque per-widget config blob (gaka-lzr), forwarded to instance.render.
   * The primitive reads exactly ONE key out of it generically — `title`, a
   * string override for the header label — everything else stays opaque and
   * is only round-tripped to instance.render. */
  config?: Record<string, unknown>;
  editable: boolean;
  /** Edit-mode selection state — drives `data-selected` styling. */
  selected?: boolean;
  /** "Hide but keep placement" (gaka-lzr Phase 5). Only ever true in EDIT
   * mode — DraggableGridLayout drops hidden tiles from the render entirely
   * in preview, so this prop exists purely to dim + label the tile while
   * editing so the operator can find it again to un-hide. */
  hidden?: boolean;
  /** Fired when the tile header is clicked in edit mode (select this tile). */
  onSelect?: () => void;
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
  {
    tileIndex,
    instance,
    view,
    config,
    editable,
    selected,
    hidden,
    onSelect,
    onViewChange,
    onRemove,
    children,
    ...gridProps
  },
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
  // gaka-lzr Phase 5: a per-tile title override lives at `config.title` — the
  // ONE key this generic primitive reads out of the otherwise-opaque config
  // blob (see the CONFIGURE form in boomtime's DashboardEditSidebar).
  const titleOverride =
    typeof config?.title === "string" && config.title.trim().length > 0
      ? config.title
      : undefined;

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
      data-selected={selected || undefined}
      data-hidden={hidden || undefined}
    >
      <span className="catalyst-grid-tile__corner catalyst-grid-tile__corner--tl" aria-hidden>┌</span>
      <span className="catalyst-grid-tile__corner catalyst-grid-tile__corner--tr" aria-hidden>┐</span>
      <span className="catalyst-grid-tile__corner catalyst-grid-tile__corner--bl" aria-hidden>└</span>
      <span className="catalyst-grid-tile__corner catalyst-grid-tile__corner--br" aria-hidden>┘</span>

      <header
        className="catalyst-grid-tile__header"
        // Click (not drag) selects the tile in edit mode (gaka-lzr). RGL still
        // starts a drag on mousedown+move; a plain click falls through here.
        onClick={editable && onSelect ? onSelect : undefined}
      >
        <span className="catalyst-grid-tile__prompt" aria-hidden>▎</span>
        <span className="catalyst-grid-tile__title">
          {(titleOverride ?? instance.displayName ?? instance.key).toUpperCase()}
        </span>
        {/* Kind slug is developer-relevant in edit mode (helps operators
            identify which tile is which when the display name isn't unique).
            In view mode (public profile, non-editable) it's just visual
            noise duplicating the title, so hide it. */}
        {editable && (
          <span className="catalyst-grid-tile__kind" aria-hidden>· {instance.key}</span>
        )}
        {editable && hidden && (
          <span className="catalyst-grid-tile__hidden-badge" data-testid="tile-hidden-badge">
            hidden
          </span>
        )}
        <span className="catalyst-grid-tile__spacer" />
        {editable ? (
          <button
            type="button"
            onClick={(e) => {
              // Don't let the remove click bubble to the header's select.
              e.stopPropagation();
              onRemove?.();
            }}
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
        {instance.render({ view: currentView, width: size.width, height: size.height, config })}
      </div>

      <span className="catalyst-grid-tile__id" aria-hidden>#{instance.key}</span>
      {children /* RGL injects the resize handle here as its last child */}
    </div>
  );
});
