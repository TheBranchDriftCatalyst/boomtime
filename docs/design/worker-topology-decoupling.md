# Worker Topology Decoupling — Implementation Blueprint

**Status:** Implementation-ready blueprint. Buildable manifests + app-code change
spec inline. Staged, default-off; `main` and prod stay deployable at every stage.
**Scope of stage 1 (this blueprint):** move boomtime's **image-generation** jobs
(`internal/queue/imagejobs` + `internal/worker/labelimages`) off the in-process
pool onto the homelab's **RabbitMQ + Dragonfly + KEDA** topology, as a **Go worker
Deployment** consuming AMQP. The Wakatime/GitHub **import** worker stays in-process
for now — it is Phase 2 (Postgres-native), sketched in §9.

**Why image jobs first:** they are the bursty, external-compute-bound jobs (each
regen is a 5–10 min ComfyUI/Ollama call that ignores `ctx`), they are already
isolated behind an `Executor` interface, and they are explicitly **non-durable**
today (`gaka-8bz`) so we lose nothing by re-homing them. The import worker, by
contrast, is durable Postgres-native day-by-day work whose natural queue is the DB.

---

## 1. Recommendation (read this first)

Keep the image pipeline in **Go** — do **not** rewrite it as Celery-the-framework.
boomtime's image worker is Go orchestrating external image services, so the fit is a
boomtime **Go worker Deployment** that consumes a RabbitMQ queue (reusing
`labelimages.Worker.RegenerateEntry` verbatim as the job handler, swapping only the
queue transport), is **KEDA-scaled on queue depth** (with scale-to-zero), and uses
**Dragonfly** for cross-pod progress fan-out + a per-label dedup lock. Everything is
gated behind a `--role=server|worker|all` flag (default `all` = today's behavior,
zero change) and a `BOOM_QUEUE_BROKER=inprocess|rabbitmq` config switch (default
`inprocess`), so the in-process path stays the default until an explicit cutover and
every stage is independently reversible.

### Hard acceptance criterion (the definition of done for stage 1)

> **`tilt up` on a laptop, then SEE one image-generation job flow end-to-end,
> cross-pod, with NO GPU/ComfyUI:** enqueue a label-image regen → watch the
> RabbitMQ mgmt UI queue `boomtime.image-jobs` depth go **0 → N → 0** → watch the
> **separate** `boomtime-worker` pod's logs consume it and call a **mock image
> backend** → watch the job reach `done` (the new `label_images` DB row + the
> `queued→running→done` progress events arriving in the browser Admin tab via the
> cross-pod Dragonfly/Redis relay). Full manifests, Tiltfile diff, mock backend,
> and step-by-step recipe are in **§9**.

**Staged plan (each independently deployable + reversible; verify local before prod):**

| Stage | What ships | Default-off gate | Reversible by |
|---|---|---|---|
| **a** | `--role` flag (`server`/`worker`/`all`), default `all` | flag defaults to today's wiring | drop the flag |
| **b** | AMQP producer + Go AMQP consumer behind `BOOM_QUEUE_BROKER` | `BOOM_QUEUE_BROKER=inprocess` (default) | flip config back to `inprocess` |
| **c** | Cross-pod progress (Dragonfly pub/sub → local registry mirror → existing WS) | only active when broker=`rabbitmq` | inert under `inprocess` |
| **d** | Provision RabbitmqCluster + Queue + Dragonfly + `boomtime-worker` Deployment — **local first, verify, then prod** | new workloads; server Deployment untouched | scale worker to 0, flip config to `inprocess` |
| **e** | KEDA `ScaledObject` (rabbitmq queue-length) + scale-to-zero + PDB | ScaledObject can be paused (`paused-replicas`) | delete/pause ScaledObject |

---

## 2. Current state (image-generation path, precise)

