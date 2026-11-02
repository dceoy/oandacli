package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testAccount = "101-004-1234567-001"

type harness struct {
	srv    *httptest.Server
	env    map[string]string
	calls  atomic.Int32
	conns  atomic.Int32 // new TCP connections accepted by the server
	last   atomic.Pointer[http.Request]
	body   atomic.Pointer[[]byte]
	stdout bytes.Buffer
	stderr bytes.Buffer
}

func newHarness(t *testing.T, respond func(w http.ResponseWriter, r *http.Request)) *harness {
	t.Helper()
	h := &harness{}
	h.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.calls.Add(1)
		b, _ := io.ReadAll(r.Body)
		h.last.Store(r)
		h.body.Store(&b)
		respond(w, r)
	}))
	h.srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			h.conns.Add(1)
		}
	}
	h.srv.Start()
	t.Cleanup(h.srv.Close)
	h.env = map[string]string{
		EnvToken:     "test-token",
		EnvAccount:   testAccount,
		EnvAPIURL:    h.srv.URL,
		EnvStreamURL: h.srv.URL,
	}
	return h
}

func (h *harness) app(stdin string) *App {
	h.stdout.Reset()
	h.stderr.Reset()
	f := false
	return &App{Stdin: strings.NewReader(stdin), Stdout: &h.stdout, Stderr: &h.stderr, Getenv: func(k string) string { return h.env[k] }, Pretty: &f}
}

func (h *harness) run(args ...string) int { return h.app("").Run(context.Background(), args) }

func okJSON(body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) }
}

