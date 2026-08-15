package jobs

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// KindLimiter caps how many jobs of a given kind may run concurrently across the
// whole fleet (all pods, all users). It is the throughput-control layer that sits
// in front of the queue: a kind at its limit is excluded from ClaimNext so its
// backlog stays durably status=queued in Postgres and drains as slots free, while
// every OTHER kind keeps flowing. This is intentionally separate from any
// per-HTTP-request rate limiting — the unit throttled here is the JOB.
//
// A nil KindLimiter is a valid "unlimited" limiter — callers must tolerate it and
// treat every kind as unbounded. NewKindLimiter never returns nil, though.
type KindLimiter interface {
	// Excluded returns the kinds currently AT their configured concurrency limit,
	// to be merged into the ClaimNext exclude list so they are not claimed. Only
	// kinds present in limits with max>0 can be excluded; a missing kind or max<=0
	// means unlimited.
	Excluded(ctx context.Context, limits map[string]int) ([]string, error)
	// Acquire atomically reserves a slot for kind held by holder. ok=false means
	// the kind is already at max (a race lost after the exclude check) and the
	// caller must not run the job — leave it queued for a later slot. When ok is
	// true the returned release func frees the slot; it is safe to call once.
	// max<=0 is unlimited: ok is always true and release is a no-op.
	Acquire(ctx context.Context, kind, holder string, max int) (release func(), ok bool, err error)
	// Refresh re-stamps a live holder's slot so it isn't pruned by the TTL while
	// the job is still running (the slot analogue of the DB heartbeat). It only
	// touches an EXISTING holder — a refresh that races a release must not
	// resurrect the freed slot. Best-effort; a transient error is non-fatal.
	Refresh(ctx context.Context, kind, holder string) error
}

// semTTL is how long a reserved slot survives without a refresh. A running job
// re-stamps its slot every slotRefreshInterval (see the refresh loop in
// runJob), so a LIVE job of any duration keeps its slot; a pod that CRASHES
// mid-job stops refreshing and its slot is pruned within semTTL so the kind
// can't wedge at limit. It must exceed slotRefreshInterval with margin (NOT the
// job runtime — the refresh decouples the TTL from how long a job runs, which
// also fixes the old bug where a job longer than the TTL lost its slot mid-run
// and let a second run start over the cap).
const semTTL = 2 * time.Minute

// slotRefreshInterval is how often a running job re-stamps its semaphore slot.
// Well under semTTL so a live holder is never pruned.
const slotRefreshInterval = 30 * time.Second

// NewKindLimiter returns a Dragonfly/Redis-backed limiter when rdb is non-nil
// (fleet-wide, shared across pods), else an in-process fallback that is only
// correct for a single pod. It never returns nil.
func NewKindLimiter(rdb *redis.Client) KindLimiter {
	if rdb != nil {
		return newRedisLimiter(rdb)
	}
	return newMemLimiter()
}

// ---------------------------------------------------------------------------
// Dragonfly / Redis implementation
// ---------------------------------------------------------------------------

// semKey namespaces a kind's slot set. Members are holders, scores are the
// unix-second acquire time (used to prune crashed holders past the TTL).
func semKey(kind string) string { return "boomtime:jobs:sem:" + kind }

// redisLimiter backs the semaphore on a Dragonfly (Redis-wire) sorted set per
// kind. Acquire is a single atomic Lua eval so the prune + capacity check + add
// can't race across pods.
type redisLimiter struct {
	rdb *redis.Client
	ttl time.Duration
}

func newRedisLimiter(rdb *redis.Client) *redisLimiter {
	return &redisLimiter{rdb: rdb, ttl: semTTL}
}

// acquireScript: prune crashed holders older than the cutoff, then reserve a slot
// only if the live count is below max.
//
//	KEYS[1] = sem key
//	ARGV[1] = now (unix seconds)
//	ARGV[2] = cutoff (now - ttl)
//	ARGV[3] = max
//	ARGV[4] = holder
//
// Returns 1 when a slot was reserved, 0 when the kind is at limit.
var acquireScript = redis.NewScript(`
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', ARGV[2])
if redis.call('ZCARD', KEYS[1]) < tonumber(ARGV[3]) then
    redis.call('ZADD', KEYS[1], ARGV[1], ARGV[4])
    return 1
end
return 0
`)

