//
//  WatchConfigContextTests.swift
//  BoomtimeWatchTests
//
//  Round-trips the WatchConfigContext through its NSDictionary transport
//  (WCSession applicationContext demands a flat dictionary, so the whole
//  Codable payload is JSON-serialized under a single "payload" key).
//

import XCTest
@testable import BoomtimeWatch

final class WatchConfigContextTests: XCTestCase {

    func test_emptyContext_roundTripsThroughDictionary() throws {
        let dict = WatchConfigContext.empty.toDictionary()
        let decoded = try XCTUnwrap(WatchConfigContext.fromDictionary(dict))
        XCTAssertEqual(decoded, .empty)
    }

    func test_populatedContext_survivesRoundTrip() throws {
        let annotated = WorkoutPreset.dogWalk.makeAnnotated()
        let ctx = WatchConfigContext(
            enabledKinds: Set(SyncKind.allCases),
            annotatedWorkouts: [annotated]
        )

        let dict = ctx.toDictionary()
        // Payload arrives as a JSON string under the "payload" key.
        XCTAssertNotNil(dict["payload"])

        let decoded = try XCTUnwrap(WatchConfigContext.fromDictionary(dict))
        XCTAssertEqual(decoded.enabledKinds, ctx.enabledKinds)
        XCTAssertEqual(decoded.annotatedWorkouts.count, 1)
        XCTAssertEqual(decoded.annotatedWorkouts[0].label, "Dog Walk")
        XCTAssertEqual(decoded.annotatedWorkouts[0].homeDetection, .both)
    }

    func test_malformedDictionary_decodesToNil() {
        // No "payload" key → nil.
        XCTAssertNil(WatchConfigContext.fromDictionary(["wrong": "shape"]))
        // "payload" present but not valid JSON → nil.
        XCTAssertNil(WatchConfigContext.fromDictionary(["payload": "not json"]))
    }
}
