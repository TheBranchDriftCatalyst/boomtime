//
//  LiveWorkoutView.swift
//  BoomtimeWatch Watch App
//
//  Elapsed / countdown time + live HR + kcal readout with an End button.
//  Kicks WorkoutController.requestAuthorization + start on appear, and
//  calls end() on tap. Doesn't touch the network — the finished HKWorkout is
//  saved to Health and the phone-side observer posts it to Boomtime.
//
//  Countdown layout: big MM:SS countdown flips red under 60s, small elapsed
//  timer underneath. Open-ended (no duration) or overtime (after countdown
//  hit 0 with .haptic behavior) => big elapsed, no countdown.
//
//  Home-presence: if the preset carries a homeDetection ≠ .off, we tell the
//  phone via WCSession at task-start; the phone's reply carries the actually-
//  active mode (after any downgrades). The waiting-to-return-home label
//  underneath the timer is driven by `WatchConnector.activeHomeMode`. If the
//  phone reports "home-end", the WatchConnector-held callback ends the
//  workout — same code path as manual End or auto-end.
//

import SwiftUI

struct LiveWorkoutView: View {
    @EnvironmentObject var workoutController: WorkoutController
    @EnvironmentObject var settings: WatchSettings
    @EnvironmentObject var connector: WatchConnector
    @Environment(\.dismiss) private var dismiss

    let activityRaw: Int
    let label: String
    let defaultDurationMin: Int?
    let endBehavior: WorkoutEndBehavior
    /// The preset's home-detection preference. Passed to the phone at start
    /// so it can spin up HomePresenceMonitor; the actually-active mode may
    /// downgrade based on Config.homeBase / Config.homeSSID availability.
    let homeDetection: HomeDetection

    var body: some View {
        VStack(spacing: 8) {
            Text(label)
                .font(.headline)
                .foregroundStyle(.secondary)
                .lineLimit(1)

            timerReadout
            homeWaitingLabel
            hrRow

            if let err = workoutController.errorMessage {
                Text(err)
                    .font(.caption2)
                    .foregroundStyle(.red)
                    .multilineTextAlignment(.center)
                    .lineLimit(3)
            }

            Button(role: .destructive) {
                workoutController.end()
                connector.sendWorkoutEnded()
                dismiss()
            } label: {
                Text("End Workout").frame(maxWidth: .infinity)
            }
        }
        .padding()
        .navigationTitle(label)
        .navigationBarTitleDisplayMode(.inline)
        .task {
            guard !workoutController.isActive else { return }
            do {
                try await workoutController.requestAuthorization()
            } catch {
                // Auth failure surfaces via WorkoutController.errorMessage after start().
            }
            workoutController.start(
                activityRaw: activityRaw,
                defaultDurationMin: defaultDurationMin,
                endBehavior: endBehavior
            )
            // Ping the phone. Register the home-end callback — the phone
            // fires it via WCSession message when the presence monitor trips.
            connector.sendWorkoutStarted(homeDetection: homeDetection) { [workoutController, connector] in
                workoutController.end()
                connector.sendWorkoutEnded()
                dismiss()
            }
        }
        .onDisappear {
            // Backstop: if the user navigates away (rather than tapping End)
            // and the workout was still active, treat it as an implicit end
            // so the phone stops monitoring. WorkoutController's own teardown
            // handles the HK finish path.
            if workoutController.isActive {
                connector.sendWorkoutEnded()
            }
        }
    }

    /// Big timer + optional small-elapsed row. Layout swaps depending on
    /// whether a countdown is active.
    @ViewBuilder
    private var timerReadout: some View {
        if let remaining = workoutController.remainingSeconds {
            VStack(spacing: 0) {
                Text(formatMS(remaining))
                    .font(.system(size: 44, weight: .semibold, design: .rounded))
                    .monospacedDigit()
                    .foregroundStyle(remaining < 60 ? .red : .primary)
                Text("elapsed \(formatMS(workoutController.elapsedSeconds))")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }
        } else {
            Text(formatMS(workoutController.elapsedSeconds))
                .font(.system(size: 44, weight: .semibold, design: .rounded))
                .monospacedDigit()
        }
    }

    /// Subtle line describing the current home-detection state. Hidden when
    /// nothing is monitoring (either preset says .off, or the phone downgraded
    /// to .off because both signals are missing).
    @ViewBuilder
    private var homeWaitingLabel: some View {
        switch connector.activeHomeMode {
        case .off:
            EmptyView()
        case .wifiOnly:
            Text("🛜 Waiting for home Wi-Fi")
                .font(.caption2)
                .foregroundStyle(.secondary)
        case .geofenceOnly:
            Text("🏠 Waiting to return home")
                .font(.caption2)
                .foregroundStyle(.secondary)
        case .both:
            Text("🛜 Waiting to return home")
                .font(.caption2)
                .foregroundStyle(.secondary)
        }
    }

    /// HR + kcal layout adapts to `WatchSettings.hrDisplay`.
    @ViewBuilder
    private var hrRow: some View {
        switch settings.hrDisplay {
        case .largeNumeric:
            HStack(spacing: 16) {
                bigMetric(title: "HR", value: workoutController.currentHR.map { "\($0)" } ?? "—", unit: "bpm")
                bigMetric(title: "kcal", value: String(format: "%.0f", workoutController.kcal), unit: nil)
            }
        case .smallNumeric:
            HStack(spacing: 16) {
                smallMetric(title: "HR", value: workoutController.currentHR.map { "\($0)" } ?? "—", unit: "bpm")
                smallMetric(title: "kcal", value: String(format: "%.0f", workoutController.kcal), unit: nil)
            }
        case .hidden:
            HStack {
                smallMetric(title: "kcal", value: String(format: "%.0f", workoutController.kcal), unit: nil)
            }
        }
    }

    private func bigMetric(title: String, value: String, unit: String?) -> some View {
        VStack(spacing: 0) {
            Text(value)
                .font(.title2.monospacedDigit())
            HStack(spacing: 2) {
                Text(title)
                if let unit { Text(unit) }
            }
            .font(.caption2)
            .foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity)
    }

    private func smallMetric(title: String, value: String, unit: String?) -> some View {
        HStack(spacing: 4) {
            Text(title).foregroundStyle(.secondary).font(.caption2)
            Text(value).font(.body.monospacedDigit())
            if let unit { Text(unit).foregroundStyle(.secondary).font(.caption2) }
        }
        .frame(maxWidth: .infinity)
    }

    /// H:MM:SS when >=1h, MM:SS otherwise.
    private func formatMS(_ total: Int) -> String {
        let t = max(0, total)
        let h = t / 3600
        let m = (t % 3600) / 60
        let s = t % 60
        if h > 0 { return String(format: "%d:%02d:%02d", h, m, s) }
        return String(format: "%02d:%02d", m, s)
    }
}
