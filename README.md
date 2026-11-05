# oandactl

A secure, dependency-free Go SDK and CLI for the [OANDA REST-V20 API](https://developer.oanda.com/rest-live-v20/introduction/), built for AI agents and scripts.

- **Agent-friendly I/O.** Every command takes only named `--flags` and prints exactly the JSON that OANDA returned. Errors go to stderr as JSON, exit codes are stable, and streams print NDJSON.
- **Agent Skill included.** [`skills/oandactl`](skills/oandactl/SKILL.md) teaches coding agents to run the CLI directly and safely, with no server or plugin to run.
- **Self-describing.** `oandactl schema` prints every command with its flags, types, enums and defaults as JSON.
- **Secure by default.** It defaults to the practice environment, refuses live trading unless you opt in, and has a read-only mode, dry runs bound to the confirmed request by a plan hash, and an order size limit. The token is never accepted as a flag and never logged. Only HTTPS is used, redirects are never followed, inputs are validated, and orders are never retried.
- **Zero third-party dependencies.** It uses only the Go standard library.

## Install

```sh
go install github.com/dceoy/oandactl@latest
```

To build from source:

```sh
make build   # ./bin/oandactl
```

## Configuration

| Variable                            | Purpose                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| ----------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `OANDA_API_TOKEN`                   | Personal access token.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| `OANDA_API_TOKEN_FILE`              | Path to a file containing the token. It is preferred over `OANDA_API_TOKEN` and must not be readable by group or others (`chmod 600`).                                                                                                                                                                                                                                                                                                                                                                |
| `OANDA_API_TOKEN_COMMAND`           | Credential helper whose stdout is the token, e.g. `op read op://vault/oanda/token`. It is split on spaces and run without a shell, with only `PATH`, `HOME`, `TMPDIR` and `SYSTEMROOT` set and a 10 s timeout. Used when no token file is set; it wins over `OANDA_API_TOKEN`. Output over 4 KiB stops the helper. Failures report only the exit status. It is executed as given, so set it only from trusted configuration (your shell profile or the agent runtime), never from repository content. |
| `OANDA_ACCOUNT_ID`                  | Default account, e.g. `101-004-1234567-001`. Override it with `--account`.                                                                                                                                                                                                                                                                                                                                                                                                                            |
| `OANDA_ENVIRONMENT`                 | `practice` (default) or `live`. Override it with `--env`.                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| `OANDA_ALLOW_LIVE`                  | Must be `true` (or pass `--allow-live`) before any state-changing request is sent to the **live** environment.                                                                                                                                                                                                                                                                                                                                                                                        |
| `OANDA_READ_ONLY`                   | `true` refuses state-changing execution; `--dry-run` previews are allowed.                                                                                                                                                                                                                                                                                                                                                                                                                            |
| `OANDA_PLAN_TTL`                    | Dry-run plan lifetime as a Go duration, from `1m` to `60m` inclusive (default `10m`). `--plan-ttl` overrides it; invalid values are usage errors.                                                                                                                                                                                                                                                                                                                                                     |
| `OANDA_REQUIRE_CONFIRM_PLAN`        | `true` refuses every state-changing command that lacks a matching `--confirm-plan` (see below).                                                                                                                                                                                                                                                                                                                                                                                                       |
| `OANDA_MAX_ORDER_UNITS`             | Rejects orders whose absolute units exceed this value. It must be a positive plain decimal such as `10000`; `NaN`, `Inf`, exponents and `0` are refused, and the comparison is exact. While it is set, orders whose units can't be checked are refused.                                                                                                                                                                                                                                               |
| `OANDA_API_URL`, `OANDA_STREAM_URL` | Override the base URLs, e.g. for a test server. They must be HTTPS unless the host is loopback.                                                                                                                                                                                                                                                                                                                                                                                                       |

The token is never accepted as a command-line flag, because flags are visible to other users in the process list.

### Credential brokers and agent sandboxes

When a broker keeps the real credential out of the sandbox, point `OANDA_API_TOKEN_COMMAND` at a helper that prints the opaque surrogate token. `oanda.NewToken` accepts any printable ASCII token without whitespace, so surrogates work unchanged. The token stays in memory and is never written to disk or logged. Leave `OANDA_API_URL`/`OANDA_STREAM_URL` unset so requests only go to the registered OANDA hosts, and keep `OANDA_READ_ONLY=true` as the default posture.

### Confirmed plans

For a state-changing command, `--dry-run` also prints `planHash` (a SHA-256 over the environment, account, method, URL, body and expiry) and `planExpiresAt` (10 minutes later by default). Choose a different lifetime explicitly with `--plan-ttl 30m` or `OANDA_PLAN_TTL=30m`, bounded to 1–60 minutes. The expiry is bound into the hash; execution does not need to repeat the TTL setting, and rejects expiries more than 60 minutes ahead. Running the command with `--confirm-plan <planHash>` sends it only if the actual request hashes to the same value and the plan has not expired; otherwise it exits 5 and sends nothing. A plan authorizes a single request only while all invocations share the same persistent user cache namespace: its use is recorded atomically under the user cache directory (`oandactl/plans`) before the request is sent, so a replay in that namespace is refused even if the first send failed. Changing `XDG_CACHE_HOME`, deleting the cache, or using isolated sandboxes or separate hosts can allow the same unexpired plan to send another request. This is not broker-side idempotency or cross-worker replay protection; check orders and transactions before retrying or moving execution. Previews are allowed under read-only mode without lifting it. Executing a plan still requires read-only mode to be disabled by the operator; a plan does not replace `--allow-live` or read-only mode. Set `OANDA_REQUIRE_CONFIRM_PLAN=true` to make confirmation mandatory.

```sh
oandactl order market --instrument EUR_USD --units 1000 --dry-run      # -> "planHash":"1793..."
oandactl order market --instrument EUR_USD --units 1000 --confirm-plan 1793...
```

The hash binds approval to the previewed request; it is not a secret, so it does not stop a process that can run `--dry-run` itself from producing a plan.

## CLI usage

```sh
oandactl help                               # list commands
oandactl order market --help                # flags of one command

oandactl account summary
oandactl account instruments --instruments EUR_USD,USD_JPY
oandactl instrument candles --instrument EUR_USD --granularity H1 --count 100 --price BA
oandactl pricing get --instruments EUR_USD,USD_JPY
oandactl pricing stream --instruments EUR_USD --max-messages 20 --duration 30s

# Orders: positive units buy, negative units sell.
oandactl order market --instrument EUR_USD --units 1000 --sl-distance 0.0050 --tp-price 1.1200
oandactl order limit  --instrument USD_JPY --units -500 --price 151.250 --time-in-force GTD --gtd-time 2026-12-31T00:00:00Z
oandactl order create --body-file order.json          # any order type as raw JSON
oandactl order market --instrument EUR_USD --units 1000 --dry-run   # preview, nothing is sent

oandactl trade open
oandactl trade orders --trade 123 --sl-price 1.0950 --cancel tp
oandactl trade close --trade 123 --units 500
oandactl position close --instrument EUR_USD --long-units ALL
oandactl transaction since --id 6410 --type ORDER_FILL
```

Global flags work on every command: `--account`, `--env`, `--token-file`, `--timeout`, `--pretty`, `--compact`, `--dry-run`, `--confirm-plan`, `--plan-ttl`, `--allow-live`, `--read-only` and `--verbose`. Output is pretty-printed when stdout is a terminal and compact otherwise.

### Commands

| Group         | Commands                                                                                                                                     |
| ------------- | -------------------------------------------------------------------------------------------------------------------------------------------- |
| `account`     | `list`, `get`, `summary`, `instruments`, `changes`, `configure`\*                                                                            |
| `instrument`  | `candles`, `order-book`, `position-book`                                                                                                     |
| `pricing`     | `get`, `candles`, `latest-candles`, `stream`                                                                                                 |
| `order`       | `list`, `pending`, `get`, `market`\*, `limit`\*, `stop`\*, `market-if-touched`\*, `create`\*, `replace`\*, `cancel`\*, `client-extensions`\* |
| `trade`       | `list`, `open`, `get`, `close`\*, `client-extensions`\*, `orders`\*                                                                          |
| `position`    | `list`, `open`, `get`, `close`\*                                                                                                             |
| `transaction` | `list`, `get`, `range`, `since`, `stream`                                                                                                    |

\* State-changing. Execution is refused in read-only mode, and in the live environment unless `--allow-live` is given. `--dry-run` previews remain allowed.

### Errors and exit codes

On failure, stderr gets a single JSON line:

```json
{"error":{"kind":"api","message":"oanda: HTTP 400 Bad Request [INSUFFICIENT_MARGIN]: ...","statusCode":400,"errorCode":"INSUFFICIENT_MARGIN","requestID":"...","response":{"orderRejectTransaction":{...}},"exitCode":3}}
```

| Exit code | Meaning                                                            |
| --------- | ------------------------------------------------------------------ |
| 0         | Success. A dry run also exits 0.                                   |
| 1         | Transport, timeout or unexpected error.                            |
| 2         | Invalid command or arguments (`kind: usage`). Nothing was sent.    |
| 3         | OANDA returned 4xx (`kind: api`).                                  |
| 4         | OANDA returned 5xx (`kind: api`).                                  |
| 5         | Refused by local safety policy (`kind: policy`). Nothing was sent. |

## Use with AI agents

Agents call `oandactl` like any other shell command. The repository ships an [Agent Skill](skills/oandactl/SKILL.md) that teaches them how: setup and authentication, command discovery with `oandactl help` and `oandactl schema`, read-only and trading workflows, output and exit-code handling, and bounded streaming.

The skill lives in `skills/oandactl/`. `.agents/skills/oandactl` is a relative symlink to it, and `.claude/skills` links to `.agents/skills`, so agents working in this repository pick it up automatically. To use it elsewhere, copy or link `skills/oandactl` into that agent's skills directory.

The skill tells agents to stay read-only by default, to preview every trading command with `--dry-run`, and to trade only when the user explicitly asks. The CLI enforces the actual limits whatever an agent does:

- `OANDA_READ_ONLY=true` refuses state-changing execution, but allows `--dry-run` previews.
- The live environment refuses trading unless `--allow-live` or `OANDA_ALLOW_LIVE=true` is given.
- `OANDA_MAX_ORDER_UNITS` caps order size.

For an agent session that should only read data, set `OANDA_READ_ONLY=true` in its environment.

## Go SDK

```go
import "github.com/dceoy/oandactl/oanda"

token, _ := oanda.NewToken(os.Getenv("OANDA_API_TOKEN"))
client, err := oanda.NewClient(token,
 oanda.WithEnvironment(oanda.Practice),
 oanda.WithReadOnly(false),
)
if err != nil { ... }

summary, err := client.GetAccountSummary(ctx, accountID)
fmt.Println(summary.Account.NAV, summary.RequestID())

order := oanda.NewMarketOrder("EUR_USD", "1000")
order.StopLossOnFill = &oanda.StopLossDetails{Distance: "0.0050"}
res, err := client.CreateOrder(ctx, accountID, order)
var apiErr *oanda.APIError
if errors.As(err, &apiErr) {
 fmt.Println(apiErr.ErrorCode, string(apiErr.Body)) // e.g. orderRejectTransaction
}
fmt.Println(res.Transaction("orderFillTransaction").Price)

err = client.StreamPricing(ctx, accountID, oanda.PricingStreamParams{Instruments: []string{"EUR_USD"}},
 func(m oanda.StreamMessage) error {
  if m.IsHeartbeat() { return nil }
  p, _ := m.Price()
  fmt.Println(p.Instrument, p.Bids[0].Price)
  return nil // or oanda.ErrStopStream to finish
 })
```

Notes on the SDK:

- Prices, units and amounts are `oanda.Decimal` strings, so values never pass through floating point.
- Every response embeds `ResponseMeta`. `RawJSON()` returns the exact body, `RequestID()` returns OANDA's request ID, and `StatusCode()` returns the HTTP status.
- Mutation endpoints return `*TransactionResult`, which collects every `...Transaction` field (`orderCreateTransaction`, `orderFillTransaction`, `longOrderFillTransaction`, …) in a map.

Options:

| Option                   | Default              |
| ------------------------ | -------------------- |
| `WithEnvironment`        | Practice             |
| `WithBaseURLs`           |                      |
| `WithHTTPClient`         |                      |
| `WithTimeout`            | 30s                  |
| `WithRateLimit`          | 100 rps, burst 20    |
| `WithMaxRetries`         | 3, GET requests only |
| `WithMaxResponseBytes`   | 32 MiB               |
| `WithStreamStallTimeout` | 30s                  |
| `WithReadOnly`           |                      |
| `WithDryRun`             |                      |
| `WithMutationCheck`      |                      |
| `WithLogger`             |                      |
| `WithUserAgent`          |                      |

## Security model

| Threat                      | Mitigation                                                                                                                                                                                                                                                                                                           |
| --------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Token disclosure            | `oanda.Token` redacts itself in `fmt`, JSON, text and `slog`. The CLI reads it only from the environment, a `0600` file or a credential helper's stdout (run without a shell, with a minimal environment and a timeout; its output and stderr are never shown), and logs never include the token or bodies.          |
| Token sent to another host  | Only HTTPS (loopback HTTP for tests), TLS ≥ 1.2, and redirects are never followed. `GetTransactionPage` only fetches URLs on the client's own API host.                                                                                                                                                              |
| Injection or path traversal | Account IDs, instruments, order and trade specifiers, transaction IDs, enums, decimals and timestamps are validated, and path segments are escaped.                                                                                                                                                                  |
| Preview/execution drift     | `--confirm-plan` refuses a state-changing request unless it matches the hash of the request shown by `--dry-run`, before its expiry (default 10 minutes, explicitly configurable from 1 to 60 minutes); single-request replay prevention requires all invocations to share the same persistent user cache namespace. |
| Duplicate orders            | Only idempotent GETs are retried. POST, PUT and PATCH are sent once.                                                                                                                                                                                                                                                 |
| Runaway agents              | Practice is the default. Live trading requires explicit opt-in. Read-only mode, dry runs, `OANDA_MAX_ORDER_UNITS`, a client-side rate limit (below OANDA's 120 req/s), `--max-messages`/`--duration` bounds for streams, a 32 MiB response limit and a 1 MiB stream line limit also apply.                           |
| Supply chain                | No third-party modules. CI runs golangci-lint, gosec, govulncheck, OSV-Scanner, Trivy, Semgrep and CodeQL.                                                                                                                                                                                                           |

## Development

```sh
make check   # gofmt, go vet, golangci-lint, gosec, tests with -race, govulncheck
make qa      # full local QA (also lints Markdown and GitHub Actions workflows)
```

CI uses the shared [`go-package-lint-and-scan`](https://github.com/dceoy/gha-for-devops/blob/main/.github/workflows/go-package-lint-and-scan.yml) and [`go-package-test`](https://github.com/dceoy/gha-for-devops/blob/main/.github/workflows/go-package-test.yml) workflows from `dceoy/gha-for-devops`. Agent skills live in `.agents/skills/`; `.claude/skills` is a symlink to that directory.

## License

[AGPL-3.0](LICENSE)
