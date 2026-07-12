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
  createdAt: string;
}

export interface WidgetLinksPayload {
  links: WidgetLinkRow[];
}
