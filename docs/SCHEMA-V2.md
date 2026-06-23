# stack — schema v2: patterns + uniform merge (DESIGN)

Status: built. This supersedes the app/env schema described in `DESIGN.md`.

> **Evolution note — `type` was removed.** Earlier drafts gave each pattern a
> `type:` (k8s/native/compose) that selected a hardcoded engine sequence. That's
> gone. A pattern now **IS its `pipeline` + step blocks**: each pipeline stage runs
> its step block's *tool* for the matching abstract step (`build` runs the `build:`
> block's tool — `go` or `docker`; the manifest knows how). The engine no longer
> switches on type. Tool-specific preamble (e.g. helm's repo-add + dependency-build)
> lives in the manifest as a general `pre:` mechanism, not engine code. Mentions of
> `type:` below are historical — read `pipeline:` + step blocks instead.

## Why

The v1 schema scattered one concern across two files by axis: scan *policy*
(`scan:` in app.yaml) sat apart from the scan *tool* (`scan-artifact: grype` in
env.yaml), and the same for build (`images` in app vs `build-artifact: docker` in
env) and apply (`apply` config in env). It read like duplication because the
nouns nearly collide, and you had to learn *which axis lives in which file*. The
env file was a grab-bag of identity + bindings + config.

**v2 principle: the schema and its override behavior must be intuitive.** One
merge rule, applied everywhere, with no special cases. If the rule is obvious, the
result is never surprising — that, not access locks, is what removes surprises.

## The model

- **app.yaml declares the *deployment shapes* the app supports** — `patterns.<name>`.
  Each pattern is a self-contained template: its images, tool wiring, scan policy,
  apply config, checks, and hooks, with sensible defaults filled in.
- **env.yaml selects one pattern and fills in / overrides the blanks** for that
  environment. It reads as "use the k8s pattern; here's what's different here."

Mental model: **app = the recipe; env = the ingredients for this kitchen.**

### Pattern name vs pattern type

A pattern's **name** is user-defined and arbitrary (`k8s`, `prod-cluster`,
`homelab`, `fast`). The env selects by name. The engine never hardcodes names.

A pattern's **type** is the engine contract — which abstract step sequence runs:

```yaml
patterns:
  k8s:          # MY name (the env selects this)
    type: k8s   # the ENGINE contract: build → deliver → scan → render → apply → wait
```

Types are the small closed set stack knows (`k8s`, `native`, `compose`). Names are
open. The same type may appear under multiple names — e.g. a `fast` pattern (type
k8s, no scan) and a `full` pattern (type k8s, with scan) differ in template
content, not engine type.

## The one merge rule

Resolution precedence: **env value ▸ pattern-template value ▸ built-in default.**

Merging, applied uniformly at every level:

