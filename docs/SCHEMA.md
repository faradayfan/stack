# stack — context-file schema

The reference for `stack`'s context files: how you describe an app's deployment
shapes and select one per environment. For writing tool manifests see
[PLUGINS.md](PLUGINS.md); for the tools-manager flow see [SETUP.md](SETUP.md).

## The two files

Context lives under `.stack/` in your repo:

- **`.stack/app.yaml`** — declares the *deployment shapes* your app supports, as
  named **patterns**. Each pattern is a complete template: its pipeline, the
  artifacts to build, which tool runs each step, the checks, and hooks. **A pattern
  is the runnable unit.**
- **`.stack/<env>.yaml`** — *optional*, one per environment. It **selects a
  pattern** and **overrides** the parts that differ for that environment.

The mental model: **app.yaml is the recipe; an env file is the ingredients for one
kitchen.** Most env files are a few lines — "use the `k8s` pattern, but here's this
cluster's context and registry."

If your app has no per-environment variance, you need no env file at all — `stack`
runs the pattern directly. `stack` resolves which pattern to run in this order:

1. `--pattern <name>` — that pattern from app.yaml, with no env overrides.
2. `--env <name>` — that env file (which selects a pattern and supplies overrides).
3. the current context set by `stack use <env>`.
4. when none of the above is set, the sole pattern (an error if the app has
   several — pass `--pattern`).

`stack use <env>` records the current environment per-repo (kubectl-style).
`--pattern` and `--env` are mutually exclusive.

## app.yaml

```yaml
name: myapp
tools_manager: asdf                # how `stack setup` installs check tools

patterns:
  k8s:                             # a pattern name you choose; envs select it
    pipeline: [build, deliver, scan, apply]

    # identity — properties of the deployment shape (an env may override these).
    namespace: myapp
    image_delivery: load           # load into the local cluster, or push
    default_tag: dev               # tag for artifacts that don't pin their own

    # what to build, keyed by name.
    artifacts:
      myapp:
        context: .

    # which tool runs each step, plus that step's config.
    build:
      tool: docker
    deliver:
      tool: docker
      node: kind-control-plane
    scan:
      tool: grype
      images: [myapp]
      fail_on: high
    apply:
      tool: helm
      chart: ./deploy/chart
      values: [./deploy/values.yaml]
      repos:
        - name: bitnami
          url: https://charts.bitnami.com/bitnami
    wait_ready:
      tool: helm
    teardown:
      tool: helm
    destroy:
      tool: kubectl
    status:
      tool: kubectl

    # the verification suite this pattern requires (the `stack check` flow).
    checks:
      format:
        tool: gofmt
      lint:
        tool: golangci
        args:
          timeout: 5m
      unit:
        tool: go-test
        args:
          short: true

    hooks:
      seed: "./deploy/seed.sh"
```

Only two fields are **app-global**: `name` and `tools_manager`. Everything else is
a **pattern property** — including the identity fields — so the whole deployment
shape lives in one place and an env file overrides into it with one consistent
rule (below). An app may declare any number of patterns.

## Environment files

An env file selects a pattern and overrides what differs. Unset fields are
inherited from the pattern template.

```yaml
# .stack/local.yaml — local cluster
pattern: k8s
kube_context: kind-myapp
```

```yaml
# .stack/prod.yaml — remote cluster: push to a registry, build for arm64
pattern: k8s
kube_context: prod
registry: registry.example.com
platform: linux/arm64
tag: "{{ git_short_sha }}"
remote: true                       # confirm before deploy/down
deliver:           # override the template's `load`
  delivery: push
apply:
  values: [./deploy/prod-values.yaml]
  set:
    image.registry: registry.example.com
    image.tag: "{{ git_short_sha }}"
```

## The merge rule

Resolution precedence is **env value ▸ pattern template ▸ built-in default**.
Merging is uniform at every level:

- **maps merge by key** — override an existing key, or add a new one;
- **scalars replace** — the env's value wins;
- **lists replace** — an env list replaces the template's list (no append);
- **null clears** — an explicit `null` in the env deletes that key.

That is the entire rule — artifacts, step blocks, the `set` map under apply, all
merge the same way. Examples:

```yaml
# template:  deliver: { tool: docker, delivery: load }
# env:       deliver: { delivery: push }
# result:    deliver: { tool: docker, delivery: push }       # map merge by key

# template:  apply: { values: [base.yaml], repos: [...] }
# env:       apply: { values: [prod.yaml, secrets.yaml] }
# result:    apply.values = [prod.yaml, secrets.yaml]         # list replaces
#            apply.repos  = [...]                              # untouched key kept

# template:  apply: { set: { a: "1" } }
# env:       apply: { set: { b: "2" } }
# result:    apply.set = { a: "1", b: "2" }                   # map merge → both
```

