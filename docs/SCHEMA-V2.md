# stack — schema v2: patterns + uniform merge (DESIGN)

Status: agreed design, not yet built. This supersedes the app/env schema described
in `DESIGN.md` (which stays accurate for the v1 schema until the migration lands).

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
    type: k8s                      # engine contract

    default_tag: dev               # tag for images that don't pin their own

    images:                        # keyed by image name; map-merge friendly
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

## Engine internals (build notes)

- `type` replaces today's `pattern` field as the step-sequence selector. The
  engine maps `type` → ordered abstract steps; the pattern *name* is just a selector.
- The object the engine consumes is the **resolved pattern** =
  `merge(built-in defaults, pattern template, env overrides)`. Everything downstream
  (Step, ImageRef, scan loop, check runner) reads from it.
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
