# catalyst-go-jobs

A small, generic, **domain-free** background-job subsystem: a job queue + a
periodic scheduler with a swappable **provider** (transport) boundary. It lives
in `internal/jobs` today but imports nothing boomtime-specific (only stdlib +
`pgx` + `amqp091`), so it's ready to lift into a standalone module.

## Concepts

| Type | Role |
|------|------|
| `Job` | one unit of work: `Kind`, `Owner`, `Payload` (jsonb), status, attempts, `RunAt` |
| `Handler` / `Registry` | `Kind → Handler`; a handler runs a job (nil = done, error = retry/fail) |
| `Store` | Postgres record-of-truth (`jobs` + `job_schedules`); the admin UI + both providers read it |
| `Provider` | the transport: `Enqueuer` + `Runner` + `Name()` + `SetNotifier()` |
| `Scheduler` | leader-singleton periodic enqueue (atomic `ClaimDueSchedules`) |
| `Notifier` / `JobEvent` | terminal (done/failed) push events, for user toasts |

## Providers

- **`LocalProvider`** — Postgres *is* the broker (`SELECT … FOR UPDATE SKIP
  LOCKED`). No extra infra, safe with many replicas, retries "just work" (a
  retry is a re-queued row with a future `run_at`). **The default.**
- **`AMQPProvider`** — celery-style over RabbitMQ; records every job in the
  `jobs` table too, so admin visibility is provider-agnostic (the transport
  only decides *how a worker is woken*).

Pick one; the `Scheduler`, admin API, and notifications work identically either
way.

## Usage

```go
store := jobs.NewStore(pool)
reg := jobs.NewRegistry()
reg.Register("hello", jobs.HandlerFunc(func(ctx context.Context, j jobs.Job) error {
    log.Println("running", j.Kind, "for", j.Owner)
    return nil
}))

provider := jobs.NewLocalProvider(store, logger, hostID) // or NewAMQPProvider(...)
provider.SetNotifier(hub)                                 // optional push

// producer side (any process):
provider.Enqueue(ctx, "hello", payload, jobs.Owner("alice"), jobs.MaxAttempts(3), jobs.Delay(time.Minute))

// worker side:
go provider.Run(ctx, reg)

// periodic:
sched := jobs.NewScheduler(store, provider, logger)
sched.Register(ctx, "hello", 8*time.Hour)
go sched.Run(ctx)
```

## Schema

Migrations `00054_jobs.sql` (tables + claim/kind indexes) and
`00055_jobs_owner.sql` (owner column). To extract as a module, ship these as the
package's own migration set.

## Roadmap

- **Fold `internal/queue/imagejobs`** in as a `label-image` kind (gaka-hney.3),
  behind `BOOM_JOBS_UNIFIED`, parity-tested before deleting the bespoke
  producer/consumer.
- **Cross-pod push relay** (Redis/Dragonfly) for the split worker topology, so
  `Notifier` events reach the server pod's WS (mirrors the worker-log relay).
- **Lift to a standalone Go module** (`catalyst-go-jobs`) with its own README +
  example once a second service needs it.

See boomtime's use: `cmd/boomtime/main.go` (wiring), `internal/admin/jobs.go`
(admin API), `internal/jobsevents` (push hub), `internal/identity/jobs_ws.go`
(user WS).
