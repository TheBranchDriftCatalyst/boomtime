#!/usr/bin/env python3
"""Deploy-surface regression checks (boom-iadc, epic boom-l827).

This is the regression test for the infra partition of the 2026-09-06 audit.
Every check below pins ONE specific failure that was found in the tree — not
adjacent coverage — and is written so that reverting the corresponding fix
makes exactly that check fail with a message naming the symptom.

Nothing here needs a cluster. It renders each kustomize overlay and asserts on
the YAML that comes out, plus a few text-level assertions on the Tiltfile,
docker-compose.yml, and the publish workflows.

Usage:
    python3 .github/scripts/validate-manifests.py        # from the repo root

Requires: kustomize on PATH, PyYAML, and (for the books dep-closure check) go.
The go check downgrades to a skip when go is unavailable so the script still
runs on a bare machine.
"""

from __future__ import annotations

import os
import re
import shutil
import subprocess
import sys
from pathlib import Path

try:
    import yaml
except ImportError:  # pragma: no cover - environment problem, not a finding
    sys.exit("validate-manifests: PyYAML is required (pip install pyyaml)")

REPO = Path(__file__).resolve().parents[2]
OVERLAYS = sorted(p for p in (REPO / "k8s" / "overlays").iterdir() if p.is_dir())

WORKLOAD_KINDS = {"Deployment", "StatefulSet", "DaemonSet", "Job", "CronJob", "Pod"}

failures: list[str] = []
skips: list[str] = []
_rendered: dict[str, list[dict]] = {}


def fail(check: str, msg: str) -> None:
    failures.append(f"{check}: {msg}")
    print(f"  FAIL  {check}\n        {msg}")


def ok(check: str, msg: str = "") -> None:
    print(f"  ok    {check}" + (f"  ({msg})" if msg else ""))


def skip(check: str, msg: str) -> None:
    skips.append(f"{check}: {msg}")
    print(f"  SKIP  {check}  ({msg})")


def render(overlay: Path) -> list[dict]:
    """kustomize build <overlay>, memoised. [] on build failure."""
    key = overlay.name
    if key in _rendered:
        return _rendered[key]
    proc = subprocess.run(
        ["kustomize", "build", str(overlay)],
        capture_output=True,
        text=True,
    )
    if proc.returncode != 0:
        fail(
            "overlays-build",
            f"kustomize build {overlay.relative_to(REPO)} failed:\n{proc.stderr.strip()}",
        )
        _rendered[key] = []
        return []
    docs = [d for d in yaml.safe_load_all(proc.stdout) if d]
    _rendered[key] = docs
    return docs


def container_env(dep: dict, container: str) -> dict[str, str]:
    for c in dep["spec"]["template"]["spec"].get("containers", []):
        if c["name"] == container:
            return {e["name"]: e.get("value") for e in c.get("env", [])}
    return {}


def find(docs: list[dict], kind: str, name: str) -> dict | None:
    for d in docs:
        if d.get("kind") == kind and d.get("metadata", {}).get("name") == name:
            return d
    return None


# ── 1. every overlay renders ────────────────────────────────────────────────
# A fix that repairs one overlay while breaking a sibling is the known trap in
# this tree; render them all, every run.
def check_overlays_build() -> None:
    for overlay in OVERLAYS:
        if render(overlay):
            ok("overlays-build", overlay.name)


# ── 2. Tiltfile k8s_resource names all have workloads ───────────────────────
# boom-iadc: Tiltfile declared k8s_resource('boomtime-cache') after the local
# Dragonfly/Redis manifest was deleted in the RabbitMQ sweep. Tilt hard-fails at
# assembly on a k8s_resource with no matching object, so `tilt up` never started
# the local environment at all.
def check_tilt_resources_have_workloads() -> None:
    check = "tilt-resources-have-workloads"
    tiltfile = (REPO / "Tiltfile").read_text()
    declared = re.findall(r"k8s_resource\(\s*['\"]([^'\"]+)['\"]", tiltfile)
    deps = set()
    for block in re.findall(r"resource_deps\s*=\s*\[([^\]]*)\]", tiltfile):
        deps.update(re.findall(r"['\"]([^'\"]+)['\"]", block))

    docs = render(REPO / "k8s" / "overlays" / "local")
    if not docs:
        skip(check, "local overlay did not render")
        return
    workloads = {
        d["metadata"]["name"] for d in docs if d.get("kind") in WORKLOAD_KINDS
    }
    for name in sorted(set(declared) | deps):
        if name not in workloads:
            fail(
                check,
                f"Tiltfile references resource {name!r} but `kustomize build "
                f"k8s/overlays/local` emits no workload with that name — "
                f"`tilt up` aborts at Tiltfile assembly and the whole local dev "
                f"environment never starts. Rendered workloads: "
                f"{sorted(workloads)}",
            )
    if not failures or not any(f.startswith(check) for f in failures):
        ok(check, f"{len(set(declared) | deps)} names")


