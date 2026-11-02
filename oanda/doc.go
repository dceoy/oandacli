// Package oanda is a small, dependency-free client for the OANDA REST-V20 API
// (https://developer.oanda.com/rest-live-v20/introduction/).
//
// The client is designed to be safe to hand to automated callers such as AI
// agents:
//
//   - The API token is held in a [Token] that never prints, logs or marshals
//     its value.
//   - Only HTTPS endpoints are accepted (plain HTTP is allowed for loopback
//     test servers only), TLS 1.2 is the minimum, and redirects are never
//     followed so the bearer token cannot leak to another host.
//   - Path parameters are validated and escaped before a request is built.
//   - Requests are rate-limited client-side, response bodies are size-capped,
//     and only idempotent GET requests are retried (orders are never
//     re-submitted automatically).
//   - A client can be made read-only ([WithReadOnly]) so that no
//     state-changing request can be sent, and a dry-run mode ([WithDryRun])
//     returns the request that would have been sent instead of sending it.
//
// Every response type embeds [ResponseMeta], whose RawJSON method returns the
// exact JSON body returned by OANDA, so fields that are not modelled by the
// typed structs are never lost.
package oanda
