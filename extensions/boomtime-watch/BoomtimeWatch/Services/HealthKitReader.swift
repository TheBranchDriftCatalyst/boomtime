//
//  HealthKitReader.swift
//  BoomtimeWatch
//
//  All HealthKit I/O: authorization, background delivery registration,
//  observers, and anchored fetches. Emits Swift-native results — no
//  wire encoding here (see PayloadBuilder).
//

import Foundation
import HealthKit

/// Bundled result of one anchored fetch: the new samples and the anchor to
/// persist iff the caller successfully uploads them.
struct AnchoredFetchResult<S> {
    let samples: [S]
    let newAnchor: HKQueryAnchor?
}

/// Errors surfaced up to SyncCoordinator.
enum HealthKitError: Error, LocalizedError {
    case notAvailable
    case authFailed(Error)
    case queryFailed(Error)

    var errorDescription: String? {
        switch self {
        case .notAvailable: return "HealthKit is not available on this device."
        case .authFailed(let e): return "HealthKit auth failed: \(e.localizedDescription)"
        case .queryFailed(let e): return "HealthKit query failed: \(e.localizedDescription)"
        }
    }
}

final class HealthKitReader {
    let store = HKHealthStore()

    // MARK: Types (iOS 15.4+ short-form initializers)

    static let workoutType = HKObjectType.workoutType()  // no short form for this one
    static let heartRateType = HKQuantityType(.heartRate)
    static let restingHeartRateType = HKQuantityType(.restingHeartRate)
    static let stepCountType = HKQuantityType(.stepCount)
    static let activeEnergyType = HKQuantityType(.activeEnergyBurned)
    static let hrvType = HKQuantityType(.heartRateVariabilitySDNN)
    static let sleepType = HKCategoryType(.sleepAnalysis)
    static let mindfulType = HKCategoryType(.mindfulSession)

    /// All types we request read access for.
    static var allReadTypes: Set<HKObjectType> {
        [
            workoutType,
            heartRateType,
            restingHeartRateType,
            stepCountType,
            activeEnergyType,
            hrvType,
            sleepType,
            mindfulType,
        ]
    }

    /// Map SyncKind -> HKSampleType for observer/anchor plumbing.
    static func sampleType(for kind: SyncKind) -> HKSampleType {
        switch kind {
        case .workouts: return workoutType
        case .heart_rate: return heartRateType
        case .resting_heart_rate: return restingHeartRateType
        case .steps: return stepCountType
        case .active_energy: return activeEnergyType
        case .hrv: return hrvType
        case .sleep_stage: return sleepType
        case .mindful: return mindfulType
        }
    }

    /// Background delivery frequency policy: workouts and sleep must be
    /// `.immediate`; other types can go `.hourly` to reduce wake pressure.
    static func backgroundFrequency(for kind: SyncKind) -> HKUpdateFrequency {
        switch kind {
        case .workouts, .sleep_stage: return .immediate
        default: return .hourly
        }
    }

    // MARK: Auth

    /// Requests read access for all types we care about. Idempotent.
    func requestAuthorization() async throws {
        guard HKHealthStore.isHealthDataAvailable() else {
            throw HealthKitError.notAvailable
        }
        do {
            // toShare is empty — we're a read-only client.
            try await store.requestAuthorization(toShare: [], read: Self.allReadTypes)
        } catch {
            throw HealthKitError.authFailed(error)
        }
    }

    // MARK: Background delivery

    /// Register background delivery for every read type in the passed set.
    /// Failures are swallowed here — the foreground sync path still works.
    ///
    /// Callers pass the current `Config.enabledKinds` so a disabled kind never
    /// wakes the app. Kinds outside the set are left in whatever state they
    /// were previously (background delivery isn't cheap to churn on the OS
    /// side; re-toggling requires an app restart to fully drop, which we
    /// document in Settings).
    func enableBackgroundDelivery(for kinds: Set<SyncKind>) async {
        for kind in kinds {
            let type = Self.sampleType(for: kind)
            let freq = Self.backgroundFrequency(for: kind)
            do {
                try await store.enableBackgroundDelivery(for: type, frequency: freq)
            } catch {
                // Non-fatal: the observer still works while the app is foregrounded.
            }
        }
    }

