//
//  AboutView.swift
//  BoomtimeWatch (iOS)
//
//  Small "About" tab: app version, Watch pairing status, and the Unpair
//  button (moved out of SyncStatusView). Unpair clears the Keychain token
//  and drops the hasCompletedPairing flag, forcing the first-run modal to
//  reappear.
//

import SwiftUI

struct AboutView: View {
    @EnvironmentObject var app: AppState
    @EnvironmentObject var config: Config

    @State private var showingUnpairConfirm = false

    private var appVersion: String {
        let short = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "?"
        let build = Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String ?? "?"
        return "\(short) (\(build))"
    }

    var body: some View {
        NavigationStack {
            Form {
                Section("App") {
                    LabeledContent("Version", value: appVersion)
                }

                Section("Apple Watch") {
                    LabeledContent("Paired", value: app.watchConnector.isPaired ? "Yes" : "No")
                    LabeledContent("Reachable", value: app.watchConnector.isWatchReachable ? "Yes" : "No")
                }

                Section {
                    Button(role: .destructive) {
                        showingUnpairConfirm = true
                    } label: {
                        Text("Unpair from Boomtime").frame(maxWidth: .infinity)
                    }
                }
            }
            .navigationTitle("About")
            .confirmationDialog(
                "Unpair from Boomtime?",
                isPresented: $showingUnpairConfirm,
                titleVisibility: .visible
            ) {
                Button("Unpair", role: .destructive) { unpair() }
                Button("Cancel", role: .cancel) {}
            } message: {
                Text("The token will be removed from Keychain. HealthKit permissions must be revoked from the iOS Settings app.")
            }
        }
    }

    private func unpair() {
        Keychain.deleteToken()
        config.hasCompletedPairing = false
        // Server URL, anchors, per-metric toggles, and labels intentionally
        // preserved — a re-pair to the same server picks up where we left off.
    }
}

#Preview {
    let state = AppState()
    return AboutView()
        .environmentObject(state)
        .environmentObject(state.config)
}
