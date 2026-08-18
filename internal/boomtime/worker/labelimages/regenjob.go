package labelimages

import (
	"encoding/json"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/boomtime/labelcatalog"
)

// RegenJobKind is the catalyst-go-jobs kind for a single label-image
// regeneration (gaka-hney Stage 3 — folds the imagejobs pipeline onto the
// generic DB queue). One job == one label. A label-image job sets its `owner`
// to the LabelID so the admin status endpoint can pull the latest job per
// label without parsing JSON payloads, and so re-enqueues dedupe per label.
const RegenJobKind = "label-image"

// RegenJobPayload is the JSON payload of a label-image job. It carries the FULL
// entry (prompt + model/size/seed overrides) so the handler dispatches to
// Worker.RegenerateEntry with byte-for-byte the fidelity the imagejobs executor
// had — RegenerateOne(labelID) would drop the per-entry overrides.
type RegenJobPayload struct {
	LabelID     string `json:"labelId"`
	Description string `json:"description"`
	Prompt      string `json:"prompt"`
	Model       string `json:"model"`
	Size        string `json:"size"`
	Seed        *int64 `json:"seed"`
}

// Entry maps the payload back to the labelcatalog.Entry RegenerateEntry wants.
func (p RegenJobPayload) Entry() labelcatalog.Entry {
	return labelcatalog.Entry{
		ID:          p.LabelID,
		Description: p.Description,
		Prompt:      p.Prompt,
		Model:       p.Model,
		Size:        p.Size,
		Seed:        p.Seed,
	}
}

// JSON marshals the payload for jobs.Enqueuer.Enqueue.
func (p RegenJobPayload) JSON() (json.RawMessage, error) {
	b, err := json.Marshal(p)
	return json.RawMessage(b), err
}
