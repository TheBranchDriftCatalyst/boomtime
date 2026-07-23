package logging

import (
	"testing"
)

func TestLogHubPublishDeliversToSubscribers(t *testing.T) {
	h := NewLogHub(10)
	chA := h.Subscribe()
	chB := h.Subscribe()

	h.Publish(LogEntry{Level: "INFO", Msg: "hello"})

	for i, ch := range []chan LogEntry{chA, chB} {
		select {
		case e := <-ch:
			if e.Msg != "hello" {
				t.Errorf("subscriber %d: Msg = %q, want hello", i, e.Msg)
			}
			if e.ID != 1 {
				t.Errorf("subscriber %d: ID = %d, want 1 (monotonic)", i, e.ID)
			}
		default:
			t.Errorf("subscriber %d: expected an entry, channel empty", i)
		}
	}
}

func TestLogHubAssignsMonotonicIDs(t *testing.T) {
	h := NewLogHub(10)
	for i := 0; i < 5; i++ {
		h.Publish(LogEntry{Msg: "x"})
	}
	all := h.Backfill(0)
	if len(all) != 5 {
		t.Fatalf("Backfill(0) returned %d, want 5", len(all))
	}
	for i, e := range all {
		if e.ID != int64(i+1) {
			t.Errorf("entry %d: ID = %d, want %d", i, e.ID, i+1)
		}
	}
}

func TestLogHubRingBufferEvictsOldest(t *testing.T) {
	h := NewLogHub(3)
	for i := 1; i <= 5; i++ {
		h.Publish(LogEntry{Msg: "x"})
	}
	all := h.Backfill(0)
	if len(all) != 3 {
		t.Fatalf("ring holds %d entries, want 3 (capacity)", len(all))
	}
	// Oldest two (IDs 1,2) evicted; retained tail is IDs 3,4,5 in order.
	if all[0].ID != 3 || all[2].ID != 5 {
		t.Fatalf("retained IDs = [%d..%d], want [3..5]", all[0].ID, all[2].ID)
	}
}

func TestLogHubBackfillAfterID(t *testing.T) {
	h := NewLogHub(10)
	for i := 0; i < 5; i++ {
		h.Publish(LogEntry{Msg: "x"})
	}
	// Resume after ID 3: expect only IDs 4 and 5.
	got := h.Backfill(3)
	if len(got) != 2 || got[0].ID != 4 || got[1].ID != 5 {
		t.Fatalf("Backfill(3) = %+v, want IDs [4,5]", got)
	}
}

func TestLogHubBufferFullDropsWithoutBlocking(t *testing.T) {
	h := NewLogHub(2000)
	ch := h.Subscribe() // cap 256, undrained

	// Publish 300 entries; publishing must never block even though the
	// subscriber buffer (256) fills up — the excess is silently dropped.
	for i := 0; i < 300; i++ {
		h.Publish(LogEntry{Msg: "x"})
	}

	if len(ch) != 256 {
		t.Fatalf("channel holds %d entries, want 256 (rest dropped)", len(ch))
	}
	drained := 0
	for {
		select {
		case <-ch:
			drained++
			continue
		default:
		}
		break
	}
	if drained != 256 {
		t.Fatalf("drained %d entries, want 256", drained)
	}
}

func TestLogHubUnsubscribeClosesAndSilencesPublish(t *testing.T) {
	h := NewLogHub(10)
	ch := h.Subscribe()

	h.Unsubscribe(ch)

	e, ok := <-ch
	if ok {
		t.Fatalf("receive after unsubscribe: ok = true, want false (closed)")
	}
	if e.Msg != "" {
		t.Fatalf("receive after unsubscribe: Msg = %q, want zero value", e.Msg)
	}

	// A subsequent Publish must be a no-op (no send on closed channel).
	h.Publish(LogEntry{Msg: "after"})
}

func TestLogHubNilReceiverIsNoOp(t *testing.T) {
	var h *LogHub
	h.Publish(LogEntry{Msg: "x"}) // must not panic
	if got := h.Backfill(0); got != nil {
		t.Fatalf("nil hub Backfill = %v, want nil", got)
	}
}

// The tests below exercise FilterForUser — the owner-scope predicate that
// fixes gaka-awh.2. They deliberately assert an EXCLUSION as well as pass-
// throughs so the anti-tautology rule holds: swapping the filter body for
// `return records // no filter` would flip the "user-A sees user-B" assertion
// from green to red.

// asMsgs is a small readability helper: pluck Msg strings so an inclusion /
// exclusion set can be compared without dragging IDs into every test.
func asMsgs(entries []LogEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Msg)
	}
	return out
}

// contains reports whether any element of xs equals target — used below to
// assert an entry is ABSENT.
func contains(xs []string, target string) bool {
	for _, x := range xs {
		if x == target {
			return true
		}
	}
	return false
}

