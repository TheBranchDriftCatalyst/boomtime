# Tiltfile — local k3s dev via k8s/overlays/local.
#
# Runs the Go backend + a plain Postgres inside your local k3s. The Vite frontend
# is intentionally NOT here — run `cd web && npm run dev` on the host and let it
# proxy to http://localhost:8080 (see web/vite.config.ts). That keeps HMR fast
# and matches the docker-compose dev flow.
#
# Usage:
#   task db:up || true         # optional; not needed under Tilt
#   tilt up                    # starts k8s side
#   cd web && npm run dev      # separate terminal: frontend on :5173
#
# Requires: tilt, kubectl, kustomize (bundled with kubectl), a running local k3s
# cluster with context selected via `kubectl config use-context`.

# ── Safety ───────────────────────────────────────────────────────────────────
# Prevent accidental `tilt up` against the homelab cluster.
allow_k8s_contexts([
    'k3d-boomtime',
    'k3d-local',
    'k3d-catalyst-dev',  # shared local k3d dev cluster (boomtime runs in its own ns)
    'kind-boomtime',
    'kind-local',
    'rancher-desktop',
    'docker-desktop',
    'orbstack',
])

# ── Image build ──────────────────────────────────────────────────────────────
# Dockerfile.dev embeds air (github.com/air-verse/air) so `air` inside the
# container rebuilds the Go binary when source files change. Tilt's live_update
# syncs the source tree into the container; air picks up the change and reloads.
# Result: no full image rebuild per code change.
docker_build(
    'boomtime',
    context='.',
    dockerfile='Dockerfile.dev',
    only=[
        './cmd',
        './internal',
        './embed.go',
        './go.mod',
        './go.sum',
        './.air.toml',
        './CHANGELOG.md',  # embed.go //go:embed CHANGELOG.md needs it in-context
    ],
    live_update=[
        sync('./cmd', '/src/cmd'),
        sync('./internal', '/src/internal'),
        sync('./embed.go', '/src/embed.go'),
        # air rebuilds; no explicit `run` step needed. If .air.toml watchers
        # miss a file type, add `run('go build …', trigger=[…])` here.
    ],
)

# ── k8s workloads ────────────────────────────────────────────────────────────
k8s_yaml(kustomize('k8s/overlays/local'))

k8s_resource(
    'boomtime',
    port_forwards=['8080:8080'],
    labels=['app'],
    # RabbitMQ is gone (boom-piig phase 3) — one job system on the Postgres
    # queue. Cache stays: the per-kind concurrency limiter and the log relay use
    # it, and sequencing after it avoids a needless reconnect churn at boot.
    resource_deps=['boomtime-postgres', 'boomtime-cache'],
)

k8s_resource(
    'boomtime-postgres',
    port_forwards=['5432:5432'],
    labels=['db'],
)

# ── Image-job worker tier (worker-topology decoupling, boom-8bz follow-up) ───
# ── Cache: Redis-wire (Dragonfly stand-in) ──────────────────────────────────
# NOT a broker. It backs the fleet-wide per-kind concurrency semaphore and the
# cross-pod log relay. The boomtime-worker resource and the local RabbitMQ that
# used to sit here went with the broker (boom-piig phase 3) — local now runs the
# same single job system as prod: the Postgres queue, executed in-process by the
# server at role=all.
k8s_resource(
    'boomtime-cache',
    port_forwards=['6379:6379'],  # redis-cli -p 6379 for MONITOR
    labels=['cache'],
)

# ── Mock image backend (no GPU/ComfyUI) ──────────────────────────────────────
k8s_resource(
    'comfyui-shim',
    labels=['broker'],
)

# ── Authentik dev stack (boom-93f.8) ─────────────────────────────────────────
# Self-contained OIDC provider for exercising the boomtime login flow. Heavy +
# slow to boot (DB migrate → blueprint apply); give it a minute after `tilt up`.
# boomtime still runs BOOM_AUTH_PROVIDER=local — this is here so the OIDC
# resolver (boom-0oe.11) has a real Authentik + declared boomtime app to hit.
# UI + issuer at http://localhost:9000 (akadmin / see authentik-secrets).
k8s_resource(
    'authentik-postgres',
    labels=['authentik'],
)

k8s_resource(
    'authentik-redis',
    labels=['authentik'],
)

k8s_resource(
    'authentik-server',
    port_forwards=['9000:9000'],
    labels=['authentik'],
    resource_deps=['authentik-postgres', 'authentik-redis'],
)

k8s_resource(
    'authentik-worker',
    labels=['authentik'],
    resource_deps=['authentik-postgres', 'authentik-redis'],
)
