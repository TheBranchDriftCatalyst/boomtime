//
//  AnnotatedWorkout.swift
//  BoomtimeWatch (shared)
//
//  A user-defined (label, activity type, emoji, duration, end-behavior) tuple.
//  Drives the watch's Start Workout picker AND the phone's Workout Presets
//  editor. Replaces the old `[wireKind: label]` string map — a real list gives
//  users multiple entries per activity type (e.g. "Morning Run" and "Evening
//  Run" both use .running, and Sauna vs Dog Walk both use .other/.walking with
//  their own auto-end policies).
//
//  Persisted phone-side in Config as JSON in UserDefaults, mirrored to the
//  watch via WCSession applicationContext (see WatchConfigContext).
//

import Foundation

/// What happens when a preset's `defaultDurationMin` elapses.
enum WorkoutEndBehavior: String, Codable, Hashable, CaseIterable, Identifiable {
    /// No auto-end, no reminder. Records until the user taps End.
    case manual
    /// Fires a notification haptic at 0. Keeps recording (workout goes into "overtime").
    case haptic
    /// Fires the haptic and then calls `session.end()` at 0.
    case autoEnd

    var id: String { rawValue }
    var displayName: String {
        switch self {
        case .manual:  return "Manual"
        case .haptic:  return "Haptic reminder"
        case .autoEnd: return "Auto-end"
        }
    }
}

/// How the phone decides "user has arrived home" for this workout.
///
/// Wi-Fi SSID is a much sharper signal than a raw GPS geofence — a 100m circle
/// picks up neighbors, an SSID match is a specific network. But SSID reads
/// don't wake the phone in the background (no iOS API for that), so the
/// interesting mode is `.both`: use the geofence to wake, use SSID to confirm.
enum HomeDetection: String, Codable, Hashable, CaseIterable, Identifiable {
    /// No auto-end at home.
    case off
    /// Foreground-only SSID poll. Won't fire while backgrounded.
    case wifiOnly
    /// GPS geofence only. Fires on region entry; can trigger 30–50m outside
    /// the actual home coord in dense areas.
    case geofenceOnly
    /// Geofence wakes the phone, SSID confirms. Fewest false positives.
    case both

    var id: String { rawValue }
    var displayName: String {
        switch self {
        case .off:          return "Off"
        case .wifiOnly:     return "Wi-Fi only"
        case .geofenceOnly: return "Geofence only"
        case .both:         return "Both (recommended)"
        }
    }
}

/// One user-defined workout preset.
///
/// `activityRaw` is `HKWorkoutActivityType.rawValue` (UInt on Apple's side —
/// we widen to Int for Codable friendliness and downcast when we need the
/// enum back). Storing the raw value here means the shared file doesn't have
/// to import HealthKit, which keeps the phone-side Settings UI free of the
/// HealthKit framework unnecessarily.
///
/// `wireKind` is the same lowercase string PayloadBuilder.wireName emits for
/// the raw activity — snapshotted at edit time so watch and phone don't have
/// to duplicate the switch table. Server-side it stays the stable aggregation
/// key.
struct AnnotatedWorkout: Codable, Equatable, Identifiable, Hashable {
    /// Stable across sync — generated on the phone at creation time.
    let id: UUID
    /// User-facing label (also flows through to the wire `label` field).
    var label: String
    /// `HKWorkoutActivityType.rawValue`.
    var activityRaw: Int
    /// Snapshot of PayloadBuilder's wire name for `activityRaw`; used by the
    /// watch to render a fallback emoji when `emoji` is nil.
    var wireKind: String
    /// Optional leading glyph.
    var emoji: String?
    /// Default session length in minutes. `nil` = open-ended (user ends manually).
    /// When non-nil the watch live view renders a countdown.
    var defaultDurationMin: Int?
    /// What happens when `defaultDurationMin` elapses.
    var endBehavior: WorkoutEndBehavior
    /// How the phone confirms "user is home" and ends this workout. Runtime
    /// downgrades to `.off` if the referenced signal (homeBase / homeSSID)
    /// isn't configured; `.both` degrades to whichever one *is* configured.
    var homeDetection: HomeDetection

