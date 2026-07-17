//
//  SettingsView.swift
//  BoomtimeWatch (iOS)
//
//  Post-pairing config surface. Five sections:
//    - Server: editable URL + token (Keychain-backed).
//    - Metrics: per-SyncKind on/off toggles.
//    - Home Wi-Fi: captured SSID/BSSID for home-presence confirmation.
//    - Home Base: captured geofence coord + radius.
//    - Workout Presets: full CRUD for AnnotatedWorkouts. Each row is a
//      user-defined (label, HKWorkoutActivityType, emoji, duration, end
//      behavior, home detection) tuple that shows up on the watch's Start
//      Workout picker and drives the wire `label` field on outgoing
//      WorkoutPayloads.
//

import SwiftUI
import HealthKit
import CoreLocation
import NetworkExtension

struct SettingsView: View {
    @EnvironmentObject var app: AppState
    @EnvironmentObject var config: Config

    @State private var serverURLText: String = ""
    @State private var tokenText: String = ""
    @State private var serverStatus: String? = nil
    @State private var isSavingServer = false

    @State private var editingWorkout: AnnotatedWorkout? = nil
    @State private var showingAddSheet = false
    @State private var showingPresetPicker = false

    // Home config — single StateObject shared by both cards so the auth flow
    // is centralized and we don't spin up two CLLocationManagers.
    @StateObject private var homeCoordinator = HomeSetupCoordinator()

    var body: some View {
        NavigationStack {
            Form {
                serverSection
                metricsSection
                homeWifiSection
                homeBaseSection
                workoutsSection
            }
            .navigationTitle("Settings")
            .onAppear {
                serverURLText = config.serverURL?.absoluteString ?? ""
                tokenText = Keychain.getToken() ?? ""
            }
            .alert("Enable Location Always?", isPresented: $homeCoordinator.showAlwaysAlert) {
                Button("Not now", role: .cancel) {}
                Button("Enable") { homeCoordinator.requestAlways() }
            } message: {
                Text("Boomtime needs Location Always so it can end your Dog Walk when you get home — even when the app isn't open.")
            }
            .sheet(item: $editingWorkout) { workout in
                WorkoutEditorSheet(workout: workout, mode: .edit) { updated in
                    if let idx = config.annotatedWorkouts.firstIndex(where: { $0.id == updated.id }) {
                        config.annotatedWorkouts[idx] = updated
                    }
                }
            }
            .sheet(isPresented: $showingAddSheet) {
                WorkoutEditorSheet(workout: nil, mode: .create) { new in
                    config.annotatedWorkouts.append(new)
                }
            }
            .sheet(isPresented: $showingPresetPicker) {
                PhonePresetPickerSheet { preset in
                    // Append as an AnnotatedWorkout carrying the preset's
                    // defaults, then immediately open the editor so the user
                    // can fine-tune label / duration / behavior before it
                    // syncs to the watch.
                    let created = preset.makeAnnotated()
                    config.annotatedWorkouts.append(created)
                    showingPresetPicker = false
                    editingWorkout = created
                }
            }
        }
    }

    // MARK: Server

    private var serverSection: some View {
        Section("Server") {
            TextField("https://boomtime.example.com", text: $serverURLText)
                .keyboardType(.URL)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .textContentType(.URL)

            TextField("API Token", text: $tokenText)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .font(.system(.body, design: .monospaced))

            if let serverStatus {
                Text(serverStatus)
                    .font(.footnote)
                    .foregroundStyle(serverStatus == "Saved" ? .green : .red)
            }

            Button {
                Task { await saveServer() }
            } label: {
                if isSavingServer {
                    ProgressView()
                } else {
                    Text("Save").frame(maxWidth: .infinity)
                }
            }
            .disabled(isSavingServer || serverURLText.isEmpty || tokenText.isEmpty)
        }
    }

