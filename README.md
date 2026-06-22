# stack

A small CLI that stands up, tears down, and deploys an app into one of a few
**environment patterns** — `native` (run binaries + compose infra), `local-k8s`
(docker-desktop cluster), `remote-k8s` (Pi) — driven by per-app + per-environment
**context files**. Think "kubectl contexts, but for *how you run the whole app*."

It exists because the same orchestration Makefile keeps getting rewritten per app
(baseline, finances, …) with ~70% shared shape and inconsistent names. `stack`
replaces that with one fixed vocabulary + declarative context, where tools and
deployment patterns extend via data, not code.

## Status: DESIGN COMPLETE, NOT YET BUILT

This repo currently holds the **design** only. No Go code yet. The design is
settled enough to build against — start with **M1** (below).

- **[docs/DESIGN.md](docs/DESIGN.md)** — commands, context-file schema, the three
  patterns, secrets (1Password), hooks, Makefile→stack mapping, phased plan,
  settled decisions.
- **[docs/PLUGIN-MODEL.md](docs/PLUGIN-MODEL.md)** — the extension architecture:
  abstract steps (the contract) ← tool manifests (plugins) ← env-context bindings.

## The model in three sentences

1. The **engine** knows only **abstract steps** (`build-artifact`, `deliver`,
   `scan`, `render-config`, `apply`, `wait-ready`, `teardown`, `status`, `logs`,
   `resolve-secrets`, `hook`) and sequences them per **pattern**.
2. **Plugins** are declarative YAML manifests that teach the engine how a specific
   **tool + version** performs a step (e.g. `docker >=24` → `buildx build`).
3. The **environment context** (`.stack/<env>.yaml`) binds each step to a tool and
   supplies the values; `~/.stack/config/<repo>` tracks the selected env
   (kubectl-style current-context).

The engine is a **version-aware template renderer + sequencer**. All tool, secret,
and app-specific knowledge lives in data (manifests, context, hooks).

## Settled decisions (don't relitigate)

- Binary/repo/command: **`stack`** (module `github.com/faradayfan/stack`).
- **Go CLI** (cobra-style), **orchestrates native tools** by shelling out (no SDKs).
- **kubectl-style current-context**; `stack use <env>`, `--env` overrides.
- **Declarative + hook escape hatches** (hooks declare explicit env-var mappings;
  `type: bash` for M1).
- Abstract-step contract · **one manifest per tool, version ranges inside** ·
  **env context declares step→tool bindings** (no auto-pick of *which* tool;
  engine auto-detects the *version*).
- State at **`~/.stack/config/<repo>`** (per-user, not committed).
- **Secrets from a pluggable provider** — default **1Password** via per-secret
  `op read`, **one vault per app**, auth = whatever `op` session exists; a
  `provider: file` fallback for the rare local case.
- Execution always supports **`--dry-run`** (print the commands) and **confirms
  before remote/destructive** actions.

See DESIGN.md "Decisions" + "Decided" for the full rationale.

## M1 — first milestone (build this first)

**Goal:** `stack` drives **baseline** on the **local-k8s** pattern end-to-end,
exactly reproducing today's `make local-up` / `make local-down`.

**Scope:** the `k8s` pattern with `image_delivery: load`; commands `use`, `env`,
`up`/`deploy`, `down`, `status`; `--dry-run` from the first commit. Tool manifests
needed: `docker` (build + load-to-node), `grype` (scan), `helm` (render/apply/
wait/teardown), `kubectl` (status). Secrets/native/remote-k8s are later milestones.

### Worked example — the exact commands M1 must produce

Given this context (to be authored in the **baseline** repo at
`.stack/local-k8s.yaml`):

