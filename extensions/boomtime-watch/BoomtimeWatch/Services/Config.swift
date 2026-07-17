//
//  Config.swift
//  BoomtimeWatch
//
//  Persisted, non-secret configuration: server URL, per-type
//  HKQueryAnchor blobs, per-type last-sync timestamps for the UI,
//  per-kind enabled toggles, and the annotated-workout list (user-defined
//  presets that drive the watch's Start Workout picker AND the phone-side
//  Workout Presets editor).
//  Secrets (the API token) live in Keychain.swift.
//
//  NOTE: `SyncKind` moved to Shared/SyncKind.swift so the watch target
//  can share the enum without importing HealthKit-heavy code. The old
//  `[wireKind: label]` map is gone — labels now live inside AnnotatedWorkout.
//

import Foundation
import HealthKit
import Combine

/// Namespaces every UserDefaults key we use so we don't collide with system state.
enum ConfigKey {
    static let serverURL = "boomtime.serverURL"
    static let anchorPrefix = "boomtime.anchor."       // + kind
    static let lastSyncPrefix = "boomtime.lastSync."   // + kind, stored as unix seconds
    static let lastErrorPrefix = "boomtime.lastError." // + kind, stored as string
    static let hasCompletedPairing = "boomtime.pairingComplete"
    static let enabledKinds = "boomtime.enabledKinds"           // JSON [String] of raw values
    static let annotatedWorkouts = "boomtime.annotatedWorkouts" // JSON [AnnotatedWorkout]
    static let homeBase = "boomtime.homeBase"                   // JSON HomeBase (nil until user sets)
    static let homeSSID = "boomtime.homeSSID"                   // JSON HomeSSID (nil until user sets)
}

/// Small facade over UserDefaults so tests can inject a suite and views can
/// observe via a shared reference. Not a singleton for testability, but the
/// app instantiates one at launch and passes it down.
///
/// An ObservableObject so SwiftUI Toggles and the annotated-workout editor can
/// bind directly. Anchor / sync-status accessors stay as plain methods since
/// they're read imperatively during sync and don't drive UI reactively.
final class Config: ObservableObject {
    private let defaults: UserDefaults

    /// Set of kinds the user has opted into. Default = all cases.
    /// HealthKitReader skips observers/anchored queries for disabled kinds.
    @Published var enabledKinds: Set<SyncKind> {
        didSet { persistEnabledKinds() }
    }

    /// User-defined workout presets. Drives the phone Settings editor and the
    /// watch's Start Workout picker (mirrored via WCSession applicationContext).
    /// PayloadBuilder uses these to look up the `label` field on outgoing
    /// WorkoutPayloads — the server treats `label` as the project bucket,
    /// falling back to raw `kind` when unset.
    @Published var annotatedWorkouts: [AnnotatedWorkout] {
        didSet { persistAnnotatedWorkouts() }
    }

    /// User's captured home location for the geofence auto-end feature. Nil
    /// until the user sets it in Settings. Never mirrored to the watch —
    /// presence monitoring is a phone-side responsibility.
    @Published var homeBase: HomeBase? {
        didSet { persistHomeBase() }
    }

    /// User's captured home Wi-Fi network. Nil until set. Used by
    /// HomePresenceMonitor to confirm the user is actually home (vs. the
    /// coffee shop across the geofence). Never mirrored to the watch.
    @Published var homeSSID: HomeSSID? {
        didSet { persistHomeSSID() }
    }

    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults

        // Hydrate enabledKinds — default to all-on for first-launch users so
        // they get the same behavior as before this feature landed.
        if let data = defaults.data(forKey: ConfigKey.enabledKinds),
           let raw = try? JSONDecoder().decode([String].self, from: data) {
            let parsed = raw.compactMap { SyncKind(rawValue: $0) }
            self.enabledKinds = Set(parsed)
        } else {
            self.enabledKinds = Set(SyncKind.allCases)
        }

        // Hydrate annotatedWorkouts — default empty (first launch). PayloadBuilder
        // treats an empty list as "no override", which preserves pre-feature
        // behavior of using raw kind as the project bucket.
        if let data = defaults.data(forKey: ConfigKey.annotatedWorkouts),
           let arr = try? JSONDecoder().decode([AnnotatedWorkout].self, from: data) {
            self.annotatedWorkouts = arr
        } else {
            self.annotatedWorkouts = []
        }

        // Hydrate homeBase — nil until the user captures a location. Decoding
        // failures fall back to nil (versus throwing) so a corrupted blob
        // doesn't brick launch; user can re-capture from Settings.
        if let data = defaults.data(forKey: ConfigKey.homeBase),
           let hb = try? JSONDecoder().decode(HomeBase.self, from: data) {
            self.homeBase = hb
        } else {
            self.homeBase = nil
        }