Flow today, all **in one process** (`boomtime run`, `--role` doesn't exist yet):

```
FE Admin tab ──POST /api/v1/admin/label-images/regenerate──▶ Handler.AdminLabelImagesRegenerate
                                                               │  h.ImageJobQueue.Enqueue(EnqueueInput{...})
                                                               ▼
                        imagejobs.Registry (in-memory)  ──jobID on r.jobsCh──▶ imagejobs.Pool.worker
                          │ byLabel dedup, retention timers        │ Executor.Execute(ctx, Job)
                          │ broadcastLocked(Event)                 ▼
                          │                          labelimages.Worker.RegenerateEntry(ctx, labelcatalog.Entry)
                          │                                        │ ComfyUI shim call (5–10 min, ignores ctx)
                          ▼                                        ▼
   Registry.Subscribe()/Snapshot() ◀── MarkRunning / MarkDone / MarkError
                          │
   GET /admin/label-images/ws (Handler.AdminLabelImagesWS) ──wsjson──▶ browser
```

Concrete wiring (`cmd/boomtime/main.go` `runCmd`, L241–264):

```go
registry := imagejobs.NewRegistry(logger)
exec := imagejobs.ExecutorFunc(func(execCtx context.Context, j imagejobs.Job) error {
    return liWorker.RegenerateEntry(execCtx, labelcatalog.Entry{ID: j.LabelID, Description: j.Description,
        Prompt: j.Prompt, Model: j.Model, Size: j.Size, Seed: j.Seed})
})
imgPool = imagejobs.NewPool(imagejobs.PoolConfig{Concurrency: concurrency, Registry: registry, Executor: exec, Logger: logger})
imgPool.Start(ctx)
h.SetImageJobQueue(registry)     // Handler.ImageJobQueue *imagejobs.Registry
```

Key facts that shape the design (all verified in code):

- **Enqueue → execute is welded in-process.** `AdminLabelImagesRegenerate` calls `h.ImageJobQueue.Enqueue(imagejobs.EnqueueInput{...})` (`internal/admin/admin_label_images.go` L211); `Registry.Enqueue` pushes the jobID onto `r.jobsCh` and the same-process `Pool.worker` claims it (`internal/queue/imagejobs/registry.go` L229, `pool.go` L124). A separate pod has nothing to consume.
- **Progress is in-memory.** `Registry.broadcastLocked` fans `Event`s to `Subscribe()` channels (`registry.go` L357); `AdminLabelImagesWS` (`admin_label_images.go` L248) subscribes + snapshots and streams to the browser. Split pods ⇒ the worker's `MarkDone` and the API pod's WS `Subscribe` are different in-memory registries ⇒ **live progress dies**.
- **Idempotent per label already.** `Registry.Enqueue` dedups an in-flight label via `byLabel` (L181); `labelimages.Worker.RegenerateEntry` **deletes the row then writes fresh** (last-write-wins). So re-running a job is safe — exactly what at-least-once delivery needs.
- **Non-durable by design.** Package docs (`registry.go` L15–18, `pool.go` L19–22): "NOT DB-backed — the user explicitly does not want persistence across boomtime restarts. If boomtime restarts mid-run, in-flight state is lost." Re-homing to a broker is a strict upgrade in durability.
- **The Celery TODO** lives at `internal/worker/labelimages/worker.go` L20: *"probably update this with a cellery task … we have celery CRD operator for our clsuiter."* This blueprint is the answer: **Go worker on the Celery broker, not a Celery task.**
- **Executor is already an interface** (`imagejobs.Executor` / `ExecutorFunc`, `pool.go` L37–48). The AMQP consumer reuses the exact same `ExecutorFunc` closure — the ComfyUI orchestration logic is untouched.

**Pinning / failure modes today:** import + ComfyUI regens share the server's pod +
`500m/512Mi` limits, so a "Regen all" batch competes with request handling; an OOM
in a regen goroutine takes down the HTTP server (`replicas:1`, `strategy:Recreate`);
no independent scaling; SIGTERM drain times out because ComfyUI ignores `ctx`
(`imgPool.Stop(30s)` in `main.go` L280) so in-flight regens are simply dropped.

---

## 3. Homelab infra to reuse (operators already installed cluster-wide)

Confirmed present in `../talos-homelab` (Crossplane-provisioned, operators live even
though the `crossplane-demo` CRs are **paused at `replicas: 0`**). boomtime provisions
its **own** CRs in the `boomtime` namespace, mirroring the demo manifests.

| Capability | Operator / CRD | What it creates |
|---|---|---|
| Broker | **RabbitMQ Cluster Operator** — `RabbitmqCluster` (`rabbitmq.com/v1beta1`) | Service `<name>` + Secret `<name>-default-user` (keys: `username`, `password`, `host`, `port`) |
| Declarative queue | **Messaging Topology Operator** — `Queue` (`rabbitmq.com/v1beta1`) | a durable queue bound via `rabbitmqClusterReference.name` |
| Redis-compatible cache/pubsub | **Dragonfly operator** — `Dragonfly` (`dragonflydb.io/v1alpha1`) | Service on `:6379` (size via `--proactor_threads` / `--maxmemory`) |
| Autoscaler | **KEDA** — `ScaledObject` + `TriggerAuthentication` | cron + `rabbitmq` queue-length triggers, scale-to-zero |

**Reference manifests mirrored:** `talos-homelab/applications/crossplane-demo/`
— `rabbitmq.yaml` (RabbitmqCluster + Queue), `dragonfly.yaml`, `celery/scaledobject.yaml`
(the real `- type: rabbitmq {protocol: amqp, queueName, mode: QueueLength, value}` +
`authenticationRef` pattern, sitting commented-out because it needs a
TriggerAuthentication), `flex/deployment.yaml` (Go producer assembling `RABBITMQ_URL`
from the `-default-user` Secret via `$(VAR)` interpolation), `flex/checks_messaging.go`
(the exact Go AMQP publish/consume + Dragonfly SET/GET code we reuse).

**Go libraries** (already used by `flex`, so proven in this cluster):
`github.com/rabbitmq/amqp091-go` (AMQP) and `github.com/redis/go-redis/v9` (Dragonfly).

---

## 4. Options: Go-worker-on-RabbitMQ vs Celery Python tier

| | **Go worker on RabbitMQ (recommended)** | Celery Python tier |
|---|---|---|
| Job handler | Reuse `labelimages.Worker.RegenerateEntry` **as-is** | Rewrite the ComfyUI orchestration in Python |
| Language/runtime | One (Go), one image | Two (Go API + Python workers), two images, two pipelines |
| Broker | RabbitMQ (reuse operator) | RabbitMQ (reuse operator) |
| Effort | Low–Med (swap transport, add flag+config) | High (reimplement + retest a working pipeline) |
| Fit | ✅ boomtime's worker is Go orchestrating external services | ✗ only justified if the work were genuinely Python |

**Commit to the Go path.** Celery executes Python tasks; boomtime's image worker is
Go. "Workers on Celery" here sensibly means *reuse Celery's broker (RabbitMQ)* with a
Go consumer — which is exactly this design — **not** porting `RegenerateEntry` to
Python. A real Celery/Python tier is only warranted by future genuinely-Python work
(e.g. an in-cluster ML/inference step); see §10 open questions.

---

## 5. Target architecture (decoupled image-job flow)

```
                         ┌───────────────────────────── API pod(s)  (--role=server, replicas: N) ─────────────┐
 FE Admin tab ──POST────▶│ AdminLabelImagesRegenerate                                                          │
                         │   broker.Enqueue(EnqueueInput)  ─┐                                                  │
                         │                                  │ amqp publish                                     │
 FE Admin WS ◀───────────│ AdminLabelImagesWS ◀─ local imagejobs.Registry (MIRROR, no Pool) ◀─ Dragonfly SUB  │
                         └──────────────────────────────────┼───────────────────────────────────▲─────────────┘
                                                            │ RabbitMQ queue                      │ redis PUBLISH(events)
                                                            ▼ boomtime.image-jobs                 │
                         ┌───────────────────── boomtime-worker Deployment (--role=worker, KEDA-scaled 0..N) ──┐
                         │ amqp consume (prefetch=concurrency)                                                 │
                         │   publish "running" event ─▶ Dragonfly ───────────────────────────────────────────▶│
                         │   labelimages.Worker.RegenerateEntry(ctx, Entry)   (ComfyUI shim call, unchanged)   │
                         │   publish "done"/"error" event ─▶ Dragonfly ─▶  … then basic.ack (at-least-once)    │
                         └──────────────────────────────────────────────────────────────────────────────────┘
                RabbitmqCluster `boomtime-rabbit`   Dragonfly `boomtime-cache`   (both in ns boomtime, operator-provisioned)
```

- **Enqueue** becomes an AMQP publish (API side). Cross-pod **per-label dedup** moves from the in-memory `byLabel` map to a Dragonfly `SET NX` lock.
- **Execute** runs in the worker pod, reusing `RegenerateEntry` behind the same `ExecutorFunc`.
- **Progress** flows worker → Dragonfly pub/sub → every API pod's **local mirror `Registry`** → the **unchanged** `AdminLabelImagesWS`. Because every API pod subscribes, any pod can serve any job's live tail and browser reconnect-to-a-different-pod just works.

**Why Dragonfly pub/sub for progress (not Postgres LISTEN/NOTIFY here):** we're
already provisioning Dragonfly as the broker's natural result/marker backend (mirrors
the demo), image-job events are ephemeral UI feedback (the registry is explicitly
non-durable), and it keeps the image path's infra self-contained. LISTEN/NOTIFY is the
right choice for the **Phase 2 import** worker, which is already Postgres-native (§9).

