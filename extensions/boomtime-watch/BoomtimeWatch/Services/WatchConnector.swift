//
//  WatchConnector.swift
//  BoomtimeWatch (iOS)
//
//  Phone-side WatchConnectivity glue. Handles:
//
//  1. Watch → Phone messages:
//       {action: "sync-now"} — invoke SyncCoordinator.syncAll().
//         Reply {status: "ok"} or {status: "error", reason: "..."}.
//       {action: "add-annotated", preset: "<presetKey>"} — append a curated
//         AnnotatedWorkout to Config.
//       {action: "workout-started", homeDetection: "off|wifiOnly|geofenceOnly|both"}
//         — the watch just started a workout. If the requested mode ≠ off,
//         start HomePresenceMonitor with the config's HomeBase + HomeSSID.
//         Reply {status: "ok", activeMode: "<mode>"} so the watch can render
//         the correct waiting-to-return-home label.
//       {action: "workout-ended"} — stop any active HomePresenceMonitor.
//         Reply {status: "ok"}.
//
//  2. Phone → Watch:
//     - applicationContext push (enabledKinds + annotatedWorkouts) on Config
//       changes.
//     - sendMessage {action: "home-end"} when the presence monitor fires.
//

import Foundation
import WatchConnectivity
import Combine

@MainActor
final class WatchConnector: NSObject, ObservableObject {
    private let coordinator: SyncCoordinator
    private let config: Config
    private let presence: HomePresenceMonitor
    private var cancellables: Set<AnyCancellable> = []

    /// Advisory flags for the About tab.
    @Published private(set) var isWatchReachable: Bool = false
    @Published private(set) var isPaired: Bool = false

    init(coordinator: SyncCoordinator, config: Config, presence: HomePresenceMonitor) {
        self.coordinator = coordinator
        self.config = config
        self.presence = presence
        super.init()

        guard WCSession.isSupported() else { return }
        let session = WCSession.default
        session.delegate = self
        session.activate()

        // Push updates whenever the user toggles a kind or edits presets.
        config.$enabledKinds
            .dropFirst()
            .sink { [weak self] _ in self?.pushContext() }
            .store(in: &cancellables)
        config.$annotatedWorkouts
            .dropFirst()
            .sink { [weak self] _ in self?.pushContext() }
            .store(in: &cancellables)
    }

    /// Coalesced fire-and-forget push. WCSession dedupes multiple calls before
    /// delivery; only the most recent context arrives on the watch.
    func pushContext() {
        guard WCSession.isSupported() else { return }
        let session = WCSession.default
        guard session.activationState == .activated else { return }
        let dict = config.watchContext().toDictionary()
        do {
            try session.updateApplicationContext(dict)
        } catch {
            // Non-fatal — next successful push will overwrite.
        }
    }

    /// Fire-and-forget "home-end" ping to the watch. Called by
    /// HomePresenceMonitor's onArrivedHome closure. When the watch isn't
    /// reachable we silently drop the message — the workout will still end on
    /// timer (or when the user returns and taps End manually).
    private func sendHomeEndToWatch() {
        guard WCSession.isSupported() else { return }
        let session = WCSession.default
        guard session.activationState == .activated, session.isReachable else { return }
        session.sendMessage(
            ["action": "home-end"],
            replyHandler: nil,
            errorHandler: { _ in
                // Best-effort. If we can't reach the watch there's nothing else
                // to try — the workout's own timer or manual End catches it.
            }
        )
    }
}

extension WatchConnector: WCSessionDelegate {

    nonisolated func session(_ session: WCSession, activationDidCompleteWith activationState: WCSessionActivationState, error: Error?) {
        Task { @MainActor in
            self.isPaired = session.isPaired
            self.isWatchReachable = session.isReachable
            if activationState == .activated {
                self.pushContext()
            }
        }
    }

    nonisolated func sessionDidBecomeInactive(_ session: WCSession) {}

    /// iOS requires we reactivate after deactivate — WCSession deactivates
    /// when the paired watch changes.
    nonisolated func sessionDidDeactivate(_ session: WCSession) {
        WCSession.default.activate()
    }

    nonisolated func sessionReachabilityDidChange(_ session: WCSession) {
        Task { @MainActor in
            self.isWatchReachable = session.isReachable
        }
    }

    nonisolated func sessionWatchStateDidChange(_ session: WCSession) {
        Task { @MainActor in
            self.isPaired = session.isPaired
            self.isWatchReachable = session.isReachable
        }
    }

    nonisolated func session(
        _ session: WCSession,
        didReceiveMessage message: [String: Any],
        replyHandler: @escaping ([String: Any]) -> Void
    ) {
        guard let action = message["action"] as? String else {
            replyHandler(["status": "error", "reason": "missing action"])
            return
        }
        switch action {
        case "sync-now":
            Task { @MainActor in
                await self.coordinator.syncAll()
                replyHandler(["status": "ok"])
            }
        case "add-annotated":
            let presetKey = message["preset"] as? String ?? ""
            guard let preset = WorkoutPreset(rawValue: presetKey) else {
                replyHandler(["status": "error", "reason": "unknown preset: \(presetKey)"])
                return
            }
            Task { @MainActor in
                self.config.addPreset(preset)
                // The @Published sink pushes an updated applicationContext to the
                // watch automatically — no explicit push needed here.
                replyHandler(["status": "ok"])
            }
        case "workout-started":
            let rawMode = (message["homeDetection"] as? String) ?? "off"
            let requested = HomeDetection(rawValue: rawMode) ?? .off
            Task { @MainActor in
                await self.presence.startMonitoring(
                    detection: requested,
                    home: self.config.homeBase,
                    wifi: self.config.homeSSID,
                    onArrivedHome: { [weak self] in
                        // Bounce onto the main actor to touch WCSession safely.
                        Task { @MainActor [weak self] in
                            self?.sendHomeEndToWatch()
                        }
                    }
                )
                // Reply with the actually-active mode; the requested value may
                // have downgraded (e.g. .both → .geofenceOnly when SSID is
                // missing) so the watch's UI label matches reality.
                replyHandler([
                    "status": "ok",
                    "activeMode": self.presence.activeMode.rawValue,
                ])
            }
        case "workout-ended":
            Task { @MainActor in
                await self.presence.stopMonitoring()
                replyHandler(["status": "ok"])
            }
        default:
            replyHandler(["status": "error", "reason": "unknown action: \(action)"])
        }
    }
}
