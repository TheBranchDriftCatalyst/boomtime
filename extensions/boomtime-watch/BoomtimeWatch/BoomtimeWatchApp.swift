//
//  BoomtimeWatchApp.swift
//  BoomtimeWatch
//
//  App entry. Constructs the shared services (Config, HealthKitReader,
//  Uploader, SyncCoordinator, WatchConnector) once and hands them to the
//  view tree.
//

import SwiftUI

@main
struct BoomtimeWatchApp: App {
    @StateObject private var appState = AppState()

    var body: some Scene {
        WindowGroup {
            ContentView()
                .environmentObject(appState)
                .environmentObject(appState.config)
                .task {
                    // Kick sync coordinator only after pairing exists — otherwise
                    // Uploader will just throw notConfigured on every observer fire.
                    if appState.config.hasCompletedPairing {
                        await appState.coordinator.start()
                    }
                }
        }
    }
}

/// Owns the long-lived singletons for the app.
///
/// Marked @MainActor because SyncCoordinator + WatchConnector are @MainActor;
/// keeping this on the same actor avoids cross-actor init headaches.
@MainActor
final class AppState: ObservableObject {
    let config: Config
    let reader: HealthKitReader
    let uploader: Uploader
    let coordinator: SyncCoordinator
    let watchConnector: WatchConnector
    let presence: HomePresenceMonitor

    init() {
        let cfg = Config()
        let rd = HealthKitReader()
        let up = Uploader(config: cfg)
        let coord = SyncCoordinator(reader: rd, uploader: up, config: cfg)
        let pres = HomePresenceMonitor()
        self.config = cfg
        self.reader = rd
        self.uploader = up
        self.coordinator = coord
        self.presence = pres
        self.watchConnector = WatchConnector(coordinator: coord, config: cfg, presence: pres)
    }
}