---

## 6. App-code change spec (real symbols)

All additions are **default-off**. Package `internal/queue/imagejobs` gains a small
broker abstraction; `labelimages` is untouched; `cmd/boomtime` gains the flag + wiring.

### 6.1 `--role` flag + config gate

`internal/config/config.go` — add to `Config` (near `FeatureLabelImages`, L149):

```go
// Role selects which loops this process runs: "server" (HTTP + progress relay,
// no image Pool/consumer), "worker" (AMQP consumer + Executor, no HTTP), or
// "all" (today's single-process behavior). Default "all". BOOM_ROLE or --role.
Role string
// QueueBroker selects the image-job transport: "inprocess" (today's in-memory
// Registry+Pool) or "rabbitmq" (AMQP producer/consumer + Dragonfly progress).
// Default "inprocess" so nothing changes until cutover.
QueueBroker string
// RabbitMQ + Dragonfly wiring (only read when QueueBroker=="rabbitmq").
RabbitURL       string // assembled amqp:// URL (see overlay $(VAR) interpolation)
RabbitQueue     string // default "boomtime.image-jobs"
RedisAddr       string // Dragonfly host:port, e.g. boomtime-cache:6379
RedisPassword   string // usually empty in-cluster
```

In `Load()` (L294+): `Role: getEnv("BOOM_ROLE", "all")`,
`QueueBroker: getEnv("BOOM_QUEUE_BROKER", "inprocess")`,
`RabbitURL: getEnv("BOOM_RABBITMQ_URL", "")`,
`RabbitQueue: getEnv("BOOM_RABBITMQ_QUEUE", "boomtime.image-jobs")`,
`RedisAddr: getEnv("BOOM_REDIS_ADDR", "")`,
`RedisPassword: getEnv("BOOM_REDIS_PASSWORD", "")`.
Add helpers: `func (c *Config) IsServerRole() bool { return c.Role == "server" || c.Role == "all" }`,
`func (c *Config) IsWorkerRole() bool { return c.Role == "worker" || c.Role == "all" }`,
`func (c *Config) BrokerRabbit() bool { return strings.EqualFold(c.QueueBroker, "rabbitmq") }`.

`cmd/boomtime/main.go` `runCmd()` — add a persistent `--role` flag that overrides
`BOOM_ROLE` when set. **Default path (`role=all`, `broker=inprocess`) is byte-identical
to today.**

### 6.2 Broker abstraction (new, in `internal/queue/imagejobs`)

Introduce two tiny interfaces so the handler and `main` don't branch on broker type:

```go
// Enqueuer accepts a regen request. Satisfied by *Registry (inprocess) and by
// *AMQPProducer (rabbitmq). Handler.ImageJobQueue becomes an Enqueuer.
type Enqueuer interface {
    Enqueue(in EnqueueInput) (*Job, bool)
}
// EventSource is what the WS subscribes to. *Registry already satisfies it.
type EventSource interface {
    Subscribe() (<-chan Event, func())
    Snapshot() []Job
}
```

`*Registry` already implements both (methods exist verbatim). `Handler.ImageJobQueue`
changes type `*imagejobs.Registry → imagejobs.Enqueuer`, and a new
`Handler.ImageJobEvents imagejobs.EventSource` backs the WS (in `all`/`inprocess`
both point at the same `*Registry`; the WS handler `AdminLabelImagesWS` swaps
`h.ImageJobQueue.Subscribe()/Snapshot()` → `h.ImageJobEvents.…` — a mechanical rename,
no behavior change).

### 6.3 AMQP producer (API side) — `internal/queue/imagejobs/amqp_producer.go` (new)

```go
type AMQPProducer struct {
    conn  *amqp.Connection      // github.com/rabbitmq/amqp091-go
    ch    *amqp.Channel
    queue string
    rdb   *redis.Client         // Dragonfly, for dedup lock + "queued" event
    bus   *RedisEventBus
    log   *slog.Logger
}
// jobMessage is the AMQP body (JSON). Mirrors imagejobs.Job's execute fields.
type jobMessage struct {
    JobID, LabelID, Description, Prompt, Model, Size string
    Seed *int64
}
func (p *AMQPProducer) Enqueue(in EnqueueInput) (*Job, bool) {
    // Cross-pod dedup replacing Registry.byLabel: SET NX imagejobs:label:<id> with
    // a short TTL. If the key already exists -> return existing=true, no publish.
    // Otherwise: mint JobID=uuid, publish jobMessage to p.queue (DeliveryMode
    // Persistent), publish an EventAdded (Status=queued) to p.bus so every API
    // pod's mirror + WS shows it immediately, and return (&Job{...,Queued}, false).
}
```

- Dedup TTL ≈ a generous upper bound on a regen (e.g. 15 min), cleared by the consumer on terminal (DEL the lock in the done/error event). This reproduces the old byLabel semantics across pods.
- Publish uses `ch.PublishWithContext(ctx, "", p.queue, false, false, amqp.Publishing{ContentType:"application/json", DeliveryMode: amqp.Persistent, Body: json})` — the exact call shape from `flex/checks_messaging.go`.

### 6.4 AMQP consumer (worker side) — `internal/queue/imagejobs/amqp_consumer.go` (new)

```go
type AMQPConsumer struct {
    conn *amqp.Connection
    ch   *amqp.Channel
    queue string
    exec Executor          // the SAME ExecutorFunc wrapping RegenerateEntry
    bus  *RedisEventBus
    rdb  *redis.Client
    log  *slog.Logger
    concurrency int
}
func (c *AMQPConsumer) Run(ctx context.Context) error {
    c.ch.Qos(c.concurrency, 0, false)                 // prefetch == pool concurrency
    deliveries, _ := c.ch.Consume(c.queue, "", false, /*autoAck=*/false, false, false, nil)
    // fan out to `concurrency` worker goroutines; each:
    //   1. decode jobMessage
    //   2. bus.Publish(EventUpdated{Status:running})
    //   3. err := c.exec.Execute(ctx, Job{...})       // labelimages.RegenerateEntry
    //   4. bus.Publish(EventUpdated{Status: done|error})  + rdb.Del(dedup lock)
    //   5. d.Ack(false)   // manual ack AFTER terminal -> at-least-once
    // On ctx cancel (SIGTERM): stop pulling new deliveries, let in-flight finish
    // or hit terminationGracePeriodSeconds; unacked msgs redeliver -> idempotent.
}
```

