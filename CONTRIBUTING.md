# Contributing to stack

Thanks for your interest in contributing. This guide covers the basics; the
design is documented in [docs/SCHEMA.md](docs/SCHEMA.md) (context-file schema),
[docs/PLUGINS.md](docs/PLUGINS.md) (writing a tool manifest), and
[docs/SETUP.md](docs/SETUP.md) (the tools-manager flow).

## Prerequisites

- Go 1.26+ (the repo pins the exact version in `.tool-versions`).
- [asdf](https://asdf-vm.com/) for the pinned tool versions (optional but
  recommended — `stack` uses it as its tools manager).

## Building and testing

`stack` is built with itself. The whole verification suite is one command:

```console
$ go build -o bin/stack ./cmd/stack   # build the binary
$ ./bin/stack setup                   # install the tools the checks need
$ ./bin/stack check                   # format + lint + test (the CI gate)
```

`stack check` is the single source of truth for verification — it's exactly what
CI runs. If it's green locally, CI will be green. You can also run the underlying
tools directly:

```console
$ go test ./...
$ gofmt -l .
$ golangci-lint run ./...
```

To rebuild `stack` itself through `stack`:

```console
$ ./bin/stack build      # runs the checks, then `go build`
```

## Tests

- Unit tests live next to the code (`*_test.go`). The suite is self-contained —
  it does **not** require docker/helm/kubectl/grype to be installed (it asserts
  the *rendered commands*, not their execution), so `go test ./...` runs on a
  clean machine.
- Add tests for new behavior. Bug fixes should include a test that fails before
  the fix and passes after.

## Adding support for a tool

Tools are declarative YAML manifests, not Go code — see
[docs/PLUGINS.md](docs/PLUGINS.md). To add a tool (e.g. `podman`, `kustomize`),
write a manifest under `internal/plugins/manifests/` describing how it performs
the abstract steps it provides. No engine changes needed.

## Commit messages

Use [Conventional Commits](https://www.conventionalcommits.org/). Releases and the
changelog are generated from commit history, so the prefix matters:

- `fix:` → patch release
- `feat:` → minor release
- `feat!:` / `fix!:` (or a `BREAKING CHANGE:` footer) → major release
- `docs:` / `chore:` / `refactor:` / `test:` / `ci:` → no release on their own

A note on `refactor:` — if a change alters user-facing behavior (not just internal
structure), use `feat:` so it ships in a release and lands in the changelog.

A commit-msg hook enforces the format. Activate it once after cloning:

```console
$ git config core.hooksPath .githooks
```

## Pull requests

- Branch from `main`; keep PRs focused.
- Make sure `stack check` passes before opening the PR.

## Releases

Releases are automated. [Release Please](https://github.com/googleapis/release-please)
maintains a release PR that bumps the version and changelog from the commit
history; merging it tags a GitHub Release, and the binaries are cross-compiled and
attached automatically. Don't hand-tag versions.

Dependency updates are automated by [Renovate](https://docs.renovatebot.com/),
which opens `fix(deps):` PRs (so each merged update cuts a patch release) and
holds new versions for 14 days before proposing them.

## License

By contributing, you agree that your contributions are licensed under the
[MIT License](LICENSE).
