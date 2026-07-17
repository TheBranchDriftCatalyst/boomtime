//
//  AnnotatedWorkoutCodableTests.swift
//  BoomtimeWatchTests
//
//  Guards the Codable migration path on AnnotatedWorkout:
//    - New-form JSON (with `homeDetection` string) round-trips.
//    - Legacy JSON (with `geofenceEndAtHome` Bool, no `homeDetection`) decodes
//      to `.both` (true) / `.off` (false) so users mid-upgrade keep working.
//    - endBehavior defaults to `.manual` when the field is missing.
//    - Optional fields (emoji, defaultDurationMin) round-trip through nil.
//

import XCTest
@testable import BoomtimeWatch

final class AnnotatedWorkoutCodableTests: XCTestCase {

    // MARK: Round-trip

    func test_roundTrip_preservesAllFields() throws {
        let original = AnnotatedWorkout(
            id: UUID(),
            label: "Marathon Training",
            activityRaw: 37,
            wireKind: "running",
            emoji: "🏃",
            defaultDurationMin: 45,
            endBehavior: .haptic,
            homeDetection: .geofenceOnly
        )
        let data = try JSONEncoder().encode(original)
        let decoded = try JSONDecoder().decode(AnnotatedWorkout.self, from: data)

        XCTAssertEqual(decoded, original)
    }

    // MARK: Legacy migration

    func test_legacyDecode_geofenceTrue_becomesBoth() throws {
        let legacyJSON = """
        {
          "id": "\(UUID().uuidString)",
          "label": "Dog Walk",
          "activityRaw": 52,
          "wireKind": "walking",
          "emoji": "🐕",
          "defaultDurationMin": 15,
          "endBehavior": "autoEnd",
          "geofenceEndAtHome": true
        }
        """.data(using: .utf8)!

        let decoded = try JSONDecoder().decode(AnnotatedWorkout.self, from: legacyJSON)
        XCTAssertEqual(decoded.homeDetection, .both)
        XCTAssertEqual(decoded.endBehavior, .autoEnd)
        XCTAssertEqual(decoded.label, "Dog Walk")
    }

    func test_legacyDecode_geofenceFalse_becomesOff() throws {
        let legacyJSON = """
        {
          "id": "\(UUID().uuidString)",
          "label": "Morning Run",
          "activityRaw": 37,
          "wireKind": "running",
          "geofenceEndAtHome": false
        }
        """.data(using: .utf8)!

        let decoded = try JSONDecoder().decode(AnnotatedWorkout.self, from: legacyJSON)
        XCTAssertEqual(decoded.homeDetection, .off)
        // Missing endBehavior defaults to .manual (Codable migration).
        XCTAssertEqual(decoded.endBehavior, .manual)
    }

    func test_legacyDecode_neitherField_defaultsToOff() throws {
        let legacyJSON = """
        {
          "id": "\(UUID().uuidString)",
          "label": "Yoga",
          "activityRaw": 57,
          "wireKind": "yoga"
        }
        """.data(using: .utf8)!

        let decoded = try JSONDecoder().decode(AnnotatedWorkout.self, from: legacyJSON)
        XCTAssertEqual(decoded.homeDetection, .off)
        XCTAssertEqual(decoded.endBehavior, .manual)
        XCTAssertNil(decoded.defaultDurationMin)
        XCTAssertNil(decoded.emoji)
    }

    // MARK: New-form takes precedence over legacy

    func test_newFormWinsWhenBothPresent() throws {
        // If a blob somehow carries both keys (data written on a mid-migration
        // build), the new enum-typed key wins.
        let jsonWithBoth = """
        {
          "id": "\(UUID().uuidString)",
          "label": "Weird",
          "activityRaw": 3000,
          "wireKind": "other",
          "geofenceEndAtHome": true,
          "homeDetection": "off"
        }
        """.data(using: .utf8)!

        let decoded = try JSONDecoder().decode(AnnotatedWorkout.self, from: jsonWithBoth)
        XCTAssertEqual(decoded.homeDetection, .off)
    }

    // MARK: WorkoutPreset.makeAnnotated

    func test_dogWalkPreset_defaultsToBothAndAutoEnd() {
        let annotated = WorkoutPreset.dogWalk.makeAnnotated()
        XCTAssertEqual(annotated.homeDetection, .both)
        XCTAssertEqual(annotated.endBehavior, .autoEnd)
        XCTAssertEqual(annotated.defaultDurationMin, 15)
        XCTAssertEqual(annotated.wireKind, "walking")
    }

    func test_saunaPreset_autoEndsButNotHomeDetection() {
        let annotated = WorkoutPreset.sauna.makeAnnotated()
        XCTAssertEqual(annotated.endBehavior, .autoEnd)
        XCTAssertEqual(annotated.homeDetection, .off)
    }

    func test_morningRunPreset_openEndedByDefault() {
        let annotated = WorkoutPreset.morningRun.makeAnnotated()
        XCTAssertEqual(annotated.endBehavior, .manual)
        XCTAssertEqual(annotated.homeDetection, .off)
        XCTAssertEqual(annotated.defaultDurationMin, 30)
    }
}
