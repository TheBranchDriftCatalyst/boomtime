//
//  PayloadBuilder.swift
//  BoomtimeWatch
//
//  Pure conversion functions: HKSample / HKWorkout -> our wire structs.
//  Kept side-effect-free (aside from the HK statistics API on HKWorkout,
//  which is a read on the sample itself, not the store) so
//  PayloadBuilderTests can exercise the mappings without a live store.
//

import Foundation
import HealthKit

extension HKWorkoutActivityType {
    /// Lowercase, snake_case name the backend expects for `kind`.
    /// `@unknown default` case future-proofs against new activity types.
    var wireName: String {
        switch self {
        case .running: return "running"
        case .walking: return "walking"
        case .hiking: return "hiking"
        case .cycling: return "cycling"
        case .swimming: return "swimming"
        case .yoga: return "yoga"
        case .functionalStrengthTraining: return "functional_strength"
        case .traditionalStrengthTraining: return "strength"
        case .highIntensityIntervalTraining: return "hiit"
        case .rowing: return "rowing"
        case .elliptical: return "elliptical"
        case .stairClimbing, .stairs, .stepTraining: return "stairs"
        case .coreTraining: return "core"
        case .pilates: return "pilates"
        case .flexibility: return "flexibility"
        case .dance, .cardioDance, .socialDance: return "dance"
        case .mixedCardio: return "mixed_cardio"
        case .crossTraining: return "cross_training"
        case .soccer: return "soccer"
        case .basketball: return "basketball"
        case .tennis: return "tennis"
        case .martialArts, .kickboxing, .boxing: return "martial_arts"
        case .climbing: return "climbing"
        case .other: return "other"
        @unknown default: return "type_\(rawValue)"
        }
    }
}

enum PayloadBuilder {

    // MARK: Workouts

    /// Legacy entrypoint kept for tests. Returns the same string as
    /// `HKWorkoutActivityType.wireName`.
    static func workoutKindString(_ type: HKWorkoutActivityType) -> String {
        return type.wireName
    }

    /// Pick the correct distance quantity type for an activity, or nil if the
    /// activity doesn't have a distance concept (yoga, strength, etc.).
    static func distanceType(for activity: HKWorkoutActivityType) -> HKQuantityType? {
        switch activity {
        case .running, .walking, .hiking:
            return HKQuantityType(.distanceWalkingRunning)
        case .cycling:
            return HKQuantityType(.distanceCycling)
        case .swimming:
            return HKQuantityType(.distanceSwimming)
        default:
            return nil
        }
    }

    /// Build the wire payload for a workout.
    ///
    /// `hrSeries` and `route` come from separate HealthKit queries the caller
    /// has already run (SyncCoordinator handles that).
    ///
    /// `labels` is the user-configured [wireKind: userLabel] map from Config;
    /// when a non-empty override exists for the workout's kind, it's threaded
    /// through as the payload's optional `label`. Server side treats `label` as
    /// the project bucket (falls back to `kind` when nil/empty), so raw kind
    /// aggregation stays stable.
    ///
    /// Energy and distance use HKWorkout.statistics(for:) — the direct
    /// `totalEnergyBurned`/`totalDistance` properties are deprecated in iOS 18.
    static func makeWorkoutPayload(
        workout: HKWorkout,
        hrSeries: [HRSeriesPoint]?,
        route: [RoutePoint]?,
        labels: [String: String] = [:]
    ) -> WorkoutPayload {
        let energyType = HKQuantityType(.activeEnergyBurned)
        let kcal = workout.statistics(for: energyType)?
            .sumQuantity()?
            .doubleValue(for: .kilocalorie())

        let distance: Double?
        if let dtype = distanceType(for: workout.workoutActivityType) {
            distance = workout.statistics(for: dtype)?
                .sumQuantity()?
                .doubleValue(for: .meter())
        } else {
            distance = nil
        }

        // Average HR from series if we have it. HealthKit doesn't hand us a
        // ready-made "avg HR for this workout" number.
        let avgHR: Int?
        if let s = hrSeries, !s.isEmpty {
            let mean = Double(s.reduce(0) { $0 + $1.bpm }) / Double(s.count)
            avgHR = Int(mean.rounded())
        } else {
            avgHR = nil
        }

        let rawKind = workout.workoutActivityType.wireName
        // Only pass through a label when the user explicitly set one; a nil/empty
        // override lets the server keep its existing "project == kind" behavior.
        let userLabel: String? = {
            let override = labels[rawKind]?.trimmingCharacters(in: .whitespacesAndNewlines)
            return (override?.isEmpty == false) ? override : nil
        }()

        return WorkoutPayload(
            kind: rawKind,
            label: userLabel,
            start: workout.startDate.timeIntervalSince1970,
            end: workout.endDate.timeIntervalSince1970,
            duration_s: Int(workout.duration.rounded()),
            kcal: kcal,
            distance_m: distance,
            avg_hr: avgHR,
            hr_series: (hrSeries?.isEmpty ?? true) ? nil : hrSeries,
            route: (route?.isEmpty ?? true) ? nil : route,
            source_uuid: workout.uuid.uuidString
        )
    }

    /// Convert an HR HKQuantitySample to a single series point.
    /// Uses `startDate` since HR is instantaneous.
    static func hrPoint(from sample: HKQuantitySample) -> HRSeriesPoint {
        let bpmUnit = HKUnit.count().unitDivided(by: .minute())
        let bpm = Int(sample.quantity.doubleValue(for: bpmUnit).rounded())
        return HRSeriesPoint(t: sample.startDate.timeIntervalSince1970, bpm: bpm)
    }

