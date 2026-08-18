// monitorconfig.go — THE single source of truth for every reading-monitor knob
// and every recommendation/classification/composition coefficient (catalyst-books
// §5.1). Before this file these values were scattered across four packages:
// engine intervals lived in config.go (BOOM_RM_*), the scheduler cadence + run
// budget were hardcoded consts in cmd/boomtime/main.go, the session gap +
// reading-time lookback were consts in reading_time.go, and the recommendation's
// floors/multipliers/window were magic numbers in monitor_recommend.go /
// reading_monitor.go. They are now ONE struct, loaded once from the environment
// with documented defaults, and threaded everywhere: main.go passes a single
// MonitorConfig into the engine, RecommendIntervals reads its coefficients, and
// reading_time.go composes against its session model.
//
// Read this file as the spec: each field documents its env var, default, and the
// equation it feeds.
package kindle

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// MonitorConfig is the complete, documented tuning surface for the persistent
// server-side Kindle reading-monitor and everything it drives. A zero value is
// safe: withDefaults() folds in the shipped defaults for any unset field, so a
// partial config (or a bare MonitorConfig{} in a test) never stalls the engine.
type MonitorConfig struct {
	// ── Two-level engine intervals (§5.1 "limit hack") ──────────────────────
	// DetectInterval is T1, the coarse L1 cadence: poll ALL in-progress books
	// this often to spot which one advanced. Env BOOM_RM_DETECT_SECS, default
	// 120s. A larger T1 costs toast latency, not data (creationTime backfills the
	// true event time). Feeds the L1 due-check in runMonitorPass.
	DetectInterval time.Duration
	// CaptureInterval is T2, the fine L2 cadence: poll only an actively-advancing
	// book this often. Env BOOM_RM_CAPTURE_SECS, default 60s — NOT finer, because
	// reading-time is minute-level analytics (the fidelity floor). Feeds the L2
	// due-check.
	CaptureInterval time.Duration
	// IdleGap is G: an active book with no advance for this long drops back to L1.
	// Env BOOM_RM_IDLE_SECS, default 300s. Feeds the idle-expiry test + gates the
	// "fresh vs stale first sight" decision.
	IdleGap time.Duration
	// BaseTick is the engine's internal loop period inside one scheduled run:
	// RunMonitorLoop runs a full pass every BaseTick until RunBudget elapses.
	// Env BOOM_RM_BASE_SECS, default 10s. Per-book due-checks against T1/T2/
	// CalibrationInterval make the effective cadence exact regardless of this, but
	// BaseTick is the RESOLUTION floor: no book can be polled faster than BaseTick.
	// Defaulted to 10s (was 15s) so a 10s calibration burst has real resolution;
	// withDefaults clamps BaseTick down to CalibrationInterval if it is coarser.
	BaseTick time.Duration
	// ScheduleInterval is how often the leader-singleton scheduler re-arms the
	// engine job. Env BOOM_RM_SCHEDULE_SECS, default 60s. Kept >= RunBudget so
	// runs don't overlap under the concurrency-1 cap. Was a hardcoded const in
	// main.go (readingMonitorScheduleInterval).
	ScheduleInterval time.Duration
	// RunBudget is how long one scheduled engine run loops before returning to let
	// the next scheduled job re-arm it. Env BOOM_RM_BUDGET_SECS, default 50s. Kept
	// under ScheduleInterval so runs don't pile up. Was a hardcoded const in
	// main.go (readingMonitorRunBudget).
	RunBudget time.Duration

	// ── Session model (reading_time.go composition) ─────────────────────────
	// SessionGap is the max wall-clock gap between two position samples for the
	// interval between them to count as one continuous reading session (gap-sum).
	// Env BOOM_RM_SESSION_GAP_SECS, default 900s (15m) — matches the coding
	// heartbeat gap cutoff so a "reading session" and a "coding session" mean the
	// same thing on the fused calendar. Feeds composeSessions + the monitor's
	// in-session reading-seconds counter.
	SessionGap time.Duration
	// ReadingTimeLookback bounds how far back a recompose rewrites reading_activity
	// from samples. Env BOOM_RM_READING_LOOKBACK_DAYS, default 90 days. Older
	// buckets are immutable, so not rewriting them is safe.
	ReadingTimeLookback time.Duration

	// ── Recommendation equation coefficients (monitor_recommend.go) ─────────
	// MinSamples: below this many usable advance intervals RecommendIntervals
	// returns nil ("not enough data yet"). Default 5.
	MinSamples int
	// FidelityFloorSecs: never recommend a capture cadence finer than this —
	// minute-level analytics. captureSecs = max(FidelityFloorSecs, round(p50)).
	// Default 60.
	FidelityFloorSecs int
	// IdleFloorSecs: floor for the recommended idle gap.
	// idleSecs = max(IdleFloorSecs, round(p90 * IdleMultiplier)). Default 180.
	IdleFloorSecs int
	// IdleMultiplier: the p90 multiplier in the idle-gap recommendation. Default 2.
	IdleMultiplier float64
	// DetectMultiplier: the p90 multiplier in the detect recommendation.
	// detectSecs = max(2*captureSecs, round(p90 * DetectMultiplier)). Default 3.
	DetectMultiplier float64
	// RecommendLookback bounds how far back the recommendation reads advance
	// samples. Env BOOM_RM_RECOMMEND_LOOKBACK_DAYS, default 30 days.
	RecommendLookback time.Duration
	// WindowCap bounds how many recent advance samples the recommendation reads,
	// so the percentile computation stays cheap. Default 1000.
	WindowCap int

	// ── Sync-pattern classification thresholds (§5.1 table) ─────────────────
	// A book's advances are CONTINUOUS (⇒ gap-sum) when they are frequent
	// (p50 interval < ContinuousMaxIntervalSecs) AND small (p50 dloc < MaxDloc).
	ContinuousMaxIntervalSecs int
	MaxDloc                   int64
	// They are SESSION-BOUNDARY (⇒ position-delta) when sparse
	// (p50 interval >= SessionBoundaryMinIntervalSecs) AND large
	// (p50 dloc >= SessionBoundaryMinDloc). Anything else ⇒ unknown.
	SessionBoundaryMinIntervalSecs int
	SessionBoundaryMinDloc         int64

	// ── Position-delta composition (reading_time.go) ────────────────────────
	// DefaultSecPerLocation is the reading-speed (seconds per Kindle location
	// unit) the position-delta method uses when the observed sec/location signal
	// is unavailable. Default 1.0. The observed value (median interval/dloc over
	// the advance window) is preferred when present.
	DefaultSecPerLocation float64

	// ── Calibration mode (PART 2 — high-fidelity diagnostic burst) ──────────
	// CalibrationInterval is the poll cadence for ALL in-progress books while a
	// user is calibrating — a high-fidelity burst that beats the L1/L2 aliasing of
	// the sub-60s whispersync cadence. Env BOOM_RM_CALIBRATION_SECS, default 10s.
	// BaseTick is clamped <= this so the burst has real resolution.
	CalibrationInterval time.Duration
	// CalibrationDuration is how long a calibration window lasts once started; the
	// engine auto-reverts to normal L1/L2 when it expires, with no manual step.
	// Env BOOM_RM_CALIBRATION_DURATION_SECS, default 1200s (20m).
	CalibrationDuration time.Duration
}