    init(
        id: UUID = UUID(),
        label: String,
        activityRaw: Int,
        wireKind: String,
        emoji: String? = nil,
        defaultDurationMin: Int? = nil,
        endBehavior: WorkoutEndBehavior = .manual,
        homeDetection: HomeDetection = .off
    ) {
        self.id = id
        self.label = label
        self.activityRaw = activityRaw
        self.wireKind = wireKind
        self.emoji = emoji
        self.defaultDurationMin = defaultDurationMin
        self.endBehavior = endBehavior
        self.homeDetection = homeDetection
    }

    /// Fallback glyph derived from the wire kind — used when `emoji` is nil.
    /// Keep this table small; anything exotic can be user-set.
    var effectiveEmoji: String {
        if let e = emoji, !e.isEmpty { return e }
        switch wireKind {
        case "running": return "🏃"
        case "walking": return "🚶"
        case "hiking":  return "🥾"
        case "cycling": return "🚴"
        case "swimming": return "🏊"
        case "yoga":    return "🧘"
        case "functional_strength", "strength": return "🏋️"
        case "hiit":    return "⚡"
        case "rowing":  return "🚣"
        case "elliptical": return "🌀"
        case "stairs":  return "🪜"
        default:        return "💪"
        }
    }

    // MARK: Codable — hand-rolled so older stored blobs (pre-timers) still hydrate.

    enum CodingKeys: String, CodingKey {
        case id, label, activityRaw, wireKind, emoji, defaultDurationMin, endBehavior
        case homeDetection
        /// Legacy key retained ONLY for backward-compat decode. Never re-emitted.
        case geofenceEndAtHome
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        self.id = try c.decode(UUID.self, forKey: .id)
        self.label = try c.decode(String.self, forKey: .label)
        self.activityRaw = try c.decode(Int.self, forKey: .activityRaw)
        self.wireKind = try c.decode(String.self, forKey: .wireKind)
        self.emoji = try c.decodeIfPresent(String.self, forKey: .emoji)
        self.defaultDurationMin = try c.decodeIfPresent(Int.self, forKey: .defaultDurationMin)
        // Older blobs lack endBehavior — default to .manual.
        self.endBehavior = try c.decodeIfPresent(WorkoutEndBehavior.self, forKey: .endBehavior) ?? .manual
        // Migration: prefer the new `homeDetection` enum; fall back to the
        // legacy `geofenceEndAtHome` Bool if present (true → .both, false → .off).
        if let detection = try c.decodeIfPresent(HomeDetection.self, forKey: .homeDetection) {
            self.homeDetection = detection
        } else if let legacy = try c.decodeIfPresent(Bool.self, forKey: .geofenceEndAtHome) {
            self.homeDetection = legacy ? .both : .off
        } else {
            self.homeDetection = .off
        }
    }
}

/// Curated fallback presets shown on the watch when the phone hasn't pushed a
/// list yet (fresh pair / first launch). Also the source of the "+ Add preset"
/// menu on the watch's Settings screen — selecting one sends its `presetKey`
/// to the phone, which appends the matching AnnotatedWorkout to its list.
///
/// Twelve entries total: the ten canonical Apple-Health activity types plus
/// two lifestyle presets (Sauna, Dog Walk) that use existing activity types
/// under the hood but carry their own defaults.
enum WorkoutPreset: String, CaseIterable, Codable, Identifiable {
    case morningRun = "morning-run"
    case eveningRun = "evening-run"
    case commuteRide = "commute-ride"
    case intervalRide = "interval-ride"
    case strength = "strength"
    case yoga = "yoga"
    case walk = "walk"
    case dogWalk = "dog-walk"
    case hike = "hike"
    case hiit = "hiit"
    case rowing = "rowing"
    case sauna = "sauna"

    var id: String { rawValue }

