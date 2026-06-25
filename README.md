# stack

A small CLI for running and deploying an app — "kubectl contexts, but for *how you
run the whole app*." You describe your app's build and deploy shape once, in
declarative context files, and `stack` drives the underlying tools (docker, helm,
kubectl, grype, go, …) for you.

```console
$ stack deploy --env prod
$ stack build               # just compile/build the artifacts
$ stack down --destroy      # tear down, drop volumes
$ stack check               # run the verification suite (lint, tests, scans)
```

## Why

Most projects accrete an orchestration Makefile (or a pile of shell scripts) that
builds images, scans them, loads or pushes them, renders a chart, and applies it —
plus a parallel set of targets for each environment. The shape is ~the same across
projects, but the names and details drift, and every tool-flag change means editing
scripts.

`stack` replaces that with **one fixed vocabulary** and **declarative context**.
The deployment shape lives in data; swapping docker for podman, or handling a CLI's
flag changes across versions, is a config/manifest edit — not a code change.

## How it works

Three layers:

1. **Abstract steps** — the engine speaks a fixed vocabulary: `build`, `deliver`,
   `scan`, `apply`, `wait`, `teardown`, `destroy`, `status`, `check`. It knows
   nothing about any specific tool.
2. **Plugins** — declarative YAML manifests teach the engine how a specific
   **tool + version** performs a step (e.g. `docker >=24` builds with
   `buildx build`). One manifest per tool; built-ins ship in the binary. See
   [docs/PLUGINS.md](docs/PLUGINS.md).
3. **Patterns** — your `.stack/app.yaml` declares one or more named *patterns* (a
   deployment shape: a pipeline of stages, the artifacts to build, and which tool
   runs each step). A pattern is the runnable unit. Optional per-environment files
   select a pattern and override the bits that differ between environments. See
   [docs/SCHEMA.md](docs/SCHEMA.md).

The engine is a **version-aware template renderer + sequencer** — it names no
tools; every command comes from a manifest. Adding a deployment style is new data,
not new code.

## Install

```console
$ go install github.com/faradayfan/stack/cmd/stack@latest
```

Requires Go 1.26+. `stack` shells out to the tools your patterns reference (docker,
helm, etc.); `stack setup` installs the ones your checks need (see
[docs/SETUP.md](docs/SETUP.md)).

## Quick start

Create `.stack/app.yaml` describing how your app builds and deploys:

```yaml
name: myapp
tools_manager: asdf

patterns:
  k8s:
    # The ordered stages this pattern runs. `stack build` runs up to `build`;
    # `stack deploy` runs the whole list. Put `check` first to gate on the checks.
    pipeline: [build, deliver, scan, apply]

    namespace: myapp
    image_delivery: load     # load into the local cluster's containerd
    default_tag: dev

    # What to build, keyed by name.
    artifacts:
      myapp:
        context: .

    # Which tool runs each step (+ that step's config).
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
    wait_ready:
      tool: helm
    teardown:           # stack down
      tool: helm
    destroy:            # stack down --destroy (drops PVCs)
      tool: kubectl
    status:             # stack status
      tool: kubectl
```

Then drive it. If the app has a single pattern, `stack` runs it directly — no env
file required:

```console
$ stack env                        # show the resolved pattern + step → tool
$ stack deploy --dry-run           # print the exact commands without running them
$ stack deploy                     # build → deliver → scan → apply
$ stack status
$ stack down --destroy
```

When you have environments that differ (a local cluster vs. a remote registry),
add an env file that selects the pattern and overrides what changes:

```yaml
# .stack/prod.yaml
pattern: k8s
kube_context: prod
registry: registry.example.com
image_delivery: push
```

```console
$ stack deploy --env prod          # use the prod overrides
$ stack use prod                   # or set it as the current context (kubectl-style)
$ stack deploy                     # ...then bare commands use it
```

## Commands

| Command | What it does |
| --- | --- |
| `stack use <env>` | Select the current environment for this repo (stored per-user, not committed). |
| `stack env` | Show the resolved pattern and its step → tool bindings. |
| `stack build` | Run the pipeline up to and including the `build` stage (artifacts only). |
| `stack deploy` | Run the full forward pipeline. |
| `stack down [--destroy]` | Tear down; `--destroy` also runs the pattern's `destroy` step (e.g. drop volumes). |
| `stack status` | Show the running app. |
| `stack check [--pattern <name>]` | Run the verification suite declared by the pattern (lint, tests, scans). |
| `stack setup [--check]` | Get ready to run the pattern: install/verify every tool it needs (checks + deploy tools) at pinned versions; `--check` only diagnoses. |
| `stack version` | Print the version (also `stack --version`). |

stack resolves which pattern to run in this order: `--pattern <name>` (a pattern
from app.yaml directly, no env overrides) ▸ `--env <name>` (an env file) ▸ the
current context from `stack use` ▸ the sole pattern when the app has exactly one.
So an env file is optional — you only need one for per-environment overrides.

Global flags: `--pattern <name>` runs a pattern directly; `--env <name>` selects an
environment file; `--dry-run` prints the rendered commands instead of running them.
(`--pattern` and `--env` are mutually exclusive.)

## Documentation

- **[docs/SCHEMA.md](docs/SCHEMA.md)** — the context-file schema: patterns,
  pipelines, artifacts, step blocks, the merge rules, template tokens, and checks.
- **[docs/PLUGINS.md](docs/PLUGINS.md)** — writing a tool manifest (add support for
  a new tool).
- **[docs/SETUP.md](docs/SETUP.md)** — the `stack setup` tools-manager flow.

## License

[MIT](LICENSE)