// withDefaults returns a copy with every unset (non-positive / zero) field folded
// to its shipped default, then clamps BaseTick <= CalibrationInterval so a
// calibration burst is never coarser than the loop resolution.
func (c MonitorConfig) withDefaults() MonitorConfig {
	// intervals
	if c.DetectInterval <= 0 {
		c.DetectInterval = 120 * time.Second
	}
	if c.CaptureInterval <= 0 {
		c.CaptureInterval = 60 * time.Second
	}
	if c.IdleGap <= 0 {
		c.IdleGap = 300 * time.Second
	}
	if c.BaseTick <= 0 {
		c.BaseTick = 10 * time.Second
	}
	if c.ScheduleInterval <= 0 {
		c.ScheduleInterval = 60 * time.Second
	}
	if c.RunBudget <= 0 {
		c.RunBudget = 50 * time.Second
	}
	// session model
	if c.SessionGap <= 0 {
		c.SessionGap = 900 * time.Second
	}
	if c.ReadingTimeLookback <= 0 {
		c.ReadingTimeLookback = 90 * 24 * time.Hour
	}
	// recommendation coefficients
	if c.MinSamples <= 0 {
		c.MinSamples = 5
	}
	if c.FidelityFloorSecs <= 0 {
		c.FidelityFloorSecs = 60
	}
	if c.IdleFloorSecs <= 0 {
		c.IdleFloorSecs = 180
	}
	if c.IdleMultiplier <= 0 {
		c.IdleMultiplier = 2.0
	}
	if c.DetectMultiplier <= 0 {
		c.DetectMultiplier = 3.0
	}
	if c.RecommendLookback <= 0 {
		c.RecommendLookback = 30 * 24 * time.Hour
	}
	if c.WindowCap <= 0 {
		c.WindowCap = 1000
	}
	// classification thresholds
	if c.ContinuousMaxIntervalSecs <= 0 {
		c.ContinuousMaxIntervalSecs = 120
	}
	if c.MaxDloc <= 0 {
		c.MaxDloc = 500
	}
	if c.SessionBoundaryMinIntervalSecs <= 0 {
		c.SessionBoundaryMinIntervalSecs = 300
	}
	if c.SessionBoundaryMinDloc <= 0 {
		c.SessionBoundaryMinDloc = 500
	}
	// position-delta default
	if c.DefaultSecPerLocation <= 0 {
		c.DefaultSecPerLocation = 1.0
	}
	// calibration
	if c.CalibrationInterval <= 0 {
		c.CalibrationInterval = 10 * time.Second
	}
	if c.CalibrationDuration <= 0 {
		c.CalibrationDuration = 1200 * time.Second
	}
	// A calibration burst polls at CalibrationInterval, but no book can be polled
	// faster than the loop's BaseTick — so clamp BaseTick down to make the burst
	// resolution real.
	if c.BaseTick > c.CalibrationInterval {
		c.BaseTick = c.CalibrationInterval
	}
	return c
}

