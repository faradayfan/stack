# stack — design doc (DRAFT)

`stack` — a small CLI that stands up, tears down, and deploys an app into one of
a few **environment patterns** (local-native, local-k8s, remote-k8s), driven by
per-app + per-environment **context files**. Think "kubectl contexts, but for
*how you run the whole app*," not just which cluster.

> The binary, repo, and command are all `stack` (module
> `github.com/faradayfan/stack`). Note: Haskell's build tool is also `stack` —
> niche, but a possible PATH collision to be aware of; only the installed binary
> name is affected, not the module path.

> Status: design for review. Not built. Decisions below are settled (see
> "Decisions"); the schema and command surface are the parts to react to.

## Why

The same orchestration Makefile has been written several times across apps
(baseline, finances, …). They share ~70% of their shape — build images → load or
push → scan → helm upgrade → seed — but each reinvents it with **inconsistent
names** (baseline `local-up` vs finances `k8s-up` for the *same* concept) and
copy-pasted variables. The divergent ~30% (migrations, compose infra, Keycloak
vs RBAC seeding, image sets) is real but small.

A single tool with a **fixed vocabulary** + **context files** removes the
duplication and the naming drift, while keeping the per-app specifics in
declared hooks rather than hardcoded logic.

## Decisions (settled)

- **Standalone, multi-repo.** Its own repo/binary; each app carries a `.stack/`
  config dir. (Proving ground: implement against baseline first, but build it as
  the standalone tool from day one.)
- **Go CLI** (cobra-style), cross-compiled, installable — matches the "Go over
  bash for tooling" preference and the existing `baseline-mcp` precedent.
- **Orchestrates native tools** (`docker`, `helm`, `kubectl`, `docker compose`)
  by shelling out, sequenced per-pattern. It does NOT reimplement them via SDKs —
  transparent, debuggable, and it mirrors what the Makefiles already do.
- **kubectl-style current-context.** The tool tracks a selected environment
  (`stack use local-k8s`); commands act on it unless `--env` overrides.
- **Declarative + escape hatches.** Context files declare the common shape; the
  app-specific 30% is named **hooks** the tool runs at defined phases.
- **First milestone:** one pattern, one app, end-to-end — `local-k8s` for
  baseline (`up` / `deploy` / `down` / `status`) through the context engine.

## The three patterns

| Pattern | What "up" means | Build/ship | Example |
| --- | --- | --- | --- |
| `native` | run the app's binaries directly + start infra via docker-compose | `go build` / `pnpm build`; no images | finances local (compose Postgres+Keycloak), baseline standards-only |
| `k8s` (local) | helm install into a local cluster; images built + **loaded** into the node (no registry) | `docker build` → `ctr import` | docker-desktop cluster |
| `k8s` (remote) | helm install into a remote cluster; images built for the cluster arch + **pushed** to a registry | `docker buildx --push` | Pi k3s |

`native` and `k8s` are the two **engines**; local-vs-remote k8s is the same engine
with a different **image delivery** mode (`load` vs `push`) and `kube_context`.

## Command surface

```
stack use <env>            # select the current environment (writes .stack/state)
stack env                  # show current env + resolved config
stack up                   # stand up the environment (infra + app)
stack deploy               # build → scan → load/push → apply (the inner-loop verb)
stack down [--destroy]     # tear down (──destroy also drops volumes/data)
stack status               # pods / services / health for the env
stack logs [component]     # tail logs
stack seed                 # run the app's seed hook
stack <hook>               # run any named hook (e.g. `stack migrate`)
```

- Global flags: `--env <name>` (override current), `--dry-run` (print the
  commands it *would* run — important for trust), `--verbose`.
- **Guardrail:** destructive or *remote* actions confirm before running unless
  `--yes`. "Deployed to the wrong cluster" is the failure mode to design against.

## Context files

Two layers, both under `.stack/` in the app repo:

### `.stack/app.yaml` — app-wide, environment-independent

