package model

import (
	"encoding/json"
	"testing"
	"time"
)

// TestWireFormat pins the EXACT JSON wire format of the oddball payload shapes
// (raw Haskell field names, convertReservedWords renames, omitempty behavior)
// so later refactors cannot silently change what goes over the wire.
func TestWireFormat(t *testing.T) {
	ts := time.Date(2024, 3, 1, 10, 30, 0, 0, time.UTC)
	tsEnd := time.Date(2024, 3, 1, 11, 0, 0, 0, time.UTC)
	d0 := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	d1 := time.Date(2024, 3, 2, 0, 0, 0, 0, time.UTC)

	name := "ci"
	desc := "laptop"
	editor := "vim"
	isWrite := true
	lines := int64(120)

	cases := []struct {
		name string
		in   any
		want string
	}{
		{
			name: "TimelineItem raw Haskell keys (tName/tRangeStart/tRangeEnd)",
			in: TimelineItem{
				Name:       "boomtime",
				RangeStart: ts,
				RangeEnd:   tsEnd,
			},
			want: `{"tName":"boomtime","tRangeStart":"2024-03-01T10:30:00Z","tRangeEnd":"2024-03-01T11:00:00Z"}`,
		},
		{
			name: "StoredApiToken raw Haskell keys, nil pointers marshal as null",
			in:   StoredApiToken{ID: "dG9rZW4="},
			want: `{"tknId":"dG9rZW4=","lastUsage":null,"tknName":null,"tknDesc":null}`,
		},
		{
			name: "StoredApiToken populated",
			in:   StoredApiToken{ID: "dG9rZW4=", LastUsage: &ts, Name: &name, Desc: &desc},
			want: `{"tknId":"dG9rZW4=","lastUsage":"2024-03-01T10:30:00Z","tknName":"ci","tknDesc":"laptop"}`,
		},
		{
			name: "StatusBarPayload grand_total envelope",
			in: StatusBarPayload{
				Data: DayGrandTotal{
					GrandTotal: DayTextValue{Text: "2 hrs 15 min"},
					Categories: []string{"coding"},
				},
			},
			want: `{"data":{"grand_total":{"text":"2 hrs 15 min"},"categories":["coding"]}}`,
		},
		{
			name: "StatsPayload populated",
			in: StatsPayload{
				StartDate:    d0,
				EndDate:      d1,
				TotalSeconds: 3600,
				DailyAvg:     1800,
				DailyTotal:   []int64{3600, 0},
				Projects: []ResourceStats{{
					Name:         "boomtime",
					TotalSeconds: 3600,
					TotalPct:     100,
					TotalDaily:   []int64{3600, 0},
					PctDaily:     []float64{100, 0},
				}},
				Languages:     []ResourceStats{},
				Platforms:     []ResourceStats{},
				Machines:      []ResourceStats{},
				Editors:       []ResourceStats{},
				Categories:    []ResourceStats{},
				ProjectsCount: 1,
			},
			want: `{"startDate":"2024-03-01T00:00:00Z","endDate":"2024-03-02T00:00:00Z","totalSeconds":3600,"dailyAvg":1800,"dailyTotal":[3600,0],"projects":[{"name":"boomtime","totalSeconds":3600,"totalPct":100,"totalDaily":[3600,0],"pctDaily":[100,0]}],"languages":[],"platforms":[],"machines":[],"editors":[],"categories":[],"projectsCount":1,"languagesCount":0,"platformsCount":0,"machinesCount":0,"editorsCount":0,"categoriesCount":0}`,
		},
		{
			name: "ProjectStatistics populated",
			in: ProjectStatistics{
				StartDate:    d0,
				EndDate:      d1,
				TotalSeconds: 3600,
				DailyTotal:   []int64{3600},
				Languages: []ResourceStats{{
					Name:         "Go",
					TotalSeconds: 3600,
					TotalPct:     100,
					TotalDaily:   []int64{3600},
					PctDaily:     []float64{100},
				}},
				LanguagesDaily:  []LanguageDaily{{Name: "Go", Daily: []int64{3600}}},
				Files:           []ResourceStats{},
				WeekDay:         []ResourceStats{},
				Hour:            []ResourceStats{},
				LanguagesCount:  1,
				WriteSeconds:    1200,
				ReadSeconds:     2400,
				DailyWriteRatio: []float64{0.5},
				Branches:        []ResourceStats{},
				DailyEntities:   []int64{3},
			},
			want: `{"startDate":"2024-03-01T00:00:00Z","endDate":"2024-03-02T00:00:00Z","totalSeconds":3600,"dailyTotal":[3600],"languages":[{"name":"Go","totalSeconds":3600,"totalPct":100,"totalDaily":[3600],"pctDaily":[100]}],"languagesDaily":[{"name":"Go","daily":[3600]}],"files":[],"weekDay":[],"hour":[],"languagesCount":1,"filesCount":0,"writeSeconds":1200,"readSeconds":2400,"dailyWriteRatio":[0.5],"branches":[],"branchesCount":0,"dailyEntities":[3],"entitiesCount":0}`,
		},
		{
			name: "HeartbeatPayload zero value: no omitempty, nil pointers stay as null",
			in:   HeartbeatPayload{},
			want: `{"editor":null,"plugin":null,"platform":null,"machine":null,"sender":null,"user_agent":"","branch":null,"category":null,"cursorpos":null,"dependencies":null,"entity":"","is_write":null,"language":null,"lineno":null,"lines":null,"project":null,"type":"","time":0}`,
		},
		{
			name: "HeartbeatPayload populated: reserved-word renames (lines/type/time)",
			in: HeartbeatPayload{
				Editor:       &editor,
				UserAgent:    "wakatime/1.0",
				Dependencies: []string{"fmt"},
				Entity:       "main.go",
				IsWrite:      &isWrite,
				FileLines:    &lines,
				Type:         FileType,
				TimeSent:     1709287800.25,
			},
			want: `{"editor":"vim","plugin":null,"platform":null,"machine":null,"sender":null,"user_agent":"wakatime/1.0","branch":null,"category":null,"cursorpos":null,"dependencies":["fmt"],"entity":"main.go","is_write":true,"language":null,"lineno":null,"lines":120,"project":null,"type":"file","time":1709287800.25}`,
		},
		{
			name: "LoginResponse",
			in:   LoginResponse{Token: "jwt", TokenExpiry: ts, TokenUsername: "alice"},
			want: `{"token":"jwt","tokenExpiry":"2024-03-01T10:30:00Z","tokenUsername":"alice"}`,
		},
		{
			name: "TokenResponse",
			in:   TokenResponse{APIToken: "secret"},
			want: `{"apiToken":"secret"}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(tc.in)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("wire format changed\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}