// LoadMonitorConfig builds a MonitorConfig from the environment, applying every
// documented default for an unset/invalid var. This is the ONE place env is read
// for the reading-monitor; config.Config.RM holds the result and threads it to
// main.go (engine), the admin handler (recommendation coefficients), and the
// reading-time composition. Back-compatible with the pre-consolidation env names
// (BOOM_RM_DETECT_SECS / _CAPTURE_SECS / _IDLE_SECS / _BASE_SECS).
func LoadMonitorConfig() MonitorConfig {
	return MonitorConfig{
		DetectInterval:      secsEnv("BOOM_RM_DETECT_SECS", 120),
		CaptureInterval:     secsEnv("BOOM_RM_CAPTURE_SECS", 60),
		IdleGap:             secsEnv("BOOM_RM_IDLE_SECS", 300),
		BaseTick:            secsEnv("BOOM_RM_BASE_SECS", 10),
		ScheduleInterval:    secsEnv("BOOM_RM_SCHEDULE_SECS", 60),
		RunBudget:           secsEnv("BOOM_RM_BUDGET_SECS", 50),
		SessionGap:          secsEnv("BOOM_RM_SESSION_GAP_SECS", 900),
		ReadingTimeLookback: daysEnv("BOOM_RM_READING_LOOKBACK_DAYS", 90),
		MinSamples:          intEnv("BOOM_RM_MIN_SAMPLES", 5),
		FidelityFloorSecs:   intEnv("BOOM_RM_FIDELITY_FLOOR_SECS", 60),
		IdleFloorSecs:       intEnv("BOOM_RM_IDLE_FLOOR_SECS", 180),
		IdleMultiplier:      floatEnv("BOOM_RM_IDLE_MULTIPLIER", 2.0),
		DetectMultiplier:    floatEnv("BOOM_RM_DETECT_MULTIPLIER", 3.0),
		RecommendLookback:   daysEnv("BOOM_RM_RECOMMEND_LOOKBACK_DAYS", 30),
		WindowCap:           intEnv("BOOM_RM_WINDOW_CAP", 1000),

		ContinuousMaxIntervalSecs:      intEnv("BOOM_RM_CONTINUOUS_MAX_INTERVAL_SECS", 120),
		MaxDloc:                        int64(intEnv("BOOM_RM_MAX_DLOC", 500)),
		SessionBoundaryMinIntervalSecs: intEnv("BOOM_RM_SESSION_BOUNDARY_MIN_INTERVAL_SECS", 300),
		SessionBoundaryMinDloc:         int64(intEnv("BOOM_RM_SESSION_BOUNDARY_MIN_DLOC", 500)),

		DefaultSecPerLocation: floatEnv("BOOM_RM_DEFAULT_SEC_PER_LOCATION", 1.0),

		CalibrationInterval: secsEnv("BOOM_RM_CALIBRATION_SECS", 10),
		CalibrationDuration: secsEnv("BOOM_RM_CALIBRATION_DURATION_SECS", 1200),
	}.withDefaults()
}

// ── env helpers (self-contained so this package owns its config surface) ────

// secsEnv reads an integer number of seconds into a Duration; a missing,
// unparseable, or non-positive value falls back to def seconds.
func secsEnv(key string, def int) time.Duration {
	return time.Duration(intEnv(key, def)) * time.Second
}

// daysEnv reads an integer number of days into a Duration.
func daysEnv(key string, def int) time.Duration {
	return time.Duration(intEnv(key, def)) * 24 * time.Hour
}

func intEnv(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func floatEnv(key string, def float64) float64 {
	if v, ok := os.LookupEnv(key); ok {
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && f > 0 {
			return f
		}
	}
	return def
}