`null` is the only way to *remove* an inherited value (since maps merge by key, an
override can't otherwise drop a key). For example, if the template builds an image
with a build arg that one environment must not apply:

```yaml
# template:  worker: { context: ./worker, args: { DEBUG: "1" } }
# env:       worker: { args: null }
# result:    worker: { context: ./worker }                    # args removed
```

## Artifacts

A pattern's build targets live in `artifacts:`, keyed by name. The collection is
tool-agnostic — each artifact's fields are read by *its build tool's manifest*:

- a docker build reads `context`, `tag`, `args` → an image reference;
- a go build reads `package`, `output`, `ldflags` → a binary.

```yaml
# building images with docker
artifacts:
  api:
    context: .
  ui:
    context: ./frontend
    tag: latest

# building a binary with go
artifacts:
  myapp:
    package: ./cmd/myapp
    output: bin/myapp
build:
  tool: go
```

An env overrides artifacts by key (the same merge rule): restate one to change a
field, add a new key, or `args: null` to drop a build arg.

## Pipeline

A pattern declares `pipeline: [...]` — the ordered list of **stages** it runs. The
stage vocabulary:

| stage | what it does | runs |
| --- | --- | --- |
| `check` | the verification suite (lint, tests, scans) | once |
| `build` | build each artifact | per artifact |
| `deliver` | load/push each artifact | per artifact |
| `scan` | vuln-gate the scan images | per scan image |
| `apply` | reconcile desired state (e.g. helm upgrade) | once |
| `wait` | block until healthy | once |

A forward **verb** runs the pipeline **up to and including its terminal stage**, so
**list order is gating order** — put `check` first and everything after it is gated
by the checks:

| verb | runs the pipeline up to & including |
| --- | --- |
| `stack check` | `check` |
| `stack build` | `build` |
| `stack deploy` | the last stage (the whole forward pipeline) |

With `pipeline: [check, build, scan, deliver, apply]`:

- `stack check` → check
- `stack build` → check, build
- `stack deploy` → check, build, scan, deliver, apply

A failing stage stops the run — a failed `check` never reaches `build`. A pattern
must declare a `pipeline` (it defines what the pattern does).

`down` and `status` are reverse/observe verbs, outside the forward pipeline. They
run the pattern's `teardown` / `status` step blocks. `stack down --destroy`
*additionally* runs the pattern's `destroy` step block (e.g. `kubectl delete pvc`)
after teardown — volume cleanup is opt-in via the flag, and the tool that performs
it is data. (`--destroy` with no `destroy:` block declared is an error, not a
silent no-op.)

## Step blocks

Each step a pattern runs is a block: a `tool` plus that step's config.

```yaml
build:
  tool: docker
deliver:
  tool: docker
  node: kind-control-plane
scan:
  tool: grype
  images: [api, ui]
  fail_on: high
apply:
  tool: helm
  chart: ./deploy/chart
  values: [./deploy/values.yaml]
  set:
    replicas: "3"
```

The fields a block accepts depend on its tool (the tool's manifest declares them
and rejects unknown keys — see [PLUGINS.md](PLUGINS.md)). The engine itself names
no tools: each stage runs whatever tool its block binds, so swapping `helm` for
`kustomize` in `apply` is a one-line change plus the relevant manifest.

The available step blocks are `build`, `deliver`, `scan`, `render`, `apply`,
`wait_ready`, `teardown`, `destroy`, `status`, `logs`.

## Checks

A pattern declares the verification suite it requires under `checks:` — a keyed map
of "run one tool, get pass/fail":

```yaml
checks:
  format:
    tool: gofmt
  lint:
    tool: golangci
    args:
      timeout: 5m
  unit:
    tool: go-test
    args:
      short: true
  vuln:
    tool: gosec
    blocking: false
  scan:
    tool: grype-image
    after: build
    args:
      target: "myapp:dev"
  ui:
    tool: pnpm-script
    dir: frontend
    args:
      script: build
```

Each check is one tool invocation. Fields:

| field | meaning |
| --- | --- |
| `tool` | the tool that runs the check |
| `args` | template inputs for the tool's check command |
| `blocking` | `false` → report-only (a failure doesn't fail the run); default blocking |
| `dir` | run from this subdirectory (e.g. a frontend package) |
| `after` | depends on a prior stage (e.g. an image scan that needs `build`); skipped in a standalone `stack check` |
| `serial` | must not run alongside other checks |

`stack check` runs them — independent checks in parallel, non-blocking ones
reporting without failing the run. When an app has one pattern, `stack check`
selects it automatically; with several, pass `--pattern <name>`. Because `check` is
also a pipeline stage, putting it first in a pattern's `pipeline` gates the rest of
that pattern's flow on the checks passing.

## Hooks

`hooks:` maps a name to a command, run from the repo root — the escape hatch for
app-specific steps (seed, migrate, …):

```yaml
hooks:
  seed:    "./deploy/seed.sh"
  migrate: "goose up"
```

## Template tokens

Two runtime tokens are resolved in **any** step-block config value, before the
tool's command renders:

- `{{ now_unix }}` — the current Unix timestamp.
- `{{ git_short_sha }}` — the short git SHA of HEAD.

They work anywhere a string appears — whole-value or embedded, at any depth
(scalars, list elements, map values):

```yaml
tag: "{{ git_short_sha }}"
apply:
  set:
    image.tag: "{{ git_short_sha }}"
    rolloutAt: "{{ now_unix }}"
  values: ["cfg-{{ now_unix }}.yaml"]
```

This is a fixed allowlist of two tokens, not arbitrary templating — any other
`{{...}}` in a config value passes through untouched (it belongs to the tool's
command template). git is only invoked when a git token is actually present.

## Inspecting the resolved config

`stack env` prints the resolved pattern for the current (or `--env`-selected)
environment — the merged identity, pipeline, and step → tool bindings — so the
result of the merge is always explicit on inspection. `--dry-run` on any verb
prints the exact commands it would run without executing them.
