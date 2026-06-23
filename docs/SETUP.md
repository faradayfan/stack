# `stack setup` — getting ready to run a pattern

How `stack` ensures the tools a pattern needs are present, at the versions the
repo pins — via a pluggable tools manager (asdf), with a declared fallback for
tools that have no manager plugin.

## The problem it solves

A pattern references tools in two places: its **checks** (a linter, a formatter, a
scanner) and its **step blocks** (docker, helm, kubectl, …). When one is missing,
you get a cryptic `exit 127` mid-run instead of a clear "install this" up front.
And the version a repo expects lives in its pin file (`.tool-versions` for asdf).
`stack setup` answers "am I ready to run this pattern?" in one command, honoring an
asdf-first, repo-pinned, no-global-versions workflow.

See [SCHEMA.md](SCHEMA.md) for the pattern, check, and step-block reference, and
[PLUGINS.md](PLUGINS.md) for the tool-manifest shape that this flow extends.

## What `stack setup` does

`stack setup` walks **every tool the selected pattern references** — both its check
tools and its step-block tools — and for each one:

- detects whether it's present and at what version;
- if the tool is **installable** (it declares a `setup:` method) and is missing or
  at the wrong version, installs it via the tools manager (asdf) or the tool's
  declared **unmanaged** fallback, then re-detects and verifies the version matches
  the pin — a mismatch fails with a clear message;
- if the tool is **presence-only** (a system tool like docker/helm/kubectl with no
  `setup:` method), reports whether it's on `PATH` — stack never tries to install
  it, but a missing one still means you're not ready.

Any required tool that isn't usable at the end leaves the run unsatisfied (non-zero
exit), so `stack setup --check` is a reliable readiness gate — for local onboarding
or in CI.

```
stack setup            # install missing / wrong-version tools, report the rest
stack setup --check    # doctor: report only, install nothing
stack setup --pattern <name>   # choose which pattern (when the app has several)
```

Flags:

- **`--check`** (doctor) is read-only: it reports what's missing or at the wrong
  version, with the exact fix per tool, and installs nothing. This is the cure
  for the cryptic-`127` failure.
- **`--pattern <name>`** selects which pattern's checks to consider when an app
  defines more than one.

A tool that ends up still unsatisfied — missing with no install method, or
installed but failing version verification — fails the run.

## The tools manager

A tools manager is selected app-wide via `tools_manager:` in `.stack/app.yaml`:

```yaml
# .stack/app.yaml (excerpt)
name: myapp
tools_manager: asdf
```

The manager owns the install mechanics; a tool manifest contributes only its
*identity* under that manager (its asdf plugin name) plus an unmanaged fallback.
This split keeps a tool manifest about *running* the tool and the manager
manifest about *installing* it.

Two install paths:

- **asdf-managed** — the tool has an asdf plugin. Its version comes from
  `.tool-versions`. `stack setup` adds the plugin (idempotently) and installs the
  pinned version.
- **unmanaged** — the tool has no asdf plugin. The manifest declares a literal
  install command (commonly `go install …@vX`) and pins the expected version
  itself, since it isn't in `.tool-versions`.

If `tools_manager` is unset, asdf-managed tools report that they need a manager
rather than silently global-installing — stack never guesses.

## The `setup:` block in a manifest

A tool manifest declares how it's installed under `setup:`. Exactly one install
method applies:

```yaml
# golangci-lint — asdf-managed; the asdf plugin name differs from the tool name
tool: golangci
detect: "golangci-lint version"
version_pattern: "version (\\d+\\.\\d+\\.\\d+)"
setup:
  asdf: golangci-lint          # asdf plugin name; version comes from .tool-versions
provides: [check]
steps:
  check:
    command: "golangci-lint run ./..."
```

```yaml
# gosec — no asdf plugin → unmanaged install, version pinned in the manifest
tool: gosec
detect: "gosec --version"
setup:
  unmanaged: "go install github.com/securego/gosec/v2/cmd/gosec@v2.22.5"
provides: [check]
steps:
  check:
    command: "gosec -quiet ./..."
```