```yaml
pattern: k8s
kube_context: docker-desktop
namespace: baseline
node: desktop-control-plane
image_delivery: load
tools: { build-artifact: docker, deliver-artifact: docker, scan-artifact: grype,
         render-config: helm, apply: helm, wait-ready: helm, status: kubectl }
chart: deploy/charts/baseline
values: [deploy/local/values.yaml]
helm_set: { rollmeTimestamp: "{{ now_unix }}" }
deps: { helm_repos: [{ name: bitnami, url: https://charts.bitnami.com/bitnami }] }
images:                                   # keyed by image name (map, not list)
  baseline:            { context: . }
  baseline-ui:         { context: ./frontend }
  baseline-postgresql: { context: ./deploy/postgres, tag: "16-pgvector" }
  baseline-mem0-api:   { context: ./deploy/mem0-api, tag: "ollama", args: { PATCH_OLLAMA: "1" } }
scan: { images: [baseline, baseline-ui], fail_on: high }
```

`stack deploy --env local-k8s` must run the following (this is the acceptance
fixture — verify via `--dry-run`), equivalent to today's `make local-up`:

```bash
# build-artifact (docker)
docker build -t baseline:dev .
docker build -t baseline-ui:dev ./frontend
docker build -t baseline-postgresql:16-pgvector ./deploy/postgres
docker build --build-arg PATCH_OLLAMA=1 -t baseline-mem0-api:ollama ./deploy/mem0-api
# deliver-artifact (load into the node's containerd)
docker save baseline:dev                    | docker exec -i desktop-control-plane ctr -n k8s.io images import -
docker save baseline-ui:dev                 | docker exec -i desktop-control-plane ctr -n k8s.io images import -
docker save baseline-postgresql:16-pgvector | docker exec -i desktop-control-plane ctr -n k8s.io images import -
docker save baseline-mem0-api:ollama        | docker exec -i desktop-control-plane ctr -n k8s.io images import -
# scan-artifact (first-party only, fail on high) — grype reads .grype.yaml
grype baseline:dev
grype baseline-ui:dev
# render+apply (helm), with deps fetched first
helm repo add bitnami https://charts.bitnami.com/bitnami
helm dependency build deploy/charts/baseline
helm upgrade --install baseline deploy/charts/baseline \
  --kube-context docker-desktop -n baseline --create-namespace \
  -f deploy/local/values.yaml --set rollmeTimestamp=<unix>
```

`stack down --env local-k8s` (= `make local-down`):

```bash
helm --kube-context docker-desktop -n baseline uninstall baseline || true
# stack down --destroy also: kubectl --context docker-desktop -n baseline delete pvc --all
```

### M1 acceptance criteria

1. `stack use local-k8s` records the current env at `~/.stack/config/<repo>`;
   `stack env` prints the resolved config + step→tool bindings.
2. `stack deploy --dry-run` prints **exactly** the command sequence above
   (modulo the `rollmeTimestamp` value) — this is the regression fixture.
3. `stack deploy` (no dry-run) actually stands baseline up on docker-desktop;
   `kubectl -n baseline get pods` shows it Running. (Equivalent to `make local-up`.)
4. `stack down` uninstalls; `stack down --destroy` also drops PVCs.
5. `stack status` shows the namespace's pods.
6. Tool manifests for docker/grype/helm/kubectl exist as data (embedded), with
   docker's version-variant handling present even if only one variant is needed
   for M1.
7. A failing scan (grype high+) fails `deploy` before helm runs.
8. Built/tested to the same bar as baseline: Go, unit tests on the
   engine/template/version-range logic, `gofmt`/`golangci-lint` clean.

When M1 is green against baseline, M2 (remote-k8s/Pi, `image_delivery: push`) and
M3 (native/compose + the finances app) follow the plan in DESIGN.md.

## Source-of-truth pointers (for the implementer)

- The behavior M1 must match lives in **`../baseline/Makefile`** (targets
  `local-images`, `scan`, `local-up`, `local-down`, `helm-deps`) and
  **`../baseline/deploy/local/values.yaml`**.
- The second app to onboard (M3) is **`../finances/Makefile`** (targets `k8s-up`,
  `redeploy`, `k8s-down`, `migrate-*`, the docker-compose `local-*` infra).
- Both already carry a `.grype.yaml` and a `scan` make target to mirror.
