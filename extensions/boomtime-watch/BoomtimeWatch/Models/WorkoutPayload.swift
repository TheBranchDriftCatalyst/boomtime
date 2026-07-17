//
//  WorkoutPayload.swift
//  BoomtimeWatch
//
//  Wire-format DTO for POST /api/v1/users/current/workouts.bulk.
//  The JSON shape here is contractual — the Go backend expects exactly
//  these snake_case keys and unix-second floats.
//

import Foundation

/// One point in a workout heart-rate series.
struct HRSeriesPoint: Codable, Equatable {
    /// Unix seconds (float64 on the wire, we send integers here since HKQuantitySample.startDate rounds fine).
    let t: Double
    let bpm: Int
}

/// One breadcrumb along a workout GPS route.
struct RoutePoint: Codable, Equatable {
    let t: Double
    let lat: Double
    let lon: Double
    /// Altitude in meters. Nullable because indoor routes / early samples may not carry it.
    let alt: Double?
}

/// A single workout as the backend expects it.
///
/// Optional fields use `nil` -> omitted via `encodeIfPresent`, matching the plan's
/// "nullable, may be omitted" contract.
struct WorkoutPayload: Codable, Equatable {
    /// Lowercase HKWorkoutActivityType name, e.g. "running", "cycling", "strength_training".
    let kind: String
    /// Optional user-facing bucket label — when non-nil the server persists this
    /// as the heartbeat's `project` in place of `kind`. Additive field; older
    /// server builds ignore it and fall back to `kind`.
    let label: String?
    /// Unix seconds.
    let start: Double
    let end: Double
    /// Authoritative duration; server does not gap-infer for workouts.
    let duration_s: Int
    /// Active energy burned in kcal.
    let kcal: Double?
    /// Total distance in meters.
    let distance_m: Double?
    /// Average heart rate in BPM (integer).
    let avg_hr: Int?
    /// Per-sample HR points collected during the workout window.
    let hr_series: [HRSeriesPoint]?
    /// GPS breadcrumb.
    let route: [RoutePoint]?
    /// HKWorkout.uuid so the server can dedupe on re-upload.
    let source_uuid: String

    enum CodingKeys: String, CodingKey {
        case kind, label, start, end
        case duration_s
        case kcal
        case distance_m
        case avg_hr
        case hr_series
        case route
        case source_uuid
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(kind, forKey: .kind)
        try c.encodeIfPresent(label, forKey: .label)
        try c.encode(start, forKey: .start)
        try c.encode(end, forKey: .end)
        try c.encode(duration_s, forKey: .duration_s)
        try c.encodeIfPresent(kcal, forKey: .kcal)
        try c.encodeIfPresent(distance_m, forKey: .distance_m)
        try c.encodeIfPresent(avg_hr, forKey: .avg_hr)
        try c.encodeIfPresent(hr_series, forKey: .hr_series)
        try c.encodeIfPresent(route, forKey: .route)
        try c.encode(source_uuid, forKey: .source_uuid)
    }
}

/// Envelope for the bulk endpoint.
struct WorkoutBulkRequest: Codable, Equatable {
    let data: [WorkoutPayload]
}
