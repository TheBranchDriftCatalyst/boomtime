package stats

import (
	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
)

// ---- Timeline (Stats.hs) ----

// ToTimelinePayload builds the timeline map, dropping spans shorter than 60s
// (Stats.timelineStatsHandler.go).
func ToTimelinePayload(rows []db.TimelineRow) model.TimelinePayload {
	langs := map[string][]model.TimelineItem{}
	for _, r := range rows {
		if r.RangeEnd.Sub(r.RangeStart).Seconds() < 60 {
			continue
		}
		langs[r.Lang] = append(langs[r.Lang], model.TimelineItem{
			Name:       r.Project,
			RangeStart: r.RangeStart,
			RangeEnd:   r.RangeEnd,
		})
	}
	return model.TimelinePayload{TimelineLangs: langs}
}
