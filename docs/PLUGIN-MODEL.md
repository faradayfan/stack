# stack — plugin & pattern model (DRAFT)

How stack stays small while supporting many tools and deployment styles. The
core idea: **the engine knows only abstract steps; plugins (declarative tool
manifests) teach it how a specific tool+version performs a step; the environment
context wires steps → tools.** No compiled adapters, no plugin runtime — plugins
are data the engine reads.

> Status: design for review. Companion to `DESIGN.md` (commands, context files,
> patterns overview). This doc defines the extension architecture.

## The three layers

```
┌─ Abstract steps ─────────────┐   the CONTRACT. tool-agnostic vocabulary.
│  build-artifact, deliver,    │   patterns are ordered selections of these.
│  scan, render-config, apply, │   the engine only ever speaks these.
│  wait-ready, teardown,       │
│  status, logs, hook          │
└──────────────┬───────────────┘
               │ fulfilled by
┌──────────────▼───────────────┐   PLUGINS = declarative tool manifests.
│  Tool manifests (plugins)    │   "tool X (version range Y) performs step Z
│  docker.yaml, helm.yaml,     │    via this command, mapping these inputs."
│  grype.yaml, compose.yaml,   │   one file per tool; version ranges inside.
│  kubectl.yaml, kustomize.yaml│
└──────────────┬───────────────┘
               │ selected by
┌──────────────▼───────────────┐   ENVIRONMENT CONTEXT binds step → tool.
│  .stack/<env>.yaml            │   explicit: `build: docker`, `apply: helm`.
│  build: docker                │   no magic — you always know what runs.
│  apply: helm                  │
└──────────────────────────────┘
```

Swapping docker→podman, helm→kustomize, or handling docker v23 vs v27 flag
changes = **add/edit a manifest + change one line of env context.** The engine
never changes.

## Layer 1 — Abstract steps (the contract) — LOCKED

Every deployment flow decomposes into these. Each step has a defined input
contract (what the engine hands the plugin) and is tool-agnostic.

| Step | Purpose | Engine-provided inputs (illustrative) |
| --- | --- | --- |
| `build-artifact` | produce the deployable thing (image / binary / bundle) | `tag`, `context`, `args`, `platform` |
| `deliver-artifact` | get it where the runtime sees it (load to node / push registry) | `tag`, `delivery` (load\|push), `node`, `registry` |
| `scan-artifact` | vuln-gate the artifact | `target` (image\|dir), `fail_on` |
| `render-config` | templates + values → deployable manifests | `chart`/`dir`, `values[]`, `set{}` |
| `apply` | reconcile desired state into the target | `name`, `namespace`, `kube_context`, rendered config |
| `wait-ready` | block until healthy | `name`, `namespace`, `kube_context`, `timeout` |
| `teardown` | reverse `apply` | `name`, `namespace`, `destroy` (drop volumes?) |
| `status` | observe state | `namespace`, `kube_context` |
| `logs` | tail logs | `component?`, `namespace`, `kube_context` |
| `resolve-secrets` | fetch declared secrets from a store, inject into apply/render (helm `--set` or ephemeral values file), never persist | `refs{}` (name → `op://…`), `vault` |
| `hook` | app-specific escape hatch (seed, migrate) | engine sets the env-var mappings the hook DECLARES (explicit, not an implicit blob) — see DESIGN.md "Hook contract". A hook is "the app's own plugin": same command + explicit-input-mapping shape as a tool manifest. |

A **pattern** is an ordered selection (from `DESIGN.md`):
- `native`: `build-artifact`(binary) → [`apply`(compose, for infra)] → run
- `k8s`: `build-artifact` → `scan-artifact` → `deliver-artifact` → `render-config` → `apply` → `wait-ready`
- `compose`: `build-artifact` → `scan-artifact` → `apply`(compose up) → `wait-ready`

`teardown`/`status`/`logs` are the inverse/observe verbs each pattern also wires.

## Layer 2 — Plugin = declarative tool manifest — DECIDED: one file per tool, version ranges inside

A plugin is a YAML file describing a system tool: its role (which abstract steps
it can perform), how to detect its version, and — **per version range** — the
command template + input mapping for each step it provides.

```yaml
# plugins/docker.yaml
tool: docker
detect: "docker version --format '{{.Client.Version}}'"   # → e.g. 27.1.1
provides: [build-artifact, deliver-artifact]

# Version-conditional command variants. The engine detects the installed
# version and picks the FIRST matching range. This is the docker-CLI-semantics
# point: flags/subcommands shift across majors.
variants:
  - when: ">=24.0"
    steps:
      build-artifact:
        # {{.x}} are engine-provided inputs for the step (see Layer 1 table).
        command: >-
          docker buildx build {{if .platform}}--platform {{.platform}}{{end}}
          -t {{.tag}} {{range $k,$v := .args}}--build-arg {{$k}}={{$v}} {{end}}
          {{if eq .delivery "push"}}--push{{else}}--load{{end}} {{.context}}
      deliver-artifact:
        # buildx with --push/--load already delivered during build → no-op.
        command: "true"
  - when: ">=20.0 <24.0"
    steps:
      build-artifact:
        command: "docker build -t {{.tag}} {{range $k,$v := .args}}--build-arg {{$k}}={{$v}} {{end}}{{.context}}"
      deliver-artifact:
        command: >-
          {{if eq .delivery "push"}}docker push {{.tag}}{{else}}
          docker save {{.tag}} | docker exec -i {{.node}} ctr -n k8s.io images import -{{end}}

incompatible: "<20.0"   # fail clearly with a message, don't guess
```