```yaml
name: baseline
# Images the app builds. `context` is the docker build context; `args` optional.
images:
  - name: baseline            # the service
    context: .
  - name: baseline-ui
    context: ./frontend
  - name: baseline-postgresql
    context: ./deploy/postgres
  - name: baseline-mem0-api
    context: ./deploy/mem0-api
    args: { PATCH_OLLAMA: "1" }
# First-party images to vuln-scan (third-party bases excluded).
scan:
  images: [baseline, baseline-ui]
  fail_on: high              # grype threshold
# Named hooks the tool runs at phase boundaries or on demand. Each is just a
# command run from the repo root. This is the escape hatch for the app-specific
# 30% (migrations, custom seed, Keycloak, etc.).
hooks:
  seed: "./deploy/seed.sh"
  # migrate: "goose ... up"   # finances would set this; baseline doesn't
```

### `.stack/<env>.yaml` — one per environment

```yaml
# .stack/local-k8s.yaml
pattern: k8s
kube_context: docker-desktop
image_delivery: load          # load → import into node | push → registry
node: desktop-control-plane   # for `ctr import` (load mode)
namespace: baseline
chart: deploy/charts/baseline
values: [deploy/local/values.yaml]
helm_set:                     # --set overrides (e.g. the rollme trick)
  rollmeTimestamp: "{{ now_unix }}"
deps:                         # helm dependency build prerequisites
  helm_repos: [{ name: bitnami, url: https://charts.bitnami.com/bitnami }]
prereqs:                      # soft checks; warn (don't fail) if missing
  - "ollama list | grep -q qwen2.5:3b"
```

```yaml
# .stack/pi.yaml
pattern: k8s
kube_context: k3s
image_delivery: push
registry: "<REGISTRY_HOST>:5000"
platform: linux/arm64
tag: "{{ git_short_sha }}"
namespace: baseline
chart: deploy/charts/baseline
values: [deploy/pi/values.yaml, deploy/pi/secrets.yaml?]   # ? = optional-if-present
remote: true                  # → confirm before deploy/down
```

```yaml
# .stack/native.yaml
pattern: native
compose: deploy/local/docker-compose.yaml   # infra (if any)
run:                          # processes `up` starts (or just builds)
  - "go run ./cmd/baseline"
```

Template tokens (`{{ now_unix }}`, `{{ git_short_sha }}`) cover the few dynamic
values the Makefiles compute inline.

## How a command resolves (example: `stack deploy` on local-k8s)

1. Load `.stack/app.yaml` + `.stack/local-k8s.yaml`, merge.
2. `pattern: k8s`, `image_delivery: load` → for each `images[]`: `docker build`
   (+ args), then `ctr import` into `node`.
3. `scan` → grype each `scan.images` at `fail_on` (reuse `.grype.yaml`).
4. helm: add `deps.helm_repos`, `helm dependency build`, then
   `helm upgrade --install <name> <chart> -f <values…> --set <helm_set…>` against
   `kube_context` / `namespace`.
5. Done. `stack seed` (or an `after_deploy` hook) runs the seed.

`--dry-run` prints each shell command instead of running it.

## Mapping the current Makefiles (proof it covers reality)

| Makefile target | stack equivalent |
| --- | --- |
| `make local-up` (baseline) / `make k8s-up` (finances) | `stack up --env local-k8s` |
| `make redeploy` | `stack deploy` |
| `make local-down` / `k8s-down` | `stack down` (`--destroy` for PVCs) |
| `make pi-deploy` | `stack deploy --env pi` |
| `make scan` / `scan-pi` | folded into `deploy` (scan phase) |
| `make local-seed` / `pi-seed` | `stack seed` |
| `make local-logs` / `k8s-status` | `stack logs` / `stack status` |
| `make dev` (finances, native) | `stack up --env native` |
| `make migrate-up` (finances) | `stack migrate` (hook) |

The naming drift (`local-up` vs `k8s-up`) collapses into one verb + an env.

## What stays in the app repo (NOT the tool's job)

- The Helm charts, Dockerfiles, compose files, seed scripts, migrations — the
  tool *invokes* these, never owns them.
- App-specific build logic beyond `docker build` lives behind a hook.

## Phased build plan

