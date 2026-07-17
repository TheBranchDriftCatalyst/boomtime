# BoomtimeWatch

A native iOS companion app that reads HealthKit data (workouts, heart rate, steps, energy, HRV, sleep, mindful minutes) from your iPhone — including everything the Apple Watch mirrors to it — and POSTs it to your self-hosted Boomtime server.

This is an iPhone-only app (deployment target iOS 17). There is no separate watchOS target; the iPhone can see every Watch-authored sample, so a companion watch app is not necessary for v1.

---

## Prerequisites

- A Mac running Xcode 15 or newer.
- A free Apple ID (Personal Team is fine — no paid Developer Program membership required).
- An iPhone on iOS 17 or newer, with a USB or Lightning cable.
- You're signed into iCloud with the same Apple ID on both the Mac and the iPhone.
- A running Boomtime server you can reach from the phone, and an API token from that server.

---

## One-time Xcode signing setup

1. Open Xcode. Menu bar → `Xcode` → `Settings…` → `Accounts` tab.
2. Click `+` → `Apple ID`, sign in with your personal Apple ID. Xcode automatically provisions a "Personal Team" under that ID.
3. In Finder, open `extensions/boomtime-watch/BoomtimeWatch.xcodeproj`.
4. In the project navigator (left sidebar), click the top-level `BoomtimeWatch` project, then select the `BoomtimeWatch` target.
5. Open the `Signing & Capabilities` tab.
6. Check `Automatically manage signing`.
7. Under `Team`, pick `<Your Name> (Personal Team)`.
8. Change the `Bundle Identifier` from `com.boomtime.watch` to something unique to you — e.g. `com.<yourname>.boomtimewatch`. Personal Teams reject bundle IDs that another Personal Team has already claimed anywhere on Apple's servers, and `com.boomtime.watch` may already be taken.
9. Confirm the `HealthKit` capability is present in this tab. If not, click `+ Capability` at the top of the tab, search for HealthKit, and add it.
10. Under the HealthKit capability card, tick `Background Delivery`.

---

## Install onto your phone

1. Connect the iPhone to the Mac. Unlock the phone. If prompted, tap `Trust This Computer` on the phone and enter your passcode.
2. In Xcode's toolbar (top center), click the destination selector and pick your device by name — not `Any iOS Device` and not a simulator (simulators can't run HealthKit).
3. Hit `Run` (or press `⌘R`). First install takes 30–60 seconds; you may see `Preparing your iPhone for development…` — that's normal.
4. On the iPhone, open `Settings` → `General` → `VPN & Device Management`. Under `Developer App` you'll see your Apple ID; tap it, then tap `Trust "<Your Apple ID>"`, confirm.
5. Return to the home screen and tap the BoomtimeWatch icon. If Trust succeeded, the app launches; if it silently fails, repeat step 4.

---

## Grant HealthKit permission

1. The app opens on the `Pair with Boomtime` screen. Enter your server URL and token (see next section), then tap `Save & Grant HealthKit Access`.
2. iOS shows its system HealthKit consent sheet. Tap `Turn On All` (or toggle each category individually), then `Allow`.
3. If you deny or skip categories, iOS silently returns empty results forever — the reset path is `Settings` → `Privacy & Security` → `Health` → `BoomtimeWatch` → toggle categories back on.

---

## Pair with the server

On the Pairing screen:

1. `Server` field: your Boomtime URL, e.g. `https://boomtime.example.com`. Include the scheme (`http://` or `https://`). Trailing slashes are stripped for you.
2. `API Token` field: paste the token from your Boomtime dashboard's API-token page. It's a UUID.
3. Tap `Save & Grant HealthKit Access`. The app persists the URL, stores the token in Keychain, requests HealthKit permission, and kicks the first sync.

After pairing, the app shows the `Sync Status` screen: one row per data type with its last-sync timestamp. Pull-to-refresh or tap `Sync now` to force a walk of every type.

---

## The 7-day free-provisioning caveat

Personal Team provisioning profiles expire every **seven days**. After expiry, the app icon still exists but tapping it silently fails, and iOS may show `Untrusted Enterprise Developer` or `Could Not Verify App`.

Recovery is always the same: plug the phone into the Mac, open Xcode, hit Run — Xcode reprovisions and reinstalls.

To avoid the 7-day cycle, options are:

- Join the paid Apple Developer Program (~$99/yr) — profiles last a year.
- Use AltStore or Sideloadly to auto-reprovision on a schedule.
- Live with it and set a weekly calendar reminder.

---

## Verify sync works

