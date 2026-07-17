//
//  WatchConnector.swift
//  BoomtimeWatch Watch App
//
//  watchOS-side WCSession client. Responsibilities:
//    1. Receives applicationContext pushes from the phone (Set<SyncKind> +
//       annotatedWorkouts) and republishes them for the UI to consume.
//    2. Sends messages to the phone:
//         {action: "sync-now"} — force a full sync.
//         {action: "add-annotated", preset: "<key>"} — ask the phone to
//            append the matching WorkoutPreset to its annotated-workout list.
//         {action: "workout-started", homeDetection: "..."} — informs the
//            phone that a workout has started and (if requested) it should
//            start HomePresenceMonitor. Reply carries the actually-active
//            mode after runtime downgrades.
//         {action: "workout-ended"} — informs the phone to stop any home
//            presence monitoring for this session.
//    3. Receives {action: "home-end"} from the phone when its presence
//       monitor fires and invokes `onHomeEnd`, which LiveWorkoutView wires to
//       WorkoutController.end() at .task time.
//
//  Wrapped in `#if canImport(WatchConnectivity)` so SourceKit stays quiet on
//  the macOS toolchain (WatchConnectivity is only present on iOS + watchOS).
//

#if canImport(WatchConnectivity)

import Foundation
import WatchConnectivity
import Combine

@MainActor
final class WatchConnector: NSObject, ObservableObject {
    /// Latest context from the phone. Defaults preserve the "all-on" behavior
    /// so the picker still shows every kind before the first push lands.
    @Published var context: WatchConfigContext = .empty

    /// The mode the phone confirmed is actually running for the current
    /// workout. Drives LiveWorkoutView's "waiting to return home" label.
    /// Reset to .off after every workout-ended send.
    @Published var activeHomeMode: HomeDetection = .off

    /// Toast state for the Sync Now button.
    enum SyncOutcome: Equatable {
        case idle
        case inFlight
        case ok
        case failed(reason: String)
    }
    @Published var lastSyncOutcome: SyncOutcome = .idle

    /// Ephemeral toast for "+ Add preset" outcomes on Settings.
    @Published var lastAddOutcome: SyncOutcome = .idle

    /// Registered by LiveWorkoutView on task-start. Invoked from the main
    /// actor when the phone sends {action: "home-end"} — the view wires this
    /// to WorkoutController.end(). Cleared automatically on `sendWorkoutEnded`
    /// so a stale callback can't fire after a workout has already ended.
    private var onHomeEnd: (@MainActor () -> Void)?

    override init() {
        super.init()
        guard WCSession.isSupported() else { return }
        let session = WCSession.default
        session.delegate = self
        session.activate()

        if let ctx = WatchConfigContext.fromDictionary(session.receivedApplicationContext) {
            self.context = ctx
        }
    }

    // MARK: Sync now / add preset

    /// Fire-and-forget "sync now". Immediate reachability check — if the phone
    /// isn't up we surface the failure now rather than queueing.
    func sendSyncNow() {
        guard WCSession.isSupported() else {
            lastSyncOutcome = .failed(reason: "WCSession not supported")
            return
        }
        let session = WCSession.default
        guard session.activationState == .activated else {
            lastSyncOutcome = .failed(reason: "Session not activated")
            return
        }
        guard session.isReachable else {
            lastSyncOutcome = .failed(reason: "iPhone unreachable")
            return
        }
        lastSyncOutcome = .inFlight
        session.sendMessage(
            ["action": "sync-now"],
            replyHandler: { [weak self] reply in
                Task { @MainActor in
                    let status = reply["status"] as? String ?? "unknown"
                    if status == "ok" {
                        self?.lastSyncOutcome = .ok
                    } else {
                        let reason = reply["reason"] as? String ?? status
                        self?.lastSyncOutcome = .failed(reason: reason)
                    }
                }
            },
            errorHandler: { [weak self] error in
                Task { @MainActor in
                    self?.lastSyncOutcome = .failed(reason: error.localizedDescription)
                }
            }
        )
    }

