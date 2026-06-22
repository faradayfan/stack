# stack — the check flow (verification) — DRAFT

How `stack` runs verification — unit/integration tests, lint, format, and the
various scans (image, dependency, SAST, secret, license, …) — without bloating
the engine. Companion to DESIGN.md (deploy) and PLUGIN-MODEL.md (plugins).

> Status: design for review. Decisions below are settled (see "Decided").

## The key insight: verification is a different FLOW, not new deploy steps

Deploy steps (`build-artifact`, `deliver`, `apply`…) all serve **deploying**.
Tests/lint/scans don't deploy — they **verify**. So they're a separate **flow**
(verb) with its own sequence, not additions to the deploy pipeline:

- `stack deploy` → build → scan-image → deliver → apply
- `stack check`  → format · lint · unit · integration · scan-deps · sast · …

A flow is an ordered selection of abstract steps — exactly like a pattern. The CI
you already hand-wrote in GitHub Actions *is* this flow; `stack check` becomes its
single definition (see "stack check IS the CI").

## Decided

- **One abstract step `check` + a declared list.** The engine gains exactly ONE
  new step (`check`) and one new flow (`stack check`). It never learns what
  "SAST" or "lint" *mean* — the context declares an ordered list of checks, each
  binding a tool plugin. New check kinds = a manifest + a list entry, no engine
  change. (Same philosophy as the deploy model: the engine knows shapes, not
  specifics.)