    // MARK: Observers

    /// Starts an HKObserverQuery for every kind in the passed set. The onFire
    /// callback runs on a background queue. IMPORTANT: the observer's
    /// completionHandler must always be invoked (in the Task here) or iOS
    /// silently throttles us.
    func startObservers(
        for kinds: Set<SyncKind>,
        onFire: @escaping (SyncKind) async -> Void
    ) {
        for kind in kinds {
            let type = Self.sampleType(for: kind)
            let query = HKObserverQuery(sampleType: type, predicate: nil) { _, completionHandler, error in
                // We complete synchronously *after* dispatching the sync work — HealthKit
                // just needs to know the observer accepted the notification, not that we've
                // finished uploading. Upload finish is our own concern via the anchor commit.
                Task {
                    if error == nil {
                        await onFire(kind)
                    }
                    completionHandler()
                }
            }
            store.execute(query)
        }
    }

    // MARK: Anchored fetches
    //
    // These use the classic 5-arg HKAnchoredObjectQuery callback form; it's the
    // best fit inside async wrappers and hasn't been deprecated.

    private func anchoredFetch<S: HKSample>(
        type: HKSampleType,
        anchor: HKQueryAnchor?,
        as _: S.Type
    ) async throws -> AnchoredFetchResult<S> {
        try await withCheckedThrowingContinuation { cont in
            let q = HKAnchoredObjectQuery(
                type: type,
                predicate: nil,
                anchor: anchor,
                limit: HKObjectQueryNoLimit
            ) { _, samples, _, newAnchor, error in
                if let error = error {
                    cont.resume(throwing: HealthKitError.queryFailed(error))
                    return
                }
                let typed = (samples as? [S]) ?? []
                cont.resume(returning: AnchoredFetchResult(samples: typed, newAnchor: newAnchor))
            }
            store.execute(q)
        }
    }

    func fetchWorkouts(anchor: HKQueryAnchor?) async throws -> AnchoredFetchResult<HKWorkout> {
        try await anchoredFetch(type: Self.workoutType, anchor: anchor, as: HKWorkout.self)
    }

    func fetchQuantitySamples(type: HKQuantityType, anchor: HKQueryAnchor?) async throws -> AnchoredFetchResult<HKQuantitySample> {
        try await anchoredFetch(type: type, anchor: anchor, as: HKQuantitySample.self)
    }

    func fetchCategorySamples(type: HKCategoryType, anchor: HKQueryAnchor?) async throws -> AnchoredFetchResult<HKCategorySample> {
        try await anchoredFetch(type: type, anchor: anchor, as: HKCategorySample.self)
    }

    // MARK: Workout-scoped HR series

    /// Fetches every HR sample explicitly linked to this workout.
    /// `predicateForObjects(from:)` scopes to samples the watch attached to the
    /// workout — much cleaner than a time-window predicate that would sweep in
    /// unrelated background HR readings.
    func heartRateSeries(for workout: HKWorkout) async throws -> [HRSeriesPoint] {
        let predicate = HKQuery.predicateForObjects(from: workout)
        return try await withCheckedThrowingContinuation { cont in
            let sort = NSSortDescriptor(key: HKSampleSortIdentifierStartDate, ascending: true)
            let q = HKSampleQuery(
                sampleType: Self.heartRateType,
                predicate: predicate,
                limit: HKObjectQueryNoLimit,
                sortDescriptors: [sort]
            ) { _, samples, error in
                if let error = error {
                    cont.resume(throwing: HealthKitError.queryFailed(error))
                    return
                }
                let points = (samples as? [HKQuantitySample])?.map(PayloadBuilder.hrPoint(from:)) ?? []
                cont.resume(returning: points)
            }
            self.store.execute(q)
        }
    }
}
