//
//  SyncCoordinator.swift
//  BoomtimeWatch
//
//  Owns the sync lifecycle. On start(): auth, enable background delivery,
//  install observers. On each observer fire (or manual "Sync now"), run
//  an anchored fetch, build payloads, upload, and only advance the
//  persisted anchor when the upload returns 2xx.
//

import Foundation
import HealthKit
import Combine

@MainActor
final class SyncCoordinator: ObservableObject {
    private let reader: HealthKitReader
    private let uploader: Uploader
    private let config: Config

    /// Bumped whenever any per-kind status changes so views recompute cheaply.
    @Published private(set) var statusVersion: Int = 0

    /// True while an upload is in flight, drives the "Sync now" button state.
    @Published private(set) var isSyncing = false

    init(reader: HealthKitReader, uploader: Uploader, config: Config) {
        self.reader = reader
        self.uploader = uploader
        self.config = config
    }

    // MARK: Lifecycle

    /// Called once from the app on launch (and after pairing completes).
    /// Idempotent: re-registering background delivery / observers is fine.
    func start() async {
        do {
            try await reader.requestAuthorization()
        } catch {
            // Nothing to do — the UI shows an error state. We can still
            // register observers; they'll fire once auth is granted.
        }
        let enabled = config.enabledKinds
        await reader.enableBackgroundDelivery(for: enabled)

        // Observer callback is `(SyncKind) async -> Void`; the HealthKitReader
        // is responsible for calling HealthKit's completionHandler().
        reader.startObservers(for: enabled) { [weak self] kind in
            await self?.syncOne(kind: kind)
        }
    }

    /// Manual "Sync now" — walks every enabled kind sequentially so we don't stampede.
    func syncAll() async {
        isSyncing = true
        for kind in SyncKind.allCases where config.enabledKinds.contains(kind) {
            await syncOne(kind: kind)
        }
        isSyncing = false
    }

    // MARK: Per-kind sync

    /// One anchored fetch + upload for a specific kind. Anchor is only
    /// persisted on 2xx.
    func syncOne(kind: SyncKind) async {
        do {
            switch kind {
            case .workouts:
                try await syncWorkouts()
            case .heart_rate:
                try await syncQuantity(kind: kind, type: HealthKitReader.heartRateType, map: PayloadBuilder.heartRateSample)
            case .resting_heart_rate:
                try await syncQuantity(kind: kind, type: HealthKitReader.restingHeartRateType) {
                    PayloadBuilder.restingHeartRateSample($0)
                }
            case .steps:
                try await syncQuantity(kind: kind, type: HealthKitReader.stepCountType, map: PayloadBuilder.stepsSample)
            case .active_energy:
                try await syncQuantity(kind: kind, type: HealthKitReader.activeEnergyType, map: PayloadBuilder.activeEnergySample)
            case .hrv:
                try await syncQuantity(kind: kind, type: HealthKitReader.hrvType) {
                    PayloadBuilder.hrvSample($0)
                }
            case .sleep_stage:
                try await syncCategory(kind: kind, type: HealthKitReader.sleepType, map: PayloadBuilder.sleepSample)
            case .mindful:
                try await syncCategory(kind: kind, type: HealthKitReader.mindfulType, map: PayloadBuilder.mindfulSample)
            }
            config.recordSuccessfulSync(for: kind)
        } catch {
            config.recordError(error.localizedDescription, for: kind)
        }
        statusVersion &+= 1
    }

    // MARK: Type-specific helpers

    private func syncWorkouts() async throws {
        let anchor = config.anchor(for: .workouts)
        let result = try await reader.fetchWorkouts(anchor: anchor)
        guard !result.samples.isEmpty else {
            if let a = result.newAnchor { config.setAnchor(a, for: .workouts) }
            return
        }

        // Build each payload with its associated HR series (looked up per-workout).
        // Derive a [wireKind: label] map from the annotated-workout list (the
        // canonical source of user labels); first-added wins on collisions.
        var labels: [String: String] = [:]
        for entry in config.annotatedWorkouts where labels[entry.wireKind] == nil {
            labels[entry.wireKind] = entry.label
        }
        var payloads: [WorkoutPayload] = []
        payloads.reserveCapacity(result.samples.count)
        for w in result.samples {
            let series = (try? await reader.heartRateSeries(for: w)) ?? []
            payloads.append(PayloadBuilder.makeWorkoutPayload(
                workout: w,
                hrSeries: series,
                route: nil,  // GPS route lookup is out of scope for v1
                labels: labels
            ))
        }

        try await uploader.uploadWorkouts(payloads)
        if let a = result.newAnchor { config.setAnchor(a, for: .workouts) }
    }

    /// Generic quantity-sample sync. `map` converts each sample into a wire payload.
    private func syncQuantity(
        kind: SyncKind,
        type: HKQuantityType,
        map: (HKQuantitySample) -> HealthSamplePayload
    ) async throws {
        let anchor = config.anchor(for: kind)
        let result = try await reader.fetchQuantitySamples(type: type, anchor: anchor)
        guard !result.samples.isEmpty else {
            if let a = result.newAnchor { config.setAnchor(a, for: kind) }
            return
        }
        let payloads = result.samples.map(map)
        try await uploader.uploadHealthSamples(payloads)
        if let a = result.newAnchor { config.setAnchor(a, for: kind) }
    }

    private func syncCategory(
        kind: SyncKind,
        type: HKCategoryType,
        map: (HKCategorySample) -> HealthSamplePayload
    ) async throws {
        let anchor = config.anchor(for: kind)
        let result = try await reader.fetchCategorySamples(type: type, anchor: anchor)
        guard !result.samples.isEmpty else {
            if let a = result.newAnchor { config.setAnchor(a, for: kind) }
            return
        }
        let payloads = result.samples.map(map)
        try await uploader.uploadHealthSamples(payloads)
        if let a = result.newAnchor { config.setAnchor(a, for: kind) }
    }
}
