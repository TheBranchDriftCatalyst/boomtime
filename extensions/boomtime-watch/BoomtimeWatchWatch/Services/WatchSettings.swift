//
//  WatchSettings.swift
//  BoomtimeWatch Watch App
//
//  Small on-watch preferences store — separate from the phone's Config.
//  These are on-wrist knobs that never round-trip back to the phone
//  (server URL, token, and the annotated-workout list stay phone-only).
//
//  Persisted via UserDefaults so the watch remembers state across launches.
//

import Foundation
import Combine

/// How prominently the live workout view renders the current HR reading.
enum HRDisplayMode: String, CaseIterable, Codable, Identifiable {
    case largeNumeric
    case smallNumeric
    case hidden

    var id: String { rawValue }
    var displayName: String {
        switch self {
        case .largeNumeric: return "Large numeric"
        case .smallNumeric: return "Small numeric"
        case .hidden:       return "Hidden"
        }
    }
}

private enum WatchSettingsKey {
    static let defaultWorkoutID = "boomtime.watch.defaultWorkoutID"
    static let hrDisplay = "boomtime.watch.hrDisplay"
    static let showLabelPicker = "boomtime.watch.showLabelPicker"
}

@MainActor
final class WatchSettings: ObservableObject {
    private let defaults: UserDefaults

    /// UUID of the AnnotatedWorkout to pre-highlight in the Start Workout
    /// picker. Nil = first row wins. When the referenced ID is gone from the
    /// mirrored list (user deleted the preset on the phone) StartWorkoutView
    /// silently falls back to first-row behavior.
    @Published var defaultWorkoutID: UUID? {
        didSet {
            if let id = defaultWorkoutID {
                defaults.set(id.uuidString, forKey: WatchSettingsKey.defaultWorkoutID)
            } else {
                defaults.removeObject(forKey: WatchSettingsKey.defaultWorkoutID)
            }
        }
    }

    /// HR readout size on LiveWorkoutView.
    @Published var hrDisplay: HRDisplayMode {
        didSet { defaults.set(hrDisplay.rawValue, forKey: WatchSettingsKey.hrDisplay) }
    }

    /// When true, tapping a preset first shows the raw label as an intermediate
    /// confirm/pick screen — useful when multiple presets share the same
    /// activity type. When false, workout starts immediately.
    @Published var showLabelPickerOnStart: Bool {
        didSet { defaults.set(showLabelPickerOnStart, forKey: WatchSettingsKey.showLabelPicker) }
    }

    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults

        if let raw = defaults.string(forKey: WatchSettingsKey.defaultWorkoutID),
           let uuid = UUID(uuidString: raw) {
            self.defaultWorkoutID = uuid
        } else {
            self.defaultWorkoutID = nil
        }

        if let raw = defaults.string(forKey: WatchSettingsKey.hrDisplay),
           let m = HRDisplayMode(rawValue: raw) {
            self.hrDisplay = m
        } else {
            self.hrDisplay = .largeNumeric
        }

        // Default false — "just start it" is what most users want.
        self.showLabelPickerOnStart = defaults.bool(forKey: WatchSettingsKey.showLabelPicker)
    }
}