    private func saveServer() async {
        serverStatus = nil
        isSavingServer = true
        defer { isSavingServer = false }

        var s = serverURLText.trimmingCharacters(in: .whitespaces)
        if s.hasSuffix("/") { s.removeLast() }
        guard let url = URL(string: s), url.scheme?.hasPrefix("http") == true else {
            serverStatus = "Server URL must start with http:// or https://"
            return
        }
        let token = tokenText.trimmingCharacters(in: .whitespaces)
        guard !token.isEmpty else {
            serverStatus = "Token is required"
            return
        }
        config.serverURL = url
        guard Keychain.setToken(token) else {
            serverStatus = "Failed to store token in Keychain"
            return
        }
        serverStatus = "Saved"
    }

    // MARK: Metrics

    private var metricsSection: some View {
        Section {
            ForEach(SyncKind.allCases, id: \.self) { kind in
                Toggle(kind.displayName, isOn: metricBinding(for: kind))
            }
        } header: {
            Text("Metrics")
        } footer: {
            Text("Disabling a metric skips its background observer on the next app launch. Existing samples on the server are untouched.")
                .font(.caption2)
        }
    }

    private func metricBinding(for kind: SyncKind) -> Binding<Bool> {
        Binding(
            get: { config.enabledKinds.contains(kind) },
            set: { on in
                if on { config.enabledKinds.insert(kind) }
                else  { config.enabledKinds.remove(kind) }
            }
        )
    }

    // MARK: Home Wi-Fi

