//
//  SyncStatusView.swift
//  BoomtimeWatch
//
//  Sync tab: per-type last-sync timestamps, error surface, and a "Sync now"
//  button that walks every enabled kind. Server URL / token editing and the
//  Unpair action moved to the Settings / About tabs.
//

import SwiftUI

struct SyncStatusView: View {
    @EnvironmentObject var app: AppState
    @EnvironmentObject var config: Config

    var body: some View {
        NavigationStack {
            List {
                Section {
                    Text(config.serverURL?.absoluteString ?? "no server")
                        .font(.footnote.monospaced())
                        .foregroundStyle(.secondary)
                }

                Section("Sync Status") {
                    ForEach(SyncKind.allCases, id: \.self) { kind in
                        row(for: kind)
                    }
                }

                Section {
                    Button {
                        Task { await app.coordinator.syncAll() }
                    } label: {
                        HStack {
                            if app.coordinator.isSyncing {
                                ProgressView().padding(.trailing, 8)
                            }
                            Text(app.coordinator.isSyncing ? "Syncing…" : "Sync now")
                        }
                        .frame(maxWidth: .infinity)
                    }
                    .disabled(app.coordinator.isSyncing)
                }
            }
            .navigationTitle("Boomtime")
            .refreshable {
                await app.coordinator.syncAll()
            }
        }
    }

    private func row(for kind: SyncKind) -> some View {
        let last = config.lastSync(for: kind)
        let err = config.lastError(for: kind)
        let disabled = !config.enabledKinds.contains(kind)
        return VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text(kind.displayName)
                    .foregroundStyle(disabled ? .secondary : .primary)
                Spacer()
                Text(disabled ? "off" : (last.map { relative($0) } ?? "never"))
                    .foregroundStyle(.secondary)
                    .font(.footnote.monospacedDigit())
            }
            if let err, !disabled {
                Text(err)
                    .font(.caption2)
                    .foregroundStyle(.red)
                    .lineLimit(2)
            }
        }
        .padding(.vertical, 2)
        // The published statusVersion on the coordinator forces this List to
        // recompute when per-kind status changes.
        .id(app.coordinator.statusVersion)
    }

    private func relative(_ date: Date) -> String {
        let f = RelativeDateTimeFormatter()
        f.unitsStyle = .abbreviated
        return f.localizedString(for: date, relativeTo: Date())
    }
}

#Preview {
    let state = AppState()
    return SyncStatusView()
        .environmentObject(state)
        .environmentObject(state.config)
}
