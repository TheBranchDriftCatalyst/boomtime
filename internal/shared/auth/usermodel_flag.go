package auth

import "sync/atomic"

// Process-global user-model switch (gaka-0oe). The substrate feature flag is
// env-driven and immutable after boot, so it lives as one process-global here
// rather than being threaded through every domain Handler's config — the
// handlers have three different config shapes (*config.Config, a narrow
// interface, none at all), which made per-handler threading a mess.
//
// main() sets it once from cfg.FeatureUserModel at startup; apihelpers.Identify*
// reads it. Default is false (zero value) so every code path that never sets it
// — including the entire existing test suite — behaves exactly as before the
// substrate landed. Flag-on tests call SetUserModelEnabled(true) with a
// t.Cleanup reset.
var userModelEnabled atomic.Bool

// SetUserModelEnabled sets the process-global user-model switch. Called once at
// boot from cfg.FeatureUserModel (and by flag-on tests).
func SetUserModelEnabled(on bool) { userModelEnabled.Store(on) }

// UserModelEnabled reports whether the user-model substrate is active. Read by
// apihelpers.Identify* to decide all-caps (off) vs real role/capabilities +
// disabled-fail-closed (on).
func UserModelEnabled() bool { return userModelEnabled.Load() }