    /// Home Wi-Fi capture card. Uses `NEHotspotNetwork.fetchCurrent()` which
    /// requires Location When-In-Use (that's an Apple rule as of iOS 13,
    /// unrelated to Boomtime storing anything location-y — nothing here goes
    /// on the wire).
    @ViewBuilder
    private var homeWifiSection: some View {
        Section {
            if let ssid = config.homeSSID {
                homeWifiSetView(ssid)
            } else {
                Text("Not set — Wi-Fi auto-end disabled.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            Button {
                Task { await homeCoordinator.captureCurrentWifi(into: config) }
            } label: {
                Label(config.homeSSID == nil ? "Use current network" : "Update to current network",
                      systemImage: "wifi")
            }
            .disabled(homeCoordinator.isBusy)

            if let msg = homeCoordinator.wifiStatus {
                Text(msg)
                    .font(.caption2)
                    .foregroundStyle(msg.hasPrefix("Saved") ? .green : .red)
            }

            if config.homeSSID != nil {
                Button(role: .destructive) {
                    config.homeSSID = nil
                } label: {
                    Label("Clear", systemImage: "trash")
                }
            }

            DisclosureGroup("Adjust SSID") {
                HomeSSIDAdjustEditor(current: config.homeSSID) { updated in
                    config.homeSSID = updated
                }
            }
        } header: {
            Text("Home Wi-Fi")
        } footer: {
            Text("Confirms you're home by matching the network name. Wi-Fi name lookup needs Location — Boomtime doesn't record your position for this. Foreground-only unless combined with Home Base.")
                .font(.caption2)
        }
    }

    private func homeWifiSetView(_ ssid: HomeSSID) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack {
                Text(ssid.ssid).font(.body)
                Spacer()
                Text("Captured \(relative(ssid.setAt))")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }
            if let bssid = ssid.bssid {
                Text("BSSID \(bssid)")
                    .font(.caption2.monospaced())
                    .foregroundStyle(.secondary)
            }
        }
    }

    // MARK: Home Base

    /// Geofence card. Requests When-In-Use, then chains to Always via the
    /// coordinator's `showAlwaysAlert` prompt.
    @ViewBuilder
    private var homeBaseSection: some View {
        Section {
            if let hb = config.homeBase {
                homeBaseSetView(hb)
            } else {
                Text("Not set — geofence auto-end disabled.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            Button {
                Task { await homeCoordinator.captureCurrentLocation(into: config) }
            } label: {
                Label(config.homeBase == nil ? "Set to current location" : "Update to current location",
                      systemImage: "location.circle.fill")
            }
            .disabled(homeCoordinator.isBusy)

            if let msg = homeCoordinator.locationStatus {
                Text(msg)
                    .font(.caption2)
                    .foregroundStyle(msg.hasPrefix("Saved") ? .green : .red)
            }

            if config.homeBase != nil {
                Stepper(
                    "Radius \(Int(config.homeBase?.radiusM ?? 100)) m",
                    value: Binding(
                        get: { Int(config.homeBase?.radiusM ?? 100) },
                        set: { newVal in
                            guard var hb = config.homeBase else { return }
                            hb.radiusM = Double(newVal)
                            config.homeBase = hb
                        }
                    ),
                    in: 50...500,
                    step: 50
                )

                Button(role: .destructive) {
                    config.homeBase = nil
                } label: {
                    Label("Clear", systemImage: "trash")
                }
            }

            if config.homeBase != nil {
                DisclosureGroup("Adjust coordinates") {
                    HomeBaseCoordEditor(current: config.homeBase) { updated in
                        config.homeBase = updated
                    }
                }
            }
        } header: {
            Text("Home Base")
        } footer: {
            Text("Location Always is required so we can auto-end walks in the background. Denying it leaves this feature disabled at runtime — the coord itself still saves.")
                .font(.caption2)
        }
    }

    private func homeBaseSetView(_ hb: HomeBase) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack {
                Text("\(hb.lat, specifier: "%.4f"), \(hb.lon, specifier: "%.4f")")
                    .font(.system(.body, design: .monospaced))
                Spacer()
                Text("Captured \(relative(hb.setAt))")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }
            Text("Radius \(Int(hb.radiusM)) m")
                .font(.caption2)
                .foregroundStyle(.secondary)
        }
    }

    private func relative(_ date: Date) -> String {
        let f = RelativeDateTimeFormatter()
        f.unitsStyle = .abbreviated
        return f.localizedString(for: date, relativeTo: Date())
    }

    // MARK: Workout presets

    private var workoutsSection: some View {
        Section {
            if config.annotatedWorkouts.isEmpty {
                Text("No presets yet — tap Add to create one.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            ForEach(config.annotatedWorkouts) { workout in
                Button {
                    editingWorkout = workout
                } label: {
                    HStack {
                        Text(workout.effectiveEmoji)
                        VStack(alignment: .leading) {
                            Text(workout.label).foregroundStyle(.primary)
                            HStack(spacing: 6) {
                                Text(workout.wireKind)
                                if let min = workout.defaultDurationMin {
                                    Text("· \(min) min")
                                    if workout.endBehavior == .autoEnd {
                                        Text("· auto-end")
                                    } else if workout.endBehavior == .haptic {
                                        Text("· haptic")
                                    }
                                }
                                if workout.homeDetection != .off {
                                    Text("· home:\(workout.homeDetection.rawValue)")
                                }
                            }
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                        }
                        Spacer()
                        Image(systemName: "chevron.right")
                            .font(.caption)
                            .foregroundStyle(.tertiary)
                    }
                }
            }
            .onDelete { indexSet in
                config.annotatedWorkouts.remove(atOffsets: indexSet)
            }
            .onMove { from, to in
                config.annotatedWorkouts.move(fromOffsets: from, toOffset: to)
            }

            Menu {
                Button {
                    showingPresetPicker = true
                } label: {
                    Label("From Preset", systemImage: "square.grid.2x2")
                }
                Button {
                    showingAddSheet = true
                } label: {
                    Label("Custom", systemImage: "square.and.pencil")
                }
            } label: {
                Label("Add Preset", systemImage: "plus.circle")
            }
        } header: {
            HStack {
                Text("Workout Presets")
                Spacer()
                EditButton().font(.footnote)
            }
        } footer: {
            Text("Presets appear on the watch's Start Workout screen. The label becomes the project bucket in Boomtime.")
                .font(.caption2)
        }
    }
}

// MARK: - Editor sheet

