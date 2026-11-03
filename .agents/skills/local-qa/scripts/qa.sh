#!/usr/bin/env bash

set -euox pipefail
cd "$(git rev-parse --show-toplevel)"

COOLDOWN_DAYS=1
export UV_EXCLUDE_NEWER="${COOLDOWN_DAYS} days"
export NPM_CONFIG_MIN_RELEASE_AGE="${COOLDOWN_DAYS}"
export PNPM_CONFIG_MINIMUM_RELEASE_AGE=$((COOLDOWN_DAYS * 24 * 60))

# Go
go mod tidy
if [[ -f .golangci.yml ]]; then
  GOLANGCI_LINT_CONFIG_ARGS=(-c .golangci.yml)
else
  GOLANGCI_LINT_CONFIG_ARGS=()
fi
golangci-lint "${GOLANGCI_LINT_CONFIG_ARGS[@]}" fmt --enable=gofumpt --enable=goimports
golangci-lint "${GOLANGCI_LINT_CONFIG_ARGS[@]}" run --fix
govulncheck ./...
gosec ./...
go test -race -count=1 ./...

# Markdown
npx -y oxfmt --write '**/*.md'
# Same markdownlint rules as the shared CI workflow (markdown-lint.yml) unless the repo has its own config
if [[ -f .markdownlint-cli2.jsonc ]]; then
  git ls-files -z -- '*.md' '*.mdx' | xargs -0 -t npx -y markdownlint-cli2 --fix --config .markdownlint-cli2.jsonc
else
  printf '{"config":{"MD013":false,"MD033":false,"MD041":false}}' > .markdownlint-cli2.jsonc
  set +e
  git ls-files -z -- '*.md' '*.mdx' | xargs -0 -t npx -y markdownlint-cli2 --fix --config .markdownlint-cli2.jsonc
  markdownlint_exit_code="${?}"
  set -e
  rm -f .markdownlint-cli2.jsonc
  [[ "${markdownlint_exit_code}" -eq 0 ]] || exit "${markdownlint_exit_code}"
fi

# GitHub Actions
uvx zizmor --fix=safe .github/workflows
git ls-files -z -- '.github/workflows/*.yml' | xargs -0 -t actionlint
git ls-files -z -- '.github/workflows/*.yml' | xargs -0 -t uvx yamllint -d '{"extends": "relaxed", "rules": {"line-length": "disable"}}'
uvx checkov --framework=all --output=github_failed_only --directory=.