```yaml
# gofmt — ships with the Go toolchain, so its presence rides on asdf's `golang`
tool: gofmt
detect: "go version"
setup:
  asdf: golang
provides: [check]
```

The `setup:` fields:

- **`asdf`** — the asdf plugin name. May differ from the tool name (the tool is
  `golangci`, the plugin is `golangci-lint`). The pinned version is read from
  `.tool-versions`.
- **`unmanaged`** — a literal install command for tools with no asdf plugin.
- **`version`** — the expected version for an unmanaged tool, used for
  verification (since it isn't in `.tool-versions`). Omit it when the tool can't
  report a clean version (see below).
- **`dir`** — a subdirectory from which to run `detect` and read the pin. This
  matters when a tool's version is pinned in a nested `.tool-versions` (e.g. a
  frontend subdir pinning its own node).

A manifest with no `setup:` block is reported as "install manually" — stack
can't manage what isn't described, and says so without failing the other tools.

## Version verification and pins

- For **asdf-managed** tools, the pinned version is read from `.tool-versions`
  (from the manifest's `setup.dir` if set, otherwise the repo root).
- For **unmanaged** tools, the pin is the manifest's `setup.version`.

Version comparison is loose: a leading `v` is stripped and a missing patch is
treated as `0`, so `2.12` matches `2.12.0`.

`detect` output formats vary per tool, so the engine extracts the version with
the manifest's `version_pattern` (a regex with one capture group) when set,
falling back to the first `X.Y(.Z)` token otherwise:

```yaml
detect: "golangci-lint version"
# golangci-lint version 2.12.2 built with go1.26.4 …
version_pattern: "version (\\d+\\.\\d+\\.\\d+)"
```

### Presence-only verification

Some installs report no clean version — for example a `go install`-ed tool that
prints `dev` instead of an embedded version. For these, omit `setup.version`: the
install command's pinned `@vX` is the version control, and stack verifies
presence rather than a version string. The tool is treated as satisfied once
`detect` succeeds.

### Honoring a tool's working directory

When a manifest sets `setup.dir`, both the version detection and the pin read
happen from that directory. This lets a tool pinned inside a subproject — a
frontend folder with its own `.tool-versions` pinning node — resolve correctly
even though the repo root pins different versions.

## The asdf manager manifest

The manager manifest owns the install mechanics and where versions come from:

```yaml
manager: asdf
detect: "asdf --version"          # is the manager itself present?
version_source: tool-versions     # versions come from .tool-versions
ops:
  # read the pinned version for a plugin from .tool-versions (empty if absent)
  pinned: "awk '$1==\"{{.plugin}}\"{print $2}' .tool-versions"
  # add the plugin (idempotent), then install + reshim the pinned version
  install: |
    asdf plugin add {{.plugin}} 2>/dev/null || true
    asdf install {{.plugin}} {{.version}}
    asdf reshim {{.plugin}}
```

`{{.plugin}}` is the tool manifest's `setup.asdf`; `{{.version}}` is the result
of the `pinned` op. A different manager ships its own `version_source` and `ops`;
nothing in the tool manifests changes.

## How `stack setup` resolves a tool

For each tool referenced by the selected pattern's checks:

1. Load the tool manifest's `setup:` and the selected manager.
2. **`setup.asdf`** — read the pinned version via the manager's `pinned` op over
   `.tool-versions`; run the tool's `detect`. If absent or the version differs
   from the pin, run the manager's `install` op with `{plugin, version}`.
3. **`setup.unmanaged`** — run `detect`. If absent (or the version differs from
   the manifest's declared `version`), run the unmanaged install command.
4. **No `setup:`** — report "install manually" and move on without failing other
   tools.
5. **Verify** — after any install, re-run `detect` and assert the version matches
   the pin (or, for presence-only tools, that it's now detected). A mismatch
   fails with a clear message.

`stack setup --check` performs steps 1–4's diagnosis and prints a per-tool table,
installing nothing.

Errors are actionable, naming the tool and the fix — for example a tool with no
setup method that isn't installed, or an asdf-managed tool whose `tools_manager`
isn't set.
