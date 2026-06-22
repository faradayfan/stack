# stack — the setup flow (tools manager) — DRAFT

How `stack` ensures the tools its checks/steps need are installed, at the versions
the repo pins — via a pluggable **tools manager** (asdf first). Companion to
DESIGN.md (deploy), PLUGIN-MODEL.md (plugins), CHECK-MODEL.md (verify).

> Status: design for review. Decisions below are settled (see "Decided").

## The problem this solves

`stack check` declares the tools it needs (golangci-lint, gosec, grype, node…),
but if one isn't installed you get a cryptic `exit 127`. And the version a repo
needs lives in `.tool-versions` (asdf) — stack should make "install what this repo
needs, at the pinned version" a single command, honoring the asdf-first,
repo-pinned, avoid-global-versions workflow.

## The model: a tools manager is a new plugin KIND

Parallel to tool plugins (how to RUN a step) and secret providers (how to RESOLVE
a secret), a **tools manager** declares how a version manager performs SETUP
operations — detect a tool, install it, read its pinned version. The repo config
selects one (`tools_manager: asdf`). `stack setup` uses it.

```
tool plugins      → how tool X runs an abstract step      (docker.yaml, helm.yaml)
secret providers  → how store X resolves a secret          (onepassword.yaml)
tools managers    → how manager X installs/verifies tools  (asdf.yaml)   ← NEW
```

This keeps the split clean: a **tool manifest** stays about *running* the tool; the
**manager manifest** owns the *install mechanics*; the tool manifest contributes
only its *identity under that manager* (its asdf plugin name) + an unmanaged
fallback. Adding mise/nix later = one new manager manifest, zero tool-manifest
rewrites.

## Decided

- **Tools manager = a plugin kind**, selected per repo via `tools_manager:` in the
  env/app config. asdf is the first (and the user's primary).
- **`stack setup`** walks the tools the repo's checks/steps need; for each: detect
  presence + version via the manager; if missing or wrong-version, install via the
  manager (asdf) or the tool's declared **unmanaged fallback** (for tools with no
  asdf plugin, e.g. gosec/govulncheck → `go install …@version`).
- **No tools manager set → `stack setup` errors**, listing the tools to install
  manually. It never guesses or silently global-installs.
- **`stack setup --check` (doctor)** is read-only: report what's missing /
  wrong-version + the exact fix per tool. Safe; kills the cryptic-127 problem.
