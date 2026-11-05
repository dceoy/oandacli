package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dceoy/oandactl/oanda"
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
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("HOME", cache)
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
		{name: "read-only env cannot be revoked by flag", env: map[string]string{EnvReadOnly: "true"}, args: []string{"order", "cancel", "--order", "1", "--read-only=false"}, wantCode: ExitPolicy, wantKind: "policy"},
		{name: "max units limit whitespace only", env: map[string]string{EnvMaxOrderUnits: " "}, args: []string{"order", "market", "--instrument", "EUR_USD", "--units", "1"}, wantCode: ExitUsage, wantKind: "usage"},
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

func TestTransactionStreamHeartbeats(t *testing.T) {
	const (
		first       = `{"type":"HEARTBEAT","time":"2026-11-05T11:10:23.257Z","lastTransactionID":"351219"}`
		second      = `{"type":"HEARTBEAT","time":"2026-11-05T11:10:23.292Z","lastTransactionID":"351219"}`
		transaction = `{"type":"ORDER_FILL","id":"351220"}`
	)
	for _, tt := range []struct {
		name    string
		lines   []string
		include bool
		limit   int
		want    []string
	}{
		{"single heartbeat is not duplicated", []string{first, transaction}, true, 2, []string{first, transaction}},
		{"startup pair is preserved", []string{first, second, transaction}, true, 3, []string{first, second, transaction}},
		{"identical server lines are not deduplicated", []string{first, first, transaction}, true, 3, []string{first, first, transaction}},
		{"included heartbeats count toward limit", []string{first, second, transaction}, true, 2, []string{first, second}},
		{"hidden heartbeats do not count", []string{first, second, transaction}, false, 1, []string{transaction}},
		{"no synthetic heartbeat", []string{transaction}, true, 1, []string{transaction}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v3/accounts/"+testAccount+"/transactions/stream" {
					t.Errorf("unexpected stream path %s", r.URL.Path)
				}
				for _, line := range tt.lines {
					_, _ = io.WriteString(w, line+"\n")
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			})
			args := []string{"transaction", "stream", "--duration", "5s", "--max-messages", strconv.Itoa(tt.limit)}
			if tt.include {
				args = append(args, "--include-heartbeats")
			}
			if code := h.run(args...); code != ExitOK {
				t.Fatalf("exit %d: %s", code, h.stderr.String())
			}
			if want := strings.Join(tt.want, "\n") + "\n"; h.stdout.String() != want {
				t.Errorf("stdout = %q, want %q", h.stdout.String(), want)
			}
			if h.calls.Load() != 1 {
				t.Errorf("stream requests = %d, want 1", h.calls.Load())
			}
		})
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

func writeScript(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts are not available on Windows")
	}
	p := filepath.Join(t.TempDir(), "helper.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil { // #nosec G306 -- executable test helper
		t.Fatal(err)
	}
	return p
}

func TestTokenCommand(t *testing.T) {
	// The helper must not inherit the parent environment.
	t.Setenv(EnvToken, "inherited-secret")
	tests := []struct {
		name     string
		script   string
		env      map[string]string
		wantCode int
		wantAuth string
		wantMsg  string
	}{
		{name: "surrogate token", script: `[ -z "$OANDA_API_TOKEN" ] || exit 9; echo hsurr:abc123`, wantAuth: "Bearer hsurr:abc123"},
		{name: "command wins over env", script: "echo from-command", env: map[string]string{EnvToken: "from-env"}, wantAuth: "Bearer from-command"},
		{name: "failure hides output", script: "echo stdout-secret; echo stderr-secret >&2; exit 3", wantCode: ExitUsage, wantMsg: "exit status 3"},
		{name: "invalid token hides output", script: "echo 'bad secret value'", wantCode: ExitUsage, wantMsg: "whitespace"},
		{name: "empty output", script: "true", wantCode: ExitUsage, wantMsg: "empty"},
		{name: "timeout", script: "exec sleep 5", wantCode: ExitUsage, wantMsg: "timed out"},
		{name: "unbounded output is cut off", script: "exec yes secret", wantCode: ExitUsage, wantMsg: "exceeds 4096 bytes"},
		{name: "background child holding stdout", script: "sleep 5 & echo secret-token", wantCode: ExitUsage, wantMsg: "holding its stdout open"},
	}
	defer func(d time.Duration) { tokenCommandTimeout = d }(tokenCommandTimeout)
	tokenCommandTimeout = 500 * time.Millisecond
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, okJSON(`{}`))
			h.env[EnvToken] = ""
			h.env["PATH"] = os.Getenv("PATH")
			h.env[EnvTokenCommand] = writeScript(t, tt.script)
			for k, v := range tt.env {
				h.env[k] = v
			}
			if code := h.run("account", "list"); code != tt.wantCode {
				t.Fatalf("exit = %d, want %d (stderr %s)", code, tt.wantCode, h.stderr.String())
			}
			if tt.wantAuth != "" {
				if got := h.last.Load().Header.Get("Authorization"); got != tt.wantAuth {
					t.Errorf("Authorization = %q", got)
				}
				return
			}
			if h.calls.Load() != 0 {
				t.Error("request sent without a token")
			}
			out := h.stderr.String()
			if !strings.Contains(out, tt.wantMsg) || strings.Contains(out, "secret") {
				t.Errorf("stderr = %s", out)
			}
		})
	}
}

