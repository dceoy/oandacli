---
name: local-qa
description: Run local QA including formatting, linting, security scanning, and testing for the repository. Use whenever any file has been updated.
disable-model-invocation: false
---

# Local QA (format, lint, scan, and test)

Run `scripts/qa.sh` relative to this skill's directory, not the shell's current working directory. The script changes to the repository root itself.

## Procedure

- Execute `scripts/qa.sh` from the skill directory when this skill is triggered (it formats and fixes files in place, so check `git status` afterward).
- Capture and summarize key output (success/failure, major warnings, and any files modified).
- The Go tools `golangci-lint`, `govulncheck`, and `gosec` must be on `PATH`. If one is missing, install it with `go install` at the version pinned in CI (`dceoy/gha-for-devops` `go-package-lint-and-scan.yml`):
  - `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0`
  - `go install golang.org/x/vuln/cmd/govulncheck@v1.9.0`
  - `go install github.com/securego/gosec/v2/cmd/gosec@v2.30.0`
- If the script fails due to missing tooling (`command not found`, missing executable, or equivalent), install the missing tool(s) and rerun `scripts/qa.sh` from the skill directory.
- Install tools using this order of preference:
  1. Use the project's toolchain when applicable (`go install` for Go tools).
  2. Use a system package manager (`brew` on macOS, `apt` on Debian/Ubuntu) when project-local install is not applicable.
  3. Use language-specific installers as fallback (`uv`/`pipx`, `pnpm`/`npm`, etc.).
- If multiple tools are missing, repeat install -> rerun until QA completes or you hit a blocker.
- If installation fails, requires unavailable privileges, or a scanner cannot reach its database (e.g. `govulncheck` without network access), report what was attempted, the exact failure, and stop.
- If QA fails for another reason, fix the root cause and rerun until QA passes or a blocker is reached. Do not bypass or weaken checks.
- Do not run unrelated commands; only run commands needed for QA, fixes, and missing-tool installation.
