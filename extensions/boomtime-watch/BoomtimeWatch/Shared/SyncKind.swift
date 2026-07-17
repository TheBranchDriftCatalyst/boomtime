//
//  SyncKind.swift
//  BoomtimeWatch (shared)
//
//  Sample-kind identifiers that mirror what we send on the wire. Also used as
//  keys for anchor persistence — one anchor per kind. Shared between the iOS
//  and watchOS targets so the WCSession applicationContext can round-trip
//  Set<SyncKind> without either side redefining the enum.
//

import Foundation

/// Sample-kind identifiers. `Codable` so we can persist an enabled-set to
/// UserDefaults and pass it through WatchConnectivity.
enum SyncKind: String, CaseIterable, Codable, Hashable {
    case workouts
    case heart_rate
    case resting_heart_rate
    case steps
    case active_energy
    case hrv
    case sleep_stage
    case mindful

    /// Human label for status UI.
    var displayName: String {
        switch self {
        case .workouts: return "Workouts"
        case .heart_rate: return "Heart Rate"
        case .resting_heart_rate: return "Resting HR"
        case .steps: return "Steps"
        case .active_energy: return "Active Energy"
        case .hrv: return "HRV"
        case .sleep_stage: return "Sleep"
        case .mindful: return "Mindful"
        }
    }
}
