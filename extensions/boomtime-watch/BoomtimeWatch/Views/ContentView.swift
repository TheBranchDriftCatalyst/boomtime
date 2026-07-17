//
//  ContentView.swift
//  BoomtimeWatch
//
//  Top-level view — three-tab shell with a first-run PairingView modal
//  covering the whole screen when the server URL isn't set yet.
//

import SwiftUI

struct ContentView: View {
    @EnvironmentObject var app: AppState
    @EnvironmentObject var config: Config

    /// Drives the pairing modal. We keep a local mirror of
    /// `config.hasCompletedPairing` so dismissing the modal by completing
    /// pairing kicks the sync coordinator exactly once.
    @State private var showPairing: Bool = false

    var body: some View {
        TabView {
            SyncStatusView()
                .tabItem { Label("Sync", systemImage: "arrow.triangle.2.circlepath") }

            SettingsView()
                .tabItem { Label("Settings", systemImage: "gearshape") }

            AboutView()
                .tabItem { Label("About", systemImage: "info.circle") }
        }
        .fullScreenCover(isPresented: $showPairing) {
            PairingView(onComplete: {
                showPairing = false
                Task { await app.coordinator.start() }
            })
            .environmentObject(app)
            .environmentObject(config)
        }
        .onAppear {
            showPairing = !config.hasCompletedPairing
        }
    }
}

#Preview {
    let state = AppState()
    return ContentView()
        .environmentObject(state)
        .environmentObject(state.config)
}