/// A discrete duration option in the editor. `nil` == open-ended (no countdown).
private struct DurationOption: Hashable, Identifiable {
    let minutes: Int?
    var id: Int { minutes ?? -1 }
    var displayName: String {
        guard let m = minutes else { return "Off (open-ended)" }
        return "\(m) min"
    }
    static let choices: [DurationOption] = [
        .init(minutes: nil),
        .init(minutes: 10),
        .init(minutes: 15),
        .init(minutes: 20),
        .init(minutes: 30),
        .init(minutes: 45),
        .init(minutes: 60),
        .init(minutes: 90),
        .init(minutes: 120),
    ]
}

private struct WorkoutEditorSheet: View {
    enum Mode { case create, edit }

    @Environment(\.dismiss) private var dismiss

    let mode: Mode
    let onSave: (AnnotatedWorkout) -> Void

    @State private var label: String
    @State private var emoji: String
    @State private var pickerKind: PickerKind
    @State private var durationChoice: DurationOption
    @State private var endBehavior: WorkoutEndBehavior
    @State private var homeDetection: HomeDetection
    private let editingID: UUID?

    init(workout: AnnotatedWorkout?, mode: Mode, onSave: @escaping (AnnotatedWorkout) -> Void) {
        self.mode = mode
        self.onSave = onSave
        if let w = workout {
            self._label = State(initialValue: w.label)
            self._emoji = State(initialValue: w.emoji ?? "")
            self._pickerKind = State(initialValue: PickerKind(activityRaw: w.activityRaw))
            let match = DurationOption.choices.first { $0.minutes == w.defaultDurationMin }
                ?? DurationOption(minutes: w.defaultDurationMin)
            self._durationChoice = State(initialValue: match)
            self._endBehavior = State(initialValue: w.endBehavior)
            self._homeDetection = State(initialValue: w.homeDetection)
            self.editingID = w.id
        } else {
            self._label = State(initialValue: "")
            self._emoji = State(initialValue: "")
            self._pickerKind = State(initialValue: .running)
            self._durationChoice = State(initialValue: DurationOption(minutes: nil))
            self._endBehavior = State(initialValue: .manual)
            self._homeDetection = State(initialValue: .off)
            self.editingID = nil
        }
    }

    var body: some View {
        NavigationStack {
            Form {
                Section("Label") {
                    TextField("Morning Run", text: $label)
                        .textInputAutocapitalization(.words)
                }
                Section("Activity") {
                    Picker("Kind", selection: $pickerKind) {
                        ForEach(PickerKind.allCases, id: \.self) { k in
                            Text(k.displayName).tag(k)
                        }
                    }
                }
                Section("Duration") {
                    Picker("Default", selection: $durationChoice) {
                        ForEach(DurationOption.choices) { c in
                            Text(c.displayName).tag(c)
                        }
                    }
                    Picker("End behavior", selection: $endBehavior) {
                        ForEach(WorkoutEndBehavior.allCases) { b in
                            Text(b.displayName).tag(b)
                        }
                    }
                    .disabled(durationChoice.minutes == nil)
                }
                Section {
                    Picker("End when home", selection: $homeDetection) {
                        ForEach(HomeDetection.allCases) { d in
                            Text(d.displayName).tag(d)
                        }
                    }
                } header: {
                    Text("Home detection")
                } footer: {
                    Text("Requires Home Wi-Fi and/or Home Base to be configured. Runtime downgrades to whatever's set — an unset half is silently ignored.")
                        .font(.caption2)
                }
                Section("Emoji") {
                    TextField("Optional", text: $emoji)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                }
            }
            .navigationTitle(mode == .create ? "New Preset" : "Edit Preset")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Save") {
                        save()
                        dismiss()
                    }
                    .disabled(label.trimmingCharacters(in: .whitespaces).isEmpty)
                }
            }
        }
    }

    private func save() {
        let trimmed = label.trimmingCharacters(in: .whitespaces)
        let trimmedEmoji = emoji.trimmingCharacters(in: .whitespaces)
        let workout = AnnotatedWorkout(
            id: editingID ?? UUID(),
            label: trimmed,
            activityRaw: pickerKind.activityRaw,
            wireKind: pickerKind.wireKind,
            emoji: trimmedEmoji.isEmpty ? nil : trimmedEmoji,
            defaultDurationMin: durationChoice.minutes,
            // End behavior only matters when a duration is set; the model
            // still stores the picked value so re-enabling the duration
            // keeps the user's choice.
            endBehavior: endBehavior,
            homeDetection: homeDetection
        )
        onSave(workout)
    }
}

