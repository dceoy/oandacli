# AGENTS.md

Guidance for coding agents working on this repository.

## Layout

- `oanda/` – SDK package (stdlib only). One file per OANDA endpoint group; `client.go` holds transport, retries, rate limiting and safety options; `validate.go` holds input validation.
- `internal/cli/` – the `oandactl` CLI. `commands.go` is the single declarative command registry; the CLI flags, help text and `oandactl schema` are all generated from it.
- `main.go` – entry point.
- `skills/oandactl/` – the operational Agent Skill for using the CLI; `.agents/skills/oandactl` is a relative symlink to it. Edit the skill only under `skills/oandactl/`.
- `.agents/skills/` – agent skills (`local-qa`, `oanda-v20-docs`, and the `oandactl` symlink); `.claude/skills` is a symlink to it.
- `.github/workflows/` – CI built on the shared `dceoy/gha-for-devops` reusable workflows.

## Rules

- Do not add third-party dependencies.
- Validate every path/query input in the SDK before building a request; escape path segments with `seg`.
- Never retry non-GET requests. Never log or print tokens or request bodies.
- New state-changing commands must set `Mutating: true` so policy checks (read-only mode, live guard, dry run) apply.
- Keep `skills/oandactl/SKILL.md` in sync with CLI commands, flags and safety behavior.
- Prefer table-driven tests with `httptest` servers; no test may reach the real OANDA API.

## Checks

```sh
make check   # Go: fmt, vet, golangci-lint, gosec, race tests, govulncheck
make qa      # everything, incl. Markdown and GitHub Actions linting (local-qa skill)
```
