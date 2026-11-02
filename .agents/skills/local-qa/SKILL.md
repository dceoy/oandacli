---
name: local-qa
description: Run local QA including formatting, linting, security scanning, and testing for the repository. Use whenever any file has been updated.
disable-model-invocation: false
---

# Local QA (format, lint, scan, and test)

Run the local QA script `scripts/qa.sh` in this skill.

## Procedure

- Execute the script exactly as shown above when this skill is triggered.
- Capture and summarize key output (success/failure, major warnings, and any files modified).
- The Go tools (golangci-lint, gosec, govulncheck) are run with `go run` at the versions pinned in CI, so they need no separate installation.
- If the script fails due to missing tooling (`command not found`, missing executable, or equivalent), install the missing tool(s) and rerun `./scripts/qa.sh`.
- Install tools using this order of preference:
  1. Use the project's toolchain when applicable (`go install` for Go tools).
  2. Use a system package manager (`brew` on macOS, `apt` on Debian/Ubuntu) when project-local install is not applicable.
  3. Use language-specific installers as fallback (`uv`/`pipx`, `pnpm`/`npm`, etc.).
- If multiple tools are missing, repeat install -> rerun until QA completes or you hit a blocker.
- If installation fails, requires unavailable privileges, or a scanner cannot reach its database (e.g. `govulncheck` without network access), report what was attempted, the exact failure, and stop.
- Do not run unrelated commands; only run commands needed for QA and missing-tool installation.