- **`stack check` IS the CI.** The `.stack` check list is the single source of
  truth; GitHub Actions calls `stack check` rather than re-declaring jobs. This
  removes the local-vs-CI drift (baseline's own fact: *"CI gates merges but local
  iteration skips it"*) — what runs in CI is exactly what you can run locally.
- **Parallel where possible.** Independent checks run concurrently; a check may
  declare `after: <step>` to depend on a prior step (e.g. image scan needs the
  built artifact). `blocking: false` checks report but don't fail the run
  (mirrors gosec-non-blocking in the current CI).

## The check declaration

Checks are **app-wide** (the same everywhere CI runs), so they live in
`.stack/app.yaml`. Each entry: a name, the tool plugin that runs it, blocking-ness,
optional `after` dependency, optional args.

```yaml
# .stack/app.yaml  (excerpt)
checks:                                      # keyed by check name (map, not list)
  format:      { tool: gofmt,       blocking: true }
  lint:        { tool: golangci,    blocking: true }
  unit:        { tool: go-test,     blocking: true, args: { short: true } }
  integration: { tool: go-test,     blocking: true }            # needs docker
  scan-deps:   { tool: grype-src,   blocking: true }
  scan-image:  { tool: grype-image, blocking: true, after: build-artifact }
  sast:        { tool: gosec,       blocking: false }           # report-only
  vuln-go:     { tool: govulncheck, blocking: false }
  # frontend checks bind to their own tools:
  ui-typecheck: { tool: pnpm-script, args: { script: typecheck }, dir: frontend }
  ui-build:     { tool: pnpm-script, args: { script: build },     dir: frontend }
```

A check entry is "run ONE tool, get pass/fail." If a check needs real logic
(multi-step, conditional), that's the signal it's a **hook**, not a check —
keep checks atomic so the runner can parallelize and report them uniformly.

## Checks are the same plugin shape

Each `tool` is a manifest that `provides: [check]`. The engine calls the `check`
step with the entry's inputs (`args`, `dir`); pass/fail = the command's exit code.

```yaml
# plugins/go-test.yaml
tool: go-test
detect: "go version"
provides: [check]
steps:
  check:
    command: "go test {{if .short}}-short {{end}}./..."

# plugins/gofmt.yaml — pass/fail by emptiness of `gofmt -l`
tool: gofmt
provides: [check]
steps:
  check:
    command: "test -z \"$(gofmt -l .)\""

# plugins/golangci.yaml
tool: golangci
detect: "golangci-lint version"
provides: [check]
steps:
  check:
    command: "golangci-lint run {{if .timeout}}--timeout={{.timeout}}{{end}} ./..."

# plugins/grype-src.yaml / grype-image.yaml — source vs image targets
tool: grype-src
provides: [check]
steps:
  check:
    command: "grype dir:."          # reads .grype.yaml (threshold + ignores)

# plugins/pnpm-script.yaml — run a package.json script in a subdir
tool: pnpm-script
detect: "pnpm --version"
provides: [check]
steps:
  check:
    command: "cd {{.dir}} && pnpm install --frozen-lockfile && pnpm run {{.script}}"
```

The scan plugins reuse the deploy model's grype: `grype-src` scans the tree
(`dir:.`), `grype-image` scans a built image (`after: build-artifact` so the
artifact exists). "Multiple scanning steps" (image + deps + SAST + secret) are
just multiple check entries binding different tools — no special-casing.

## Execution (`stack check`)

1. Resolve the check list (from `.stack/app.yaml`).
2. Build a dependency graph: a check with `after: X` waits for step X; everything
   else is independent.
3. Run independent checks **concurrently** (bounded pool); stream each check's
   output under its name. (`--dry-run` prints the commands; `--check <name>` runs
   one; `--no-<name>` / `only:` selectors later.)
4. A failing **blocking** check fails the run (non-zero exit) after letting the
   rest finish (so you see all failures, not just the first). **Non-blocking**
   checks report a warning and never fail the run.
5. Summary: per-check pass/fail/skip, total result.

```
stack check                 # run all checks (parallel, honor blocking)
stack check --dry-run       # print the commands
stack check unit lint       # run only named checks
stack check --env ci        # (checks are env-independent, but --env selects
                            #  toolchain availability / a ci profile if needed)
```

## stack check IS the CI — the GitHub Actions integration

The GitHub Actions workflow collapses to: check out, install toolchain, run
`stack check`. One job (or a thin matrix) instead of N hand-maintained jobs:

```yaml
# .github/workflows/ci.yml  (the END STATE this enables)
jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - uses: actions/setup-go@v6        # + node/helm as the checks need
      - run: go install github.com/faradayfan/stack/cmd/stack@latest
      - run: stack check                 # the single source of truth
```

Result: the check list in `.stack/app.yaml` is what runs **both** locally and in
CI. No more "CI does X but my machine does Y." (CI may still want native caching
for speed — a later refinement; the *definition* unifies regardless.)

> Migration note: baseline's current CI hand-codes format/lint/security/build/test
> as separate jobs. Those map 1:1 to check entries (format→gofmt, lint→golangci,
> security→grype-src+gosec+govulncheck, the Go test job→unit+integration). The
> `build` job's image builds belong to `stack deploy`'s build-artifact, scanned
> via the `scan-image` check (`after: build-artifact`).

## Where this could rot (guardrails)

- **Checks-as-shell-scripts:** if an entry's command grows conditionals/pipes
  beyond "run one tool," promote it to a hook. Checks stay atomic.
- **Parallelism vs. resource contention:** integration tests spin Docker
  testcontainers; running many in parallel can thrash. Bound the pool and let a
  check declare `serial: true` (or a resource tag) if it must not run alongside
  others. Start simple (bounded pool); add resource tags only if it bites.
- **CI caching:** `stack check` in one CI step loses GitHub's per-job caching.
  Acceptable for correctness/parity now; if speed matters, a `--only` selector
  lets the workflow shard checks across cached jobs while keeping one definition.

## Open questions (carry into the check-flow build)

- **Check inputs/exit semantics:** standardize that exit 0 = pass; do any tools
  need output parsing instead of exit code? (gofmt handled via `test -z`.)
- **`after:` granularity:** only `build-artifact`, or arbitrary inter-check deps?
  (Start with `after: build-artifact` for image scans; generalize if needed.)
- **Profiles:** does `stack check` need a `--env ci` profile (e.g. skip checks
  that need a local-only tool), or are checks uniform? Likely uniform; revisit.
- **Where deploy's `scan-artifact` and the `scan-image` check converge** — same
  grype-image tool; ensure one manifest, two call sites (deploy + check).
