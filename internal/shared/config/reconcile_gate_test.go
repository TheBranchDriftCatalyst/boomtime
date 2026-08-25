// reconcile_gate_test.go — LabelImagesReconcileEnabled.
//
// The broker axis this used to sweep (QueueBroker inprocess vs rabbitmq) is gone
// with RabbitMQ (boom-piig phase 3). "auto" previously meant "only when there is
// no separate AMQP worker", because both would generate and double-fire; regen is
// now a catalyst-go-jobs kind with per-label dedup, so auto is unconditionally on
// and only the explicit on/off overrides remain meaningful.
package config

import "testing"

func TestLabelImagesReconcileEnabled(t *testing.T) {
	cases := []struct {
		mode string
		want bool
	}{
		{"on", true}, {"true", true}, {"1", true}, {"yes", true},
		{"off", false}, {"false", false}, {"0", false}, {"no", false},
		{"auto", true}, {"", true}, {"garbage", true}, {"  AUTO  ", true},
	}
	for _, c := range cases {
		cfg := &Config{LabelImagesReconcile: c.mode}
		if got := cfg.LabelImagesReconcileEnabled(); got != c.want {
			t.Errorf("LabelImagesReconcile=%q: got %v, want %v", c.mode, got, c.want)
		}
	}
}