- **maps merge by key** (override an existing key, or add a new one);
- **scalars replace** (env's value wins);
- **lists replace** (NOT appended — a list in env replaces the template's list);
- **null clears** (an explicit `null` in the env deletes that key from the result
  — the one way to *remove* an inherited value).

That's the entire rule. `images`, `scan`, `apply`, a step block like `deliver`,
the `set` map under apply — all merge the same way. There is nothing else to learn.

The null-clear exists because "maps merge by key" means an override can't otherwise
drop an inherited key. Concretely: the app's `baseline-mem0-api` image carries
`args: { PATCH_OLLAMA: 1 }`; the Pi uses the OpenAI variant which must NOT apply
that patch. `args: null` in the Pi's image override removes it:

```yaml
# template: baseline-mem0-api: { context: ./deploy/mem0-api, tag: ollama, args: { PATCH_OLLAMA: 1 } }
# env:      baseline-mem0-api: { tag: openai, args: null }
# result:   baseline-mem0-api: { context: ./deploy/mem0-api, tag: openai }   # args gone
```

Examples:

```yaml
# template:  deliver: { tool: docker, delivery: load }
# env:       deliver: { delivery: push }
# result:    deliver: { tool: docker, delivery: push }     # map merge by key

# template:  apply: { values: [deploy/local/values.yaml], repos: [...] }
# env:       apply: { values: [deploy/pi/values.yaml, deploy/pi/secrets.yaml] }
# result:    apply.values = [deploy/pi/values.yaml, deploy/pi/secrets.yaml]   # list REPLACES
#            apply.repos  = [...]                                             # untouched key kept

# template:  apply: { set: { rollmeTimestamp: "{{ now_unix }}" } }
# env:       apply: { set: { image.tag: "{{ git_short_sha }}" } }
# result:    apply.set = { rollmeTimestamp: ..., image.tag: ... }            # map merge → both
```

## What's app-global vs pattern-scoped

Only two fields are app-global. **Everything else is a pattern property** — including
the identity fields (`kube_context`, `namespace`, `registry`, `platform`, `tag`,
`image_delivery`, `remote`, `release_name`). A pattern template is the *complete*
deployment shape; the env merges overrides into it. There is no third "identity"
tier and no field that "lives elsewhere" — every field follows the one merge rule.

| Field | Scope | Why |
| --- | --- | --- |
| `name` | app-global | identity of the app |
| `tools_manager` | app-global | about your dev box (setup flow), not a deployment shape |
| identity (`kube_context`, `namespace`, `registry`, `platform`, `tag`, `image_delivery`, `remote`, `release_name`) | pattern | a property of the deployment shape; pattern may default, env overrides |
| everything else | pattern | `default_tag`, `images`, tool steps, `scan`, `apply`, `checks`, `hooks` |

The check/setup flows are pattern-independent at the *flow* level, but a pattern
*declares which checks it requires* (`patterns.<name>.checks`). A pattern may omit
`checks:` entirely — that's a valid "no checks required" shape.

**`stack check` pattern selection (resolved):** auto-select when the app has exactly
one pattern; require `--pattern <name>` when there are several. Checks are
pre-deploy and env-independent, so selection is by pattern name, not by env.

## Schema (baseline, fully worked)

```yaml
# .stack/app.yaml
name: baseline
tools_manager: asdf                # app-global

patterns:
  k8s:                             # name; envs select via `pattern: k8s`
    pipeline: [build, deliver, scan, apply]   # stage order (no `type`)

    default_tag: dev               # tag for images that don't pin their own

    artifacts:                     # keyed by name; map-merge friendly
      baseline:            { context: . }
      baseline-ui:         { context: ./frontend }
      baseline-postgresql: { context: ./deploy/postgres, tag: "16-pgvector" }
      baseline-mem0-api:   { context: ./deploy/mem0-api, tag: ollama, args: { PATCH_OLLAMA: "1" } }

    # tool wiring + per-step policy/config, each step in ONE block.
    build:   { tool: docker }
    deliver: { tool: docker, delivery: load }
    scan:    { tool: grype, images: [baseline, baseline-ui], fail_on: high }
    apply:
      tool: helm
      chart: deploy/charts/baseline
      values: [deploy/local/values.yaml]
      repos: [{ name: bitnami, url: https://charts.bitnami.com/bitnami }]
    wait_ready: { tool: helm }
    status:     { tool: kubectl }

    checks:                        # this pattern requires checks
      format:       { tool: gofmt }
      lint:         { tool: golangci, args: { timeout: 5m } }
      unit:         { tool: go-test, args: { short: true } }
      integration:  { tool: go-test, serial: true }
      scan-deps:    { tool: grype-src }
      sast:         { tool: gosec, blocking: false }
      vuln-go:      { tool: govulncheck, blocking: false }
      ui-typecheck: { tool: pnpm-script, dir: frontend, args: { script: typecheck } }
      ui-build:     { tool: pnpm-script, dir: frontend, args: { script: build } }

    hooks:
      seed: "./deploy/seed.sh"
```

```yaml
# .stack/local-k8s.yaml — selects k8s, fills this box's blanks
pattern: k8s
kube_context: docker-desktop
namespace: baseline
deliver: { node: desktop-control-plane }      # the one blank the template left
apply:
  set: { rollmeTimestamp: "{{ now_unix }}" }   # local-only force-rollout
```

```yaml
# .stack/pi.yaml — same pattern, the Pi's differences
pattern: k8s
kube_context: k3s
namespace: baseline
registry: 192.168.3.250:5000
platform: linux/arm64
tag: "{{ git_short_sha }}"
remote: true
deliver: { delivery: push }                    # override the template's `load`
images:
  baseline-mem0-api: { context: ./deploy/mem0-api, tag: openai }   # per-key override
apply:
  values: [deploy/pi/values.yaml, deploy/pi/secrets.yaml]          # list replaces
  set:
    image.registry: 192.168.3.250:5000
    image.tag: "{{ git_short_sha }}"
    ui.image.registry: 192.168.3.250:5000
    ui.image.tag: "{{ git_short_sha }}"
    postgres.image.registry: 192.168.3.250:5000
    postgres.image.repository: baseline-postgresql
    postgres.image.tag: 16-pgvector
    mem0.api.image: 192.168.3.250:5000/baseline-mem0-api:openai
    mem0.postgres.image: 192.168.3.250:5000/baseline-postgresql:16-pgvector
```

The scan duplication is gone: `scan` is one block (tool + images + threshold). The
env files are small and read as "k8s, but here's what's different."

## Artifacts (tool-agnostic build targets)

A pattern's build targets live in `artifacts:` (keyed by name), NOT `images:` —
the collection is tool-agnostic so it spans pattern types. Each artifact's fields
are read by its build TOOL's manifest:

- **docker** (k8s pattern): `context`, `tag`, `args` → an image ref.
- **go** (native pattern): `package`, `output`, `ldflags` → a binary
  (`go build -o {{.output}} {{.package}}`).

```yaml
# k8s pattern
artifacts:
  baseline: { context: ., tag: dev }

# native pattern (stack builds itself)
artifacts:
  stack: { package: ./cmd/stack, output: bin/stack }
build: { tool: go }
```

`stack build` runs ONLY the build-artifact step for the selected pattern: a k8s
pattern builds every image; a native pattern builds every binary. No
deliver/scan/apply. (Native currently supports build only; run/install are future
verbs.) stack's own `.stack/app.yaml` uses a native `local` pattern with the `go`
plugin to compile itself — the tool dogfooding the tool.

## Pipeline (stage order is data)

A pattern declares `pipeline: [...]` — the ordered list of fine-grained **stages**
it runs (`check`, `build`, `scan`, `deliver`, `apply`, `wait`). A forward **verb**
runs the pipeline **up to and including its terminal stage**, so list order IS
gating order — put `check` first and everything after it is gated by it.

```yaml
patterns:
  local:
    type: native
    pipeline: [check, build]   # `stack build` runs check, then build
    checks:    { format: { tool: gofmt }, unit: { tool: go-test } }
    artifacts: { stack: { package: ./cmd/stack, output: bin/stack } }
    build:     { tool: go }
```

Verb → terminal stage:

| verb | runs the pipeline up to & including |
| --- | --- |
| `check` | `check` |
| `build` | `build` |
| `deploy` | the last stage (the whole forward pipeline) |

So with `pipeline: [check, build, scan, deliver, apply]`:
`stack check` → check · `stack build` → check, build · `stack deploy` → all five.
A failing stage stops the run (a failed `check` never reaches `build`).

`down` and `status` are reverse/observe verbs — outside the forward pipeline. They
run the pattern's `teardown`/`status` step blocks directly. `down --destroy`
*additionally* runs the pattern's `destroy` step block (e.g. `kubectl delete pvc`)
after teardown — volume cleanup is opt-in via the flag, and the tool that does it
is data (a `--destroy` with no `destroy:` block declared is an error, not a silent
no-op). A pattern with **no** `pipeline:` is an error — a pattern IS its pipeline.

## Manifest `pre:` (tool preamble as data)

A manifest step may declare `pre:` — commands run before its main `command`, same
template inputs. It's the general mechanism for tool-specific setup, so the engine
has no per-tool glue. Each `pre:` entry optionally takes `for: <collection>` to run
once per item in that engine-provided collection (the item's fields become inputs);
without `for`, it runs once. Helm uses it for its chart-deps preamble:

```yaml
# helm.yaml
steps:
  apply:
    pre:
      - { for: repos, command: "helm repo add {{.name}} {{.url}}" }  # once per repo
      - "{{if .repos}}helm dependency build {{.chart}}{{end}}"       # once
    command: "helm upgrade --install {{.release}} {{.chart}} ..."
```

## How a stage runs (no `type`)

Each pipeline stage maps to an abstract step + a loop kind, fixed engine
vocabulary:

| stage | abstract step | loop |
| --- | --- | --- |
| `check` | (runs the check flow) | — |
| `build` | build-artifact | per artifact |
| `deliver` | deliver-artifact | per artifact |
| `scan` | scan-artifact | per scan-image |
| `apply` | apply | once |
| `wait` | wait-ready | once |

The stage runs its **step block's tool** for that abstract step. Per-artifact
stages pass the whole artifact's fields (ref/context/args/platform AND
package/output/ldflags) as inputs; each manifest template uses only what it needs
(`missingkey=zero`), so `docker` and `go` share one code path with no type switch.

## Template tokens (resolved in any config value)

Two runtime tokens are resolved in **every** step block's config — `{{ now_unix }}`
and `{{ git_short_sha }}` — at the single `stepInputs` choke point, before the
command template renders. They work anywhere a string appears (whole-value or
embedded, at any nesting depth — scalars, lists, maps, lists-of-maps):

```yaml
apply:
  set:
    image.tag: "{{ git_short_sha }}"     # nested map value
    rollmeTimestamp: "{{ now_unix }}"
  values: ["cfg-{{ now_unix }}.yaml"]    # list element (embedded)
tag: "v{{ git_short_sha }}"              # identity, embedded
```

It's an **allowlist** (only those two tokens), not arbitrary templating — any
other `{{...}}` in a config value passes through untouched (so a command-template
ref a tool's config might carry is left for the command template). git is only
shelled out when a git token is actually present. There is no per-stage special
case: this is config-layer behavior, uniform across all tools and stages.

## Engine internals (build notes)

- **The engine names zero tools.** Every command comes from a step block's tool
  via the manifest — `build`/`deliver`/`scan`/`apply`/`wait`/`teardown`/`destroy`/
  `status` all run `e.Step(...)`, which resolves the bound tool. There is no
  `docker`/`helm`/`kubectl` literal in the engine; a new deployment style is a new
  manifest + a pattern, no engine change.
- There is no `type`. A pattern IS its `pipeline` (stage order) + step blocks
  (per-stage tool + config). The object the engine consumes is the **resolved
  pattern** = `merge(built-in defaults, pattern template, env overrides)`.
- **Step → tool binding comes from the merged step blocks** (`build.tool`,
  `deliver.tool`, `scan.tool`, `apply.tool`, `wait_ready.tool`, `status.tool`)
  instead of a separate `tools:` map. A step block carries both its tool and that
  step's config/policy (e.g. `scan: { tool: grype, images: [...], fail_on: high }`).
- Identity fields (`kube_context`, `namespace`, …) are fields on the resolved
  pattern, merged into each step's template inputs exactly as before.

## Migration

1. Define the new types: `App{ name, tools_manager, patterns map[string]Pattern }`,
   `Pattern{ type, default_tag, images, build/deliver/scan/apply/... step blocks,
   checks, hooks }`, `Env{ pattern, identity fields, override blocks }`.
2. Implement the uniform deep-merge (`merge(template, env) → ResolvedPattern`).
3. Repoint the engine: `type` selects the sequence; steps read from merged blocks.
4. Migrate `.stack/*.yaml` for baseline (and stack's own) to v2.
5. Update DESIGN/CHECK-MODEL/README; keep v1 docs noted as superseded.
6. Tests: merge-rule table tests (map/scalar/list), pattern selection, the
   baseline deploy dry-run fixture (must still match `make local-up`), checks.
```
