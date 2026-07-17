//
//  WorkoutLabelMap.swift
//  BoomtimeWatch (shared)
//
//  Codable payload sent through WCSession.updateApplicationContext. Bundles
//  the mirror-state the watch needs: enabled SyncKinds (advisory) and the
//  list of annotated workouts (source of truth for the Start Workout picker).
//
//  Historical note: this file used to hold a `[wireKind: label]` string map
//  and a hard-coded `WatchWorkoutKind` enum. Both were superseded by
//  `AnnotatedWorkout` — a real list gives users multiple entries per activity
//  type (e.g. "Morning Run" and "Evening Run" both use .running).
//

import Foundation

/// Codable payload sent through WCSession.updateApplicationContext.
///
/// WCSession requires an NSDictionary at the boundary; we serialize the whole
/// struct to a JSON string under a single "payload" key so extending the
/// context shape later doesn't require careful dictionary migration.
struct WatchConfigContext: Codable, Equatable {
    /// Set of enabled sync kinds; the phone honors this in HealthKitReader,
    /// the watch treats it as advisory info (rendered in the About / Sync tab).
    var enabledKinds: Set<SyncKind>

    /// Ordered list of user-defined workout presets. The watch's Start
    /// Workout picker renders one row per entry; when nil/empty on first
    /// pair, the watch falls back to WorkoutPreset.allCases as a read-only
    /// starter set.
    var annotatedWorkouts: [AnnotatedWorkout]

    static let empty = WatchConfigContext(
        enabledKinds: Set(SyncKind.allCases),
        annotatedWorkouts: []
    )

    /// Encode to the flat NSDictionary shape WCSession requires.
    func toDictionary() -> [String: Any] {
        guard let data = try? JSONEncoder().encode(self),
              let s = String(data: data, encoding: .utf8) else {
            return [:]
        }
        return ["payload": s]
    }

    /// Decode from the applicationContext dictionary. Returns nil on any error
    /// so the receiver can fall back to defaults without crashing.
    static func fromDictionary(_ dict: [String: Any]) -> WatchConfigContext? {
        guard let s = dict["payload"] as? String,
              let data = s.data(using: .utf8),
              let ctx = try? JSONDecoder().decode(WatchConfigContext.self, from: data) else {
            return nil
        }
        return ctx
    }
}