1. **M1 — engine + local-k8s + baseline (the first milestone).** Context loader,
   `use`/`env`, and `up`/`deploy`/`down`/`status` for the `k8s` pattern with
   `image_delivery: load`. Dry-run from the start. Prove it replaces
   `make local-up` / `local-down` exactly.
2. **M2 — remote k8s (pi).** `image_delivery: push`, `platform`, `registry`,
   `remote: true` confirmations. Replaces `pi-deploy`.
3. **M3 — native + compose.** The finances local pattern; `run`/`compose`,
   `migrate` hook. 
4. **M4 — second app (finances) adopts it.** Validates the abstraction across
   repos; fold both Makefiles' surfaces in. Extract any baseline-isms found.
5. **M5 — polish.** `logs` selectors, completion, `--yes`, install/release of the
   standalone binary (its own repo + the CI/release pipeline we just built).

## Decided

- **Binary name: `stack`.** Simple and clear; `stack up` / `stack deploy --env pi`
  read naturally. (PATH-collision caveat noted in the header.)
- **State location: `~/.stack/config/<repo>`** (per-user, not committed). The
  selected env / current-context lives here, keyed by repo, so it's never a
  committed artifact and differs per checkout. (The tool's home is `~/.stack/`.)
- **Hook contract: the context DECLARES the hook's input mappings** (explicit, not
  an implicit `STACK_*` blob). For M1, `type: bash` runs a shell command with the
  declared env-var mappings set:

  ```yaml
  hooks:
    seed:
      run: "./deploy/seed.sh"
      type: bash                 # extensibility marker — only kind for M1.
                                 # reserves room for container/http/arg-injection later.
      env:                       # explicit mapping: ENV_VAR ← resolved value/literal
        PRINCIPAL: "you"                   # literal
        NAMESPACE: "{{ .namespace }}"      # env identity
        CONTEXT:   "{{ .kube_context }}"
        IMAGE_TAG: "{{ .tag }}"            # artifact info
  ```

  Source values a hook may map from: **env identity** (`env`, `pattern`,
  `kube_context`, `namespace`), **artifact info** (`tag`, `registry`, `platform`),
  **app** (`name`), and **arbitrary literals** the author writes inline. This is
  the same shape as a tool plugin (command + explicit input mapping) — a hook is
  effectively "the app's own plugin." See `PLUGIN-MODEL.md`.

- **Secrets: sourced from a dedicated store (1Password), via a pluggable secret
  provider.** A secret provider is the same plugin shape as a tool — it provides a
  `resolve-secrets` capability. Default/preferred provider: **`onepassword`**.

  - **Mechanism: per-secret `op read`.** The env context declares secrets as
    name → `op://` reference mappings (the SAME idiom as hook env-mappings — one
    mental model across the tool). At deploy, the engine `op read`s each reference
    and injects it (helm `--set`, or an ephemeral values file passed via `-f`),
    then discards it — secrets never persist to disk.

    ```yaml
    # .stack/pi.yaml
    secrets:
      provider: onepassword
      vault: baseline                         # one-vault-per-app convention
      values:                                 # name (dotted helm path) → op:// ref
        openaiApiKey:            "op://baseline/openai/credential"
        postgres.auth.password:  "op://baseline/postgres/password"
    ```

  - **Convention: one 1Password vault per app** (`op://<app>/<item>/<field>`).
    Clean isolation; access is reasoned about per-app.
  - **Auth: rely on whatever `op` session exists.** Local dev uses the 1Password
    desktop app / biometric unlock (interactive). The tool does NOT hardcode an
    auth model — `op` resolves the session, so a `OP_SERVICE_ACCOUNT_TOKEN` works
    for headless/CI later with no tool change. (Document the token path; don't
    require it for M1.)
  - **Fallback provider `file`** (rare escape hatch): pass an existing
    `secrets.yaml` via `-f` if present. Same `secrets:` shape, `provider: file` —
    not special-cased. The dedicated store is the default; the file is the
    exception.

  See `PLUGIN-MODEL.md` for the `resolve-secrets` step + `onepassword.yaml`
  provider manifest.

## Open questions to resolve before M1

- Plugin-model questions (manifest precedence, detect-failure, soft steps) in
  `PLUGIN-MODEL.md`.
