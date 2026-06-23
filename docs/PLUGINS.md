# Writing a tool manifest (plugin)

How to teach `stack` about a new tool — `podman`, `kustomize`, `nerdctl`, a
linter — by writing a declarative YAML manifest. No Go, no compiled adapter, no
plugin runtime: a plugin is data the engine reads.

## The model

`stack` is "kubectl contexts, but for how you run the whole app." The engine
knows only **abstract steps** — a small, tool-agnostic vocabulary. A **plugin**
(a tool manifest) teaches the engine how one specific tool, at a specific version
range, performs a step. A **pattern** (in `.stack/app.yaml`) wires steps to tools
through its pipeline and step blocks, and the engine sequences them.

```
abstract steps   the CONTRACT — the vocabulary the engine speaks
       │ fulfilled by
tool manifests   PLUGINS — "tool X (version range Y) performs step Z via this command"
       │ wired by
pattern blocks   build: { tool: docker }, apply: { tool: helm }, …
```

Swapping `docker` for `podman`, or handling a tool's flag changes across major
versions, is adding or editing a manifest and naming the tool in a step block.
The engine never changes — it is a version-aware template renderer and sequencer.
All tool knowledge lives in manifests.

See [SCHEMA.md](SCHEMA.md) for the pattern and step-block reference; this document
is about the manifest side.

## The abstract-steps contract

These are the only steps the engine speaks. A manifest declares which of them a
tool `provides`.

| Step | Purpose |
| --- | --- |
| `build-artifact` | produce the deployable thing (image, binary, bundle) |
| `deliver-artifact` | get it where the runtime sees it (load to a node, push to a registry) |
| `scan-artifact` | vulnerability-gate an artifact |
| `render-config` | turn templates plus values into deployable manifests |
| `apply` | reconcile desired state into the target |
| `wait-ready` | block until the deployment is healthy |
| `teardown` | reverse an `apply` |
| `destroy` | remove persistent state (e.g. volumes) left after teardown |
| `status` | observe deployed state |
| `logs` | tail logs |
| `check` | run a repo check (lint, format, vet, scan) |