func TestFilterForUser_PassesThroughOwnerMatch(t *testing.T) {
	in := []LogEntry{
		{Msg: "wakatime key saved for A", Attrs: map[string]string{OwnerAttrKey: "alice"}},
	}
	out := FilterForUser(in, "alice")
	if len(out) != 1 || out[0].Msg != "wakatime key saved for A" {
		t.Fatalf("owner match: got %+v, want passthrough", out)
	}
}

// The load-bearing anti-tautology test: user-B's record MUST be dropped when
// user-A is the requester. This is what would break gaka-awh.2 wide open if
// the filter regressed.
func TestFilterForUser_DropsCrossOwner(t *testing.T) {
	in := []LogEntry{
		{Msg: "wakatime key cleared for B", Attrs: map[string]string{OwnerAttrKey: "bob"}},
	}
	out := FilterForUser(in, "alice")
	if len(out) != 0 {
		t.Fatalf("cross-owner leak: got %+v, want empty", out)
	}
	if contains(asMsgs(out), "wakatime key cleared for B") {
		t.Fatalf("cross-owner exclusion assertion failed — filter is a no-op")
	}
}

func TestFilterForUser_PassesThroughUnowned(t *testing.T) {
	in := []LogEntry{
		{Msg: "healthz served"},                      // no attrs at all
		{Msg: "migrations up", Attrs: map[string]string{"phase": "27"}}, // attrs w/o owner
	}
	out := FilterForUser(in, "alice")
	if len(out) != 2 {
		t.Fatalf("server-scope drop: got %d entries, want 2 (both unowned)", len(out))
	}
}

func TestFilterForUser_EmptyInputEmptyOutput(t *testing.T) {
	out := FilterForUser([]LogEntry{}, "alice")
	if len(out) != 0 {
		t.Fatalf("empty in: got %d entries, want 0", len(out))
	}
	// Nil in preserves nil out (callers rely on this to decide null vs []).
	if got := FilterForUser(nil, "alice"); got != nil {
		t.Fatalf("nil in: got %v, want nil", got)
	}
}

// FAIL-CLOSED: an unresolved requester ("") sees ONLY unowned records. This
// keeps a future endpoint that forgets to auth-gate from turning into an
// anonymous leak.
func TestFilterForUser_EmptyRequesterDropsAllUserScoped(t *testing.T) {
	in := []LogEntry{
		{Msg: "server started"}, // unowned
		{Msg: "wakatime key saved for A", Attrs: map[string]string{OwnerAttrKey: "alice"}},
		{Msg: "wakatime key saved for B", Attrs: map[string]string{OwnerAttrKey: "bob"}},
	}
	out := FilterForUser(in, "")
	msgs := asMsgs(out)
	if len(out) != 1 || msgs[0] != "server started" {
		t.Fatalf("empty requester fail-closed: got %+v, want only [server started]", msgs)
	}
	if contains(msgs, "wakatime key saved for A") || contains(msgs, "wakatime key saved for B") {
		t.Fatalf("empty requester leaked user-scoped record: %+v", msgs)
	}
}

// Mixed input in one call: the same records, filtered for user A vs user B,
// should produce disjoint user-scoped views + the same server-scope tail. If
// the filter were a no-op, both views would be identical (equal to `in`) and
// this test would fail on len alone.
func TestFilterForUser_MixedAudienceSegregation(t *testing.T) {
	in := []LogEntry{
		{Msg: "for-A-1", Attrs: map[string]string{OwnerAttrKey: "alice"}},
		{Msg: "for-B-1", Attrs: map[string]string{OwnerAttrKey: "bob"}},
		{Msg: "server-1"},
		{Msg: "for-A-2", Attrs: map[string]string{OwnerAttrKey: "alice"}},
		{Msg: "for-B-2", Attrs: map[string]string{OwnerAttrKey: "bob"}},
		{Msg: "server-2"},
	}
	viewA := asMsgs(FilterForUser(in, "alice"))
	viewB := asMsgs(FilterForUser(in, "bob"))
	if len(viewA) != 4 { // for-A-1, server-1, for-A-2, server-2
		t.Fatalf("viewA count = %d, want 4 (%v)", len(viewA), viewA)
	}
	if contains(viewA, "for-B-1") || contains(viewA, "for-B-2") {
		t.Fatalf("viewA saw bob's records: %v", viewA)
	}
	if len(viewB) != 4 { // for-B-1, server-1, for-B-2, server-2
		t.Fatalf("viewB count = %d, want 4 (%v)", len(viewB), viewB)
	}
	if contains(viewB, "for-A-1") || contains(viewB, "for-A-2") {
		t.Fatalf("viewB saw alice's records: %v", viewB)
	}
}