Other manifests follow the same shape:
- `helm.yaml` → `provides: [render-config, apply, wait-ready, teardown, status]`
- `grype.yaml` → `provides: [scan-artifact]`
- `compose.yaml` → `provides: [apply, teardown, status, logs]` (pattern: compose)
- `kubectl.yaml` → `provides: [apply, wait-ready, status, logs, teardown]` (kustomize/raw-manifest path)

**Secret providers are the same plugin shape** — they `provides: [resolve-secrets]`.
The env context's `secrets:` block (see DESIGN.md) binds to one. Per-secret fetch:
the engine calls the provider's resolve command once per declared reference and
injects the result downstream (helm `--set`), never persisting it.

```yaml
# plugins/onepassword.yaml
provider: onepassword
detect: "op --version"
provides: [resolve-secrets]
# Engine runs this per declared secret ref; stdout is the secret value, used as
# a helm --set value (or written to an ephemeral values file, then shredded).
# Auth is whatever `op` session exists (desktop/biometric local; service-account
# token headless) — the manifest doesn't hardcode it.
resolve-secrets:
  command: "op read {{.ref}}"          # {{.ref}} = an op://vault/item/field
  per: ref                             # invoked once per reference (vs bulk)

# Fallback: plugins/file.yaml → provider: file, resolve-secrets is a no-op pass
# of an existing values file via `-f` (the rare escape hatch).
```

**Manifest sourcing:** ship a built-in set inside the binary (embedded) for the
common tools; allow repo-local overrides in `.stack/plugins/` and (future)
user-level `~/.config/stack/plugins/`. So you get docker/helm/grype out of the
box, and can drop in `podman.yaml` without forking.

**Per-tool config schema (validation).** A manifest declares the config keys a
tool accepts under its env binding (`tools.<step>.config`), so stack validates
the binding up front — rejecting **unknown keys** (typo-catching) before any
command runs. `required` is intentionally NOT used for multi-step tools (helm
provides apply+wait-ready+teardown+status; `chart` is needed only by apply, so a
blanket required would wrongly reject a bare `wait-ready: helm`); a genuinely
missing value fails clearly at its step.

```yaml
# helm.yaml
config:
  - { name: chart }     # unknown-key rejection applies uniformly
  - { name: values }
  - { name: set }
  - { name: repos }
```

The env binding then carries the config, validated against this schema:

```yaml
# .stack/local-k8s.yaml
tools:
  apply:
    tool: helm
    config: { chart: deploy/charts/x, values: [v.yaml] }   # a typo'd key errors
```

## Layer 3 — Environment context binds step → tool — DECIDED: explicit in context

The `.stack/<env>.yaml` (from `DESIGN.md`) gains an explicit `tools:` mapping.
No auto-detection of *which* tool — you state it, so a deploy is never a surprise.
(The engine still auto-detects the *version* of the chosen tool.)

```yaml
# .stack/local-k8s.yaml  (excerpt — full schema in DESIGN.md)
pattern: k8s
tools:
  build-artifact: docker      # → loads plugins/docker.yaml
  deliver-artifact: docker
  scan-artifact: grype
  render-config: helm
  apply: helm
  wait-ready: helm            # (helm --wait) or kubectl
  status: kubectl
  logs: kubectl
kube_context: docker-desktop
image_delivery: load
# … chart/values/etc per DESIGN.md
```

A pattern MAY ship default bindings (k8s → docker+helm+grype+kubectl) so a
context only specifies overrides — but the resolved binding is always printable
via `stack env` so it's explicit-on-inspection even when defaulted.

## How a step executes (the engine's whole job)

For each step in the pattern's sequence:
1. Look up the bound tool from the env context (`tools.<step>`).
2. Load that tool's manifest; run `detect` to get the installed version.
3. Pick the `variant` whose `when` range matches (or fail on `incompatible`).
4. Render the step's `command` template with the engine-provided inputs.
5. Run it (or print it under `--dry-run`); fail the flow on non-zero unless the
   step is marked soft.

That's it. The engine is a **version-aware template renderer + sequencer**. All
tool knowledge is data.

## Why this holds up (and where it could rot)

**Strengths**
- Engine stays tiny and stable; tool/version churn is data, not code.
- The exact docker-semantics-drift problem you raised is a first-class feature.
- Contributing a tool (podman, nerdctl, kustomize) = one YAML file, no Go.
- `--dry-run` is trivially honest — it just prints the rendered commands.

**Risks to watch (don't over-engineer past these)**
- **Input-contract creep:** if steps need ever-more inputs to express every
  tool's flags, the contract bloats. Mitigation: keep inputs minimal; push
  genuinely tool-specific knobs into the manifest as literals, not engine inputs.
- **Template-as-programming-language:** command templates with heavy
  conditionals become unreadable mini-programs. Mitigation: if a tool needs real
  logic, that's the signal to allow a manifest to shell to a script for that step
  (the `hook` escape hatch, generalized) rather than growing the template DSL.
- **Pivoting on stringly version ranges** is fine for CLIs; don't build a
  full semver constraint solver — simple `>=x <y` comparison is enough.

## Open questions (carry into build)

- **Manifest precedence:** embedded < repo `.stack/plugins/` < user config — confirm.
- **`detect` failure:** tool absent → fail the flow with an actionable message
  ("docker not found; bound to build-artifact in local-k8s") vs. skip.
- **Soft steps:** which steps may fail without failing the flow (e.g. `prereqs`
  warnings, scan in a `--no-scan` mode)?
- **Where version detection is cached** (per-invocation is fine; don't persist).
- Naming/state questions still open in `DESIGN.md`.
