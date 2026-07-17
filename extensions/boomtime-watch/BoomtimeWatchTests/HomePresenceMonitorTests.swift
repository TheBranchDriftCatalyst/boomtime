//
//  HomePresenceMonitorTests.swift
//  BoomtimeWatchTests
//
//  Drives HomePresenceMonitor in .wifiOnly mode via an injected fake
//  WiFiPresenceProviding — no NEHotspotNetwork, no live location, no
//  CLMonitor. Poll cadence is dropped to milliseconds so the tests
//  complete near-instantly.
//
//  What we can't test in unit tests:
//    - .geofenceOnly / .both — those use CLMonitor, which doesn't currently
//      have a protocol seam. Add one when we start caring about arrival-
//      confirmation semantics beyond "SSID match fires end callback".
//

#if os(iOS)

import XCTest
@testable import BoomtimeWatch

@MainActor
final class HomePresenceMonitorTests: XCTestCase {

    // MARK: .wifiOnly

    func test_wifiOnly_missingSSID_downgradesToOff() async {
        let fake = FakeWiFiPresenceProvider(nextResult: nil)
        let mon = HomePresenceMonitor(wifi: fake, wifiPollInterval: .milliseconds(10))

        // No home SSID configured — startMonitoring should downgrade to .off.
        await mon.startMonitoring(
            detection: .wifiOnly,
            home: nil,
            wifi: nil,
            onArrivedHome: {}
        )

        XCTAssertEqual(mon.activeMode, .off)
        XCTAssertFalse(mon.isMonitoring)
        XCTAssertNotNil(mon.lastFailureReason)

        await mon.stopMonitoring()
    }

    func test_wifiOnly_ssidMatch_firesArrivedHome() async throws {
        let target = HomeSSID(ssid: "MyHomeWiFi", bssid: nil, setAt: Date())
        let fake = FakeWiFiPresenceProvider(
            nextResult: WiFiNetworkInfo(ssid: "MyHomeWiFi", bssid: nil)
        )

        let mon = HomePresenceMonitor(wifi: fake, wifiPollInterval: .milliseconds(10))

        // Bridge the closure to a suspension the test can await.
        let arrived = expectation(description: "onArrivedHome fired")
        await mon.startMonitoring(
            detection: .wifiOnly,
            home: nil,
            wifi: target,
            onArrivedHome: { arrived.fulfill() }
        )

        // First poll runs immediately in the Task loop.
        await fulfillment(of: [arrived], timeout: 1.0)

        // After firing, the monitor tears itself down.
        XCTAssertFalse(mon.isMonitoring)
        XCTAssertEqual(mon.activeMode, .off)
        // The provider was consulted at least once.
        XCTAssertGreaterThanOrEqual(fake.callCount, 1)

        await mon.stopMonitoring()
    }

    func test_wifiOnly_missAllowedThenHit_firesAfterSecondPoll() async throws {
        let target = HomeSSID(ssid: "MyHomeWiFi", bssid: nil, setAt: Date())
        let fake = FakeWiFiPresenceProvider()

        // Return the wrong SSID for the first two calls, then match.
        var callCount = 0
        fake.provider = {
            callCount += 1
            if callCount < 3 {
                return WiFiNetworkInfo(ssid: "SomeCafe", bssid: nil)
            }
            return WiFiNetworkInfo(ssid: "MyHomeWiFi", bssid: nil)
        }

        let mon = HomePresenceMonitor(wifi: fake, wifiPollInterval: .milliseconds(10))

        let arrived = expectation(description: "arrived after third poll")
        await mon.startMonitoring(
            detection: .wifiOnly,
            home: nil,
            wifi: target,
            onArrivedHome: { arrived.fulfill() }
        )

        await fulfillment(of: [arrived], timeout: 2.0)
        XCTAssertGreaterThanOrEqual(callCount, 3)

        await mon.stopMonitoring()
    }

    func test_wifiOnly_bssidTakesPrecedenceOverSSID() async throws {
        // Same BSSID, different SSID (device renamed network) — should still match.
        let target = HomeSSID(
            ssid: "OldName",
            bssid: "AA:BB:CC:DD:EE:FF",
            setAt: Date()
        )
        let fake = FakeWiFiPresenceProvider(
            nextResult: WiFiNetworkInfo(ssid: "NewName", bssid: "AA:BB:CC:DD:EE:FF")
        )

        let mon = HomePresenceMonitor(wifi: fake, wifiPollInterval: .milliseconds(10))

        let arrived = expectation(description: "BSSID match wins over SSID drift")
        await mon.startMonitoring(
            detection: .wifiOnly,
            home: nil,
            wifi: target,
            onArrivedHome: { arrived.fulfill() }
        )
        await fulfillment(of: [arrived], timeout: 1.0)

        await mon.stopMonitoring()
    }

    // MARK: stopMonitoring cancellation

    func test_stopMonitoring_cancelsBeforeArrival() async {
        let target = HomeSSID(ssid: "MyHomeWiFi", bssid: nil, setAt: Date())
        // Never match — provider always returns nil.
        let fake = FakeWiFiPresenceProvider(nextResult: nil)

        let mon = HomePresenceMonitor(wifi: fake, wifiPollInterval: .milliseconds(10))

        var arrived = false
        await mon.startMonitoring(
            detection: .wifiOnly,
            home: nil,
            wifi: target,
            onArrivedHome: { arrived = true }
        )

        // Let the poll fire once or twice.
        try? await Task.sleep(for: .milliseconds(50))
        XCTAssertTrue(mon.isMonitoring)

        await mon.stopMonitoring()

        // Wait long enough for any lingering poll to have completed.
        try? await Task.sleep(for: .milliseconds(50))
        XCTAssertFalse(mon.isMonitoring)
        XCTAssertFalse(arrived)
    }

    // MARK: idempotent restart

    func test_restart_replacesPreviousMonitor() async {
        let target = HomeSSID(ssid: "First", bssid: nil, setAt: Date())
        let fake = FakeWiFiPresenceProvider(nextResult: nil)

        let mon = HomePresenceMonitor(wifi: fake, wifiPollInterval: .milliseconds(10))
        await mon.startMonitoring(
            detection: .wifiOnly,
            home: nil,
            wifi: target,
            onArrivedHome: {}
        )
        XCTAssertTrue(mon.isMonitoring)

        // Second start with a different target — should tear down the first.
        let target2 = HomeSSID(ssid: "Second", bssid: nil, setAt: Date())
        fake.nextResult = WiFiNetworkInfo(ssid: "Second", bssid: nil)
        let arrived = expectation(description: "second monitor fires")
        await mon.startMonitoring(
            detection: .wifiOnly,
            home: nil,
            wifi: target2,
            onArrivedHome: { arrived.fulfill() }
        )
        await fulfillment(of: [arrived], timeout: 1.0)

        await mon.stopMonitoring()
    }
}

#endif