func TestReadCommandsPrintRawJSON(t *testing.T) {
	body := `{"account":{"id":"x","unmodelled":{"a":1}},"lastTransactionID":"9"}`
	h := newHarness(t, okJSON(body))
	if code := h.run("account", "summary"); code != ExitOK {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	if got := strings.TrimSpace(h.stdout.String()); got != body {
		t.Errorf("stdout = %s", got)
	}
	if got := h.last.Load().URL.Path; got != "/v3/accounts/"+testAccount+"/summary" {
		t.Errorf("path = %s", got)
	}
}

func TestExitCodesAndErrors(t *testing.T) {
	tests := []struct {
		name     string
		env      map[string]string
		args     []string
		status   int
		wantCode int
		wantKind string
		wantCall bool
	}{
		{name: "unknown command", args: []string{"nope"}, wantCode: ExitUsage, wantKind: "usage"},
		{name: "missing required", args: []string{"position", "get"}, wantCode: ExitUsage, wantKind: "usage"},
		{name: "positional arg", args: []string{"position", "get", "EUR_USD"}, wantCode: ExitUsage, wantKind: "usage"},
		{name: "bad enum", args: []string{"instrument", "candles", "--instrument", "EUR_USD", "--granularity", "X"}, wantCode: ExitUsage, wantKind: "usage"},
		{name: "bad int", args: []string{"order", "list", "--count", "ten"}, wantCode: ExitUsage, wantKind: "usage"},
		{name: "no token", env: map[string]string{EnvToken: ""}, args: []string{"account", "list"}, wantCode: ExitUsage, wantKind: "usage"},
		{name: "no account", env: map[string]string{EnvAccount: ""}, args: []string{"account", "summary"}, wantCode: ExitUsage, wantKind: "usage"},
		{name: "api 404", args: []string{"order", "get", "--order", "99"}, status: 404, wantCode: ExitAPIClient, wantKind: "api", wantCall: true},
		{name: "api 500", args: []string{"order", "cancel", "--order", "99"}, status: 500, wantCode: ExitAPIServer, wantKind: "api", wantCall: true},
		{name: "read-only refuses trading", env: map[string]string{EnvReadOnly: "true"}, args: []string{"order", "cancel", "--order", "1"}, wantCode: ExitPolicy, wantKind: "policy"},
		{name: "live refused without allow", env: map[string]string{EnvEnvironment: "live"}, args: []string{"trade", "close", "--trade", "1"}, wantCode: ExitPolicy, wantKind: "policy"},
		{name: "live allowed with flag", env: map[string]string{EnvEnvironment: "live"}, args: []string{"trade", "close", "--trade", "1", "--allow-live"}, wantCode: ExitOK, wantCall: true},
		{name: "max units", env: map[string]string{EnvMaxOrderUnits: "1000"}, args: []string{"order", "market", "--instrument", "EUR_USD", "--units", "-5000"}, wantCode: ExitPolicy, wantKind: "policy"},
		{name: "max units raw body", env: map[string]string{EnvMaxOrderUnits: "1000"}, args: []string{"order", "create", "--body", `{"order":{"type":"MARKET","instrument":"EUR_USD","units":"2000"}}`}, wantCode: ExitPolicy, wantKind: "policy"},
		{name: "max units raw body unverifiable", env: map[string]string{EnvMaxOrderUnits: "1000"}, args: []string{"order", "create", "--body", `{"order":{"type":"MARKET","instrument":"EUR_USD","units":"NaN"}}`}, wantCode: ExitPolicy, wantKind: "policy"},
		{name: "max units exact decimal at limit", env: map[string]string{EnvMaxOrderUnits: "1000.5"}, args: []string{"order", "market", "--instrument", "EUR_USD", "--units", "-1000.5"}, wantCode: ExitOK, wantCall: true},
		{name: "max units exact decimal above limit", env: map[string]string{EnvMaxOrderUnits: "1000.5"}, args: []string{"order", "market", "--instrument", "EUR_USD", "--units", "1000.50000000000000001"}, wantCode: ExitPolicy, wantKind: "policy"},
		{name: "max units limit NaN", env: map[string]string{EnvMaxOrderUnits: "NaN"}, args: []string{"order", "market", "--instrument", "EUR_USD", "--units", "1"}, wantCode: ExitUsage, wantKind: "usage"},
		{name: "max units limit Inf", env: map[string]string{EnvMaxOrderUnits: "Inf"}, args: []string{"order", "market", "--instrument", "EUR_USD", "--units", "1"}, wantCode: ExitUsage, wantKind: "usage"},
		{name: "max units limit +Inf", env: map[string]string{EnvMaxOrderUnits: "+Inf"}, args: []string{"order", "market", "--instrument", "EUR_USD", "--units", "1"}, wantCode: ExitUsage, wantKind: "usage"},
		{name: "max units limit exponent", env: map[string]string{EnvMaxOrderUnits: "1e3"}, args: []string{"order", "market", "--instrument", "EUR_USD", "--units", "1"}, wantCode: ExitUsage, wantKind: "usage"},
		{name: "max units limit zero", env: map[string]string{EnvMaxOrderUnits: "0"}, args: []string{"order", "market", "--instrument", "EUR_USD", "--units", "1"}, wantCode: ExitUsage, wantKind: "usage"},
		{name: "max units limit negative", env: map[string]string{EnvMaxOrderUnits: "-5"}, args: []string{"order", "market", "--instrument", "EUR_USD", "--units", "1"}, wantCode: ExitUsage, wantKind: "usage"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
				if tt.status != 0 {
					w.WriteHeader(tt.status)
					_, _ = io.WriteString(w, `{"errorCode":"E","errorMessage":"boom"}`)
					return
				}
				_, _ = io.WriteString(w, `{}`)
			})
			for k, v := range tt.env {
				h.env[k] = v
			}
			code := h.run(tt.args...)
			if code != tt.wantCode {
				t.Fatalf("exit = %d, want %d (stderr %s)", code, tt.wantCode, h.stderr.String())
			}
			if (h.calls.Load() > 0) != tt.wantCall {
				t.Errorf("server calls = %d, wantCall %v", h.calls.Load(), tt.wantCall)
			}
			if tt.wantKind != "" {
				var e struct {
					Error ErrorInfo `json:"error"`
				}
				if err := json.Unmarshal(h.stderr.Bytes(), &e); err != nil || e.Error.Kind != tt.wantKind {
					t.Errorf("stderr = %s", h.stderr.String())
				}
			}
		})
	}
}