**At-least-once + idempotency:** `RegenerateEntry` deletes-then-writes the
`label_images` row (already idempotent, last-write-wins), so a redelivered message
after a worker crash re-generates the same label safely. Manual `Ack` only after the
terminal event guarantees no silent loss. **Graceful drain:** cancel `Consume`, wait
on the worker `WaitGroup` up to the pod's grace period; ComfyUI calls that ignore
`ctx` will be preempted by kill and the unacked message will redeliver — strictly
better than today's silent drop.

### 6.5 Cross-pod progress bus — `internal/queue/imagejobs/redis_bus.go` (new)

```go
type RedisEventBus struct { rdb *redis.Client; channel string } // "boomtime:imagejobs:events"
func (b *RedisEventBus) Publish(ev Event) error   // rdb.Publish(ctx, channel, json(ev))
func (b *RedisEventBus) Subscribe(ctx context.Context) (<-chan Event, func())  // rdb.Subscribe → decode → chan
```

**API-pod mirror:** add `func (r *Registry) Apply(ev Event)` that applies an
externally-sourced event to the local maps **without** pushing to `r.jobsCh` (so no
Pool is needed on the server). In `--role=server` + `broker=rabbitmq`, `main` builds a
mirror `*Registry`, starts one goroutine: `for ev := range bus.Subscribe(ctx) { mirror.Apply(ev) }`,
and sets `h.ImageJobEvents = mirror`. `AdminLabelImagesWS` is otherwise unchanged.

### 6.6 `cmd/boomtime/main.go` wiring (the only branch point)

Replace the unconditional pool wiring (L241–264) with role/broker-aware wiring:

```go
switch {
case cfg.BrokerRabbit():
    bus := imagejobs.NewRedisEventBus(rdb) // rdb from cfg.RedisAddr
    if cfg.IsServerRole() {
        producer := imagejobs.NewAMQPProducer(amqpConn, cfg.RabbitQueue, rdb, bus, logger)
        mirror   := imagejobs.NewRegistry(logger)          // no Pool
        go imagejobs.PumpBusIntoRegistry(ctx, bus, mirror) // bus.Subscribe -> mirror.Apply
        h.SetImageJobQueue(producer)                       // Enqueuer
        h.SetImageJobEvents(mirror)                        // EventSource
    }
    if cfg.IsWorkerRole() {
        exec := imagejobs.ExecutorFunc(func(ec context.Context, j imagejobs.Job) error {
            return liWorker.RegenerateEntry(ec, labelcatalog.Entry{ID: j.LabelID, /*…*/})
        })
        consumer := imagejobs.NewAMQPConsumer(amqpConn, cfg.RabbitQueue, exec, bus, rdb, concurrency, logger)
        go consumer.Run(ctx)
    }
default: // "inprocess" — TODAY'S CODE, unchanged. server role only.
    registry := imagejobs.NewRegistry(logger)
    imgPool  := imagejobs.NewPool(/* … as today … */); imgPool.Start(ctx)
    h.SetImageJobQueue(registry); h.SetImageJobEvents(registry)
}
```

The label-images **startup reconcile** `go liWorker.Run(ctx)` (main.go L225) moves to
`--role=worker` (or `all`) only — the server role shouldn't do generation. The
`boomtime label-images regenerate` CLI (`cmd/boomtime/label_images.go`) is unchanged;
it still calls `RegenerateOne/RegenerateAll` directly (a one-shot admin escape hatch,
independent of the queue).

**New deps:** `github.com/rabbitmq/amqp091-go`, `github.com/redis/go-redis/v9`
(both already in the homelab `flex` go.mod, add via `go get`).

---

## 7. Manifests — `k8s/base/`

### 7.1 `k8s/base/worker-deployment.yaml` (new)

Same image, `--role=worker`. Reuses the base config/secret wiring. The rabbit
default-user secret and broker envs are `optional`/absent under `inprocess`, so this
Deployment is inert unless an overlay sets `BOOM_QUEUE_BROKER=rabbitmq` — but it is
**not** added to `base/kustomization.yaml`; each overlay opts in (§8/§9). Server
Deployment (`deployment.yaml`) is **not modified** here — the worker is a NEW workload.

```yaml
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: boomtime-worker
  labels: { app: boomtime-worker, app.kubernetes.io/name: boomtime-worker, app.kubernetes.io/part-of: catalyst }
spec:
  replicas: 1                         # KEDA drives this in prod (§8.4)
  strategy: { type: RollingUpdate }   # stateless consumer; no Recreate needed
  selector: { matchLabels: { app: boomtime-worker } }
  template:
    metadata: { labels: { app: boomtime-worker, app.kubernetes.io/name: boomtime-worker } }
    spec:
      terminationGracePeriodSeconds: 120   # let an in-flight ComfyUI regen drain
      containers:
        - name: boomtime-worker
          image: ghcr.io/thebranchdriftcatalyst/boomtime:latest
          imagePullPolicy: IfNotPresent
          args: ["run", "--role=worker"]
          envFrom:
            - configMapRef: { name: boomtime-config }
            - secretRef: { name: boomtime-secrets, optional: true }
            - secretRef: { name: boomtime-encryption-key, optional: true }
          env:
            # DB creds (CNPG) — same as the server Deployment.
            - { name: BOOM_DB_USER, valueFrom: { secretKeyRef: { name: boomtime-postgres-app, key: username } } }
            - { name: BOOM_DB_PASS, valueFrom: { secretKeyRef: { name: boomtime-postgres-app, key: password } } }
            # RabbitMQ creds from the operator's default-user Secret; URL assembled
            # via $(VAR) interpolation (mirrors flex/deployment.yaml). optional:true
            # so base/local-without-rabbit still schedules the pod (it just idles).
            - { name: RABBIT_USER, valueFrom: { secretKeyRef: { name: boomtime-rabbit-default-user, key: username, optional: true } } }
            - { name: RABBIT_PASS, valueFrom: { secretKeyRef: { name: boomtime-rabbit-default-user, key: password, optional: true } } }
            - { name: BOOM_RABBITMQ_URL, value: "amqp://$(RABBIT_USER):$(RABBIT_PASS)@boomtime-rabbit.boomtime.svc.cluster.local:5672/" }
          ports:
            - { name: health, containerPort: 8081 }   # minimal /healthz for probes
          resources:
            requests: { cpu: 250m, memory: 512Mi }    # sized for heavy regen, INDEPENDENT of API
            limits:   { cpu: "2",  memory: 2Gi }
          livenessProbe:  { httpGet: { path: /healthz, port: health }, initialDelaySeconds: 10, periodSeconds: 30 }
          readinessProbe: { httpGet: { path: /healthz, port: health }, initialDelaySeconds: 5,  periodSeconds: 10 }
```

