---
name: oanda-v20-docs
description: Search the official OANDA REST-V20 API documentation. Use when the user needs help with OANDA v20 endpoints, request parameters, response fields, definitions (orders, trades, transactions, pricing), errors, or rate limits, or when adding or changing endpoints in this repository.
---

# OANDA REST-V20 API Documentation Search

Search the official OANDA documentation at `https://developer.oanda.com/rest-live-v20/` for REST-V20 API usage.

## Agent-Agnostic Compatibility

- Prefer the agent's native web-fetch capability (for example: `WebFetch`, browser tool, or built-in URL retrieval).
- If no native fetch tool exists, use CLI fallback (`curl` or `wget`) to retrieve docs pages.
- If network access is blocked, state the limitation and provide the best guidance from the known API structure and this repository's `oanda/` package, and say which details could not be verified.

## Procedure

1. Identify the endpoint group(s) or definition(s) relevant to the user's query.
2. Fetch the corresponding documentation page(s) using the available web retrieval method.
3. Summarize the endpoint (method and path), parameters, request body, response fields, and errors.
4. If the user's query is general or unclear, fetch the introduction page first.
5. When changing this repository, map the result onto the matching file in `oanda/` and command in `internal/cli/commands.go`.

## Documentation URL Map

Base URL: `https://developer.oanda.com/rest-live-v20`

### Guides

| Topic                   | URL Path                   |
| ----------------------- | -------------------------- |
| Introduction            | `/introduction/`           |
| Development guide       | `/development-guide/`      |
| Best practices          | `/best-practices/`         |
| Troubleshooting, errors | `/troubleshooting-errors/` |

### Endpoints

| Endpoint group | URL Path           | Repository file                       |
| -------------- | ------------------ | ------------------------------------- |
| Account        | `/account-ep/`     | `oanda/account.go`                    |
| Instrument     | `/instrument-ep/`  | `oanda/instrument.go`                 |
| Order          | `/order-ep/`       | `oanda/order.go`                      |
| Trade          | `/trade-ep/`       | `oanda/trade.go`                      |
| Position       | `/position-ep/`    | `oanda/position.go`                   |
| Transaction    | `/transaction-ep/` | `oanda/transaction.go`                |
| Pricing        | `/pricing-ep/`     | `oanda/pricing.go`, `oanda/stream.go` |

### Definitions

| Definitions    | URL Path              |
| -------------- | --------------------- |
| Account        | `/account-df/`        |
| Instrument     | `/instrument-df/`     |
| Order          | `/order-df/`          |
| Trade          | `/trade-df/`          |
| Position       | `/position-df/`       |
| Transaction    | `/transaction-df/`    |
| Pricing        | `/pricing-df/`        |
| Pricing common | `/pricing-common-df/` |
| Primitives     | `/primitives-df/`     |

## Key Facts

- Hosts: practice `https://api-fxpractice.oanda.com` (stream `https://stream-fxpractice.oanda.com`); live `https://api-fxtrade.oanda.com` (stream `https://stream-fxtrade.oanda.com`).
- Authentication: `Authorization: Bearer <token>`; this repository also sends `Accept-Datetime-Format: RFC3339`.
- Prices, units and amounts are decimal strings.

## Fetching Documentation

For each relevant page, retrieve its documentation using whichever method the agent supports:

- Native fetch tools (preferred).
- Browser automation tools.
- Shell fallback, for example:

```bash
curl -fsSL "https://developer.oanda.com/rest-live-v20<url_path>"
```

Extraction target for each endpoint:

- HTTP method and path.
- Path, query, and header parameters with types and defaults.
- Request body schema.
- Response codes and body schemas.

When the query spans multiple pages, fetch them in parallel when the agent/tooling supports it.

## Response Format

Present the results as:

1. **Endpoint**: `METHOD /v3/...`
2. **Description**: What the endpoint does
3. **Parameters**: Table of parameter names, locations, types, and descriptions
4. **Request body**: Schema, if any
5. **Response**: Status codes and key fields
6. **Repository mapping**: SDK method and CLI command, if any
7. **Source**: Link to the official documentation page
