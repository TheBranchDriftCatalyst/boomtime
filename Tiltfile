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
    resource_deps=['boomtime-postgres'],
)

k8s_resource(
    'boomtime-postgres',
    port_forwards=['5432:5432'],
    labels=['db'],
)