Each step has an input contract — the values the engine hands the manifest's
command template (see [Template inputs](#template-inputs)).

## Manifest structure

A manifest is one YAML file describing one tool. Here is the shape, field by
field:

```yaml
tool: docker                       # the tool's name; used in step blocks
detect: "docker version --format {{.Client.Version}}"   # prints the installed version
provides: [build-artifact, deliver-artifact]            # abstract steps this tool can perform

config:                            # config keys this tool accepts (for validation)
  - { name: platform }
  - { name: registry }

variants:                          # version-conditional command sets
  - when: ">=20.0"
    steps:
      build-artifact:
        command: "docker build -t {{.ref}} {{.context}}"

incompatible: "<19.0"              # versions stack refuses to drive
```

Fields:

- **`tool`** — the tool's name. This is what a pattern's step block names
  (`build: { tool: docker }`) and what `stack setup` keys on.
- **`detect`** — a command whose stdout contains the installed version. The
  engine runs it to pick a variant and to verify setup. Keep it cheap and
  side-effect-free.
- **`provides`** — the abstract steps this tool can perform. A tool may provide
  several (helm provides `render-config`, `apply`, `wait-ready`, `teardown`,
  `status`).
- **`steps`** or **`variants`** — the command templates (see below). Use
  top-level `steps` when the tool's commands don't change across versions; use
  `variants` when they do.
- **`config`** — the config keys a step block may carry for this tool, used for
  unknown-key validation (see [Config validation](#config-validation)).
- **`incompatible`** — a version range stack refuses to drive, failing with a
  clear message instead of guessing.
- **`setup`** — how `stack setup` installs and version-verifies the tool. See
  [SETUP.md](SETUP.md).
- **`version_pattern`** — an optional regex (one capture group) to extract the
  version from `detect` output when the first `X.Y(.Z)` token isn't the right
  one. Also covered in [SETUP.md](SETUP.md).

### Steps and the `command` template

A step entry has a `command` — a Go `text/template` string the engine renders
with the step's inputs and runs. The simplest manifest is just steps plus
commands:

```yaml
tool: grype
detect: "grype version -o text"
provides: [scan-artifact]
steps:
  scan-artifact:
    command: "grype {{.target}}"
```

When a tool's commands are stable across versions, declare `steps` at the top
level (as above) and the engine ignores the version when selecting a command.

### Variants — version-conditional commands

When a tool's flags or subcommands shift across major versions, group the command
sets under `variants`, each gated by a `when` range. The engine detects the
installed version and picks the **first** variant whose `when` matches:

```yaml
tool: docker
detect: "docker version --format {{.Client.Version}}"
provides: [build-artifact, deliver-artifact]
variants:
  - when: ">=20.0"
    steps:
      build-artifact:
        command: "docker build -t {{.ref}} {{.context}}"
      deliver-artifact:
        command: "docker push {{.ref}}"
incompatible: "<19.0"
```

Order matters: list narrower or higher ranges first if they could overlap, since
the first match wins. See [Version handling](#version-handling).

### The `pre:` mechanism

A step can declare `pre:` — commands that run **before** its main `command`, with
the same template inputs. This is the general escape hatch for tool-specific
preamble, keeping that knowledge in the manifest rather than the engine.

Each `pre:` entry is either a bare command string (run once) or an object. With
`for: <collection>`, the command runs **once per item** in that engine-provided
collection, and the item's fields become template inputs for that iteration;
without `for`, it runs once.

Helm's apply illustrates both forms — add each declared chart repo, then build
dependencies once:

```yaml
tool: helm
detect: "helm version --template {{.Version}}"
provides: [render-config, apply, wait-ready, teardown, status]
config:
  - { name: chart }
  - { name: values }
  - { name: set }
  - { name: repos }
steps:
  apply:
    pre:
      # once per item in `repos` (each item exposes name + url)
      - { for: repos, command: "helm repo add {{.name}} {{.url}}" }
      # once, only when repos were declared
      - "{{if .repos}}helm dependency build {{.chart}}{{end}}"
    command: >-
      helm upgrade --install {{.release}} {{.chart}}
      --kube-context {{.kube_context}} -n {{.namespace}} --create-namespace
      {{range .values}}-f {{.}} {{end}}{{range $k, $v := .set}}--set {{$k}}={{$v}} {{end}}
```

A `pre:` command that renders to empty (the `{{if .repos}}…{{end}}` above when
`repos` is absent) is skipped.

### Config validation

`config` lists the config keys a step block may carry for this tool. The engine
rejects **unknown keys** in a binding before running anything, catching typos
early. A manifest with no `config` block accepts any keys (no validation).

```yaml
config:
  - { name: chart }
  - { name: values }
  - { name: set }
  - { name: repos }
```

Keys are not marked required for multi-step tools: helm provides `apply` (which
needs `chart`) alongside `wait-ready` and `status` (which don't), so a blanket
required would wrongly reject a bare `wait-ready: { tool: helm }`. A genuinely
missing value surfaces a clear error at the step that needs it.

## Template inputs

Command and `pre:` templates are Go `text/template`, rendered with
`missingkey=zero` — a manifest can reference inputs it doesn't use and they
render empty rather than erroring. This is what lets one input bag serve every
tool: docker reads `ref`/`context`, go reads `package`/`output`, and each ignores
the other's keys.

The engine composes each step's inputs from three sources:

1. **Pattern identity** — `kube_context`, `namespace`, `registry`, `platform`,
   image-delivery mode, and similar fields set on the pattern.
2. **The step block's config** — everything under the `build:`/`apply:`/etc.
   block in the pattern (the keys your `config:` list validates), with runtime
   tokens already resolved.
3. **Engine-computed dynamics** — values the engine derives per stage, such as
   the per-artifact image `ref`, `context`, `args`, `platform`, `package`,
   `output`, `ldflags` for build/deliver; `target` and `fail_on` for scan;
   `release` for apply/wait. For a `pre:` entry with `for:`, the iterated item's
   fields are added on top.

In practice, reference whatever inputs your command needs and let the others fall
away. The contract is intentionally small: push genuinely tool-specific knobs
into the manifest as literals instead of asking the engine to supply more inputs.

## Version handling

`when` and `incompatible` use simple, space-separated range constraints — this is
plain CLI-version comparison, not a full semver constraint solver.

- Each token is an operator (`>=`, `>`, `<=`, `<`, `=`) followed by a version;
  a bare version means exact match.
- Multiple tokens in one range are ANDed: `">=20.0 <24.0"` means
  `>= 20.0 and < 24.0`.
- Versions parse loosely: a leading `v` is stripped, a missing patch is treated
  as `0` (`2.12` equals `2.12.0`), and any `-prerelease`/`+build` suffix is
  ignored.

Selection:

- `incompatible` is checked first — a matching version fails with a clear message
  naming the tool and the range.
- Otherwise the engine picks the **first** `variant` whose `when` matches the
  detected version. List variants from most specific to least so the right one
  wins.
- A manifest with top-level `steps` (no variants) is version-independent.

## A complete example

A new manifest for `podman` providing image build and delivery. Podman's CLI
mirrors docker's, so the commands are familiar; the point is the full shape from
scratch:

```yaml
tool: podman
detect: "podman version --format {{.Client.Version}}"
provides: [build-artifact, deliver-artifact]

# Config keys a step block may carry for podman. Unknown keys are rejected.
config:
  - { name: platform }
  - { name: registry }

variants:
  # Modern podman: buildx-style platform flag and a single build/push path.
  - when: ">=4.0"
    steps:
      build-artifact:
        # `ref` is the engine-computed image reference; `context`, `args`,
        # `platform` come from the artifact and pattern. Unused inputs render
        # empty (missingkey=zero), so the conditionals stay clean.
        command: >-
          podman build {{if .platform}}--platform {{.platform}} {{end}}
          {{range $k, $v := .args}}--build-arg {{$k}}={{$v}} {{end}}
          -t {{.ref}} {{.context}}
      deliver-artifact:
        command: "podman push {{.ref}}"

  # Older podman: no --platform support; push is unchanged.
  - when: ">=3.0 <4.0"
    steps:
      build-artifact:
        command: >-
          podman build {{range $k, $v := .args}}--build-arg {{$k}}={{$v}} {{end}}
          -t {{.ref}} {{.context}}
      deliver-artifact:
        command: "podman push {{.ref}}"

# Refuse to drive ancient podman rather than guess at its flags.
incompatible: "<3.0"

# How `stack setup` installs and verifies podman. See SETUP.md.
setup:
  asdf: podman
```

A pattern then uses it by naming the tool in a step block:

```yaml
# .stack/app.yaml (excerpt)
patterns:
  k8s:
    pipeline: [build, deliver, apply]
    build:   { tool: podman }
    deliver: { tool: podman }
    apply:   { tool: helm }
```

A second example — `kustomize` providing config rendering and apply — shows a
version-independent tool (top-level `steps`, no variants):

```yaml
tool: kustomize
detect: "kustomize version"
provides: [render-config, apply]
config:
  - { name: dir }
steps:
  render-config:
    command: "kustomize build {{.dir}}"
  apply:
    command: >-
      kustomize build {{.dir}} |
      kubectl --context {{.kube_context}} -n {{.namespace}} apply -f -
```

## Where manifests live

The built-in manifest set is embedded in the `stack` binary, so docker, helm,
grype, go, kubectl and the rest are available out of the box with no external
files. A manifest is just one `.yaml` file matching the structure above — the
engine discovers tools by name from the embedded set when a pattern's step block
references them.
