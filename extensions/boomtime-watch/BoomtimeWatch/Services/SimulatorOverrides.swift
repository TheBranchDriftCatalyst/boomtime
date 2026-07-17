//
//  SimulatorOverrides.swift
//  BoomtimeWatch (iOS)
//
//  Escape hatches for driving code paths that can't fire in the Simulator.
//  Each override reads from either a launch argument or an environment
//  variable — both settable from Xcode's Edit Scheme dialog (Arguments tab)
//  so a Sim run can pretend to be at home, on the home Wi-Fi, etc.
//
//  All overrides are gated by `#if targetEnvironment(simulator)` so they
//  physically can't leak into a device build.
//

#if targetEnvironment(simulator)

import Foundation

extension ProcessInfo {
    /// Forced SSID for the LiveWiFiPresenceProvider. When set, that provider
    /// returns this SSID and skips NEHotspotNetwork.
    ///
    /// Launch arg: `-boomtime-sim-ssid MyHomeWiFi`
    /// Env var:    `BOOMTIME_SIM_SSID=MyHomeWiFi`
    var simulatorForcedSSID: String? {
        launchArg("-boomtime-sim-ssid") ?? environment["BOOMTIME_SIM_SSID"]
    }

    /// If set, HomePresenceMonitor's geofence path treats every startMonitoring
    /// as if the region were already `.satisfied` — fires onArrivedHome after
    /// a short delay. Useful for verifying the workout-end wiring end-to-end
    /// on Sim without needing to move the simulated location around.
    ///
    /// Launch arg: `-boomtime-sim-at-home`
    /// Env var:    `BOOMTIME_SIM_AT_HOME=1`
    var simulatorForceAtHome: Bool {
        if launchArg("-boomtime-sim-at-home") != nil { return true }
        if let raw = environment["BOOMTIME_SIM_AT_HOME"], !raw.isEmpty, raw != "0" {
            return true
        }
        return false
    }

    /// Small helper: `--flag` with no value returns "" (present sentinel);
    /// `--flag value` returns "value"; absent returns nil.
    private func launchArg(_ name: String) -> String? {
        let args = arguments
        guard let idx = args.firstIndex(of: name) else { return nil }
        let next = idx + 1
        if next < args.count, !args[next].hasPrefix("-") { return args[next] }
        return ""
    }
}

#endif
