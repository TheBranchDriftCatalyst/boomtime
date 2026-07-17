//
//  HomePresenceMonitor.swift
//  BoomtimeWatch (iOS only)
//
//  Phone-side "user has arrived home" detector. Three modes:
//
//    .wifiOnly     — foreground poll of NEHotspotNetwork.fetchCurrent() every
//                    30s. Honest about only firing while the app is foregrounded;
//                    Settings copy warns the user.
//    .geofenceOnly — CLMonitor circular condition on HomeBase. Fires on region
//                    re-entry (state transitions from unsatisfied → satisfied).
//                    Works while backgrounded/terminated (iOS wakes us).
//    .both         — Geofence wakes the phone; SSID confirms actual arrival.
//                    On geofence entry we fetch the current SSID immediately —
//                    match ⇒ fire; no-match ⇒ silently keep the geofence armed
//                    for the next boundary crossing. Cap the whole thing at 5
//                    minutes so we can't leak a monitor if a workout is somehow
//                    never marked ended.
//
//  Single-shot per call: `startMonitoring` fires `onArrivedHome` at most once,
//  then teardown. The caller (WatchConnector) is expected to call
//  `stopMonitoring` when the workout ends regardless of who ended it.
//
//  Wrapped in `#if os(iOS)` — CLMonitor and NetworkExtension are iOS-only
//  APIs, and the whole feature lives on the phone anyway.
//

#if os(iOS)

import Foundation
import CoreLocation
import NetworkExtension

@MainActor
final class HomePresenceMonitor: NSObject, ObservableObject {
    /// True while we have an active monitor / task in flight. Surfaced to
    /// SettingsView and the About tab for debugging.
    @Published private(set) var isMonitoring: Bool = false

    /// The active mode — used by WatchConnector to tell the watch which UI
    /// label to render on LiveWorkoutView.
    @Published private(set) var activeMode: HomeDetection = .off

    /// A short human-readable reason for the most recent teardown. Nil while
    /// a monitor is running or immediately after a successful home-arrival fire.
    @Published private(set) var lastFailureReason: String? = nil

    /// Injected Wi-Fi presence surface — the live impl calls NEHotspotNetwork
    /// (returns nil on Simulator unless overridden by SimulatorOverrides);
    /// tests inject a FakeWiFiPresenceProvider. Default keeps existing call
    /// sites unchanged.
    private let wifi: WiFiPresenceProviding

    /// Overridable poll cadence for `.wifiOnly` mode. Instance-level so tests
    /// can drop it to milliseconds without waiting 30s per assertion.
    private let wifiPollInterval: Duration

    /// Overridable deadline for a monitoring session. Instance-level for the
    /// same reason as above.
    private let monitoringDeadline: Duration

    // Live-monitoring state.
    private var monitor: CLMonitor?
    private var eventTask: Task<Void, Never>?
    private var wifiPollTask: Task<Void, Never>?
    private var deadlineTask: Task<Void, Never>?
    private var onArrived: (@Sendable () -> Void)?

    /// Registered condition identifier — one at a time in v1.
    private static let conditionID = "boomtime.home"

    /// Default 5-minute hard cap on any single monitoring session. Prevents a
    /// stuck loop (e.g. geofence armed but SSID never matches) from leaking a
    /// monitor.
    static let defaultMonitoringDeadline: Duration = .seconds(5 * 60)

    /// Default Wi-Fi poll cadence for `.wifiOnly` mode.
    static let defaultWiFiPollInterval: Duration = .seconds(30)

    init(
        wifi: WiFiPresenceProviding = LiveWiFiPresenceProvider(),
        wifiPollInterval: Duration = HomePresenceMonitor.defaultWiFiPollInterval,
        monitoringDeadline: Duration = HomePresenceMonitor.defaultMonitoringDeadline
    ) {
        self.wifi = wifi
        self.wifiPollInterval = wifiPollInterval
        self.monitoringDeadline = monitoringDeadline
        super.init()
    }

    // MARK: Lifecycle

    /// Start monitoring for "arrived home" using the given detection strategy.
    /// - `home` and `wifi` come from Config; both are optional so the caller
    ///   doesn't have to pre-validate them — this method downgrades the mode
    ///   at runtime.
    /// - `onArrivedHome` fires at most once, on the main actor.
    ///
    /// Idempotent: calling `start` a second time tears down the first monitor
    /// and starts fresh with the new args.
    func startMonitoring(
        detection: HomeDetection,
        home: HomeBase?,
        wifi: HomeSSID?,
        onArrivedHome: @Sendable @escaping () -> Void
    ) async {
        await stopMonitoring()

        // Runtime downgrade: if the referenced signal is missing, treat as off.
        let effective = resolveMode(requested: detection, home: home, wifi: wifi)
        activeMode = effective
        onArrived = onArrivedHome

        switch effective {
        case .off:
            lastFailureReason = degradeReason(requested: detection, home: home, wifi: wifi)
            return

        case .wifiOnly:
            guard let wifi else {
                lastFailureReason = "Wi-Fi mode picked but no home SSID configured"
                return
            }
            isMonitoring = true
            startWifiPoll(target: wifi)
            startDeadline()

        case .geofenceOnly:
            guard let home else {
                lastFailureReason = "Geofence mode picked but no home base configured"
                return
            }
            isMonitoring = true
            await startGeofence(home: home, confirmWith: nil)
            startDeadline()

        case .both:
            // Home is guaranteed non-nil by resolveMode when we land here with
            // .both; wifi likewise. Force-unwraps are safe.
            isMonitoring = true
            await startGeofence(home: home!, confirmWith: wifi!)
            startDeadline()
        }
    }

