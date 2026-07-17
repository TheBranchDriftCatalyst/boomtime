//
//  WorkoutController.swift
//  BoomtimeWatch Watch App
//
//  HKWorkoutSession + HKLiveWorkoutBuilder wrapper. Exposes @Published
//  currentHR / kcal / elapsedSeconds / remainingSeconds for the live view.
//
//  Wrapped in `#if os(watchOS)` because HKWorkoutSession is watchOS-only —
//  SourceKit checking against macOS otherwise flags it as unavailable.
//
//  Sequence:
//    1. requestAuthorization() (async) — HealthKit read/write for HR +
//       active energy + workoutType.
//    2. start(activityRaw:, defaultDurationMin:, endBehavior:) — build a
//       HKWorkoutConfiguration, create the session + builder, begin
//       collection. Kick a single Timer that drives elapsed + remaining.
//    3. Live: HKLiveWorkoutBuilderDelegate fires; we recompute @Publisheds
//       on the main actor.
//    4. When countdown hits 0:
//         .manual  -> no timer wiring exists (durationMin==nil), no-op
//         .haptic  -> WKInterfaceDevice notification, timer stops decrementing
//                     (falls back to elapsed-only display).
//         .autoEnd -> haptic + end().
//    5. end() — session.end() -> builder.endCollection -> builder.finishWorkout.
//       Cancels the timer inside teardown. Finished HKWorkout is saved
//       automatically; phone-side observer posts it.
//

#if os(watchOS)

import Foundation
import HealthKit
import WatchKit
import Combine

@MainActor
final class WorkoutController: NSObject, ObservableObject {
    private let store = HKHealthStore()
    private var session: HKWorkoutSession?
    private var builder: HKLiveWorkoutBuilder?

    /// Single tick source for elapsed + remaining.
    private var timerCancellable: AnyCancellable?

    /// Config captured at start().
    private var workoutStart: Date?
    private var durationSeconds: Int?
    private var endBehavior: WorkoutEndBehavior = .manual
    /// Once we've fired the haptic for the countdown (so we don't repeatedly
    /// trigger it as elapsed climbs past duration).
    private var haveFiredEndHaptic = false

    @Published private(set) var isActive: Bool = false
    @Published private(set) var currentActivityRaw: Int? = nil
    @Published private(set) var elapsedSeconds: Int = 0
    /// Nil when the session is open-ended (durationMin == nil) or when the
    /// countdown has already fired and behavior==.haptic (we drop to elapsed-only).
    @Published private(set) var remainingSeconds: Int? = nil
    @Published private(set) var currentHR: Int? = nil
    @Published private(set) var kcal: Double = 0
    @Published private(set) var errorMessage: String? = nil

    // MARK: Auth

    func requestAuthorization() async throws {
        let write: Set<HKSampleType> = [
            HKObjectType.workoutType(),
            HKQuantityType(.activeEnergyBurned),
            HKQuantityType(.heartRate),
        ]
        let read: Set<HKObjectType> = [
            HKQuantityType(.heartRate),
            HKQuantityType(.activeEnergyBurned),
            HKObjectType.workoutType(),
        ]
        try await store.requestAuthorization(toShare: write, read: read)
    }

    // MARK: Lifecycle

    /// Start a workout for the given HKWorkoutActivityType.rawValue.
    /// - `defaultDurationMin` nil → open-ended (no countdown, no haptic).
    /// - `endBehavior` interpreted at the countdown-zero moment.
    func start(activityRaw: Int, defaultDurationMin: Int? = nil, endBehavior: WorkoutEndBehavior = .manual) {
        guard !isActive else {
            errorMessage = "A workout is already in progress"
            return
        }
        errorMessage = nil
        haveFiredEndHaptic = false

        let activity = HKWorkoutActivityType(rawValue: UInt(activityRaw)) ?? .other
        let config = HKWorkoutConfiguration()
        config.activityType = activity
        config.locationType = Self.locationType(for: activity)

        do {
            let session = try HKWorkoutSession(healthStore: store, configuration: config)
            let builder = session.associatedWorkoutBuilder()
            builder.dataSource = HKLiveWorkoutDataSource(healthStore: store, workoutConfiguration: config)
            session.delegate = self
            builder.delegate = self

            self.session = session
            self.builder = builder
            self.currentActivityRaw = activityRaw
            self.endBehavior = endBehavior
            if let min = defaultDurationMin {
                self.durationSeconds = min * 60
                self.remainingSeconds = min * 60
            } else {
                self.durationSeconds = nil
                self.remainingSeconds = nil
            }

            let now = Date()
            session.startActivity(with: now)
            builder.beginCollection(withStart: now) { [weak self] _, error in
                Task { @MainActor in
                    if let error = error {
                        self?.errorMessage = "beginCollection failed: \(error.localizedDescription)"
                        return
                    }
                    self?.workoutStart = now
                    self?.isActive = true
                    self?.startTicker()
                }
            }
        } catch {
            errorMessage = "Failed to start session: \(error.localizedDescription)"
        }
    }