# ── 3. BOOM_REDIS_ADDR points at a Service that exists ──────────────────────
# Second half of boom-iadc: even under a direct `kubectl apply -k`, the local
# ConfigMap pointed the limiter / log-relay Redis client at boomtime-cache:6379,
# a name with nothing behind it, so it dialled dead DNS forever.
def check_redis_addr_resolves() -> None:
    check = "redis-addr-resolves"
    for overlay in OVERLAYS:
        docs = render(overlay)
        cm = find(docs, "ConfigMap", "boomtime-config")
        if not cm:
            continue
        addr = (cm.get("data") or {}).get("BOOM_REDIS_ADDR")
        if not addr:
            continue
        host, _, port = addr.partition(":")
        svc_name = host.split(".")[0]
        svc = find(docs, "Service", svc_name)
        if svc is None:
            # prod's Service is created by the Dragonfly operator from a
            # Dragonfly CR, so accept that as the owner too.
            if find(docs, "Dragonfly", svc_name) is not None:
                ok(check, f"{overlay.name}: {addr} (Dragonfly CR)")
                continue
            fail(
                check,
                f"{overlay.name}: BOOM_REDIS_ADDR={addr} but the overlay emits "
                f"no Service (or Dragonfly CR) named {svc_name!r} — the Redis "
                f"client dials dead DNS forever",
            )
            continue
        ports = {str(p.get("port")) for p in svc["spec"].get("ports", [])}
        if port and port not in ports:
            fail(
                check,
                f"{overlay.name}: BOOM_REDIS_ADDR={addr} but Service "
                f"{svc_name!r} exposes ports {sorted(ports)}",
            )
            continue
        ok(check, f"{overlay.name}: {addr}")


# ── 4. local runs role=all ──────────────────────────────────────────────────
# boom-iadc: the local overlay pinned BOOM_ROLE=server, which makes
# jobs.DeriveKindFilter exclude every offloaded kind (avatar-render,
# label-image, books-liberate-book). Local has no worker Deployment and no KEDA,
# so those rows sat status=queued forever and the comfyui-mock e2e flow the
# overlay exists to serve could never complete.
def check_local_role_all() -> None:
    check = "local-role-all"
    docs = render(REPO / "k8s" / "overlays" / "local")
    dep = find(docs, "Deployment", "boomtime")
    if dep is None:
        fail(check, "local overlay emits no Deployment named 'boomtime'")
        return
    role = container_env(dep, "boomtime").get("BOOM_ROLE")
    if role != "all":
        fail(
            check,
            f"local boomtime Deployment sets BOOM_ROLE={role!r}, want 'all'. "
            f"The local overlay ships no worker Deployment and no KEDA, so any "
            f"role that filters kinds strands the offloaded kinds "
            f"(avatar-render / label-image / books-liberate-book) at "
            f"status=queued with no claimer.",
        )
        return
    ok(check, "BOOM_ROLE=all")


# ── 5. prod's encryption key is GitOps-owned ────────────────────────────────
# boom-iadc: external-secret-encryption.yaml was authored but never listed in
# the overlay's resources, so BOOM_ENCRYPTION_KEY existed only as a hand-created
# in-cluster Secret that nothing owned, recreated, or backed up — while DB
# backups carry the ciphertext it decrypts.
def check_encryption_key_externalsecret() -> None:
    check = "encryption-key-externalsecret"
    docs = render(REPO / "k8s" / "overlays" / "talos00-knowledgedump")
    if not docs:
        skip(check, "prod overlay did not render")
        return
    es = find(docs, "ExternalSecret", "boomtime-encryption-key")
    if es is None:
        fail(
            check,
            "the prod overlay emits no ExternalSecret 'boomtime-encryption-key' "
            "— BOOM_ENCRYPTION_KEY would exist only as a hand-created Secret "
            "nothing owns or backs up, and losing it makes every stored user "
            "secret permanently undecryptable",
        )
        return
    tmpl = str(es["spec"]["target"].get("template", {}).get("data", {}))
    if "fail" not in tmpl:
        fail(
            check,
            "the boomtime-encryption-key ExternalSecret template has no "
            "length/emptiness guard — an empty 1Password field would overwrite "
            "the live key with '' and strand every ciphertext column",
        )
        return
    ok(check, "present + guarded")


# ── 6. /metrics is not publicly routed ─────────────────────────────────────
# boom-iadc: the external IngressRoute forwarded ALL paths, so GET /metrics —
# deliberately unauthenticated and rate-limiter-exempt for in-cluster scraping —
# served the full Prometheus registry to anyone on the internet.
PUBLIC_HOST = "boomtime.knowledgedump.space"


