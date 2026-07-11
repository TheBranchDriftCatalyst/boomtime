package model

// ---- Errors (Errors.hs ApiErrorData, omitNothingFields) ----

// APIErrorData is the error JSON envelope: {"error": "...", "message": "..."}.
type APIErrorData struct {
	Error   string  `json:"error"`             // apiError
	Message *string `json:"message,omitempty"` // apiMessage, omitted when nil
}
