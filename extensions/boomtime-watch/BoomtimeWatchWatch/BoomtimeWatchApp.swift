//
//  BoomtimeWatchApp.swift
//  BoomtimeWatch Watch App
//
//  watchOS entry point. A paged (horizontal-swipe) TabView with three
//  screens: Start Workout → Sync Now → Settings. All persistence /
//  networking happens on the phone; the watch is pure action-surface plus
//  a handful of on-wrist knobs (WatchSettings).
//

import SwiftUI

@main
struct BoomtimeWatchApp: App {
    @StateObject private var connector = WatchConnector()
    @StateObject private var workoutController = WorkoutController()
    @StateObject private var watchSettings = WatchSettings()

    var body: some Scene {
        WindowGroup {
            RootPager()
                .environmentObject(connector)
                .environmentObject(workoutController)
                .environmentObject(watchSettings)
        }
    }
}

/// Three-screen paged pager. `PageTabViewStyle` gives us horizontal swipe
/// with page dots that watchOS renders natively.
struct RootPager: View {
    var body: some View {
        TabView {
            NavigationStack {
                StartWorkoutView()
            }
            .tag(0)

            NavigationStack {
                ScrollView {
                    VStack {
                        SyncNowView()
                            .padding()
                    }
                }
                .navigationTitle("Sync")
            }
            .tag(1)

            NavigationStack {
                WatchSettingsView()
            }
            .tag(2)
        }
        .tabViewStyle(.page)
    }
}