    func end() {
        guard let session, let builder else { return }
        session.end()
        let endDate = Date()
        builder.endCollection(withEnd: endDate) { [weak self] _, error in
            if let error = error {
                Task { @MainActor in
                    self?.errorMessage = "endCollection failed: \(error.localizedDescription)"
                }
                return
            }
            builder.finishWorkout { [weak self] _, error in
                Task { @MainActor in
                    if let error = error {
                        self?.errorMessage = "finishWorkout failed: \(error.localizedDescription)"
                        return
                    }
                    self?.teardown()
                }
            }
        }
    }

    private func teardown() {
        timerCancellable?.cancel()
        timerCancellable = nil
        session = nil
        builder = nil
        isActive = false
        currentActivityRaw = nil
        workoutStart = nil
        durationSeconds = nil
        remainingSeconds = nil
        endBehavior = .manual
        haveFiredEndHaptic = false
        elapsedSeconds = 0
        currentHR = nil
        kcal = 0
    }

    /// Single 1Hz tick source for elapsed + countdown. Timer.publish auto-cleans
    /// when the cancellable is cancelled in teardown — no leaked DispatchQueue
    /// asyncAfter to chase after `session.end()`.
    private func startTicker() {
        timerCancellable = Timer.publish(every: 1, on: .main, in: .common)
            .autoconnect()
            .sink { [weak self] _ in
                Task { @MainActor in
                    self?.tick()
                }
            }
    }

    private func tick() {
        guard let start = workoutStart else { return }
        let e = max(0, Int(Date().timeIntervalSince(start)))
        elapsedSeconds = e

        // Countdown handling (only when duration was set).
        guard let dur = durationSeconds else { return }
        let remaining = dur - e
        if remaining > 0 {
            remainingSeconds = remaining
        } else {
            // Countdown reached zero.
            if !haveFiredEndHaptic {
                haveFiredEndHaptic = true
                WKInterfaceDevice.current().play(.notification)
                switch endBehavior {
                case .manual, .haptic:
                    // Fall back to elapsed-only display.
                    remainingSeconds = nil
                case .autoEnd:
                    remainingSeconds = 0
                    end()
                }
            } else if endBehavior == .haptic {
                // Already fired; keep remaining nil so UI stays in elapsed mode.
                remainingSeconds = nil
            }
        }
    }

    private static func locationType(for activity: HKWorkoutActivityType) -> HKWorkoutSessionLocationType {
        switch activity {
        case .running, .cycling, .hiking, .walking, .swimming:
            return .outdoor
        case .functionalStrengthTraining, .traditionalStrengthTraining,
             .yoga, .pilates, .highIntensityIntervalTraining,
             .rowing, .elliptical:
            return .indoor
        default:
            return .unknown
        }
    }
}

// MARK: - HKWorkoutSessionDelegate

extension WorkoutController: HKWorkoutSessionDelegate {
    nonisolated func workoutSession(
        _ workoutSession: HKWorkoutSession,
        didChangeTo toState: HKWorkoutSessionState,
        from fromState: HKWorkoutSessionState,
        date: Date
    ) {
        // No-op for v1.
    }

    nonisolated func workoutSession(_ workoutSession: HKWorkoutSession, didFailWithError error: Error) {
        Task { @MainActor in
            self.errorMessage = "Workout failed: \(error.localizedDescription)"
            self.teardown()
        }
    }
}

// MARK: - HKLiveWorkoutBuilderDelegate

extension WorkoutController: HKLiveWorkoutBuilderDelegate {
    nonisolated func workoutBuilderDidCollectEvent(_ workoutBuilder: HKLiveWorkoutBuilder) {
        // No event granularity needed for v1.
    }

    nonisolated func workoutBuilder(_ workoutBuilder: HKLiveWorkoutBuilder, didCollectDataOf collectedTypes: Set<HKSampleType>) {
        let hrType = HKQuantityType(.heartRate)
        let kcalType = HKQuantityType(.activeEnergyBurned)

        var newHR: Int? = nil
        var newKcal: Double? = nil

        if collectedTypes.contains(hrType),
           let stat = workoutBuilder.statistics(for: hrType),
           let q = stat.mostRecentQuantity() {
            let bpm = q.doubleValue(for: HKUnit.count().unitDivided(by: .minute()))
            newHR = Int(bpm.rounded())
        }
        if collectedTypes.contains(kcalType),
           let stat = workoutBuilder.statistics(for: kcalType),
           let q = stat.sumQuantity() {
            newKcal = q.doubleValue(for: .kilocalorie())
        }

        Task { @MainActor in
            if let newHR   { self.currentHR = newHR }
            if let newKcal { self.kcal = newKcal }
        }
    }
}

#endif