1. Do a short workout on your Watch (a 5-minute "Other" indoor workout is enough), or wait until the Watch/iPhone accumulates some HR/step samples.
2. Wait 1–5 minutes. Background delivery isn't instant — HealthKit batches wakes.
3. Open the BoomtimeWatch app. The `Sync Status` screen should show a recent timestamp for at least one row.
4. On the server, check the Boomtime web dashboard's `/wellness` route, or tail server logs for `POST /api/v1/users/current/workouts.bulk` and `POST /api/v1/users/current/health_samples.bulk`.

---

## Troubleshooting

**App won't launch (7-day expiry).** Plug in, open Xcode, hit Run.

**HealthKit consent sheet never appeared.** You already granted or denied it once. Reset via `Settings` → `Privacy & Security` → `Health` → `BoomtimeWatch`.

**No data flowing at all.** Confirm every category is enabled in `Settings` → `Privacy & Security` → `Health` → `BoomtimeWatch`. If they're all on but Sync Status still says `never`, the token is probably wrong — see the next entry.

**`401 Unauthorized` in server logs.** Token is wrong or expired. Regenerate in Boomtime, tap `Unpair` in the app, and re-enter.

**Manual "Sync now" works, but nothing arrives in the background.** Two likely causes:

1. The `com.apple.developer.healthkit.background-delivery` entitlement is missing. Re-check `Signing & Capabilities` → `HealthKit` → `Background Delivery` is ticked.
2. The observer's completionHandler didn't fire on a previous wake and iOS is now throttling us. Force-quit BoomtimeWatch (swipe up in the app switcher) and relaunch.

**A specific category shows an error.** Tap into `Sync Status` — the row shows the last error message. Common causes: server 5xx (the app will retry with exponential backoff and eventually recover), or malformed URL (the app rejects non-`http(s)` URLs at pairing time).

**Xcode complains about the bundle identifier being taken.** Change it under `Signing & Capabilities` → `Bundle Identifier` to something more unique.

**Dog Walk not auto-ending?** Check Boomtime iPhone `Settings` → `Home Base` and `Home Wi-Fi` are both set. Confirm iOS `Settings` → `Boomtime` → `Location` = `Always`, and iOS `Settings` → `Boomtime` → `Wi-Fi Info` = enabled. If you're on a network with a hidden SSID or an enterprise Wi-Fi that Apple treats as "unknown," the SSID check will fail — edit the workout in Boomtime Settings and switch its Home detection to `Geofence only` instead. And note the "Access WiFi Information" capability must be added to the iOS target in Xcode (`Signing & Capabilities` → `+ Capability`) — Personal Team supports it.

---

## Architecture (for future maintainers)

- **Models/** — Pure `Codable` DTOs matching the Boomtime wire format exactly. Do not add computed properties that leak into JSON.
- **Services/Config.swift** — `UserDefaults`-backed non-secret config: server URL, per-type `HKQueryAnchor` blobs, per-type last-sync timestamps for the UI.
- **Services/Keychain.swift** — API token storage. `kSecAttrAccessibleAfterFirstUnlock` so background wakes after a reboot can still auth.
- **Services/HealthKitReader.swift** — All HealthKit I/O. Observers, anchored fetches, and per-workout HR series (scoped via `HKQuery.predicateForObjects(from: workout)`).
- **Services/PayloadBuilder.swift** — Pure conversion `HKSample` / `HKWorkout` → wire structs. Uses `HKWorkout.statistics(for:)` for energy/distance (the direct properties are deprecated in iOS 18).
- **Services/Uploader.swift** — POST with 30s timeout, jittered exponential backoff (1s → 32s cap, ~5 attempts). Only returns success on HTTP 2xx.
- **Services/SyncCoordinator.swift** — Glues the above together. **Only advances the persisted `HKQueryAnchor` after a successful upload.** That's the durability boundary: a failed POST means the anchor stays put, so the next observer fire re-tries the same samples.

### Sync loop details

- One `HKObserverQuery` per data type, all installed at app launch. Each observer's `completionHandler` is called via `defer`-in-`Task` so iOS's throttling logic sees the wake acked immediately — the actual upload runs asynchronously.
- Workouts and sleep use `.immediate` background delivery frequency; other types use `.hourly` to reduce wake pressure.
- Per-workout HR series uses `HKQuery.predicateForObjects(from: workout)` — not a start/end time-window predicate. This scopes to samples the Watch explicitly linked to the workout, not unrelated background HR readings in the same window.
- GPS route (`HKWorkoutRoute`) is not fetched in v1. The wire format already reserves the `route` field for it; wiring it up is a follow-up.
