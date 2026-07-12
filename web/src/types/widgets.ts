// Embeddable-widget types (gaka-hsj), mirroring internal/handler/widgets.go.

export type WidgetScope = "user" | "project" | "space";

export interface WidgetLinkPayload {
  widgetBaseUrl: string;
  linkId: string;
}

export interface WidgetLinkRow {
  linkId: string;
  scopeType: WidgetScope;
  scopeRef: string;
  /** Resolved display name — space's `name` for space scope, project name for
   * project scope, empty for user scope. Prefer this over scopeRef in labels. */
  scopeName?: string;
  createdAt: string;
  /** Timestamp of the most recent public SVG fetch, absent for un-used links. */
  lastUsedAt?: string;
  /** Bounded set (max 20 most-recent) of referring pages that have fetched
   * the SVG. Empty for un-used links. */
  origins?: WidgetOriginStat[];
}

export interface WidgetOriginStat {
  /** Referer URL or "direct" when no Referer header (curl, camo, etc.) */
  origin: string;
  count: number;
  lastSeen: string;
}

export interface WidgetLinksPayload {
  links: WidgetLinkRow[];
}