def check_metrics_not_public() -> None:
    check = "metrics-not-public"
    docs = render(REPO / "k8s" / "overlays" / "talos00-knowledgedump")
    if not docs:
        skip(check, "prod overlay did not render")
        return
    routes = [
        (d["metadata"]["name"], r)
        for d in docs
        if d.get("kind") == "IngressRoute"
        for r in d["spec"].get("routes", [])
        if PUBLIC_HOST in r.get("match", "")
    ]
    if not routes:
        fail(check, f"no IngressRoute route matches {PUBLIC_HOST}")
        return
    forwarding = [
        (name, r)
        for name, r in routes
        # the http->https redirect forwards nothing; only guard real backends
        if not any(
            m.get("name") == "redirect-https" for m in r.get("middlewares", [])
        )
    ]
    for name, r in forwarding:
        if "!Path(`/metrics`)" not in r["match"]:
            fail(
                check,
                f"IngressRoute {name!r} forwards {PUBLIC_HOST} to the app with "
                f"rule {r['match']!r}, which includes /metrics — the full "
                f"Prometheus registry (route templates, job kinds, pool stats, "
                f"outbound-host rates) becomes anonymously readable from the "
                f"internet, and each read is an uncapped registry gather that "
                f"bypasses the rate limiter",
            )
            return
    ok(check, f"{len(forwarding)} public route(s) exclude /metrics")


# ── 7. the dev preview box cannot act on prod ──────────────────────────────
# boom-iadc: talos00-dev shares prod's ConfigMap (BOOM_HARDCOVER_DRYRUN=false)
# and prod's database, at --role=server — so reviving it puts unreviewed branch
# code in the shared jobs worker pool making real outbound Hardcover writes.
def check_dev_preview_prod_safety() -> None:
    check = "dev-preview-prod-safety"
    docs = render(REPO / "k8s" / "overlays" / "talos00-dev")
    dep = find(docs, "Deployment", "boomtime-dev")
    if dep is None:
        skip(check, "no boomtime-dev Deployment")
        return
    env = container_env(dep, "boomtime")
    if env.get("BOOM_HARDCOVER_DRYRUN") != "true":
        fail(
            check,
            "boomtime-dev does not pin BOOM_HARDCOVER_DRYRUN=true, so it "
            "inherits prod's ConfigMap value of 'false' — an unreviewed feature "
            "branch previewed here performs REAL writes against the live "
            "Hardcover account, against prod rows, with no undo",
        )
        return
    if not env.get("BOOM_JOBS_KINDS"):
        fail(
            check,
            "boomtime-dev does not set BOOM_JOBS_KINDS, so at --role=server it "
            "joins the shared prod jobs worker pool and executes real prod jobs "
            "with branch code",
        )
        return
    ok(check, "dry-run pinned + claims no prod jobs")


# ── 8. the books publish workflow triggers on every input it builds ────────
# boom-iadc: the paths filter omitted web/** and internal/boomtime/web/** (both
# COPY'd and type-checked by Dockerfile.catalyst-books) and internal/jobs/**
# (a real Go dependency), so edits to them shipped nothing and the stale image
# kept serving.
BOOKS_WORKFLOW = REPO / ".github" / "workflows" / "docker-publish-catalyst-books.yml"
BOOKS_REQUIRED_FE_PATHS = [
    "web/**",
    "internal/boomtime/web/**",
    "internal/boomtime/widget/specs.json",
]


def check_books_workflow_paths() -> None:
    check = "books-workflow-paths"
    wf = yaml.safe_load(BOOKS_WORKFLOW.read_text())
    # PyYAML parses the bare `on:` key as the boolean True.
    triggers = wf.get("on", wf.get(True, {}))
    paths = triggers["push"].get("paths")
    if paths is None:
        ok(check, "no paths filter — every push builds")
        return

    for want in BOOKS_REQUIRED_FE_PATHS:
        if want not in paths:
            fail(
                check,
                f"Dockerfile.catalyst-books consumes {want} (the books SPA "
                f"shell / the tsc include / the @widget-specs alias target) but "
                f"the workflow's paths filter omits it — edits there publish "
                f"nothing and the stale image serves on",
            )
            return

    if not shutil.which("go"):
        skip(check + "[go-deps]", "go not on PATH")
    else:
        proc = subprocess.run(
            ["go", "list", "-deps", "./cmd/catalyst-books"],
            capture_output=True,
            text=True,
            cwd=REPO,
            env={**os.environ, "CGO_ENABLED": "0"},
        )
        if proc.returncode != 0:
            skip(check + "[go-deps]", "go list failed")
        else:
            prefix = "github.com/TheBranchDriftCatalyst/boomtime/"
            pkgdirs = {
                "/".join(line[len(prefix):].split("/")[:2])
                for line in proc.stdout.split()
                if line.startswith(prefix)
            }
            for d in sorted(pkgdirs):
                if f"{d}/**" not in paths:
                    fail(
                        check,
                        f"{d} is in `go list -deps ./cmd/catalyst-books` (the "
                        f"binary this image builds) but {d}/** is not in the "
                        f"workflow's paths filter — a change there would not "
                        f"rebuild the image",
                    )
                    return
    for want in ("go.mod", "go.sum", "Dockerfile.catalyst-books"):
        if want not in paths:
            fail(check, f"{want} is a build input but is not in the paths filter")
            return
    ok(check, f"{len(paths)} patterns cover every input")