/// Restricted picker of activity types the phone Settings UI exposes.
/// Wider than the watch's fallback preset list (users might want types the
/// watch doesn't advertise, like walking) but still curated to keep the
/// scroll list manageable.
private enum PickerKind: String, CaseIterable, Hashable {
    case running
    case walking
    case hiking
    case cycling
    case swimming
    case functionalStrength
    case traditionalStrength
    case hiit
    case yoga
    case pilates
    case rowing
    case elliptical
    case other

    init(activityRaw: Int) {
        // Fall back to `.other` for anything we don't know — the user can then
        // re-pick if they care.
        let match = PickerKind.allCases.first { $0.activityRaw == activityRaw }
        self = match ?? .other
    }

    var displayName: String {
        switch self {
        case .running:  return "Running"
        case .walking:  return "Walking"
        case .hiking:   return "Hiking"
        case .cycling:  return "Cycling"
        case .swimming: return "Swimming"
        case .functionalStrength: return "Functional Strength"
        case .traditionalStrength: return "Traditional Strength"
        case .hiit:     return "HIIT"
        case .yoga:     return "Yoga"
        case .pilates:  return "Pilates"
        case .rowing:   return "Rowing"
        case .elliptical: return "Elliptical"
        case .other:    return "Other"
        }
    }

    var activityRaw: Int {
        Int(hkActivity.rawValue)
    }

    var hkActivity: HKWorkoutActivityType {
        switch self {
        case .running:  return .running
        case .walking:  return .walking
        case .hiking:   return .hiking
        case .cycling:  return .cycling
        case .swimming: return .swimming
        case .functionalStrength: return .functionalStrengthTraining
        case .traditionalStrength: return .traditionalStrengthTraining
        case .hiit:     return .highIntensityIntervalTraining
        case .yoga:     return .yoga
        case .pilates:  return .pilates
        case .rowing:   return .rowing
        case .elliptical: return .elliptical
        case .other:    return .other
        }
    }

    /// Direct raw-value pin for cases we want to guarantee (e.g. `.other = 3000`).
    /// Kept in sync with WorkoutPreset for cross-reference sanity.

    /// Same wire kind strings PayloadBuilder.wireName emits.
    var wireKind: String {
        switch self {
        case .running:  return "running"
        case .walking:  return "walking"
        case .hiking:   return "hiking"
        case .cycling:  return "cycling"
        case .swimming: return "swimming"
        case .functionalStrength: return "functional_strength"
        case .traditionalStrength: return "strength"
        case .hiit:     return "hiit"
        case .yoga:     return "yoga"
        case .pilates:  return "pilates"
        case .rowing:   return "rowing"
        case .elliptical: return "elliptical"
        case .other:    return "other"
        }
    }
}

// MARK: - Preset picker sheet

private struct PhonePresetPickerSheet: View {
    @Environment(\.dismiss) private var dismiss
    let onPick: (WorkoutPreset) -> Void

    var body: some View {
        NavigationStack {
            List(WorkoutPreset.allCases) { preset in
                Button {
                    onPick(preset)
                } label: {
                    HStack {
                        Text(preset.emoji ?? "💪")
                        VStack(alignment: .leading) {
                            Text(preset.label).foregroundStyle(.primary)
                            HStack(spacing: 6) {
                                Text(preset.wireKind)
                                Text("· \(preset.defaultDurationMin) min")
                                if preset.endBehavior == .autoEnd { Text("· auto-end") }
                            }
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                        }
                        Spacer()
                    }
                }
            }
            .navigationTitle("Add Preset")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
            }
        }
    }
}

