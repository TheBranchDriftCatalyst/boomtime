//
//  PairingView.swift
//  BoomtimeWatch
//
//  First-run pairing: enter Boomtime server URL and paste API token.
//  On save, persists both, requests HealthKit auth, and returns.
//

import SwiftUI

struct PairingView: View {
    @EnvironmentObject var app: AppState

    /// Called when pairing is successfully saved.
    var onComplete: () -> Void

    @State private var serverURLText: String = ""
    @State private var tokenText: String = ""
    @State private var errorText: String?
    @State private var isSaving = false

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    Text("Point this app at your self-hosted Boomtime server and paste the API token from your dashboard.")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }

                Section("Server") {
                    TextField("https://boomtime.example.com", text: $serverURLText)
                        .keyboardType(.URL)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .textContentType(.URL)
                }

                Section("API Token") {
                    // Not using SecureField — the user is pasting a UUID from a
                    // dashboard, showing it helps them verify.
                    TextField("00000000-0000-0000-0000-000000000000", text: $tokenText)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .font(.system(.body, design: .monospaced))
                }

                if let errorText {
                    Section {
                        Label(errorText, systemImage: "exclamationmark.triangle")
                            .foregroundStyle(.red)
                    }
                }

                Section {
                    Button {
                        Task { await save() }
                    } label: {
                        if isSaving {
                            ProgressView()
                        } else {
                            Text("Save & Grant HealthKit Access")
                                .frame(maxWidth: .infinity)
                        }
                    }
                    .disabled(isSaving || serverURLText.isEmpty || tokenText.isEmpty)
                }
            }
            .navigationTitle("Pair with Boomtime")
            .onAppear {
                // Pre-fill in case the user is re-opening pairing.
                serverURLText = app.config.serverURL?.absoluteString ?? serverURLText
            }
        }
    }

    private func save() async {
        errorText = nil
        isSaving = true
        defer { isSaving = false }

        // Normalize: strip trailing slash so URLs paths concatenate cleanly.
        var s = serverURLText.trimmingCharacters(in: .whitespaces)
        if s.hasSuffix("/") { s.removeLast() }
        guard let url = URL(string: s), url.scheme?.hasPrefix("http") == true else {
            errorText = "Server URL must start with http:// or https://"
            return
        }

        let token = tokenText.trimmingCharacters(in: .whitespaces)
        guard !token.isEmpty else {
            errorText = "Token is required"
            return
        }

        app.config.serverURL = url
        guard Keychain.setToken(token) else {
            errorText = "Failed to store token in Keychain"
            return
        }

        // Prompt HealthKit inline so the user is done in one flow.
        do {
            try await app.reader.requestAuthorization()
        } catch {
            errorText = error.localizedDescription
            return
        }

        app.config.hasCompletedPairing = true
        onComplete()
    }
}

#Preview {
    PairingView(onComplete: {}).environmentObject(AppState())
}
