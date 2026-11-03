package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const testAccount = "101-004-1234567-001"

type harness struct {
	srv    *httptest.Server
	env    map[string]string
	calls  atomic.Int32
	last   atomic.Pointer[http.Request]
	body   atomic.Pointer[[]byte]
	stdout bytes.Buffer
	stderr bytes.Buffer
}

func newHarness(t *testing.T, respond func(w http.ResponseWriter, r *http.Request)) *harness {
	t.Helper()
	h := &harness{}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.calls.Add(1)
		b, _ := io.ReadAll(r.Body)
		h.last.Store(r)
		h.body.Store(&b)
		respond(w, r)
	}))
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
		{name: "no mcp server", args: []string{"mcp"}, wantCode: ExitUsage, wantKind: "usage"},
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
		Name     string `json:"name"`
		Commands []struct {
			Command  string                     `json:"command"`
			Usage    string                     `json:"usage"`
			Mutating bool                       `json:"mutating"`
			Flags    map[string]json.RawMessage `json:"flags"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(h.stdout.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	if len(s.Commands) != len(Commands()) {
		t.Errorf("schema has %d commands, want %d", len(s.Commands), len(Commands()))
	}
	if s.Name != "oandactl" {
		t.Errorf("name = %q", s.Name)
	}
	byName := map[string]int{}
	for i, c := range s.Commands {
		byName[c.Command] = i
		if c.Usage != "oandactl "+c.Command+" [--flags]" {
			t.Errorf("%s usage = %q", c.Command, c.Usage)
		}
	}
	market, ok := byName["order market"]
	if !ok || !s.Commands[market].Mutating {
		t.Fatalf("order market missing or not marked mutating")
	}
	if _, ok := s.Commands[market].Flags["--units"]; !ok {
		t.Errorf("order market flags lack --units: %v", s.Commands[market].Flags)
	}
}

func TestPricingStreamReportsFailureAfterPartialOutput(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"type":"PRICE","instrument":"EUR_USD"}`+"\n")
	})
	code := h.run("pricing", "stream", "--instruments", "EUR_USD", "--max-messages", "5")
	if code != ExitError {
		t.Fatalf("exit = %d, want %d", code, ExitError)
	}
	if got := strings.Count(h.stdout.String(), "\n"); got != 1 {
		t.Errorf("stdout lines = %d, want 1: %q", got, h.stdout.String())
	}
	if !strings.Contains(h.stderr.String(), "stream closed by server") {
		t.Errorf("stderr = %s", h.stderr.String())
	}
}