// MARK: - Home setup coordinator

/// Bundles the state machine for capturing Home Wi-Fi and Home Base into a
/// single ObservableObject so SettingsView doesn't grow a delegate/manager
/// tangle. Owns one `CLLocationManager` for the location-auth handshake, and
/// exposes the alert-driver flags (`showAlwaysAlert`) that let SwiftUI render
/// the "explain before requesting Always" dialog per the spec.
///
/// Note the two capture flows are async but neither `await`s on a delegate
/// callback for auth — CLLocationManager's authorization API is fire-and-
/// observe, so we drive the async surface with a small continuation.
@MainActor
final class HomeSetupCoordinator: NSObject, ObservableObject {
    @Published var isBusy: Bool = false
    @Published var wifiStatus: String? = nil
    @Published var locationStatus: String? = nil
    @Published var showAlwaysAlert: Bool = false

    private let locationManager = CLLocationManager()
    /// Set while we're waiting for a one-shot location update.
    private var pendingLocationContinuation: CheckedContinuation<CLLocation, Error>?

    override init() {
        super.init()
        locationManager.delegate = self
        locationManager.desiredAccuracy = kCLLocationAccuracyBest
    }

    // MARK: Home Wi-Fi

    /// Fetch the current SSID via NEHotspotNetwork. Requires Location auth —
    /// requests When-In-Use if not yet granted. Persists to Config on success.
    func captureCurrentWifi(into config: Config) async {
        wifiStatus = nil
        isBusy = true
        defer { isBusy = false }

        // Bump When-In-Use if we're .notDetermined — SSID reads require it.
        if locationManager.authorizationStatus == .notDetermined {
            locationManager.requestWhenInUseAuthorization()
        }
        if let status = mapAuthDenied() {
            wifiStatus = status
            return
        }

        guard let net = await NEHotspotNetwork.fetchCurrent() else {
            wifiStatus = "Not connected to Wi-Fi, or Location denied for SSID."
            return
        }
        config.homeSSID = HomeSSID(ssid: net.ssid, bssid: net.bssid.isEmpty ? nil : net.bssid, setAt: Date())
        wifiStatus = "Saved."
    }

    // MARK: Home Base

    /// Capture the current CLLocation and persist as HomeBase. Requests
    /// When-In-Use first (needed for any location read), then teases the
    /// Always upgrade via `showAlwaysAlert`.
    func captureCurrentLocation(into config: Config) async {
        locationStatus = nil
        isBusy = true
        defer { isBusy = false }

        if locationManager.authorizationStatus == .notDetermined {
            locationManager.requestWhenInUseAuthorization()
        }
        if let status = mapAuthDenied() {
            locationStatus = status
            return
        }

        do {
            let loc = try await requestOneLocation()
            let hb = HomeBase(
                lat: loc.coordinate.latitude,
                lon: loc.coordinate.longitude,
                radiusM: config.homeBase?.radiusM ?? 100.0,
                setAt: Date()
            )
            config.homeBase = hb
            locationStatus = "Saved."
            // Now nudge to Always — but through the explanatory alert.
            if locationManager.authorizationStatus == .authorizedWhenInUse {
                showAlwaysAlert = true
            }
        } catch {
            locationStatus = "Failed: \(error.localizedDescription)"
        }
    }

    /// User confirmed the explanatory alert — request Always. If denied, the
    /// geofence still doesn't fire; that's captured at monitor-start time.
    func requestAlways() {
        showAlwaysAlert = false
        locationManager.requestAlwaysAuthorization()
    }

    /// One-shot: request a single location via `requestLocation()`; resolve on
    /// the delegate's `didUpdateLocations` or `didFailWithError`.
    private func requestOneLocation() async throws -> CLLocation {
        try await withCheckedThrowingContinuation { cont in
            self.pendingLocationContinuation = cont
            self.locationManager.requestLocation()
        }
    }