# ── 9. compose installs with yarn, not npm ─────────────────────────────────
# boom-iadc: the compose web service ran `npm install` in a yarn-1 workspace,
# ignoring web/yarn.lock and writing a stray package-lock.json into the
# bind-mounted repo — the "passes locally, fails the Docker web stage" drift.
def check_compose_uses_yarn() -> None:
    check = "compose-uses-yarn"
    compose = yaml.safe_load((REPO / "docker-compose.yml").read_text())
    cmd = compose["services"]["web"].get("command", "")
    if "npm install" in cmd or "npm ci" in cmd:
        fail(
            check,
            f"docker-compose web service installs with npm ({cmd!r}) in a "
            f"yarn-1 workspace — it ignores web/yarn.lock, resolves different "
            f"transitive versions than the Dockerfiles' "
            f"`yarn install --frozen-lockfile`, and drops a stray "
            f"package-lock.json into the bind-mounted repo",
        )
        return
    if "yarn install --frozen-lockfile" not in cmd:
        fail(check, f"compose web command does not pin the lockfile: {cmd!r}")
        return
    ok(check, "yarn --frozen-lockfile")


# ── 10. nothing publishes an image without passing the quality gate ────────
# boom-iadc: every push to main built and published a sha-tagged image that Argo
# Image Updater rolls to prod within minutes, with no test or lint job anywhere
# in .github/workflows — "the Docker build succeeded" was the only gate.
GATE_WORKFLOW = "quality-gates.yml"


def check_publish_workflows_gated() -> None:
    check = "publish-workflows-gated"
    wfdir = REPO / ".github" / "workflows"
    if not (wfdir / GATE_WORKFLOW).exists():
        fail(check, f"{GATE_WORKFLOW} is missing — nothing runs tests before publish")
        return
    for path in sorted(wfdir.glob("*.y*ml")):
        if path.name == GATE_WORKFLOW:
            continue
        wf = yaml.safe_load(path.read_text())
        jobs = wf.get("jobs", {})
        publishers = {
            name
            for name, job in jobs.items()
            for step in job.get("steps", [])
            if "build-push-action" in str(step.get("uses", ""))
            and str(step.get("with", {}).get("push", "")).lower() != "false"
        }
        if not publishers:
            continue
        gate_jobs = {
            name
            for name, job in jobs.items()
            if GATE_WORKFLOW in str(job.get("uses", ""))
        }
        if not gate_jobs:
            fail(
                check,
                f"{path.name} publishes an image but never calls "
                f"{GATE_WORKFLOW} — a commit with failing tests builds, tags "
                f"sha-<7c>, and Argo Image Updater rolls it to prod",
            )
            return
        for name in sorted(publishers):
            needs = jobs[name].get("needs", [])
            needs = [needs] if isinstance(needs, str) else needs
            if not gate_jobs & set(needs):
                fail(
                    check,
                    f"{path.name}: job {name!r} publishes an image but does not "
                    f"`needs:` the quality gate {sorted(gate_jobs)} — the gate "
                    f"runs alongside the publish instead of in front of it, so "
                    f"a red test still ships",
                )
                return
        ok(check, path.name)


CHECKS = [
    check_overlays_build,
    check_tilt_resources_have_workloads,
    check_redis_addr_resolves,
    check_local_role_all,
    check_encryption_key_externalsecret,
    check_metrics_not_public,
    check_dev_preview_prod_safety,
    check_books_workflow_paths,
    check_compose_uses_yarn,
    check_publish_workflows_gated,
]


def main() -> int:
    if not shutil.which("kustomize"):
        sys.exit("validate-manifests: kustomize is not on PATH")
    print(f"validate-manifests: {REPO}\n")
    for fn in CHECKS:
        fn()
    print()
    if skips:
        print(f"{len(skips)} check(s) skipped")
    if failures:
        print(f"\n{len(failures)} FAILURE(S):")
        for f in failures:
            print(f"  - {f}")
        return 1
    print("all deploy-surface checks passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