func TestMarketOrderBody(t *testing.T) {
	h := newHarness(t, okJSON(`{"orderFillTransaction":{"id":"2","type":"ORDER_FILL"},"lastTransactionID":"2"}`))
	code := h.run("order", "market", "--instrument", "EUR_USD", "--units", "-100", "--tp-price", "1.05", "--sl-distance", "0.01", "--client-tag", "agent")
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	r := h.last.Load()
	if r.Method != http.MethodPost || r.URL.Path != "/v3/accounts/"+testAccount+"/orders" {
		t.Errorf("request = %s %s", r.Method, r.URL.Path)
	}
	want := `{"order":{"type":"MARKET","instrument":"EUR_USD","units":"-100","timeInForce":"FOK","positionFill":"DEFAULT","clientExtensions":{"tag":"agent"},"takeProfitOnFill":{"price":"1.05"},"stopLossOnFill":{"distance":"0.01"}}}`
	if got := string(*h.body.Load()); got != want {
		t.Errorf("body = %s\nwant  %s", got, want)
	}
}

func TestDryRunSendsNothing(t *testing.T) {
	h := newHarness(t, okJSON(`{}`))
	h.env[EnvEnvironment] = "live"
	h.env[EnvToken] = ""
	code := h.run("position", "close", "--instrument", "USD_JPY", "--long-units", "ALL", "--dry-run")
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	if h.calls.Load() != 0 {
		t.Error("dry run contacted the server")
	}
	var out struct {
		DryRun  bool `json:"dryRun"`
		Request struct {
			Method string          `json:"method"`
			URL    string          `json:"url"`
			Body   json.RawMessage `json:"body"`
		} `json:"request"`
	}
	if err := json.Unmarshal(h.stdout.Bytes(), &out); err != nil || !out.DryRun || out.Request.Method != "PUT" || string(out.Request.Body) != `{"longUnits":"ALL"}` {
		t.Errorf("stdout = %s", h.stdout.String())
	}
}

