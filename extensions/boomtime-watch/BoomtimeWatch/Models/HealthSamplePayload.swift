//
//  HealthSamplePayload.swift
//  BoomtimeWatch
//
//  Wire-format DTO for POST /api/v1/users/current/health_samples.bulk.
//  One shape covers all seven sample kinds we ingest; fields not
//  applicable to a given kind are omitted via `encodeIfPresent`.
//

import Foundation

/// Discriminator sent as `kind`. String-typed on the wire.
enum HealthSampleKind: String, Codable {
    case heart_rate
    case resting_heart_rate
    case steps
    case active_energy
    case hrv
    case sleep_stage
    case mindful
}

/// Unit strings the backend expects.
enum HealthSampleUnit: String, Codable {
    case bpm
    case count
    case kcal
    case ms
    case minutes
    case stage
}

/// A single health sample. Applicable fields depend on `kind`:
///
/// - `heart_rate` / `resting_heart_rate`: `qty` in `bpm`, single instant `ts_start`.
///   Range HR samples (rare, but Apple emits them) use `q_min`/`q_avg`/`q_max`.
/// - `steps` / `active_energy`: `qty` in `count`/`kcal`, `ts_start`+`ts_end` for the accumulation window.
/// - `hrv`: `qty` in `ms`, single instant.
/// - `sleep_stage`: `unit = "stage"`, `qty = nil`, `meta.stage` carries "REM"/"deep"/etc.,
///   `ts_start`+`ts_end` bound the stage.
/// - `mindful`: `unit = "minutes"`, `qty = duration in minutes`, `ts_start`+`ts_end` bound the session.
struct HealthSamplePayload: Codable, Equatable {
    let kind: HealthSampleKind
    let unit: HealthSampleUnit
    let qty: Double?
    let q_min: Double?
    let q_avg: Double?
    let q_max: Double?
    /// Unix seconds.
    let ts_start: Double
    /// Unix seconds; populated for interval samples.
    let ts_end: Double?
    /// Arbitrary metadata (source device, sleep stage label, etc.).
    let meta: [String: String]?
    /// HKWorkout.uuid when this sample belongs to a specific workout, so the
    /// server can FK it in `health_samples.workout_id`.
    let workout_uuid: String?

    enum CodingKeys: String, CodingKey {
        case kind, unit, qty
        case q_min, q_avg, q_max
        case ts_start, ts_end
        case meta
        case workout_uuid
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(kind, forKey: .kind)
        try c.encode(unit, forKey: .unit)
        try c.encodeIfPresent(qty, forKey: .qty)
        try c.encodeIfPresent(q_min, forKey: .q_min)
        try c.encodeIfPresent(q_avg, forKey: .q_avg)
        try c.encodeIfPresent(q_max, forKey: .q_max)
        try c.encode(ts_start, forKey: .ts_start)
        try c.encodeIfPresent(ts_end, forKey: .ts_end)
        try c.encodeIfPresent(meta, forKey: .meta)
        try c.encodeIfPresent(workout_uuid, forKey: .workout_uuid)
    }
}

struct HealthSampleBulkRequest: Codable, Equatable {
    let data: [HealthSamplePayload]
}