    // MARK: Quantity samples

    /// Instantaneous HR sample -> wire payload.
    static func heartRateSample(_ s: HKQuantitySample, workoutUUID: String? = nil) -> HealthSamplePayload {
        let bpm = s.quantity.doubleValue(for: HKUnit.count().unitDivided(by: .minute()))
        return HealthSamplePayload(
            kind: .heart_rate,
            unit: .bpm,
            qty: bpm,
            q_min: nil, q_avg: nil, q_max: nil,
            ts_start: s.startDate.timeIntervalSince1970,
            ts_end: nil,
            meta: sourceMeta(s.sourceRevision),
            workout_uuid: workoutUUID
        )
    }

    static func restingHeartRateSample(_ s: HKQuantitySample) -> HealthSamplePayload {
        let bpm = s.quantity.doubleValue(for: HKUnit.count().unitDivided(by: .minute()))
        return HealthSamplePayload(
            kind: .resting_heart_rate,
            unit: .bpm,
            qty: bpm,
            q_min: nil, q_avg: nil, q_max: nil,
            ts_start: s.startDate.timeIntervalSince1970,
            ts_end: nil,
            meta: sourceMeta(s.sourceRevision),
            workout_uuid: nil
        )
    }

    /// Step count over an interval. `ts_start` and `ts_end` bound the accumulation window.
    static func stepsSample(_ s: HKQuantitySample, workoutUUID: String? = nil) -> HealthSamplePayload {
        let count = s.quantity.doubleValue(for: .count())
        return HealthSamplePayload(
            kind: .steps,
            unit: .count,
            qty: count,
            q_min: nil, q_avg: nil, q_max: nil,
            ts_start: s.startDate.timeIntervalSince1970,
            ts_end: s.endDate.timeIntervalSince1970,
            meta: sourceMeta(s.sourceRevision),
            workout_uuid: workoutUUID
        )
    }

    static func activeEnergySample(_ s: HKQuantitySample, workoutUUID: String? = nil) -> HealthSamplePayload {
        let kcal = s.quantity.doubleValue(for: .kilocalorie())
        return HealthSamplePayload(
            kind: .active_energy,
            unit: .kcal,
            qty: kcal,
            q_min: nil, q_avg: nil, q_max: nil,
            ts_start: s.startDate.timeIntervalSince1970,
            ts_end: s.endDate.timeIntervalSince1970,
            meta: sourceMeta(s.sourceRevision),
            workout_uuid: workoutUUID
        )
    }

    /// HRV SDNN — HealthKit stores this in seconds; the wire unit is `ms`.
    static func hrvSample(_ s: HKQuantitySample) -> HealthSamplePayload {
        let ms = s.quantity.doubleValue(for: HKUnit.secondUnit(with: .milli))
        return HealthSamplePayload(
            kind: .hrv,
            unit: .ms,
            qty: ms,
            q_min: nil, q_avg: nil, q_max: nil,
            ts_start: s.startDate.timeIntervalSince1970,
            ts_end: nil,
            meta: sourceMeta(s.sourceRevision),
            workout_uuid: nil
        )
    }

    // MARK: Category samples

    /// Sleep stage sample. Handles both the modern staged values (iOS 16+) and
    /// the legacy `.asleep` bucket that still appears in older samples.
    static func sleepSample(_ s: HKCategorySample) -> HealthSamplePayload {
        let stage: String
        if let v = HKCategoryValueSleepAnalysis(rawValue: s.value) {
            switch v {
            case .inBed:             stage = "in_bed"
            case .asleepUnspecified: stage = "asleep"
            case .asleepCore:        stage = "asleep_core"
            case .asleepDeep:        stage = "asleep_deep"
            case .asleepREM:         stage = "asleep_rem"
            case .awake:             stage = "awake"
            case .asleep:            stage = "asleep_legacy"
            @unknown default:        stage = "unknown"
            }
        } else {
            stage = "unknown"
        }
        var meta = sourceMeta(s.sourceRevision) ?? [:]
        meta["stage"] = stage
        return HealthSamplePayload(
            kind: .sleep_stage,
            unit: .stage,
            qty: nil,
            q_min: nil, q_avg: nil, q_max: nil,
            ts_start: s.startDate.timeIntervalSince1970,
            ts_end: s.endDate.timeIntervalSince1970,
            meta: meta,
            workout_uuid: nil
        )
    }

    /// Mindful minutes — the sample's value is `.notApplicable`; we derive
    /// duration from the interval so a truncated sample still lines up.
    static func mindfulSample(_ s: HKCategorySample) -> HealthSamplePayload {
        let minutes = s.endDate.timeIntervalSince(s.startDate) / 60.0
        return HealthSamplePayload(
            kind: .mindful,
            unit: .minutes,
            qty: minutes,
            q_min: nil, q_avg: nil, q_max: nil,
            ts_start: s.startDate.timeIntervalSince1970,
            ts_end: s.endDate.timeIntervalSince1970,
            meta: sourceMeta(s.sourceRevision),
            workout_uuid: nil
        )
    }

    // MARK: helpers

    /// Small piece of provenance the backend can surface in `meta.device`.
    private static func sourceMeta(_ src: HKSourceRevision) -> [String: String]? {
        var m: [String: String] = [
            "source": src.source.name,
        ]
        if let productType = src.productType {
            m["device"] = productType
        }
        return m.isEmpty ? nil : m
    }
}