        // Same story for homeSSID.
        if let data = defaults.data(forKey: ConfigKey.homeSSID),
           let hs = try? JSONDecoder().decode(HomeSSID.self, from: data) {
            self.homeSSID = hs
        } else {
            self.homeSSID = nil
        }
    }

    private func persistEnabledKinds() {
        let raw = enabledKinds.map { $0.rawValue }
        if let data = try? JSONEncoder().encode(raw) {
            defaults.set(data, forKey: ConfigKey.enabledKinds)
        }
    }

    private func persistAnnotatedWorkouts() {
        if let data = try? JSONEncoder().encode(annotatedWorkouts) {
            defaults.set(data, forKey: ConfigKey.annotatedWorkouts)
        }
    }

    private func persistHomeBase() {
        if let hb = homeBase, let data = try? JSONEncoder().encode(hb) {
            defaults.set(data, forKey: ConfigKey.homeBase)
        } else {
            defaults.removeObject(forKey: ConfigKey.homeBase)
        }
    }

    private func persistHomeSSID() {
        if let hs = homeSSID, let data = try? JSONEncoder().encode(hs) {
            defaults.set(data, forKey: ConfigKey.homeSSID)
        } else {
            defaults.removeObject(forKey: ConfigKey.homeSSID)
        }
    }

    // MARK: Annotated workout CRUD helpers

    /// Appends a preset as a fresh AnnotatedWorkout (idempotent: duplicate
    /// (label, wireKind) pairs are silently skipped).
    func addPreset(_ preset: WorkoutPreset) {
        let candidate = preset.makeAnnotated()
        let dupe = annotatedWorkouts.contains {
            $0.label == candidate.label && $0.wireKind == candidate.wireKind
        }
        guard !dupe else { return }
        annotatedWorkouts.append(candidate)
    }

    /// Look up the label the user attached to a raw wire kind. Returns the
    /// first matching annotated entry's label, if any — the PayloadBuilder
    /// path uses this to populate the wire `label` field.
    ///
    /// When the user has multiple entries for the same activity (e.g. two
    /// "running" presets), the first-added wins by default. The watch UI can
    /// override this by sending the picked label explicitly via WCSession
    /// (out of scope for v1; iOS-side observer just uses the default).
    func label(forWireKind wire: String) -> String? {
        annotatedWorkouts.first(where: { $0.wireKind == wire })?.label
    }

    // MARK: Server

    var serverURL: URL? {
        get {
            guard let s = defaults.string(forKey: ConfigKey.serverURL),
                  let u = URL(string: s) else { return nil }
            return u
        }
        set {
            defaults.set(newValue?.absoluteString, forKey: ConfigKey.serverURL)
        }
    }

    var hasCompletedPairing: Bool {
        get { defaults.bool(forKey: ConfigKey.hasCompletedPairing) }
        set { defaults.set(newValue, forKey: ConfigKey.hasCompletedPairing) }
    }

    // MARK: Anchors

    func anchor(for kind: SyncKind) -> HKQueryAnchor? {
        let key = ConfigKey.anchorPrefix + kind.rawValue
        guard let data = defaults.data(forKey: key) else { return nil }
        return try? NSKeyedUnarchiver.unarchivedObject(ofClass: HKQueryAnchor.self, from: data)
    }

    /// Only call after a successful POST — this is the durability boundary.
    func setAnchor(_ anchor: HKQueryAnchor, for kind: SyncKind) {
        let key = ConfigKey.anchorPrefix + kind.rawValue
        if let data = try? NSKeyedArchiver.archivedData(withRootObject: anchor, requiringSecureCoding: true) {
            defaults.set(data, forKey: key)
        }
    }

    // MARK: Sync status (for UI only — advisory)

    func lastSync(for kind: SyncKind) -> Date? {
        let key = ConfigKey.lastSyncPrefix + kind.rawValue
        let t = defaults.double(forKey: key)
        return t > 0 ? Date(timeIntervalSince1970: t) : nil
    }

    func recordSuccessfulSync(for kind: SyncKind, at date: Date = Date()) {
        let key = ConfigKey.lastSyncPrefix + kind.rawValue
        defaults.set(date.timeIntervalSince1970, forKey: key)
        defaults.removeObject(forKey: ConfigKey.lastErrorPrefix + kind.rawValue)
    }

    func lastError(for kind: SyncKind) -> String? {
        defaults.string(forKey: ConfigKey.lastErrorPrefix + kind.rawValue)
    }

    func recordError(_ message: String, for kind: SyncKind) {
        defaults.set(message, forKey: ConfigKey.lastErrorPrefix + kind.rawValue)
    }

    // MARK: Watch context snapshot

    /// Snapshot of the fields the watch cares about, for WCSession
    /// `updateApplicationContext(_:)`.
    func watchContext() -> WatchConfigContext {
        WatchConfigContext(
            enabledKinds: enabledKinds,
            annotatedWorkouts: annotatedWorkouts
        )
    }
}
