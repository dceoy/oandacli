GOLANGCI_LINT_VERSION ?= v2.15.0
GOSEC_VERSION ?= v2.30.0
GOVULNCHECK_VERSION ?= v1.9.0
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/dceoy/oandactl/internal/cli.Version=$(VERSION)

.PHONY: build test lint sec vuln check qa clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/oandactl .

test:
	go test -race -count=1 ./...

lint:
	@test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)
	go vet ./...
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run ./...

sec:
	go run github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION) -quiet ./...

vuln:
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

check: lint sec test vuln

qa:
	./.agents/skills/local-qa/scripts/qa.sh

clean:
	rm -rf bin
