//
//  WiFiPresenceProvider.swift
//  BoomtimeWatch (iOS)
//
//  Protocol seam over NEHotspotNetwork.fetchCurrent() — the one iOS API
//  HomePresenceMonitor calls to decide "am I on the home Wi-Fi." Split so:
//
//    1. Unit tests can inject a `FakeWiFiPresenceProvider` that reports any
//       SSID/BSSID pair (or nil) without touching the real Wi-Fi stack.
//    2. Simulator runs can force a specific SSID via a launch arg or env
//       var — `NEHotspotNetwork.fetchCurrent()` returns nil on Sim so
//       .wifiOnly and .both modes are otherwise untestable in the Sim.
//
//  iOS-only because NEHotspotNetwork lives in NetworkExtension. The
//  #if os(iOS) guard keeps the file out of the watch target's build.
//

#if os(iOS)

import Foundation
import NetworkExtension

/// Denormalized view of the current Wi-Fi network. `bssid` is the AP MAC
/// address — often present but redacted on some hardware.
public struct WiFiNetworkInfo: Equatable, Sendable {
    public let ssid: String
    public let bssid: String?

    public init(ssid: String, bssid: String? = nil) {
        self.ssid = ssid
        self.bssid = bssid
    }
}

/// Read-only view over the phone's current Wi-Fi. Test doubles conform to
/// return canned results; the live impl calls NEHotspotNetwork.
public protocol WiFiPresenceProviding: Sendable {
    func currentNetwork() async -> WiFiNetworkInfo?
}

/// Production adapter. Reads the current SSID + BSSID via NEHotspotNetwork.
/// On Simulator, honors a launch-arg / env-var override so smoke tests can
/// drive .wifiOnly and .both modes end-to-end without real Wi-Fi hardware.
public struct LiveWiFiPresenceProvider: WiFiPresenceProviding {
    public init() {}

    public func currentNetwork() async -> WiFiNetworkInfo? {
        #if targetEnvironment(simulator)
        // Simulator override — see SimulatorOverrides.swift.
        if let simSSID = ProcessInfo.processInfo.simulatorForcedSSID {
            return WiFiNetworkInfo(ssid: simSSID, bssid: nil)
        }
        #endif
        guard let net = await NEHotspotNetwork.fetchCurrent() else { return nil }
        return WiFiNetworkInfo(
            ssid: net.ssid,
            bssid: net.bssid.isEmpty ? nil : net.bssid
        )
    }
}

/// Test double. Set `nextResult` before the assertion; each call to
/// `currentNetwork()` returns whatever's there and increments `callCount`.
/// A closure form `provider` (if set) wins over `nextResult` — use it for
/// scripted sequences (miss, miss, then hit).
public final class FakeWiFiPresenceProvider: WiFiPresenceProviding, @unchecked Sendable {
    public var nextResult: WiFiNetworkInfo?
    public var provider: (@Sendable () -> WiFiNetworkInfo?)?
    public private(set) var callCount: Int = 0

    public init(nextResult: WiFiNetworkInfo? = nil) {
        self.nextResult = nextResult
    }

    public func currentNetwork() async -> WiFiNetworkInfo? {
        callCount += 1
        if let provider { return provider() }
        return nextResult
    }
}

#endif
