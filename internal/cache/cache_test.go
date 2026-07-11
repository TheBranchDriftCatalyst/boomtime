package cache

import (
	"testing"
	"time"
)

func TestGetSetHit(t *testing.T) {
	c := New(time.Minute)
	defer c.Close()

	c.Set("owner|stats|a", []byte(`{"ok":1}`))
	got, ok := c.Get("owner|stats|a")
	if !ok || string(got) != `{"ok":1}` {
		t.Fatalf("want hit, got ok=%v val=%q", ok, got)
	}
	if c.Len() != 1 {
		t.Fatalf("Len=%d, want 1", c.Len())
	}
}

func TestExpirationLazyEviction(t *testing.T) {
	c := New(10 * time.Millisecond)
	defer c.Close()

	c.Set("k", []byte("v"))
	if _, ok := c.Get("k"); !ok {
		t.Fatal("fresh key should hit")
	}
	time.Sleep(20 * time.Millisecond)
	if _, ok := c.Get("k"); ok {
		t.Fatal("expired key should miss")
	}
	if c.Len() != 0 {
		t.Fatalf("lazy eviction should drop the entry, Len=%d", c.Len())
	}
}

func TestInvalidatePrefixOwnerOnly(t *testing.T) {
	c := New(time.Minute)
	defer c.Close()

	c.Set("alice|stats|a", []byte("1"))
	c.Set("alice|leaderboards|a", []byte("2"))
	c.Set("bob|stats|a", []byte("3"))

	c.InvalidatePrefix("alice|")
	if _, ok := c.Get("alice|stats|a"); ok {
		t.Fatal("alice's entry should be gone")
	}
	if _, ok := c.Get("alice|leaderboards|a"); ok {
		t.Fatal("alice's other entry should be gone")
	}
	if _, ok := c.Get("bob|stats|a"); !ok {
		t.Fatal("bob's entry should have survived")
	}
}

func TestInvalidatePrefixEmptyClearsAll(t *testing.T) {
	c := New(time.Minute)
	defer c.Close()

	c.Set("a|x", []byte("1"))
	c.Set("b|y", []byte("2"))
	c.InvalidatePrefix("")
	if c.Len() != 0 {
		t.Fatalf("empty prefix should clear everything, Len=%d", c.Len())
	}
}

func TestZeroTTLDisablesCache(t *testing.T) {
	c := New(0)
	defer c.Close()

	c.Set("k", []byte("v"))
	if _, ok := c.Get("k"); ok {
		t.Fatal("zero TTL should never hit")
	}
	if c.Len() != 0 {
		t.Fatalf("zero TTL cache should store nothing, Len=%d", c.Len())
	}
}

func TestNilReceiverIsInert(t *testing.T) {
	var c *TTL
	c.Set("k", []byte("v"))
	if _, ok := c.Get("k"); ok {
		t.Fatal("nil cache should not hit")
	}
	c.InvalidatePrefix("anything")
	if c.Len() != 0 {
		t.Fatal("nil cache Len should be 0")
	}
	c.Close() // must not panic
}

func TestSweepEvictsExpiredEntries(t *testing.T) {
	// Sweep loop floors at 30s; call sweep directly so the test isn't slow.
	c := New(10 * time.Millisecond)
	defer c.Close()
	c.Set("a", []byte("1"))
	c.Set("b", []byte("2"))
	time.Sleep(15 * time.Millisecond)
	c.sweep(time.Now())
	if c.Len() != 0 {
		t.Fatalf("sweep should evict expired entries, Len=%d", c.Len())
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	c := New(time.Second)
	c.Close()
	c.Close() // must not double-close panic
}
