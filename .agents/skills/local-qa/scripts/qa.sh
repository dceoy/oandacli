#!/usr/bin/env bash

set -euox pipefail
cd "$(git rev-parse --show-toplevel)"

COOLDOWN_DAYS=1
export UV_EXCLUDE_NEWER="${COOLDOWN_DAYS} days"
export NPM_CONFIG_MIN_RELEASE_AGE="${COOLDOWN_DAYS}"
export PNPM_CONFIG_MINIMUM_RELEASE_AGE=$((COOLDOWN_DAYS * 24 * 60))

# Keep in sync with dceoy/gha-for-devops go-package-lint-and-scan.yml
GOLANGCI_LINT_VERSION=v2.15.0
GOSEC_VERSION=v2.30.0
GOVULNCHECK_VERSION=v1.9.0

# Go
gofmt -w .
go mod tidy
go vet ./...
go run "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_LINT_VERSION}" run --fix ./...
go run "github.com/securego/gosec/v2/cmd/gosec@${GOSEC_VERSION}" -quiet ./...
go run "golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}" ./...
go test -race -count=1 ./...
go build ./...

# Markdown
pnpm dlx oxfmt --write './**/*.md'

# GitHub Actions
uvx zizmor --fix=safe .github/workflows
git ls-files -z -- '.github/workflows/*.yml' | xargs -0 -t actionlint
git ls-files -z -- '.github/workflows/*.yml' | xargs -0 -t uvx yamllint -d '{"extends": "relaxed", "rules": {"line-length": "disable"}}'
uvx checkov --framework=all --output=github_failed_only --directory=.