func TestBodyFileAndTokenFile(t *testing.T) {
	h := newHarness(t, okJSON(`{}`))
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenPath, []byte("file-token\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.env[EnvToken] = ""
	h.env[EnvTokenFile] = tokenPath
	if code := h.run("account", "list"); code != ExitPolicy {
		t.Errorf("world-readable token file: exit %d, want %d", code, ExitPolicy)
	}
	if err := os.Chmod(tokenPath, 0o600); err != nil {
		t.Fatal(err)
	}
	app := h.app(`{"type":"LIMIT","instrument":"EUR_USD","units":"10","price":"1.0"}`)
	if code := app.Run(context.Background(), []string{"order", "create", "--body-file", "-"}); code != ExitOK {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	if got := h.last.Load().Header.Get("Authorization"); got != "Bearer file-token" {
		t.Errorf("Authorization = %q", got)
	}
	if got := string(*h.body.Load()); !strings.HasPrefix(got, `{"order":{"type":"LIMIT"`) {
		t.Errorf("body = %s", got)
	}
}

func TestPricingStreamNDJSON(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		for _, l := range []string{`{"type":"HEARTBEAT"}`, `{"type":"PRICE","instrument":"EUR_USD"}`, `{"type":"PRICE", "instrument":"EUR_USD"}`, `{"type":"PRICE","instrument":"EUR_USD"}`} {
			_, _ = io.WriteString(w, l+"\n")
			w.(http.Flusher).Flush()
		}
		<-r.Context().Done()
	})
	if code := h.run("pricing", "stream", "--instruments", "EUR_USD", "--max-messages", "2"); code != ExitOK {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	want := "{\"type\":\"PRICE\",\"instrument\":\"EUR_USD\"}\n{\"type\":\"PRICE\",\"instrument\":\"EUR_USD\"}\n"
	if h.stdout.String() != want {
		t.Errorf("stdout = %q", h.stdout.String())
	}
}

func TestSchemaListsAllCommands(t *testing.T) {
	h := newHarness(t, okJSON(`{}`))
	if code := h.run("schema"); code != ExitOK {
		t.Fatal(code)
	}
	var s struct {
		Commands []struct {
			Command string         `json:"command"`
			Tool    string         `json:"tool"`
			Input   map[string]any `json:"inputSchema"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(h.stdout.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	if len(s.Commands) != len(Commands()) {
		t.Errorf("schema has %d commands, want %d", len(s.Commands), len(Commands()))
	}
	for _, c := range s.Commands {
		if _, ok := c.Input["properties"].(map[string]any)["body_file"]; ok {
			t.Errorf("%s exposes body_file in its input schema", c.Command)
		}
	}
}

// mcpSession runs the MCP server over the given requests and returns the
// responses keyed by id.
func mcpSession(t *testing.T, h *harness, flags []string, reqs ...string) map[string]map[string]any {
	t.Helper()
	app := h.app(strings.Join(reqs, "\n") + "\n")
	if code := app.Run(context.Background(), append([]string{"mcp"}, flags...)); code != ExitOK {
		t.Fatalf("mcp exit %d: %s", code, h.stderr.String())
	}
	out := map[string]map[string]any{}
	sc := bufio.NewScanner(&h.stdout)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("bad response line %q: %v", sc.Text(), err)
		}
		id, _ := json.Marshal(m["id"])
		out[string(id)] = m
	}
	return out
}

func toolNames(resp map[string]any) map[string]bool {
	names := map[string]bool{}
	for _, tool := range resp["result"].(map[string]any)["tools"].([]any) {
		names[tool.(map[string]any)["name"].(string)] = true
	}
	return names
}

func toolText(t *testing.T, resp map[string]any) (string, bool) {
	t.Helper()
	res, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %v", resp)
	}
	return res["content"].([]any)[0].(map[string]any)["text"].(string), res["isError"].(bool)
}

func TestMCPReadOnlyByDefault(t *testing.T) {
	h := newHarness(t, okJSON(`{"accounts":[{"id":"1"}]}`))
	resps := mcpSession(t, h, nil,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"oanda_account_list","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"oanda_order_market","arguments":{"instrument":"EUR_USD","units":"1"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"oanda_position_get","arguments":{"instrument":"EUR_USD","bogus":1}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"nope"}`,
		`not json`,
	)
	if v := resps["1"]["result"].(map[string]any)["protocolVersion"]; v != "2025-03-26" {
		t.Errorf("protocolVersion = %v", v)
	}
	names := toolNames(resps["2"])
	if !names["oanda_account_list"] || !names["oanda_pricing_stream"] || names["oanda_order_market"] || names["oanda_trade_close"] {
		t.Errorf("unexpected tool set: %v", names)
	}
	if text, isErr := toolText(t, resps["3"]); isErr || text != `{"accounts":[{"id":"1"}]}` {
		t.Errorf("account_list = %s (isError %v)", text, isErr)
	}
	if resps["4"]["error"] == nil {
		t.Errorf("trading tool callable without --allow-trading: %v", resps["4"])
	}
	if text, isErr := toolText(t, resps["5"]); !isErr || !strings.Contains(text, "unknown argument") {
		t.Errorf("unknown argument not rejected: %s", text)
	}
	if resps["6"]["error"].(map[string]any)["code"].(float64) != -32601 {
		t.Errorf("unknown method: %v", resps["6"])
	}
	if resps["null"]["error"] == nil {
		t.Errorf("parse error not reported")
	}
	if h.calls.Load() != 1 {
		t.Errorf("server calls = %d, want 1", h.calls.Load())
	}
}

func TestMCPTrading(t *testing.T) {
	h := newHarness(t, okJSON(`{"orderCreateTransaction":{"id":"3","type":"MARKET_ORDER"}}`))
	resps := mcpSession(t, h, []string{"--allow-trading"},
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"oanda_order_market","arguments":{"instrument":"EUR_USD","units":"10","dry_run":true}}}`,
	)
	if !toolNames(resps["1"])["oanda_order_market"] {
		t.Error("trading tools not exposed with --allow-trading")
	}
	text, isErr := toolText(t, resps["2"])
	if isErr || !strings.Contains(text, `"dryRun":true`) {
		t.Errorf("dry run = %s", text)
	}
	if h.calls.Load() != 0 {
		t.Error("dry run contacted the server")
	}

	// Live environment without --allow-live must not expose trading tools.
	h.env[EnvEnvironment] = "live"
	resps = mcpSession(t, h, []string{"--allow-trading"}, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if toolNames(resps["1"])["oanda_order_market"] {
		t.Error("trading tools exposed in live without --allow-live")
	}
}

func TestMCPStreamIsBounded(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 50; i++ {
			_, _ = io.WriteString(w, `{"type":"PRICE","instrument":"EUR_USD"}`+"\n")
		}
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	resps := mcpSession(t, h, nil,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"oanda_pricing_stream","arguments":{"instruments":["EUR_USD"]}}}`,
	)
	text, isErr := toolText(t, resps["1"])
	var out struct {
		Count int `json:"count"`
	}
	if isErr || json.Unmarshal([]byte(text), &out) != nil || out.Count != mcpDefaultMessages {
		t.Errorf("stream result = %s", text)
	}
}

