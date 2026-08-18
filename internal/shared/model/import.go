package model

import "time"

// ---- Import (Import.hs) ----

// ImportRequestPayload is the body of POST /import and /import/status.
type ImportRequestPayload struct {
	APIToken  string    `json:"apiToken"`
	StartDate time.Time `json:"startDate"`
	EndDate   time.Time `json:"endDate"`
}

// ImportRequestResponse is {"jobStatus": "Submitted|Pending|Failed|Finished"}.
type ImportRequestResponse struct {
	JobStatus string `json:"jobStatus"`
}