func (l *redisLimiter) Acquire(ctx context.Context, kind, holder string, max int) (func(), bool, error) {
	if max <= 0 {
		return func() {}, true, nil
	}
	now := time.Now()
	cutoff := now.Add(-l.ttl).Unix()
	res, err := acquireScript.Run(ctx, l.rdb,
		[]string{semKey(kind)},
		now.Unix(), cutoff, max, holder,
	).Int()
	if err != nil {
		return nil, false, err
	}
	if res != 1 {
		return nil, false, nil
	}
	key := semKey(kind)
	release := func() {
		// Best-effort, on its own context so a cancelled job ctx still frees the
		// slot. A crashed pod that never reaches here is reclaimed by the TTL prune.
		l.rdb.ZRem(context.Background(), key, holder)
	}
	return release, true, nil
}

// Refresh re-stamps holder's slot to now via ZADD XX (update-only): it never
// creates a member, so a refresh that races a release (release ZREMs the member
// first) cannot resurrect the freed slot. Live holder → score bumped past the
// prune cutoff; already-released holder → no-op.
func (l *redisLimiter) Refresh(ctx context.Context, kind, holder string) error {
	return l.rdb.ZAddXX(ctx, semKey(kind), redis.Z{
		Score:  float64(time.Now().Unix()),
		Member: holder,
	}).Err()
}

func (l *redisLimiter) Excluded(ctx context.Context, limits map[string]int) ([]string, error) {
	if len(limits) == 0 {
		return nil, nil
	}
	cutoff := time.Now().Add(-l.ttl).Unix()
	cutoffStr := strconv.FormatInt(cutoff, 10)

	type entry struct {
		kind string
		max  int
		card *redis.IntCmd
	}
	var entries []entry

	// Pipeline a prune + ZCARD per rate-limited kind so the capacity read reflects
	// only live holders and one round-trip covers every kind.
	pipe := l.rdb.Pipeline()
	for kind, max := range limits {
		if max <= 0 {
			continue // unlimited: never excluded
		}
		key := semKey(kind)
		pipe.ZRemRangeByScore(ctx, key, "-inf", cutoffStr)
		entries = append(entries, entry{kind: kind, max: max, card: pipe.ZCard(ctx, key)})
	}
	if len(entries) == 0 {
		return nil, nil
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		n, err := e.card.Result()
		if err != nil {
			return nil, err
		}
		if int(n) >= e.max {
			out = append(out, e.kind)
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// In-process fallback
// ---------------------------------------------------------------------------

// memLimiter caps concurrency within a single process. Correct only for a single
// pod — used when there is no Dragonfly/Redis client (local dev, broker=local).
type memLimiter struct {
	mu     sync.Mutex
	counts map[string]int
}

func newMemLimiter() *memLimiter { return &memLimiter{counts: map[string]int{}} }

func (l *memLimiter) Acquire(_ context.Context, kind, _ string, max int) (func(), bool, error) {
	if max <= 0 {
		return func() {}, true, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.counts[kind] >= max {
		return nil, false, nil
	}
	l.counts[kind]++
	var once sync.Once
	release := func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			if l.counts[kind] > 0 {
				l.counts[kind]--
			}
		})
	}
	return release, true, nil
}

// Refresh is a no-op for the in-process limiter: its counts are only correct for
// a single pod (there is no cross-pod TTL prune to keep alive), and release is
// always run explicitly, so there is nothing to re-stamp.
func (l *memLimiter) Refresh(_ context.Context, _, _ string) error { return nil }

func (l *memLimiter) Excluded(_ context.Context, limits map[string]int) ([]string, error) {
	if len(limits) == 0 {
		return nil, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for kind, max := range limits {
		if max <= 0 {
			continue
		}
		if l.counts[kind] >= max {
			out = append(out, kind)
		}
	}
	return out, nil
}
