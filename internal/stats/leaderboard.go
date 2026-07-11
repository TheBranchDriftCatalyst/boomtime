package stats

import (
	"sort"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/model"
)

// ---- Leaderboards (Leaderboards.hs) ----

// ToLeaderboardsPayload builds the global + per-language top-20 lists (>60s filter).
func ToLeaderboardsPayload(rows []db.LeaderboardRow) model.LeaderboardsPayload {
	// group by user (global)
	global := mkGlobalList(groupBySender(rows))

	// group by language, then by user within each language.
	byLang := map[string][]db.LeaderboardRow{}
	var langOrder []string
	for _, r := range rows {
		if _, ok := byLang[r.Language]; !ok {
			langOrder = append(langOrder, r.Language)
		}
		byLang[r.Language] = append(byLang[r.Language], r)
	}
	langs := map[string][]model.UserTime{}
	for _, lang := range langOrder {
		list := mkGlobalList(groupBySender(byLang[lang]))
		if len(list) > 0 {
			langs[lang] = list
		}
	}
	return model.LeaderboardsPayload{Global: global, Lang: langs}
}

func groupBySender(rows []db.LeaderboardRow) map[string]int64 {
	m := map[string]int64{}
	for _, r := range rows {
		m[r.Sender] += r.TotalSeconds
	}
	return m
}

func mkGlobalList(sums map[string]int64) []model.UserTime {
	var list []model.UserTime
	for name, v := range sums {
		if v > 60 {
			list = append(list, model.UserTime{Name: name, Value: v})
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Value != list[j].Value {
			return list[i].Value > list[j].Value
		}
		return list[i].Name < list[j].Name
	})
	if len(list) > 20 {
		list = list[:20]
	}
	return list
}