    /// Human-readable failure for any denied/restricted auth state. Returns
    /// nil when auth is OK (either When-In-Use or Always).
    private func mapAuthDenied() -> String? {
        switch locationManager.authorizationStatus {
        case .notDetermined:  return "Waiting for Location permission…"
        case .denied:         return "Location denied — enable in iOS Settings."
        case .restricted:     return "Location restricted by device policy."
        case .authorizedWhenInUse, .authorizedAlways: return nil
        @unknown default:     return "Location auth in unknown state."
        }
    }
}

extension HomeSetupCoordinator: CLLocationManagerDelegate {
    nonisolated func locationManager(_ manager: CLLocationManager, didUpdateLocations locations: [CLLocation]) {
        guard let last = locations.last else { return }
        Task { @MainActor in
            self.pendingLocationContinuation?.resume(returning: last)
            self.pendingLocationContinuation = nil
        }
    }

    nonisolated func locationManager(_ manager: CLLocationManager, didFailWithError error: Error) {
        Task { @MainActor in
            self.pendingLocationContinuation?.resume(throwing: error)
            self.pendingLocationContinuation = nil
        }
    }
}

// MARK: - Coord + SSID adjust editors

/// Small keyed lat/lon TextField pair for the "Adjust coordinates" disclosure.
private struct HomeBaseCoordEditor: View {
    let current: HomeBase?
    let onSave: (HomeBase) -> Void

    @State private var latText: String = ""
    @State private var lonText: String = ""
    @State private var status: String? = nil

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            TextField("Latitude", text: $latText)
                .keyboardType(.numbersAndPunctuation)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .font(.system(.body, design: .monospaced))
            TextField("Longitude", text: $lonText)
                .keyboardType(.numbersAndPunctuation)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .font(.system(.body, design: .monospaced))
            Button("Save coords") { save() }
                .disabled(latText.isEmpty || lonText.isEmpty)
            if let status {
                Text(status).font(.caption2).foregroundStyle(.red)
            }
        }
        .onAppear {
            if let c = current {
                latText = String(c.lat)
                lonText = String(c.lon)
            }
        }
    }

    private func save() {
        guard let lat = Double(latText), let lon = Double(lonText) else {
            status = "Coord must be a decimal number."
            return
        }
        // Sanity range — reject "seemed like a lat/lon but was a mistake."
        guard (-90.0...90.0).contains(lat), (-180.0...180.0).contains(lon) else {
            status = "Lat must be [-90, 90], lon [-180, 180]."
            return
        }
        let hb = HomeBase(
            lat: lat,
            lon: lon,
            radiusM: current?.radiusM ?? 100.0,
            setAt: Date()
        )
        onSave(hb)
        status = nil
    }
}

/// SSID + BSSID override — a text-based sibling of the "Use current network"
/// button that lets the user pre-populate home Wi-Fi from elsewhere.
private struct HomeSSIDAdjustEditor: View {
    let current: HomeSSID?
    let onSave: (HomeSSID) -> Void

    @State private var ssidText: String = ""
    @State private var bssidText: String = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            TextField("SSID", text: $ssidText)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
            TextField("BSSID (optional)", text: $bssidText)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .font(.system(.body, design: .monospaced))
            Button("Save network") {
                let trimmedSSID = ssidText.trimmingCharacters(in: .whitespaces)
                let trimmedBSSID = bssidText.trimmingCharacters(in: .whitespaces)
                guard !trimmedSSID.isEmpty else { return }
                onSave(HomeSSID(
                    ssid: trimmedSSID,
                    bssid: trimmedBSSID.isEmpty ? nil : trimmedBSSID,
                    setAt: Date()
                ))
            }
            .disabled(ssidText.trimmingCharacters(in: .whitespaces).isEmpty)
        }
        .onAppear {
            if let c = current {
                ssidText = c.ssid
                bssidText = c.bssid ?? ""
            }
        }
    }
}

#Preview {
    let state = AppState()
    return SettingsView()
        .environmentObject(state)
        .environmentObject(state.config)
}
