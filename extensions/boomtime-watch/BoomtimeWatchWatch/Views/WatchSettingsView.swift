//
//  WatchSettingsView.swift
//  BoomtimeWatch Watch App
//
//  Minimal on-watch settings surface. Sections:
//    - Default preset (Digital Crown-scrollable Picker over the mirrored
//      annotated-workout list)
//    - HR display size (3-way Picker)
//    - Show label picker on start (Toggle)
//    - Presets (read-only view of the annotated-workout list mirrored from
//      the phone) + "+ Add preset" action that sends a WCSession message
//      asking the phone to append a curated preset.
//
//  Editing labels themselves happens on the phone — the watch never types.
//

import SwiftUI

struct WatchSettingsView: View {
    @EnvironmentObject var settings: WatchSettings
    @EnvironmentObject var connector: WatchConnector

    @State private var showingPresetPicker = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 12) {
                Text("Settings")
                    .font(.title3)
                    .padding(.top, 4)

                defaultPicker
                hrDisplayPicker
                Toggle("Label picker on start", isOn: $settings.showLabelPickerOnStart)

                presetList

                Text("Server, token, and preset editing happen in the iPhone app.")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .padding(.top, 4)
            }
            .padding(.horizontal)
        }
        .navigationTitle("Settings")
        .sheet(isPresented: $showingPresetPicker) {
            AddPresetSheet { preset in
                connector.addAnnotated(preset: preset)
                showingPresetPicker = false
            }
        }
    }

    /// Picker over whichever list is currently driving Start Workout — the
    /// mirrored list, or curated presets as a fallback.
    @ViewBuilder
    private var defaultPicker: some View {
        if !connector.context.annotatedWorkouts.isEmpty {
            Picker("Default", selection: defaultBinding) {
                Text("First row").tag(UUID?.none)
                ForEach(connector.context.annotatedWorkouts) { w in
                    Text(w.label).tag(Optional(w.id))
                }
            }
            .pickerStyle(.navigationLink)
        } else {
            LabeledContent("Default", value: "First row")
        }
    }

    private var defaultBinding: Binding<UUID?> {
        Binding(
            get: { settings.defaultWorkoutID },
            set: { settings.defaultWorkoutID = $0 }
        )
    }

    private var hrDisplayPicker: some View {
        Picker("HR display", selection: $settings.hrDisplay) {
            ForEach(HRDisplayMode.allCases) { mode in
                Text(mode.displayName).tag(mode)
            }
        }
        .pickerStyle(.navigationLink)
    }

    private var presetList: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Text("Presets").font(.headline)
                Spacer()
                Button {
                    showingPresetPicker = true
                } label: {
                    Label("Add", systemImage: "plus.circle")
                        .labelStyle(.iconOnly)
                }
                .buttonStyle(.borderless)
            }
            if connector.context.annotatedWorkouts.isEmpty {
                Text("None yet — add one below or edit in the iPhone app.")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }
            ForEach(connector.context.annotatedWorkouts) { w in
                HStack(spacing: 6) {
                    Text(w.effectiveEmoji)
                    Text(w.label).font(.body)
                    Spacer()
                    if let min = w.defaultDurationMin {
                        Text("\(min)m")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    }
                }
            }

            if case let .failed(reason) = connector.lastAddOutcome {
                Text(reason)
                    .font(.caption2)
                    .foregroundStyle(.red)
            } else if case .ok = connector.lastAddOutcome {
                Text("Added ✓")
                    .font(.caption2)
                    .foregroundStyle(.green)
            }
        }
    }
}

/// Curated preset picker. No free-text input — the watch never types.
private struct AddPresetSheet: View {
    let onPick: (WorkoutPreset) -> Void

    var body: some View {
        NavigationStack {
            List(WorkoutPreset.allCases) { preset in
                Button {
                    onPick(preset)
                } label: {
                    HStack {
                        Text(preset.emoji ?? "💪")
                        Text(preset.label)
                        Spacer()
                    }
                }
            }
            .navigationTitle("Add preset")
        }
    }
}