func TestTokenFileWinsOverCommand(t *testing.T) {
	h := newHarness(t, okJSON(`{}`))
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("file-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.env[EnvTokenFile] = tokenPath
	h.env[EnvTokenCommand] = writeScript(t, "exit 1")
	if code := h.run("account", "list"); code != ExitOK {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	if got := h.last.Load().Header.Get("Authorization"); got != "Bearer file-token" {
		t.Errorf("Authorization = %q", got)
	}
}

func TestConfirmPlan(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("HOME", cache)
	order := []string{"order", "market", "--instrument", "EUR_USD", "--units", "100"}
	h := newHarness(t, okJSON(`{}`))
	if code := h.run(append(order, "--dry-run")...); code != ExitOK {
		t.Fatalf("dry run exit %d: %s", code, h.stderr.String())
	}
	var dry struct {
		Request       oanda.DryRunError `json:"request"`
		PlanHash      string            `json:"planHash"`
		PlanExpiresAt time.Time         `json:"planExpiresAt"`
	}
	if err := json.Unmarshal(h.stdout.Bytes(), &dry); err != nil || dry.PlanHash == "" || time.Until(dry.PlanExpiresAt) <= 0 {
		t.Fatalf("dry run stdout = %s", h.stdout.String())
	}
	plan := dry.PlanHash
	expired := planHash(oanda.Practice, testAccount, dry.Request.Method, dry.Request.URL, dry.Request.Body, time.Now().Add(-time.Second).Unix())
	farFuture := planHash(oanda.Practice, testAccount, dry.Request.Method, dry.Request.URL, dry.Request.Body, time.Now().Add(24*time.Hour).Unix())
	// Distinct expiries give distinct, still valid plans for cases that consume one.
	fresh := planHash(oanda.Practice, testAccount, dry.Request.Method, dry.Request.URL, dry.Request.Body, time.Now().Add(planTTL-time.Minute).Unix())
	tampered := plan[:len(plan)-1] + map[bool]string{true: "0", false: "1"}[plan[len(plan)-1] != '0']

	tests := []struct {
		name     string
		env      map[string]string
		args     []string
		wantCode int
		wantCall bool
	}{
		{name: "matching plan", args: append(order, "--confirm-plan", plan), wantCode: ExitOK, wantCall: true},
		{name: "replayed plan in a new invocation", args: append(order, "--confirm-plan", plan), wantCode: ExitPolicy},
		{name: "changed units", args: []string{"order", "market", "--instrument", "EUR_USD", "--units", "1000", "--confirm-plan", plan}, wantCode: ExitPolicy},
		{name: "other account", args: append(order, "--confirm-plan", plan, "--account", "101-004-1234567-002"), wantCode: ExitPolicy},
		{name: "other environment", env: map[string]string{EnvEnvironment: "live", EnvAllowLive: "true"}, args: append(order, "--confirm-plan", plan), wantCode: ExitPolicy},
		{name: "tampered hash", args: append(order, "--confirm-plan", tampered), wantCode: ExitPolicy},
		{name: "malformed", args: append(order, "--confirm-plan", "nope"), wantCode: ExitPolicy},
		{name: "expired", args: append(order, "--confirm-plan", expired), wantCode: ExitPolicy},
		{name: "expiry too far ahead", args: append(order, "--confirm-plan", farFuture), wantCode: ExitPolicy},
		{name: "plan does not bypass live guard", env: map[string]string{EnvEnvironment: "live"}, args: append(order, "--confirm-plan", plan), wantCode: ExitPolicy},
		{name: "plan does not bypass read-only", env: map[string]string{EnvReadOnly: "true"}, args: append(order, "--confirm-plan", plan), wantCode: ExitPolicy},
		{name: "required but missing", env: map[string]string{EnvRequirePlan: "true"}, args: order, wantCode: ExitPolicy},
		{name: "required and matching", env: map[string]string{EnvRequirePlan: "true"}, args: append(order, "--confirm-plan", fresh), wantCode: ExitOK, wantCall: true},
		{name: "required allows dry run", env: map[string]string{EnvRequirePlan: "true"}, args: append(order, "--dry-run"), wantCode: ExitOK},
		{name: "read command rejects plan", args: []string{"account", "summary", "--confirm-plan", plan}, wantCode: ExitUsage},
	}
	base := maps.Clone(h.env)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The plan covers the URL, so every case reuses the same server.
			h.env = maps.Clone(base)
			maps.Copy(h.env, tt.env)
			h.calls.Store(0)
			if code := h.run(tt.args...); code != tt.wantCode {
				t.Fatalf("exit = %d, want %d (stderr %s)", code, tt.wantCode, h.stderr.String())
			}
			if (h.calls.Load() > 0) != tt.wantCall {
				t.Errorf("server calls = %d, wantCall %v", h.calls.Load(), tt.wantCall)
			}
		})
	}
}