- **Version is verified against the pin** (`.tool-versions` for asdf-managed tools,
  the manifest's declared version for unmanaged ones). A mismatch FAILS — however a
  tool was installed, stack confirms it ended up at the pinned version. This is the
  reproducibility guardrail.

## Version extraction — `version_pattern`

`detect` output formats vary wildly per tool, so comparing detected-vs-pinned
needs per-tool extraction:

- `golangci-lint version` → `golangci-lint has version 2.12.2 built with go1.26.4…`
- `gosec --version`       → `Version: 2.22.5 …`
- `node --version`        → `v26.3.0`

Each tool manifest declares a `version_pattern` (a regex with one capture group)
to pull the version from `detect` output. When omitted, the engine falls back to
the first `X.Y(.Z)`-looking token. The captured string is parsed by the same
loose semver parser the deploy-side variant matcher uses (strips a leading `v`).

```yaml
# golangci.yaml
detect: "golangci-lint version"
version_pattern: 'version ([0-9]+\.[0-9]+\.[0-9]+)'
```

## Tool manifest gains a `setup:` block

The minimum per-tool setup identity. Two cases:

```yaml
# golangci.yaml — managed by asdf (plugin name differs from the tool name)
tool: golangci
detect: "golangci-lint version"
setup:
  asdf: golangci-lint          # asdf plugin name; version comes from .tool-versions
provides: [check]
steps: { check: { command: "golangci-lint run …" } }

# gosec.yaml — NO asdf plugin → unmanaged fallback (pin the version for reproducibility)
tool: gosec
detect: "gosec --version"
setup:
  unmanaged: "go install github.com/securego/gosec/v2/cmd/gosec@v2.22.5"
provides: [check]

# grype.yaml — has an asdf plugin, plugin name == tool name
tool: grype
setup: { asdf: grype }
```

A tool with no `setup:` block at all → `stack setup` reports it as "install
manually" (can't manage what isn't described).

## The asdf manager manifest

The manager manifest owns the install mechanics + where versions come from.

```yaml
# managers/asdf.yaml
manager: asdf
detect: "asdf --version"          # is the manager itself present?
version_source: tool-versions     # versions come from .tool-versions
ops:
  # add the plugin (idempotent), then install the pinned version.
  install: |
    asdf plugin add {{.plugin}} 2>/dev/null || true
    asdf install {{.plugin}} {{.version}}
    asdf reshim {{.plugin}}
  # read the pinned version for a plugin from .tool-versions.
  pinned: "awk '$1==\"{{.plugin}}\"{print $2}' .tool-versions"
  # detect the installed version (delegates to the tool's own detect).
```

`{{.plugin}}` = the tool manifest's `setup.asdf`; `{{.version}}` = the result of
`pinned`. Another manager (mise/nix) ships its own `ops` + `version_source`.

## How `stack setup` resolves (asdf example)

For each tool the repo's checks/steps reference:

1. Load the tool manifest's `setup:` + the selected manager (`asdf.yaml`).
2. **If `setup.asdf`:** read the pinned version (`asdf.yaml`'s `pinned` op over
   `.tool-versions`); run the tool's `detect`; if absent or version≠pin → run the
   manager's `install` op with `{plugin, version}`.
3. **If `setup.unmanaged`:** run `detect`; if absent (or version≠the manifest's
   declared version) → run the unmanaged install command.
4. **If no `setup:`:** report "install manually" (do not fail other tools).
5. **Verify:** after install, re-run `detect` and assert the version matches the
   pin / declared version. Mismatch → fail with a clear message.

`stack setup --check` does steps 1–4's *diagnosis* and prints a table, installing
nothing.

```
stack setup            # install missing/wrong-version tools via the manager
stack setup --check    # doctor: report only, no install
stack setup --dry-run  # print the manager commands it would run
```

Errors are actionable: "tool `gofumpt` has no setup method and is not installed —
add a `setup:` block or install it manually." / "tools_manager not set in
.stack/<env>.yaml — set one (e.g. asdf) or install these manually: […]."

## Why this holds up (and where it could rot)

**Strengths**
- The cryptic-127 failure becomes a clear, fixable report.
- asdf-first + repo-pinned + version-verified — exactly the reproducibility workflow.
- Manager-agnostic by data: mise/nix = a new `managers/*.yaml`, no engine change.
- Clean separation: run (tool) vs install (manager) vs identity (tool's `setup:`).

**Risks to watch (don't over-engineer past these)**
- **asdf plugin-name drift:** the `setup.asdf` name must match asdf's plugin
  registry. It's a string the manifest declares; if asdf renames a plugin, the
  manifest updates. Acceptable — it's one field.
- **Unmanaged version drift:** an `unmanaged: "…@v2.22.5"` pins a version in the
  manifest, NOT in `.tool-versions`. Two version sources (asdf's file + manifest
  literals) is a mild smell. Mitigation: keep unmanaged tools few; revisit if asdf
  plugins appear for them. The version-verify step still enforces whatever's
  declared, so it's reproducible, just declared in two places.
- **Don't let setup become a package manager:** stack orchestrates the manager; it
  doesn't resolve dependency graphs or build from source. One tool → one
  install/verify. If a tool needs more, that's the manager's job, not stack's.

## Open questions (carry into the build)

- **Where `tools_manager` is set:** app-level (one manager for the repo) vs
  env-level (could differ per environment)? Likely app-level — the toolchain is the
  same regardless of deploy target. Confirm.
- **Which tools `setup` considers:** only those referenced by checks, or all tools
  any step (deploy included) might use? Start with checks (the immediate pain);
  widen to deploy tools (docker/helm/kubectl) if useful.
- **Version-source for unmanaged tools:** keep the `@version` literal in the
  manifest (current plan), or allow a `.tool-versions`-style entry for them too
  (asdf won't read it, but stack could). Defer.
- **`stack setup` as a prereq of `stack check`:** auto-run a doctor before check
  and hint `stack setup`? Or keep them separate (explicit)? Lean separate; check
  already reports missing tools clearly now.