    /// Ask the phone to append a preset to its annotated-workout list. The
    /// updated context flows back automatically via applicationContext.
    func addAnnotated(preset: WorkoutPreset) {
        guard WCSession.isSupported() else {
            lastAddOutcome = .failed(reason: "WCSession not supported")
            return
        }
        let session = WCSession.default
        guard session.activationState == .activated else {
            lastAddOutcome = .failed(reason: "Session not activated")
            return
        }
        guard session.isReachable else {
            lastAddOutcome = .failed(reason: "iPhone unreachable")
            return
        }
        lastAddOutcome = .inFlight
        session.sendMessage(
            ["action": "add-annotated", "preset": preset.rawValue],
            replyHandler: { [weak self] reply in
                Task { @MainActor in
                    let status = reply["status"] as? String ?? "unknown"
                    if status == "ok" {
                        self?.lastAddOutcome = .ok
                    } else {
                        let reason = reply["reason"] as? String ?? status
                        self?.lastAddOutcome = .failed(reason: reason)
                    }
                }
            },
            errorHandler: { [weak self] error in
                Task { @MainActor in
                    self?.lastAddOutcome = .failed(reason: error.localizedDescription)
                }
            }
        )
    }

    // MARK: Workout lifecycle

    /// Tell the phone a workout just started with the given home-detection
    /// preference. `onHomeEnd` is retained until `sendWorkoutEnded` is called
    /// (or the workout view disappears); when the phone reports "home-end",
    /// we invoke it exactly once and clear the reference.
    ///
    /// The phone's reply carries the effective mode (after runtime
    /// downgrades); we publish it so LiveWorkoutView can render the accurate
    /// waiting label. When the watch is unreachable, we silently record
    /// `.off` — the workout still runs; home-end just won't fire.
    func sendWorkoutStarted(homeDetection: HomeDetection, onHomeEnd: @MainActor @escaping () -> Void) {
        self.onHomeEnd = onHomeEnd
        activeHomeMode = .off  // reset until phone confirms

        guard homeDetection != .off else { return }
        guard WCSession.isSupported() else { return }
        let session = WCSession.default
        guard session.activationState == .activated, session.isReachable else { return }
        session.sendMessage(
            ["action": "workout-started", "homeDetection": homeDetection.rawValue],
            replyHandler: { [weak self] reply in
                Task { @MainActor in
                    let modeRaw = reply["activeMode"] as? String ?? "off"
                    self?.activeHomeMode = HomeDetection(rawValue: modeRaw) ?? .off
                }
            },
            errorHandler: { _ in
                // Best-effort — workout still runs, we just don't get home-end.
            }
        )
    }

    /// Tell the phone the workout ended (any reason: manual/timer/home).
    /// Clears the retained home-end callback so a late-arriving message can't
    /// re-trigger it.
    func sendWorkoutEnded() {
        onHomeEnd = nil
        activeHomeMode = .off
        guard WCSession.isSupported() else { return }
        let session = WCSession.default
        guard session.activationState == .activated, session.isReachable else { return }
        session.sendMessage(
            ["action": "workout-ended"],
            replyHandler: nil,
            errorHandler: { _ in }
        )
    }
}

extension WatchConnector: WCSessionDelegate {
    nonisolated func session(_ session: WCSession, activationDidCompleteWith activationState: WCSessionActivationState, error: Error?) {
        // Context is already seeded from receivedApplicationContext in init.
    }

    nonisolated func session(_ session: WCSession, didReceiveApplicationContext applicationContext: [String: Any]) {
        Task { @MainActor in
            if let ctx = WatchConfigContext.fromDictionary(applicationContext) {
                self.context = ctx
            }
        }
    }

    /// The phone occasionally pushes messages (not just replies). Only one
    /// action for now: home-end.
    nonisolated func session(_ session: WCSession, didReceiveMessage message: [String: Any]) {
        guard let action = message["action"] as? String else { return }
        Task { @MainActor in
            switch action {
            case "home-end":
                // Fire once and drop the reference — same as we'd do on
                // sendWorkoutEnded so a subsequent stray message can't
                // double-trigger End on the next session.
                let cb = self.onHomeEnd
                self.onHomeEnd = nil
                cb?()
            default:
                break
            }
        }
    }
}

#endif
