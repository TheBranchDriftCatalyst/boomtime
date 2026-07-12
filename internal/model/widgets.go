package model

// WidgetLinkResponse is the mint response: the public base URL for a scope's
// widgets. The FE appends "/<kind>?days=&theme=" per catalog entry.
type WidgetLinkResponse struct {
	WidgetBaseURL string `json:"widgetBaseUrl"`
	LinkID        string `json:"linkId"`
}