> App note: `--role=worker` must bind a tiny HTTP listener on `:8081` serving
> `/healthz` (liveness/readiness) even though it serves no API. Add a minimal
> `http.ServeMux` in the worker branch of `main.go`.

`k8s/base/kustomization.yaml` is **unchanged** (worker-deployment.yaml intentionally
NOT listed — overlays reference it directly).

---

## 8. Manifests — `k8s/overlays/talos00-knowledgedump/` (prod, additive)

Add to that overlay's `kustomization.yaml` `resources:` — `rabbitmq.yaml`,
`dragonfly.yaml`, `../../base/worker-deployment.yaml`, `keda-scaledobject.yaml`,
`keda-triggerauth.yaml` — and to `patchesStrategicMerge:` — `queue-config.yaml` +
`patch-server-rabbit.yaml`. Plus a `secretGenerator` for the KEDA host secret (§8.5).
**The existing server Deployment's behavior is untouched** (the only server patch is
additive env, inert until `BOOM_QUEUE_BROKER=rabbitmq`).

### 8.1 `rabbitmq.yaml` (RabbitmqCluster + Queue)

```yaml
---
apiVersion: rabbitmq.com/v1beta1
kind: RabbitmqCluster
metadata:
  name: boomtime-rabbit
  namespace: boomtime
spec:
  replicas: 1
  resources:
    requests: { cpu: 100m, memory: 256Mi }
    limits:   { memory: 512Mi }
  persistence:
    storageClassName: local-path   # broker mnesia dir; NFS is a poor fit (see open Q3)
    storage: 1Gi
---
apiVersion: rabbitmq.com/v1beta1
kind: Queue
metadata:
  name: boomtime-image-jobs
  namespace: boomtime
spec:
  name: boomtime.image-jobs
  durable: true
  autoDelete: false
  rabbitmqClusterReference:
    name: boomtime-rabbit
```

Operator creates Service `boomtime-rabbit` + Secret `boomtime-rabbit-default-user`
(keys `username`,`password`,`host`,`port`).

### 8.2 `dragonfly.yaml` (Dragonfly)

```yaml
---
apiVersion: dragonflydb.io/v1alpha1
kind: Dragonfly
metadata:
  name: boomtime-cache
  namespace: boomtime
spec:
  replicas: 1
  args:
    - "--proactor_threads=1"   # 256Mi/thread floor; fits the 512Mi limit
    - "--maxmemory=256mb"
  resources:
    requests: { cpu: 50m, memory: 128Mi }
    limits:   { memory: 512Mi }
```

Operator creates Service `boomtime-cache` on `:6379`.

### 8.3 `queue-config.yaml` (strategic-merge patch onto `boomtime-config`)

```yaml
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: boomtime-config
  labels: { app.kubernetes.io/name: boomtime }
data:
  BOOM_QUEUE_BROKER: "rabbitmq"
  BOOM_RABBITMQ_QUEUE: "boomtime.image-jobs"
  BOOM_REDIS_ADDR: "boomtime-cache.boomtime.svc.cluster.local:6379"
  # BOOM_RABBITMQ_URL is assembled per-Deployment from the operator Secret
  # (see worker-deployment.yaml + patch-server-rabbit.yaml env $(VAR) interpolation).
```

### 8.4 `patch-server-rabbit.yaml` (server must also PUBLISH enqueues)

The server role publishes to the queue, so it needs the same amqp URL. This is the
**only** change to the server Deployment — purely additive env, inert until
`BOOM_QUEUE_BROKER=rabbitmq` (set in §8.3).

```yaml
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: boomtime
spec:
  template:
    spec:
      containers:
        - name: boomtime
          args: ["run", "--role=server"]   # flip server off the in-process pool
          env:
            - { name: RABBIT_USER, valueFrom: { secretKeyRef: { name: boomtime-rabbit-default-user, key: username } } }
            - { name: RABBIT_PASS, valueFrom: { secretKeyRef: { name: boomtime-rabbit-default-user, key: password } } }
            - { name: BOOM_RABBITMQ_URL, value: "amqp://$(RABBIT_USER):$(RABBIT_PASS)@boomtime-rabbit.boomtime.svc.cluster.local:5672/" }
```

### 8.5 `keda-scaledobject.yaml` (real rabbitmq queue-length trigger, scale-to-zero)

```yaml
---
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: boomtime-worker
  namespace: boomtime
spec:
  scaleTargetRef:
    name: boomtime-worker
  minReplicaCount: 0          # scale-to-zero when the queue is empty
  maxReplicaCount: 4
  cooldownPeriod: 120         # keep a worker warm 2 min after the queue drains
  pollingInterval: 20
  triggers:
    - type: rabbitmq
      metadata:
        protocol: amqp
        queueName: boomtime.image-jobs
        mode: QueueLength
        value: "3"            # ~3 queued regens per replica
      authenticationRef:
        name: boomtime-rabbit-auth
```

### 8.6 `keda-triggerauth.yaml` + host secret (KEDA ≥2.16 split-cred pattern)

KEDA's amqp scaler accepts a **creds-less** `host` URI plus **separate**
`username`/`password` params — which maps cleanly onto the operator's default-user
Secret with **no runtime URL assembly**. The creds-less host is not sensitive; provide
it via a `secretGenerator` in the overlay kustomization:

```yaml
# in kustomization.yaml:
secretGenerator:
  - name: boomtime-rabbit-keda-host
    literals:
      - host=amqp://boomtime-rabbit.boomtime.svc.cluster.local:5672/
generatorOptions:
  disableNameSuffixHash: true
```

```yaml
# keda-triggerauth.yaml
---
apiVersion: keda.sh/v1alpha1
kind: TriggerAuthentication
metadata:
  name: boomtime-rabbit-auth
  namespace: boomtime
spec:
  secretTargetRef:
    - parameter: host                          # creds-less amqp URI
      name: boomtime-rabbit-keda-host
      key: host
    - parameter: username                      # from the operator's default-user Secret
      name: boomtime-rabbit-default-user
      key: username
    - parameter: password
      name: boomtime-rabbit-default-user
      key: password
```

