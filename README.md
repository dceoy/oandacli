# oandacli

A secure, dependency-free Go SDK and CLI for the [OANDA REST-V20 API](https://developer.oanda.com/rest-live-v20/introduction/), built for AI agents and scripts.

- **Agent-friendly I/O.** Every command takes only named `--flags` and prints exactly the JSON that OANDA returned. Errors go to stderr as JSON, exit codes are stable, and streams print NDJSON.
- **MCP server built in.** `oandacli mcp` serves every command as a [Model Context Protocol](https://modelcontextprotocol.io) tool over stdio. Tools are read-only unless you opt in to trading.
- **Self-describing.** `oandacli schema` prints a JSON Schema for every command.
- **Secure by default.** It defaults to the practice environment, refuses live trading unless you opt in, and has a read-only mode, dry runs and an order size limit. The token is never accepted as a flag and never logged. Only HTTPS is used, redirects are never followed, inputs are validated, and orders are never retried.
- **Zero third-party dependencies.** It uses only the Go standard library.

## Install

```sh
go install github.com/dceoy/oandacli@latest
```

To build from source:

```sh
make build   # ./bin/oandacli
```

## Configuration

| Variable | Purpose |
| --- | --- |
| `OANDA_API_TOKEN` | Personal access token. |
| `OANDA_API_TOKEN_FILE` | Path to a file containing the token. It is preferred over `OANDA_API_TOKEN` and must not be readable by group or others (`chmod 600`). |
| `OANDA_ACCOUNT_ID` | Default account, e.g. `101-004-1234567-001`. Override it with `--account`. |
| `OANDA_ENVIRONMENT` | `practice` (default) or `live`. Override it with `--env`. |
| `OANDA_ALLOW_LIVE` | Must be `true` (or pass `--allow-live`) before any state-changing request is sent to the **live** environment. |
| `OANDA_READ_ONLY` | `true` refuses every state-changing command. |
| `OANDA_MAX_ORDER_UNITS` | Rejects orders whose absolute units exceed this value. It must be a positive plain decimal such as `10000`; `NaN`, `Inf`, exponents and `0` are refused, and the comparison is exact. While it is set, orders whose units can't be checked are refused. |
| `OANDA_API_URL`, `OANDA_STREAM_URL` | Override the base URLs, e.g. for a test server. They must be HTTPS unless the host is loopback. |

The token is never accepted as a command-line flag, because flags are visible to other users in the process list.

## CLI usage

```sh
oandacli help                               # list commands
oandacli order market --help                # flags of one command

oandacli account summary
oandacli account instruments --instruments EUR_USD,USD_JPY
oandacli instrument candles --instrument EUR_USD --granularity H1 --count 100 --price BA
oandacli pricing get --instruments EUR_USD,USD_JPY
oandacli pricing stream --instruments EUR_USD --max-messages 20 --duration 30s

# Orders: positive units buy, negative units sell.
oandacli order market --instrument EUR_USD --units 1000 --sl-distance 0.0050 --tp-price 1.1200
oandacli order limit  --instrument USD_JPY --units -500 --price 151.250 --time-in-force GTD --gtd-time 2026-12-31T00:00:00Z
oandacli order create --body-file order.json          # any order type as raw JSON
oandacli order market --instrument EUR_USD --units 1000 --dry-run   # preview, nothing is sent

oandacli trade open
oandacli trade orders --trade 123 --sl-price 1.0950 --cancel tp
oandacli trade close --trade 123 --units 500
oandacli position close --instrument EUR_USD --long-units ALL
oandacli transaction since --id 6410 --type ORDER_FILL
```

Global flags work on every command: `--account`, `--env`, `--token-file`, `--timeout`, `--pretty`, `--compact`, `--dry-run`, `--allow-live`, `--read-only` and `--verbose`. Output is pretty-printed when stdout is a terminal and compact otherwise.

### Commands

| Group | Commands |
| --- | --- |
| `account` | `list`, `get`, `summary`, `instruments`, `changes`, `configure`\* |
| `instrument` | `candles`, `order-book`, `position-book` |
| `pricing` | `get`, `latest-candles`, `stream` |
| `order` | `list`, `pending`, `get`, `market`\*, `limit`\*, `stop`\*, `market-if-touched`\*, `create`\*, `replace`\*, `cancel`\*, `client-extensions`\* |
| `trade` | `list`, `open`, `get`, `close`\*, `client-extensions`\*, `orders`\* |
| `position` | `list`, `open`, `get`, `close`\* |
| `transaction` | `list`, `get`, `range`, `since`, `stream` |

\* State-changing. These are refused in read-only mode, and in the live environment unless `--allow-live` is given.

### Errors and exit codes

On failure, stderr gets a single JSON line:

```json
{"error":{"kind":"api","message":"oanda: HTTP 400 Bad Request [INSUFFICIENT_MARGIN]: ...","statusCode":400,"errorCode":"INSUFFICIENT_MARGIN","requestID":"...","response":{"orderRejectTransaction":{...}},"exitCode":3}}
```

| Exit code | Meaning |
| --- | --- |
| 0 | Success. A dry run also exits 0. |
| 1 | Transport, timeout or unexpected error. |
| 2 | Invalid command or arguments (`kind: usage`). Nothing was sent. |
| 3 | OANDA returned 4xx (`kind: api`). |
| 4 | OANDA returned 5xx (`kind: api`). |
| 5 | Refused by local safety policy (`kind: policy`). Nothing was sent. |

## Use with AI agents (MCP)

```sh
# Read-only tools only (default):
claude mcp add oanda --env OANDA_API_TOKEN_FILE=$HOME/.oanda-token --env OANDA_ACCOUNT_ID=101-004-1234567-001 -- oandacli mcp

# Also expose trading tools (practice account):
claude mcp add oanda-trading --env OANDA_API_TOKEN_FILE=$HOME/.oanda-token -- oandacli mcp --allow-trading
```

Generic MCP client configuration:

```json
{
  "mcpServers": {
    "oanda": {
      "command": "oandacli",
      "args": ["mcp"],
      "env": { "OANDA_API_TOKEN_FILE": "/home/me/.oanda-token", "OANDA_ACCOUNT_ID": "101-004-1234567-001" }
    }
  }
}
```

How the MCP server behaves:

- Tools are named `oanda_<group>_<command>`, e.g. `oanda_pricing_get` and `oanda_order_market`. Arguments use snake_case, and unknown arguments are rejected.
- State-changing tools are only listed when the server starts with `--allow-trading`. In the live environment they also need `--allow-live`, and they are never listed with `--read-only`. Each one accepts `dry_run: true` and carries the MCP `destructiveHint` annotation.
- All tool calls share one HTTP connection pool and one client-side rate limiter. A per-call `dry_run: false` can't override a server started with `--dry-run`.
- Stream tools always return a bounded batch: `max_messages` defaults to 10 (maximum 1000) and `duration` defaults to 10s (maximum 2m).
- Arguments that read local files (`--body-file`) are not available over MCP.

Agents that call the CLI directly can use `oandacli schema` to discover commands and their JSON Schemas.

## Go SDK

```go
import "github.com/dceoy/oandacli/oanda"

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

| Option | Default |
| --- | --- |
| `WithEnvironment` | Practice |
| `WithBaseURLs` | |
| `WithHTTPClient` | |
| `WithTimeout` | 30s |
| `WithRateLimit` | 100 rps, burst 20 |
| `WithMaxRetries` | 3, GET requests only |
| `WithMaxResponseBytes` | 32 MiB |
| `WithStreamStallTimeout` | 30s |
| `WithReadOnly` | |
| `WithDryRun` | |
| `WithLogger` | |
| `WithUserAgent` | |

## Security model

| Threat | Mitigation |
| --- | --- |
| Token disclosure | `oanda.Token` redacts itself in `fmt`, JSON, text and `slog`. The CLI reads it only from the environment or a `0600` file, and logs never include the token or bodies. |
| Token sent to another host | Only HTTPS (loopback HTTP for tests), TLS ≥ 1.2, and redirects are never followed. `GetTransactionPage` only fetches URLs on the client's own API host. |
| Injection or path traversal | Account IDs, instruments, order and trade specifiers, transaction IDs, enums, decimals and timestamps are validated, and path segments are escaped. |
| Duplicate orders | Only idempotent GETs are retried. POST, PUT and PATCH are sent once. |
| Runaway agents | Practice is the default. Live trading requires explicit opt-in. Read-only mode, dry runs, `OANDA_MAX_ORDER_UNITS`, a client-side rate limit (below OANDA's 120 req/s), bounded MCP streams, a 32 MiB response limit and a 1 MiB stream line limit also apply. |
| Supply chain | No third-party modules. CI runs golangci-lint, gosec, govulncheck, OSV-Scanner, Trivy, Semgrep and CodeQL. |

## Development

```sh
make check   # gofmt, go vet, golangci-lint, gosec, tests with -race, govulncheck
make qa      # full local QA (also lints Markdown and GitHub Actions workflows)
```

CI uses the shared [`go-package-lint-and-scan`](https://github.com/dceoy/gha-for-devops/blob/main/.github/workflows/go-package-lint-and-scan.yml) and [`go-package-test`](https://github.com/dceoy/gha-for-devops/blob/main/.github/workflows/go-package-test.yml) workflows from `dceoy/gha-for-devops`. Agent skills live in `.agents/skills/`; `.claude/skills` is a symlink to that directory.

## License

[AGPL-3.0](LICENSE)
