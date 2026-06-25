# Security Policy

## Reporting a vulnerability

Please report security vulnerabilities **privately** — do not open a public issue,
which would disclose the vulnerability before a fix is available.

Use GitHub's private vulnerability reporting:

1. Go to the **[Security tab](https://github.com/faradayfan/stack/security)** of
   this repository.
2. Click **Report a vulnerability**.
3. Describe the issue, including steps to reproduce and the affected version
   (`stack version`) where possible.

You'll get a response as soon as is practical. Once a fix is ready, a patched
release is cut and the advisory is published. Thanks for reporting responsibly.

## Supported versions

`stack` is pre-1.0; the schema and CLI may still change between minor versions.
Security fixes are made against the **latest release**. Please upgrade to the most
recent version before reporting, in case the issue is already fixed.

## Scope

This policy covers the `stack` CLI and its built-in tool manifests. `stack`
orchestrates third-party tools (docker, helm, kubectl, grype, …) by shelling out
to them — vulnerabilities in those tools should be reported to their respective
projects. The repository runs a dependency vulnerability scan (grype) in CI to
catch known CVEs in its own Go dependencies.