> Verified against KEDA docs (rabbitmq scaler, `protocol: amqp`, 2.16+): `host` may
> omit credentials when `username`/`password` are supplied as separate authParams.
> This is why boomtime does **not** need a controller to project a full amqp URL into
> a secret — the demo left its trigger commented precisely to avoid that assembly.
> If the installed KEDA is <2.16, fall back to a single `host` secret carrying the
> full `amqp://user:pass@…` URL (needs the operator password projected in — open Q4).

### 8.7 Add the worker to Argo Image Updater

The new `boomtime-worker` Deployment uses the same `ghcr.io/thebranchdriftcatalyst/boomtime`
image, so the Image Updater will bump it too **iff** its match covers this Deployment
— confirm the CRD at `talos-homelab:…/image-updater/boomtime-image-updater.yaml`
targets both Deployments (open item §10).

---

## 9. Local `tilt up` demo — SEE a job flow end-to-end (acceptance criterion)

This section is the **buildable acceptance test**. The repo's local loop is **Tilt**
+ `BOOM_ENV=dev` on a local k3s (`Tiltfile` → `docker_build('boomtime', …,
Dockerfile.dev)` with air live-reload + `k8s_yaml(kustomize('k8s/overlays/local'))`;
`k8s_resource`s for `boomtime` (pf 8080), `boomtime-postgres` (pf 5432), and the
authentik stack (pf 9000)). The operators (RabbitMQ/Dragonfly/KEDA) do **not** exist
on local k3s, so **local supplies its own broker as plain Deployments** while the
talos overlay uses the operator CRs (§8). `base/` stays operator-agnostic; each
overlay provisions its own broker. **No GPU/ComfyUI** — a mock image backend
completes jobs on a laptop.

### 9.1 `k8s/overlays/local/broker.yaml` (new — plain rabbitmq + redis + mock ComfyUI)

```yaml
---
apiVersion: apps/v1
kind: Deployment
metadata: { name: boomtime-rabbit, namespace: boomtime, labels: { app: boomtime-rabbit } }
spec:
  replicas: 1
  selector: { matchLabels: { app: boomtime-rabbit } }
  template:
    metadata: { labels: { app: boomtime-rabbit } }
    spec:
      containers:
        - name: rabbit
          image: rabbitmq:3.13-management
          ports: [ { containerPort: 5672, name: amqp }, { containerPort: 15672, name: mgmt } ]
          env:
            - { name: RABBITMQ_DEFAULT_USER, value: boomtime }
            - { name: RABBITMQ_DEFAULT_PASS, value: boomtime }
---
apiVersion: v1
kind: Service
metadata: { name: boomtime-rabbit, namespace: boomtime }
spec: { selector: { app: boomtime-rabbit }, ports: [ { name: amqp, port: 5672, targetPort: amqp }, { name: mgmt, port: 15672, targetPort: mgmt } ] }
---
# Plain Redis stands in for Dragonfly locally (same wire protocol; go-redis is agnostic).
# (Swap image to `docker.dragonflydb.io/dragonflydb/dragonfly` if you want the real thing;
# redis:7-alpine is lighter for a laptop and behaves identically for PUB/SUB + SET NX.)
apiVersion: apps/v1
kind: Deployment
metadata: { name: boomtime-cache, namespace: boomtime, labels: { app: boomtime-cache } }
spec:
  replicas: 1
  selector: { matchLabels: { app: boomtime-cache } }
  template:
    metadata: { labels: { app: boomtime-cache } }
    spec:
      containers:
        - { name: redis, image: redis:7-alpine, ports: [ { containerPort: 6379, name: redis } ] }
---
apiVersion: v1
kind: Service
metadata: { name: boomtime-cache, namespace: boomtime }
spec: { selector: { app: boomtime-cache }, ports: [ { name: redis, port: 6379, targetPort: redis } ] }
```

### 9.2 `k8s/overlays/local/comfyui-mock.yaml` (new — mock image backend, no GPU)

The worker calls `comfyui.Client.Generate` → `POST /v1/images/generations` and expects
`{"created":N,"data":[{"b64_json":"…"}]}`; the boot/CLI health path hits `GET /healthz`
expecting `200` (see `internal/comfyui/client.go` L198–246, L91). The client **decodes
the JSON body regardless of Content-Type** and `sniffMime` falls back to `image/png`,
so a single static JSON response satisfies **both** endpoints. `hashicorp/http-echo`
returns that same body (200) on every path/method — zero code:

```yaml
---
apiVersion: apps/v1
kind: Deployment
metadata: { name: comfyui-shim, namespace: boomtime, labels: { app: comfyui-shim } }
spec:
  replicas: 1
  selector: { matchLabels: { app: comfyui-shim } }
  template:
    metadata: { labels: { app: comfyui-shim } }
    spec:
      containers:
        - name: mock
          image: hashicorp/http-echo:1.0
          args:
            - "-listen=:8012"
            # 1x1 transparent PNG (valid base64 → sniffMime → image/png). Same body
            # answers GET /healthz (200) and POST /v1/images/generations (data[0].b64_json).
            - '-text={"created":0,"data":[{"b64_json":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAAC0lEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="}]}'
          ports: [ { containerPort: 8012, name: http } ]
---
apiVersion: v1
kind: Service
metadata: { name: comfyui-shim, namespace: boomtime }   # SAME name as the talos overlay's shim Service
spec: { selector: { app: comfyui-shim }, ports: [ { name: http, port: 8012, targetPort: http } ] }
```

> Because the Service is named `comfyui-shim` (identical to the talos overlay's
> external-endpoint Service), the config key `BOOM_COMFYUI_SHIM_URL=http://comfyui-shim:8012`
> is **the same string** locally and in prod — only the backing pod differs. A job
> therefore exercises the full `RegenerateEntry` → `comfyui.Client.Generate` → DB-save
> path with no GPU. (If you'd rather assert the prompt reached the backend, swap
> `http-echo` for a 15-line `python:3.12-slim` stub that logs the POST body and returns
> the same JSON — noted as an option; `http-echo` is the lightest.)

### 9.3 Local config, secret stub, and kustomization wiring

Local `patch-configmap.yaml` (already patches `boomtime-config`) gains:

```yaml
data:
  BOOM_QUEUE_BROKER: "rabbitmq"
  BOOM_RABBITMQ_QUEUE: "boomtime.image-jobs"
  BOOM_REDIS_ADDR: "boomtime-cache:6379"
  BOOM_FEATURE_LABEL_IMAGES: "on"
  BOOM_COMFYUI_SHIM_URL: "http://comfyui-shim:8012"
  BOOM_COMFYUI_MODEL: "stub"
  BOOM_ADMIN_USERS: "dev"        # the local user you'll log in / create a token as
```

