//
//  PayloadBuilderTests.swift
//  BoomtimeWatchTests
//
//  Exercises the pure conversion surfaces of PayloadBuilder plus the
//  Codable wire format. We can't easily construct real HKSample instances
//  in a unit test without a live HealthKit store, so the HKSample-specific
//  paths are covered by structural round-trip tests on the Codable structs.
//

import XCTest
import HealthKit
@testable import BoomtimeWatch

final class PayloadBuilderTests: XCTestCase {

    // MARK: workoutKindString

    func test_workoutKindString_mapsCommonTypes() {
        XCTAssertEqual(PayloadBuilder.workoutKindString(.running), "running")
        XCTAssertEqual(PayloadBuilder.workoutKindString(.cycling), "cycling")
        XCTAssertEqual(PayloadBuilder.workoutKindString(.traditionalStrengthTraining), "strength")
        XCTAssertEqual(PayloadBuilder.workoutKindString(.functionalStrengthTraining), "functional_strength")
        XCTAssertEqual(PayloadBuilder.workoutKindString(.highIntensityIntervalTraining), "hiit")
        XCTAssertEqual(PayloadBuilder.workoutKindString(.other), "other")
    }

    func test_distanceType_picksByActivity() {
        XCTAssertEqual(PayloadBuilder.distanceType(for: .running), HKQuantityType(.distanceWalkingRunning))
        XCTAssertEqual(PayloadBuilder.distanceType(for: .cycling), HKQuantityType(.distanceCycling))
        XCTAssertEqual(PayloadBuilder.distanceType(for: .swimming), HKQuantityType(.distanceSwimming))
        XCTAssertNil(PayloadBuilder.distanceType(for: .yoga))
        XCTAssertNil(PayloadBuilder.distanceType(for: .traditionalStrengthTraining))
    }

    // MARK: WorkoutPayload JSON encoding

    func test_workoutPayload_encodesRequiredFieldsSnakeCased() throws {
        let p = WorkoutPayload(
            kind: "running",
            label: nil,
            start: 1_721_000_000,
            end: 1_721_003_600,
            duration_s: 3600,
            kcal: 412.5,
            distance_m: 8100.4,
            avg_hr: 148,
            hr_series: [HRSeriesPoint(t: 1_721_000_030, bpm: 122)],
            route: nil,
            source_uuid: "F1A2"
        )
        let data = try JSONEncoder().encode(p)
        let json = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])

        XCTAssertEqual(json["kind"] as? String, "running")
        XCTAssertEqual(json["start"] as? Double, 1_721_000_000)
        XCTAssertEqual(json["end"] as? Double, 1_721_003_600)
        XCTAssertEqual(json["duration_s"] as? Int, 3600)
        XCTAssertEqual(json["kcal"] as? Double, 412.5)
        XCTAssertEqual(json["distance_m"] as? Double, 8100.4)
        XCTAssertEqual(json["avg_hr"] as? Int, 148)
        XCTAssertEqual(json["source_uuid"] as? String, "F1A2")
        // `route` was nil — must be omitted, not sent as JSON null.
        XCTAssertNil(json["route"])
        // `hr_series` should be present as an array of length 1.
        let series = try XCTUnwrap(json["hr_series"] as? [[String: Any]])
        XCTAssertEqual(series.count, 1)
        XCTAssertEqual(series[0]["bpm"] as? Int, 122)
    }

    func test_workoutPayload_omitsAllOptionalFieldsWhenNil() throws {
        let p = WorkoutPayload(
            kind: "walking",
            label: nil,
            start: 1,
            end: 2,
            duration_s: 1,
            kcal: nil,
            distance_m: nil,
            avg_hr: nil,
            hr_series: nil,
            route: nil,
            source_uuid: "X"
        )
        let data = try JSONEncoder().encode(p)
        let json = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])

        XCTAssertEqual(Set(json.keys), Set(["kind", "start", "end", "duration_s", "source_uuid"]))
    }

    // MARK: HealthSamplePayload JSON encoding

    func test_healthSamplePayload_heartRateShape() throws {
        let p = HealthSamplePayload(
            kind: .heart_rate,
            unit: .bpm,
            qty: 72.0,
            q_min: nil, q_avg: nil, q_max: nil,
            ts_start: 1_721_000_030,
            ts_end: nil,
            meta: ["device": "Apple Watch"],
            workout_uuid: nil
        )
        let data = try JSONEncoder().encode(p)
        let json = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])

        XCTAssertEqual(json["kind"] as? String, "heart_rate")
        XCTAssertEqual(json["unit"] as? String, "bpm")
        XCTAssertEqual(json["qty"] as? Double, 72.0)
        XCTAssertEqual(json["ts_start"] as? Double, 1_721_000_030)
        XCTAssertNil(json["ts_end"])
        XCTAssertNil(json["q_min"])
        XCTAssertNil(json["workout_uuid"])
        XCTAssertEqual((json["meta"] as? [String: String])?["device"], "Apple Watch")
    }

    func test_healthSamplePayload_sleepStageShape() throws {
        let p = HealthSamplePayload(
            kind: .sleep_stage,
            unit: .stage,
            qty: nil,
            q_min: nil, q_avg: nil, q_max: nil,
            ts_start: 100, ts_end: 200,
            meta: ["stage": "rem"],
            workout_uuid: nil
        )
        let data = try JSONEncoder().encode(p)
        let json = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])

        XCTAssertEqual(json["kind"] as? String, "sleep_stage")
        XCTAssertEqual(json["unit"] as? String, "stage")
        XCTAssertEqual(json["ts_end"] as? Double, 200)
        XCTAssertNil(json["qty"])
        XCTAssertEqual((json["meta"] as? [String: String])?["stage"], "rem")
    }

    // MARK: Envelope shape

    func test_bulkEnvelopeWrapsUnderDataKey() throws {
        let one = HealthSamplePayload(
            kind: .steps, unit: .count, qty: 1000,
            q_min: nil, q_avg: nil, q_max: nil,
            ts_start: 1, ts_end: 60, meta: nil, workout_uuid: nil
        )
        let env = HealthSampleBulkRequest(data: [one])
        let data = try JSONEncoder().encode(env)
        let json = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        let arr = try XCTUnwrap(json["data"] as? [[String: Any]])
        XCTAssertEqual(arr.count, 1)
        XCTAssertEqual(arr[0]["kind"] as? String, "steps")
    }
}