    /// Cancel any in-flight monitoring. Safe to call redundantly.
    func stopMonitoring() async {
        eventTask?.cancel(); eventTask = nil
        wifiPollTask?.cancel(); wifiPollTask = nil
        deadlineTask?.cancel(); deadlineTask = nil
        if let monitor {
            // CLMonitor.remove is async; awaiting it here means callers know
            // the OS-level registration is really gone.
            await monitor.remove(Self.conditionID)
        }
        isMonitoring = false
        activeMode = .off
        onArrived = nil
    }

    // MARK: Mode resolution

    /// Downgrade rules per the spec:
    /// - `.wifiOnly` needs a wifi target — downgrade to `.off` when missing.
    /// - `.geofenceOnly` needs a home — downgrade to `.off` when missing.
    /// - `.both` downgrades to whichever half is available, or `.off`.
    private func resolveMode(requested: HomeDetection, home: HomeBase?, wifi: HomeSSID?) -> HomeDetection {
        switch requested {
        case .off:
            return .off
        case .wifiOnly:
            return wifi != nil ? .wifiOnly : .off
        case .geofenceOnly:
            return home != nil ? .geofenceOnly : .off
        case .both:
            switch (home, wifi) {
            case (.some, .some): return .both
            case (.some, .none): return .geofenceOnly
            case (.none, .some): return .wifiOnly
            case (.none, .none): return .off
            }
        }
    }

    private func degradeReason(requested: HomeDetection, home: HomeBase?, wifi: HomeSSID?) -> String? {
        switch requested {
        case .off: return nil
        case .wifiOnly where wifi == nil: return "Home Wi-Fi not set"
        case .geofenceOnly where home == nil: return "Home Base not set"
        case .both where home == nil && wifi == nil: return "Home Base and Home Wi-Fi not set"
        default: return nil
        }
    }

    // MARK: Geofence

    /// Start a CLMonitor watching the home region. When it enters the
    /// `.satisfied` state, either fire immediately (`.geofenceOnly`) or
    /// confirm via SSID (`.both`).
    private func startGeofence(home: HomeBase, confirmWith wifi: HomeSSID?) async {
        do {
            // CLMonitor is iOS 17+. Name namespaces the OS-level monitor —
            // reusing the same name across launches reuses the same monitor
            // instance, which matches the spec's "lazy / reuse" note.
            let m = await CLMonitor("boomtime.homebase")
            self.monitor = m
            let condition = CLMonitor.CircularGeographicCondition(
                center: CLLocationCoordinate2D(latitude: home.lat, longitude: home.lon),
                radius: home.radiusM
            )
            await m.add(condition, identifier: Self.conditionID)

            // Drive events on a Task we can cancel from stopMonitoring().
            eventTask = Task { [weak self] in
                guard let self else { return }
                for try await event in await m.events {
                    if Task.isCancelled { break }
                    // We only care about entry (transition to `.satisfied`).
                    guard event.state == .satisfied else { continue }
                    if let wifi {
                        // Hybrid mode: confirm via SSID before firing.
                        if await self.currentWifiMatches(target: wifi) {
                            await self.fireArrivedHome()
                            break
                        }
                        // No SSID match — keep the monitor armed. iOS will
                        // deliver another `.satisfied` next time the user
                        // re-crosses the boundary. That's fine.
                    } else {
                        // Pure geofence mode: fire immediately.
                        await self.fireArrivedHome()
                        break
                    }
                }
            }
        } catch {
            // CLMonitor's throwing surface is narrow — auth issues surface as
            // no events rather than errors. Still, surface any failure so
            // Settings can show it.
            self.lastFailureReason = "Geofence start failed: \(error.localizedDescription)"
            self.isMonitoring = false
        }
    }

    // MARK: Wi-Fi

    /// Foreground poll — configurable cadence until SSID matches, we're
    /// cancelled, or the deadline hits.
    private func startWifiPoll(target: HomeSSID) {
        wifiPollTask = Task { [weak self] in
            guard let self else { return }
            let interval = self.wifiPollInterval
            while !Task.isCancelled {
                if await self.currentWifiMatches(target: target) {
                    await self.fireArrivedHome()
                    return
                }
                try? await Task.sleep(for: interval)
            }
        }
    }

    /// One-shot SSID fetch + match. Returns false on any failure (no auth,
    /// not on Wi-Fi, iOS 14+ SSID redaction, etc.) so callers can just keep
    /// polling / stay armed.
    private func currentWifiMatches(target: HomeSSID) async -> Bool {
        // Routed through the injected WiFiPresenceProviding — the live impl
        // still calls NEHotspotNetwork, but tests + Simulator overrides can
        // swap in a fake without touching this code path.
        guard let net = await wifi.currentNetwork() else {
            lastFailureReason = "Not on Wi-Fi, or Location auth denied"
            return false
        }
        return target.matches(ssid: net.ssid, bssid: net.bssid)
    }

    // MARK: Terminal

    /// Fire the onArrivedHome callback exactly once and tear down.
    private func fireArrivedHome() async {
        let callback = onArrived
        await stopMonitoring()
        // Clear failure reason on a successful fire — it was working after all.
        lastFailureReason = nil
        callback?()
    }

    /// Post a hard-cap tear-down task; if the deadline hits before we've fired,
    /// the monitor is dropped silently.
    private func startDeadline() {
        deadlineTask?.cancel()
        let deadline = monitoringDeadline
        deadlineTask = Task { [weak self] in
            try? await Task.sleep(for: deadline)
            guard let self, !Task.isCancelled else { return }
            await self.stopMonitoring()
            self.lastFailureReason = "Monitor timed out"
        }
    }
}

#endif