func TestMCPServerDryRunCannotBeDisabledPerCall(t *testing.T) {
	h := newHarness(t, okJSON(`{}`))
	resps := mcpSession(t, h, []string{"--allow-trading", "--dry-run"},
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"oanda_order_market","arguments":{"instrument":"EUR_USD","units":"10","dry_run":false}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"oanda_trade_close","arguments":{"trade":"1"}}}`,
	)
	for _, id := range []string{"1", "2"} {
		if text, isErr := toolText(t, resps[id]); isErr || !strings.Contains(text, `"dryRun":true`) {
			t.Errorf("call %s = %s", id, text)
		}
	}
	if h.calls.Load() != 0 {
		t.Errorf("server received %d requests despite --dry-run", h.calls.Load())
	}
}

func TestMCPToolCallsShareConnections(t *testing.T) {
	h := newHarness(t, okJSON(`{"accounts":[]}`))
	reqs := make([]string, 0, 5)
	for i := 1; i <= 5; i++ {
		reqs = append(reqs, `{"jsonrpc":"2.0","id":`+string(rune('0'+i))+`,"method":"tools/call","params":{"name":"oanda_account_list","arguments":{}}}`)
	}
	// Send the calls one at a time so each can reuse the idle connection.
	app := h.app("")
	pr, pw := io.Pipe()
	app.Stdin = pr
	out := make(chan []string)
	var stdout syncBuffer
	app.Stdout = &stdout
	done := make(chan int)
	go func() { done <- app.Run(context.Background(), []string{"mcp"}) }()
	go func() {
		var got []string
		for _, r := range reqs {
			_, _ = io.WriteString(pw, r+"\n")
			for stdout.Lines() <= len(got) {
				time.Sleep(time.Millisecond)
			}
			got = append(got, r)
		}
		_ = pw.Close()
		out <- got
	}()
	<-out
	if code := <-done; code != ExitOK {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	if h.calls.Load() != 5 {
		t.Fatalf("calls = %d, want 5", h.calls.Load())
	}
	if n := h.conns.Load(); n != 1 {
		t.Errorf("server saw %d new connections for 5 sequential tool calls, want 1", n)
	}
}

func TestMCPStopsOnContextCancelWhileStdinIdle(t *testing.T) {
	h := newHarness(t, okJSON(`{}`))
	app := h.app("")
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	app.Stdin = pr
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- app.Run(ctx, []string{"mcp"}) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case code := <-done:
		if code != ExitOK {
			t.Errorf("exit %d: %s", code, h.stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("MCP server did not stop after context cancellation")
	}
}

// syncBuffer is a goroutine-safe bytes.Buffer that counts written lines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) Lines() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Count(b.buf.Bytes(), []byte("\n"))
}
