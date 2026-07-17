//
//  StartWorkoutView.swift
//  BoomtimeWatch Watch App
//
//  Watch Start Workout picker. Row source:
//    - If the phone has pushed a non-empty annotated-workout list, render
//      one row per AnnotatedWorkout.
//    - If the list is empty (fresh pair / first launch), render
//      WorkoutPreset.allCases as a read-only fallback so the user can still
//      start a canonical run/ride while they configure real presets on the
//      phone.
//    - If the phone HAS pushed a list but it's explicitly empty, render an
//      instructional empty state.
//
//  Row tap:
//    - If `WatchSettings.showLabelPickerOnStart == false`: push LiveWorkoutView.
//    - If true: push LabelConfirmView so the user can double-check the picked
//      label before HR starts collecting.
//

import SwiftUI

/// Small value type the picker and confirm screen pass to LiveWorkoutView.
/// Bundles the fields WorkoutController.start needs, plus the display label.
struct WorkoutStartArgs: Hashable {
    let activityRaw: Int
    let label: String
    let defaultDurationMin: Int?
    let endBehavior: WorkoutEndBehavior
    let homeDetection: HomeDetection

    init(from workout: AnnotatedWorkout) {
        self.activityRaw = workout.activityRaw
        self.label = workout.label
        self.defaultDurationMin = workout.defaultDurationMin
        self.endBehavior = workout.endBehavior
        self.homeDetection = workout.homeDetection
    }

    init(from preset: WorkoutPreset) {
        self.activityRaw = preset.activityRaw
        self.label = preset.label
        self.defaultDurationMin = preset.defaultDurationMin
        self.endBehavior = preset.endBehavior
        self.homeDetection = preset.homeDetection
    }

    /// Used by LabelConfirmView to swap in a sibling label without disturbing
    /// activity / duration / behavior / detection.
    func withLabel(_ newLabel: String) -> WorkoutStartArgs {
        WorkoutStartArgs(
            activityRaw: activityRaw,
            label: newLabel,
            defaultDurationMin: defaultDurationMin,
            endBehavior: endBehavior,
            homeDetection: homeDetection
        )
    }

    private init(activityRaw: Int, label: String, defaultDurationMin: Int?, endBehavior: WorkoutEndBehavior, homeDetection: HomeDetection) {
        self.activityRaw = activityRaw
        self.label = label
        self.defaultDurationMin = defaultDurationMin
        self.endBehavior = endBehavior
        self.homeDetection = homeDetection
    }
}

struct StartWorkoutView: View {
    @EnvironmentObject var connector: WatchConnector
    @EnvironmentObject var settings: WatchSettings

    var body: some View {
        List {
            content
        }
        .navigationTitle("Start")
    }

    @ViewBuilder
    private var content: some View {
        if !connector.context.annotatedWorkouts.isEmpty {
            annotatedRows
        } else if connector.context == .empty {
            fallbackRows(footer: "Configure your own presets in the iPhone app.")
        } else {
            Text("No workouts configured. Open the Boomtime iPhone app to create one.")
                .font(.footnote)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .padding()
        }
    }

    private var annotatedRows: some View {
        ForEach(ordered) { workout in
            NavigationLink {
                nextScreen(for: WorkoutStartArgs(from: workout))
            } label: {
                HStack {
                    Text(workout.effectiveEmoji)
                    Text(workout.label)
                    Spacer()
                    if let min = workout.defaultDurationMin {
                        Text("\(min)m")
                            .font(.caption2)
                            .foregroundStyle(.tertiary)
                    }
                }
            }
        }
    }

    private func fallbackRows(footer: String) -> some View {
        Group {
            ForEach(WorkoutPreset.allCases) { preset in
                NavigationLink {
                    nextScreen(for: WorkoutStartArgs(from: preset))
                } label: {
                    HStack {
                        Text(preset.emoji ?? "💪")
                        Text(preset.label)
                        Spacer()
                        Text("\(preset.defaultDurationMin)m")
                            .font(.caption2)
                            .foregroundStyle(.tertiary)
                    }
                }
            }
            Text(footer)
                .font(.caption2)
                .foregroundStyle(.secondary)
        }
    }

    /// Ordered list — the user-picked default (if it still exists) floats to
    /// the top, everything else keeps declaration order.
    private var ordered: [AnnotatedWorkout] {
        let list = connector.context.annotatedWorkouts
        guard let id = settings.defaultWorkoutID,
              let idx = list.firstIndex(where: { $0.id == id })
        else { return list }
        var reordered = list
        let hoisted = reordered.remove(at: idx)
        reordered.insert(hoisted, at: 0)
        return reordered
    }

    @ViewBuilder
    private func nextScreen(for args: WorkoutStartArgs) -> some View {
        if settings.showLabelPickerOnStart {
            LabelConfirmView(args: args)
        } else {
            LiveWorkoutView(
                activityRaw: args.activityRaw,
                label: args.label,
                defaultDurationMin: args.defaultDurationMin,
                endBehavior: args.endBehavior,
                homeDetection: args.homeDetection
            )
        }
    }
}

/// Intermediate confirm screen when `showLabelPickerOnStart` is on. Reads
/// sibling labels (annotated workouts with the same activityRaw) so the user
/// can re-pick before HR collection starts. Editing labels themselves still
/// happens on the phone.
struct LabelConfirmView: View {
    @EnvironmentObject var connector: WatchConnector

    let args: WorkoutStartArgs

    var body: some View {
        List {
            Section("Log as") {
                ForEach(candidates, id: \.self) { name in
                    NavigationLink {
                        let picked = args.withLabel(name)
                        LiveWorkoutView(
                            activityRaw: picked.activityRaw,
                            label: picked.label,
                            defaultDurationMin: picked.defaultDurationMin,
                            endBehavior: picked.endBehavior,
                            homeDetection: picked.homeDetection
                        )
                    } label: {
                        Text(name)
                    }
                }
            }
        }
        .navigationTitle("Label")
    }

    private var candidates: [String] {
        var seen = Set<String>()
        var out: [String] = []
        out.append(args.label); seen.insert(args.label)
        for w in connector.context.annotatedWorkouts
            where w.activityRaw == args.activityRaw && !seen.contains(w.label) {
            out.append(w.label); seen.insert(w.label)
        }
        return out
    }
}
