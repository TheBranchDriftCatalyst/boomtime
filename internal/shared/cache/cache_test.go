// cache_ginkgo_test.go — ginkgo mirror of cache_test.go (boom-0vp).
// 1:1 case map (8 stdlib TestXxx):
//
//	TestGetSetHit                     → TTL cache > "hits and increments Len"
//	TestExpirationLazyEviction        → TTL cache > "expires and drops on next Get"
//	TestInvalidatePrefixOwnerOnly     → TTL cache > "InvalidatePrefix scopes to matching keys"
//	TestInvalidatePrefixEmptyClearsAll→ TTL cache > "empty prefix clears everything"
//	TestZeroTTLDisablesCache          → TTL cache > "TTL=0 disables the cache"
//	TestNilReceiverIsInert            → TTL cache > "nil receiver is inert on every op"
//	TestSweepEvictsExpiredEntries     → TTL cache > "sweep evicts expired"
//	TestCloseIsIdempotent             → TTL cache > "Close is idempotent"
package cache

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("TTL cache", func() {
	It("hits and increments Len", func() {
		c := New(time.Minute)
		defer c.Close()

		c.Set("owner|stats|a", []byte(`{"ok":1}`))
		got, ok := c.Get("owner|stats|a")
		Expect(ok).To(BeTrue())
		Expect(string(got)).To(Equal(`{"ok":1}`))
		Expect(c.Len()).To(Equal(1))
	})

	It("expires and drops on next Get (lazy eviction)", func() {
		c := New(10 * time.Millisecond)
		defer c.Close()

		c.Set("k", []byte("v"))
		_, ok := c.Get("k")
		Expect(ok).To(BeTrue())

		time.Sleep(20 * time.Millisecond)
		_, ok = c.Get("k")
		Expect(ok).To(BeFalse())
		Expect(c.Len()).To(BeZero())
	})

	It("InvalidatePrefix scopes to matching keys", func() {
		c := New(time.Minute)
		defer c.Close()

		c.Set("alice|stats|a", []byte("1"))
		c.Set("alice|leaderboards|a", []byte("2"))
		c.Set("bob|stats|a", []byte("3"))

		c.InvalidatePrefix("alice|")

		_, ok := c.Get("alice|stats|a")
		Expect(ok).To(BeFalse())
		_, ok = c.Get("alice|leaderboards|a")
		Expect(ok).To(BeFalse())
		_, ok = c.Get("bob|stats|a")
		Expect(ok).To(BeTrue())
	})

	It("empty prefix clears everything", func() {
		c := New(time.Minute)
		defer c.Close()

		c.Set("a|x", []byte("1"))
		c.Set("b|y", []byte("2"))
		c.InvalidatePrefix("")
		Expect(c.Len()).To(BeZero())
	})

	It("TTL=0 disables the cache", func() {
		c := New(0)
		defer c.Close()

		c.Set("k", []byte("v"))
		_, ok := c.Get("k")
		Expect(ok).To(BeFalse())
		Expect(c.Len()).To(BeZero())
	})

	It("nil receiver is inert on every op", func() {
		var c *TTL
		c.Set("k", []byte("v"))
		_, ok := c.Get("k")
		Expect(ok).To(BeFalse())

		c.InvalidatePrefix("anything")
		Expect(c.Len()).To(BeZero())
		Expect(func() { c.Close() }).NotTo(Panic())
	})

	It("sweep evicts expired entries", func() {
		c := New(10 * time.Millisecond)
		defer c.Close()
		c.Set("a", []byte("1"))
		c.Set("b", []byte("2"))
		time.Sleep(15 * time.Millisecond)
		c.sweep(time.Now())
		Expect(c.Len()).To(BeZero())
	})

	It("Close is idempotent", func() {
		c := New(time.Second)
		c.Close()
		Expect(func() { c.Close() }).NotTo(Panic())
	})
})
