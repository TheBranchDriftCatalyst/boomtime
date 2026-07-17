//
//  SyncNowView.swift
//  BoomtimeWatch Watch App
//
//  Row that fires `{action: "sync-now"}` to the phone and shows a
//  synced-check-mark / sync-failed toast based on WatchConnector.lastSyncOutcome.
//

import SwiftUI

struct SyncNowView: View {
    @EnvironmentObject var connector: WatchConnector

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Button {
                connector.sendSyncNow()
            } label: {
                HStack {
                    if case .inFlight = connector.lastSyncOutcome {
                        ProgressView().padding(.trailing, 4)
                    }
                    Text("Sync now")
                    Spacer()
                    trailingIcon
                }
            }
            .disabled(isInFlight)

            if case let .failed(reason) = connector.lastSyncOutcome {
                Text(reason)
                    .font(.caption2)
                    .foregroundStyle(.red)
                    .lineLimit(2)
            }
        }
    }

    private var isInFlight: Bool {
        if case .inFlight = connector.lastSyncOutcome { return true }
        return false
    }

    @ViewBuilder
    private var trailingIcon: some View {
        switch connector.lastSyncOutcome {
        case .ok:
            Image(systemName: "checkmark.circle.fill").foregroundStyle(.green)
        case .failed:
            Image(systemName: "xmark.octagon.fill").foregroundStyle(.red)
        default:
            EmptyView()
        }
    }
}
