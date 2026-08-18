package config

import "testing"

// TestLabelImagesReconcileEnabled locks the one-or-the-other gate: under the
// rabbitmq broker the worker's AMQP consumer already generates, so the startup
// reconcile pass must NOT also run by default (that double-generated every
// regen — one image via the job, one via the reconcile).
func TestLabelImagesReconcileEnabled(t *testing.T) {
	cases := []struct {
		mode, broker string
		want         bool
	}{
		{"auto", "inprocess", true}, // default single-process: reconcile runs (unchanged)
		{"", "inprocess", true},     // empty == auto
		{"auto", "rabbitmq", false}, // THE FIX: consumer generates, reconcile off
		{"", "rabbitmq", false},     // empty == auto
		{"on", "rabbitmq", true},    // force on (e.g. a dedicated fill-in run)
		{"off", "inprocess", false}, // force off
		{"OFF", "rabbitmq", false},  // case-insensitive
	}
	for _, c := range cases {
		cfg := &Config{LabelImagesReconcile: c.mode, QueueBroker: c.broker}
		if got := cfg.LabelImagesReconcileEnabled(); got != c.want {
			t.Errorf("LabelImagesReconcile=%q QueueBroker=%q: got %v, want %v", c.mode, c.broker, got, c.want)
		}
	}
}