Local `patch-deployment.yaml` sets the server's `args: ["run","--role=server"]`.
Local `secrets.yaml` gains a stub matching the operator's secret shape so
`worker-deployment.yaml`'s `$(VAR)` interpolation resolves identically to prod:

```yaml
---
apiVersion: v1
kind: Secret
metadata: { name: boomtime-rabbit-default-user, namespace: boomtime }
stringData: { username: boomtime, password: boomtime, host: boomtime-rabbit, port: "5672" }
```

Local `kustomization.yaml` `resources:` gains `broker.yaml`, `comfyui-mock.yaml`, and
`../../base/worker-deployment.yaml`. **No KEDA locally** — the worker runs at a fixed
`replicas: 1` (skip the ScaledObject). The queue `boomtime.image-jobs` is
auto-declared by the consumer (`ch.QueueDeclare(queue, durable=true, …)`, as in
`flex/checks_messaging.go`), so no Messaging-Topology `Queue` CR is needed locally.

### 9.4 Tiltfile diff (extends the existing app/postgres/authentik setup)

Append after the existing `k8s_resource('boomtime', …)` block — nothing else in the
Tiltfile changes:

```python
# ── Image-job worker tier (gaka worker-decoupling) ───────────────────────────
# Same image as boomtime (docker_build above), run with --role=worker. Consumes
# the RabbitMQ queue; DOES NOT serve the API. resource_deps ensures broker+cache
# are up first so the consumer connects on boot.
k8s_resource(
    'boomtime-worker',
    labels=['worker'],
    resource_deps=['boomtime-postgres', 'boomtime-rabbit', 'boomtime-cache'],
)

# ── Local broker: plain RabbitMQ (mgmt UI) + Redis (Dragonfly stand-in) ───────
k8s_resource(
    'boomtime-rabbit',
    port_forwards=['15672:15672', '5672:5672'],   # mgmt UI at http://localhost:15672 (boomtime/boomtime)
    labels=['broker'],
)
k8s_resource(
    'boomtime-cache',
    port_forwards=['6379:6379'],                  # redis-cli -p 6379 for MONITOR
    labels=['broker'],
)

# ── Mock image backend (no GPU/ComfyUI) ──────────────────────────────────────
k8s_resource(
    'comfyui-shim',
    labels=['broker'],
)
```

### 9.5 The demo recipe — enqueue one job and watch it flow

```bash
# 0. one-time: add Go deps for the AMQP producer/consumer + redis bus
cd workspace/boomtime && go get github.com/rabbitmq/amqp091-go github.com/redis/go-redis/v9 && go mod tidy

# 1. bring up the whole stack
tilt up
#    Tilt panel groups: app(boomtime), worker(boomtime-worker), broker(rabbit/cache/comfyui-shim),
#    db(postgres), authentik. Wait for boomtime-worker to go green.

# 2. confirm the roles are split into SEPARATE pods
kubectl -n boomtime get pods -L app
#    -> a `boomtime` pod (server) AND a `boomtime-worker` pod (worker), distinct.
kubectl -n boomtime logs deploy/boomtime-worker | grep -i "amqp consumer started"

# 3. create a local admin user + API token (BOOM_ADMIN_USERS=dev from §9.3)
kubectl -n boomtime exec -it deploy/boomtime -- boomtime create-user -u dev      # set a password
TOKEN=$(kubectl -n boomtime exec -it deploy/boomtime -- boomtime create-token -u dev | tail -1)

# 4. ENQUEUE one image job via the admin HTTP endpoint (this is the queue path:
#    AdminLabelImagesRegenerate -> Enqueuer.Enqueue -> AMQPProducer publishes to RabbitMQ.
#    NOTE: the `label-images regenerate` CLI bypasses the queue — use the HTTP endpoint.)
curl -sX POST localhost:8080/api/v1/admin/label-images/regenerate \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"ids":["late-night-coder"],"entries":[{"id":"late-night-coder","prompt":"demo"}]}'
#    -> 202 {"queued":1,"jobs":[{"jobId":"…","labelId":"late-night-coder","existing":false}]}

# 5. WATCH IT FLOW (the acceptance evidence):
#  (a) queue depth 0 -> 1 -> 0 in the RabbitMQ mgmt UI (http://localhost:15672, boomtime/boomtime,
#      Queues tab -> boomtime.image-jobs), or headless:
kubectl -n boomtime exec deploy/boomtime-rabbit -- rabbitmqctl list_queues name messages
#  (b) the WORKER pod consumes it and calls the mock backend:
kubectl -n boomtime logs deploy/boomtime-worker -f | grep -Ei "consume|RegenerateEntry|execute done"
#  (c) the mock backend received the POST:
kubectl -n boomtime logs deploy/comfyui-shim
#  (d) the RESULT landed — a new row in label_images (the worker's DB save):
kubectl -n boomtime exec deploy/boomtime-postgres -- psql -U boomtime -d boomtime \
  -c "select id, length(image) from label_images where id='late-night-coder';"
#  (e) cross-pod PROGRESS relay — events crossed pods via Redis pub/sub:
kubectl -n boomtime exec deploy/boomtime-cache -- redis-cli -c "SUBSCRIBE boomtime:imagejobs:events" &
#      re-run step 4 and see queued/running/done frames printed by the API-pod's mirror.
```

**The visual "SEE it" path (recommended for the human running `tilt up`):** run the
frontend (`cd web && npm run dev`, proxies to :8080), log in as `dev`, open
**Settings → Admin → label images**, click **Regenerate**. The rows animate
`queued → running → done` **in the browser** — which only works if the worker's
progress events crossed from the worker pod to the API pod (Redis relay) and out the
`AdminLabelImagesWS`. Simultaneously the RabbitMQ mgmt UI shows the depth blip and the
worker pod logs show the consume. That is the end-to-end acceptance, observed live.

### 9.6 Acceptance checklist (must all pass locally before prod)

