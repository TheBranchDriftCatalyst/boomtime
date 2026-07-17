//
//  HomeConfig.swift
//  BoomtimeWatch (shared source)
//
//  Home-presence configuration: the captured circular geofence around home
//  (`HomeBase`) and the recognized home Wi-Fi network (`HomeSSID`). Both
//  live in phone-side Config as JSON blobs — the watch never sees the raw
//  coord or SSID string, only the AnnotatedWorkout's `homeDetection` mode.
//
//  Framework-free so the Settings view can render editors without dragging
//  CoreLocation / NetworkExtension into the shared surface.
//
//  Both structs are dual-target for the same reason the rest of Shared is:
//  minimizes friction if a future revision needs the watch to know whether
//  home config exists (e.g. to grey out a preset row in the picker).
//

import Foundation

/// A single circular home-base geofence. Radius defaults to 100m — accurate
/// enough to fire reliably as you turn onto your street, tight enough to
/// avoid firing at the coffee shop three doors down. `setAt` is surfaced in
/// Settings ("captured 3 days ago") so users can tell whether the coord is
/// stale after a move.
public struct HomeBase: Codable, Equatable {
    public var lat: Double
    public var lon: Double
    public var radiusM: Double
    public var setAt: Date

    public init(lat: Double, lon: Double, radiusM: Double = 100.0, setAt: Date = Date()) {
        self.lat = lat
        self.lon = lon
        self.radiusM = radiusM
        self.setAt = setAt
    }
}

/// The recognized home Wi-Fi network. `bssid` is the AP's MAC address —
/// when present it wins over the SSID string, which resists a neighbor
/// naming their SSID the same as yours (surprisingly common on default
/// router names like "linksys" or "NETGEAR"). BSSID may be nil on Wi-Fi
/// hardware that doesn't expose it; SSID-only match is the fallback.
public struct HomeSSID: Codable, Equatable {
    public var ssid: String
    public var bssid: String?
    public var setAt: Date

    public init(ssid: String, bssid: String? = nil, setAt: Date = Date()) {
        self.ssid = ssid
        self.bssid = bssid
        self.setAt = setAt
    }

    /// Case-insensitive SSID compare, exact BSSID compare (when both sides
    /// have a BSSID). BSSID is authoritative — matching BSSIDs win even if
    /// the SSID has drifted (device renamed the network after a firmware
    /// upgrade, say).
    public func matches(ssid otherSSID: String?, bssid otherBSSID: String?) -> Bool {
        if let self_b = bssid, let other_b = otherBSSID {
            return self_b.caseInsensitiveCompare(other_b) == .orderedSame
        }
        guard let otherSSID else { return false }
        return ssid.caseInsensitiveCompare(otherSSID) == .orderedSame
    }
}