func TestConfirmPlanSeparateCaches(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("this test exercises XDG_CACHE_HOME on Linux")
	}
	h := newHarness(t, okJSON(`{}`))
	h.env[EnvRequirePlan] = "true"
	order := []string{"order", "market", "--instrument", "EUR_USD", "--units", "100"}
	if code := h.run(append(order, "--dry-run")...); code != ExitOK {
		t.Fatalf("dry run exit %d: %s", code, h.stderr.String())
	}
	var dry struct {
		PlanHash string `json:"planHash"`
	}
	if err := json.Unmarshal(h.stdout.Bytes(), &dry); err != nil || dry.PlanHash == "" {
		t.Fatalf("dry run stdout = %s", h.stdout.String())
	}
	// The same plan can send once per cache namespace, not once globally.
	// Returning to either persistent namespace still rejects its replay.
	a, b := t.TempDir(), t.TempDir()
	for _, tt := range []struct {
		name     string
		cache    string
		wantCode int
	}{
		{"first cache", a, ExitOK},
		{"separate cache allows the same plan", b, ExitOK},
		{"first cache still refuses replay", a, ExitPolicy},
		{"second cache still refuses replay", b, ExitPolicy},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("XDG_CACHE_HOME", tt.cache)
			h.calls.Store(0)
			if code := h.run(append(order, "--confirm-plan", dry.PlanHash)...); code != tt.wantCode {
				t.Fatalf("exit = %d, want %d (stderr %s)", code, tt.wantCode, h.stderr.String())
			}
			wantCalls := int32(0)
			if tt.wantCode == ExitOK {
				wantCalls = 1
			}
			if got := h.calls.Load(); got != wantCalls {
				t.Errorf("server calls = %d, want %d", got, wantCalls)
			}
		})
	}
}

func TestPlanCheckAllowsOneRequest(t *testing.T) {
	now := time.Now()
	dir := filepath.Join(t.TempDir(), "plans")
	stale := filepath.Join(dir, strconv.FormatInt(now.Add(-time.Minute).Unix(), 10)+".old")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	plan := planHash(oanda.Practice, testAccount, "PUT", "https://x/v3", []byte(`{}`), now.Add(time.Minute).Unix())
	// Separate callbacks model separate CLI invocations.
	for i, wantErr := range []bool{false, true} {
		err := planCheck(plan, oanda.Practice, testAccount, dir, func() time.Time { return now })("PUT", "https://x/v3", []byte(`{}`))
		var policy *PolicyError
		if wantErr != errors.As(err, &policy) {
			t.Errorf("request %d: err = %v, want error %v", i+1, err, wantErr)
		}
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expired marker was not pruned: %v", err)
	}
}

func TestPricingAccountCandles(t *testing.T) {
	h := newHarness(t, okJSON(`{"instrument":"EUR_USD","granularity":"M1","candles":[]}`))
	if code := h.run("pricing", "candles", "--instrument", "EUR_USD", "--granularity", "M1", "--units", "100"); code != ExitOK {
		t.Fatalf("exit %d: %s", code, h.stderr.String())
	}
	r := h.last.Load()
	if r.Method != http.MethodGet || r.URL.Path != "/v3/accounts/"+testAccount+"/instruments/EUR_USD/candles" {
		t.Errorf("request = %s %s", r.Method, r.URL.Path)
	}
	if r.URL.Query().Get("units") != "100" || r.URL.Query().Get("granularity") != "M1" {
		t.Errorf("query = %s", r.URL.RawQuery)
	}
}