    var label: String {
        switch self {
        case .morningRun:   return "Morning Run"
        case .eveningRun:   return "Evening Run"
        case .commuteRide:  return "Commute Ride"
        case .intervalRide: return "Interval Ride"
        case .strength:     return "Strength"
        case .yoga:         return "Yoga"
        case .walk:         return "Walk"
        case .dogWalk:      return "Dog Walk"
        case .hike:         return "Hike"
        case .hiit:         return "HIIT"
        case .rowing:       return "Rowing"
        case .sauna:        return "Sauna"
        }
    }

    /// `HKWorkoutActivityType.rawValue` for the preset. Sourced from
    /// Apple's HealthKit headers; hard-coded here so this file stays HK-free.
    ///   running = 37, walking = 52, hiking = 20, cycling = 13,
    ///   traditionalStrengthTraining = 50, yoga = 57, rowing = 35,
    ///   highIntensityIntervalTraining = 63, other = 3000
    var activityRaw: Int {
        switch self {
        case .morningRun, .eveningRun: return 37   // running
        case .commuteRide, .intervalRide: return 13 // cycling
        case .strength: return 50                   // traditionalStrengthTraining
        case .yoga: return 57                       // yoga
        case .walk, .dogWalk: return 52             // walking
        case .hike: return 20                       // hiking
        case .hiit: return 63                       // highIntensityIntervalTraining
        case .rowing: return 35                     // rowing
        case .sauna: return 3000                    // other
        }
    }

    /// PayloadBuilder.wireName for this preset's activity.
    var wireKind: String {
        switch self {
        case .morningRun, .eveningRun: return "running"
        case .commuteRide, .intervalRide: return "cycling"
        case .strength: return "strength"
        case .yoga: return "yoga"
        case .walk, .dogWalk: return "walking"
        case .hike: return "hiking"
        case .hiit: return "hiit"
        case .rowing: return "rowing"
        case .sauna: return "other"
        }
    }

    var emoji: String? {
        switch self {
        case .morningRun:   return "🏃"
        case .eveningRun:   return "🌆"
        case .commuteRide:  return "🚴"
        case .intervalRide: return "⚡"
        case .strength:     return "🏋️"
        case .yoga:         return "🧘"
        case .walk:         return "🚶"
        case .dogWalk:      return "🐕"
        case .hike:         return "🥾"
        case .hiit:         return "⚡"
        case .rowing:       return "🚣"
        case .sauna:        return "🧖"
        }
    }

    /// Default session length in minutes.
    var defaultDurationMin: Int {
        switch self {
        case .morningRun, .eveningRun: return 30
        case .commuteRide, .intervalRide: return 45
        case .strength: return 45
        case .yoga: return 30
        case .walk: return 30
        case .dogWalk: return 15
        case .hike: return 60
        case .hiit: return 20
        case .rowing: return 20
        case .sauna: return 20
        }
    }

    /// Default end behavior. Sauna and Dog Walk auto-end; everything else
    /// leaves it to the user (manual).
    var endBehavior: WorkoutEndBehavior {
        switch self {
        case .sauna, .dogWalk: return .autoEnd
        default: return .manual
        }
    }

    /// Default home-detection mode. Only Dog Walk defaults on — that's the
    /// canonical "start when you leave, end when you return" flow. Users can
    /// enable it on `.walk` themselves; enabling on cycling or running is
    /// possible but a bit weird (routes rarely return to origin), so we don't
    /// default it on.
    var homeDetection: HomeDetection {
        switch self {
        case .dogWalk: return .both
        default:       return .off
        }
    }

    /// Materialize this preset as a fresh AnnotatedWorkout (new UUID).
    func makeAnnotated() -> AnnotatedWorkout {
        AnnotatedWorkout(
            id: UUID(),
            label: label,
            activityRaw: activityRaw,
            wireKind: wireKind,
            emoji: emoji,
            defaultDurationMin: defaultDurationMin,
            endBehavior: endBehavior,
            homeDetection: homeDetection
        )
    }
}