1. **Roles split:** `boomtime` and `boomtime-worker` are distinct pods; the worker logs "amqp consumer started" and serves no API.
2. **Regression parity:** flip `BOOM_QUEUE_BROKER=inprocess` (+ server `--role=all`, scale worker to 0) → behavior is byte-identical to today (in-process pool). This is the safety net proving the gate.
3. **Decoupling + at-least-once:** with `rabbitmq`, enqueue → queue depth rises; `kubectl scale deploy/boomtime-worker --replicas=0` mid-flight → depth **holds** (server isn't executing); scale back to 1 → depth drains and the job completes. Kill the worker mid-job → the unacked message redelivers and the label still ends `done` exactly once (idempotent `RegenerateEntry`).
4. **Cross-pod progress:** the browser Admin tab shows `queued→running→done` with server and worker in separate pods (Redis relay working).
5. **Job result:** a `label_images` row exists for the enqueued id, produced by the **worker** pod calling the mock backend — no GPU involved.

---

## 10. Rollout stages, Phase 2, and open questions

### Rollout (each independently deployable + reversible)

- **(a) `--role` flag, no-op.** Ship the flag; everything defaults to `all`/`inprocess`. Prod unchanged. *Revert:* remove flag.
- **(b) AMQP producer/consumer behind `BOOM_QUEUE_BROKER`.** Code lands but config stays `inprocess`. Nothing runs the new paths yet. *Revert:* it's already inert.
- **(c) Cross-pod progress (Redis relay + `Registry.Apply` mirror).** Still inert under `inprocess`. Unit/integration-test the relay. *Revert:* inert.
- **(d) Provision brokers + worker Deployment — LOCAL FIRST.** Apply §9 on the dev cluster, run the §9.6 acceptance checklist. Only after it passes: apply §8 to the talos overlay (RabbitmqCluster + Dragonfly + worker + server rabbit-env patch), flip `BOOM_QUEUE_BROKER=rabbitmq`. *Revert:* scale `boomtime-worker` to 0 and set `BOOM_QUEUE_BROKER=inprocess` + server `--role=all` — the server falls back to the in-process pool instantly, no data migration.
- **(e) KEDA autoscaling + scale-to-zero.** Add §8.5/§8.6 ScaledObject + TriggerAuthentication + a PodDisruptionBudget (`minAvailable: 0`, since scale-to-zero is intended). *Revert:* pause via `autoscaling.keda.sh/paused-replicas` annotation or delete the ScaledObject (Deployment holds last `replicas`).

**Verified locally before prod:** the entire §9.6 checklist — regression parity under
`inprocess`, queue-depth decoupling, kill/restart at-least-once, cross-pod WS
progress, and SIGTERM-mid-regen idempotency. KEDA is prod-only (no KEDA locally), so
stage (e) is validated in prod behind the pausable ScaledObject.

### Phase 2 — the import worker (later, Postgres-native)

The Wakatime/GitHub importer stays in-process for now. When decoupled, it should use
**Postgres as the queue** (it's already durable day-by-day work in `import_jobs`), not
RabbitMQ: add a leased `SELECT … FOR UPDATE SKIP LOCKED` claim (replacing the welded
`ImportRequest → Worker.StartJob` and the blanket `RecoverInterrupted` /
`MarkRunningJobsFailed` fail-on-restart), and move its in-memory `importer.Hub`
progress to **Postgres LISTEN/NOTIFY** (every API pod LISTENs, fans to its WS clients —
the reconnect snapshot is already DB-backed via `GetJobLogs`). The same `--role` split
applies; the import consumer just runs alongside the image consumer in the worker pod.
Kept separate from stage 1 because its transport (Postgres) and idempotency story
differ from the external-compute image jobs.

### Open questions for the operator

1. **Argo Image Updater match** — does `boomtime-image-updater.yaml` bump *both* the `boomtime` and new `boomtime-worker` Deployments (same image tag)? If not, extend its match so they deploy in lockstep.
2. **Dev-cluster operators** — does the local k3s run the RabbitMQ/Dragonfly operators, or should local use the plain-image Deployments in §9.1 (assumed)? Confirm so we don't provision CRs against a missing operator locally.
3. **Storage class for RabbitMQ** — §8.1 uses `local-path` (the demo's choice) for the broker's mnesia dir. Confirm that class exists on talos00, or swap to whatever local/SSD class the cluster provides (NFS `fatboy-nfs-appdata` is a poor fit for mnesia).
4. **KEDA version** — the split-cred `TriggerAuthentication` (creds-less `host` + separate `username`/`password`) needs KEDA ≥ 2.16. Confirm the installed version; if older, use the single-URL fallback noted in §8.6.
5. **Genuinely-Python work?** — the only thing that would justify a real Celery/Python tier (vs. this Go-on-RabbitMQ design) is future work with no good Go story (e.g. in-cluster ML/inference). If that's on the roadmap, name it and we scope a *separate* Python worker for *that job only*, enqueued over the same broker.
6. **`BOOM_ENCRYPTION_KEY` on the worker** — not needed for image jobs, but the Phase 2 import consumer performs `auth.Encrypt` (save-on-success). Confirm the ExternalSecret is visible to the worker Deployment before Phase 2 (same namespace `boomtime`, so a re-reference suffices).

---

## Appendix — file/symbol index (for the implementer)

| Concern | Location |
|---|---|
| Process wiring (server+workers, image pool) | `cmd/boomtime/main.go` `runCmd` L203–264 (pool L241–264) |
| Image-job enqueue (API) | `internal/admin/admin_label_images.go` `AdminLabelImagesRegenerate` L125 (Enqueue L211) |
| Image-job progress WS | `admin_label_images.go` `AdminLabelImagesWS` L248 (Subscribe/Snapshot L278–283) |
| In-memory registry (byLabel dedup, broadcast, retention) | `internal/queue/imagejobs/registry.go` (Enqueue L173, broadcastLocked L357, claim L381) |
| In-memory pool + Executor interface | `internal/queue/imagejobs/pool.go` (Executor L37, worker L121) |
| Job handler to reuse verbatim | `internal/worker/labelimages/worker.go` `RegenerateEntry` L190; startup reconcile `Run` L147; **Celery TODO L20** |
| Handler queue fields | `internal/handler/handler.go` `LabelImagesWorker` L58, `ImageJobQueue` L64, `SetImageJobQueue` L140 |
| CLI regen (unchanged) | `cmd/boomtime/label_images.go` |
| Config | `internal/config/config.go` `FeatureLabelImages` L149, `ComfyUIShimURL` L163, `LabelImagesEnabled` L520, `Load` L294 |
| Server manifest (behavior unchanged; server patch is additive) | `k8s/base/deployment.yaml`; `k8s/base/kustomization.yaml` |
| Prod overlay | `k8s/overlays/talos00-knowledgedump/kustomization.yaml` (+ `label-images-config.yaml`, `comfyui-shim-external.yaml`) |
| Homelab reference pattern | `talos-homelab/applications/crossplane-demo/` — `rabbitmq.yaml`, `dragonfly.yaml`, `celery/scaledobject.yaml`, `flex/deployment.yaml`, `flex/checks_messaging.go` |
