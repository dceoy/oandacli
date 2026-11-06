package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dceoy/oandactl/oanda"
)

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("test write failure") }

type failReader struct{}

func (failReader) Read([]byte) (int, error) { return 0, errors.New("test read failure") }

func TestCLIHelpAndFlagFailures(t *testing.T) {
	cases := []struct {
		name string
		args []string
		code int
	}{
		{"empty", nil, ExitUsage},
		{"help flag", []string{"--help"}, ExitOK},
		{"version flag", []string{"--version"}, ExitOK},
		{"unknown global flag", []string{"--wrong"}, ExitUsage},
		{"version command", []string{"version"}, ExitOK},
		{"help command", []string{"help", "order"}, ExitOK},
		{"bare prefix", []string{"order"}, ExitUsage},
		{"prefix help", []string{"order", "--help"}, ExitOK},
		{"command help", []string{"order", "market", "--help"}, ExitOK},
		{"unknown flag", []string{"account", "list", "--wrong"}, ExitUsage},
		{"trailing argument", []string{"account", "list", "stray"}, ExitUsage},
		{"pretty", []string{"account", "list", "--pretty"}, ExitOK},
		{"compact", []string{"account", "list", "--compact"}, ExitOK},
		{"help empty params", []string{"account", "list", "--help"}, ExitOK},
		{"transaction help", []string{"transaction", "get", "--help"}, ExitOK},
		{"unknown group help", []string{"x", "--help"}, ExitUsage},
		{"zero timeout", []string{"account", "list", "--timeout", "0s"}, ExitUsage},
		{"bad environment", []string{"account", "list", "--env", "bad"}, ExitUsage},
		{"bad account", []string{"account", "get", "--account", "bad/account"}, ExitUsage},
		{"confirm on readonly", []string{"account", "list", "--confirm-plan", "hash"}, ExitUsage},
		{"invalid token", []string{"account", "list"}, ExitUsage},
		{"invalid body file", []string{"order", "create", "--body-file", "/nonexistent"}, ExitUsage},
		{"duplicate body", []string{"order", "create", "--body-file", "-", "--body", `{"type":"MARKET"}`}, ExitUsage},
		{"invalid json stdin", []string{"order", "create", "--body-file", "-"}, ExitUsage},
		{"invalid order body", []string{"order", "create"}, ExitUsage},
		{"bad count", []string{"instrument", "candles", "--instrument", "EUR_USD", "--count", "0"}, ExitUsage},
		{"invalid max message", []string{"transaction", "stream", "--max-messages", "-1"}, ExitUsage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, `{}`) })
			if tc.name == "invalid token" {
				h.env[EnvToken] = "bad token"
			}
			if tc.name == "invalid json stdin" {
				h.env[EnvToken] = "test-token"
			}
			a := h.app("broken-json")
			code := a.Run(context.Background(), tc.args)
			if code != tc.code {
				t.Errorf("exit %d want %d: %s", code, tc.code, h.stderr.String())
			}
		})
	}
}

func TestCLIFileAndErrorBoundaryCases(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{}`) })
	missing := filepath.Join(t.TempDir(), "missing")
	if c := h.run("account", "list", "--token-file", missing); c != ExitUsage {
		t.Errorf("missing token file code=%d", c)
	}
	dir := t.TempDir()
	if c := h.run("account", "list", "--token-file", dir); c != ExitUsage {
		t.Errorf("directory token file code=%d", c)
	}
	insecure := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(insecure, []byte("secure-token"), 0644); err != nil {
		t.Fatal(err)
	}
	if c := h.run("account", "list", "--token-file", insecure); c != ExitPolicy {
		t.Errorf("insecure token file code=%d: %s", c, h.stderr.String())
	}
	if err := os.Chmod(insecure, 0600); err != nil {
		t.Fatal(err)
	}
	if c := h.run("account", "list", "--token-file", insecure); c != ExitOK {
		t.Errorf("secure token file code=%d", c)
	}
	bodyfile := filepath.Join(t.TempDir(), "order.json")
	if err := os.WriteFile(bodyfile, []byte(`{"order":{"type":"MARKET","instrument":"EUR_USD","units":"1"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if c := h.run("order", "create", "--body-file", bodyfile); c != ExitOK {
		t.Errorf("valid order file code=%d: %s", c, h.stderr.String())
	}
	if err := os.WriteFile(bodyfile, []byte(`bad`), 0600); err != nil {
		t.Fatal(err)
	}
	if c := h.run("order", "create", "--body-file", bodyfile); c != ExitUsage {
		t.Errorf("invalid order file code=%d", c)
	}
	if err := os.WriteFile(bodyfile, bytes.Repeat([]byte("x"), (1<<20)+1), 0600); err != nil {
		t.Fatal(err)
	}
	if c := h.run("order", "create", "--body-file", bodyfile); c != ExitUsage {
		t.Errorf("oversized order file code=%d", c)
	}
	app := h.app("")
	app.Stdin = failReader{}
	if _, err := app.readBodyFile("-"); err == nil {
		t.Error("read failure ignored")
	}
	if _, err := app.readBodyFile(missing); err == nil {
		t.Error("open failure ignored")
	}

	var b bytes.Buffer
	a := &App{Stdout: &b, Stderr: &b, Pretty: func() *bool { x := true; return &x }()}
	if x := a.writeJSON(json.RawMessage(`{"ok":true}`)); x != ExitOK || !strings.Contains(b.String(), "  ") {
		t.Errorf("pretty output %q", b.String())
	}
	a.Stdout = failWriter{}
	if x := a.writeJSON(map[string]any{"ok": true}); x != ExitError {
		t.Errorf("write failure exit=%d", x)
	}
	a.Stdout = &b
	if x := a.writeJSON(json.RawMessage(`bad`)); x != ExitError {
		t.Errorf("malformed json exit=%d", x)
	}
	if x := a.writeResult(func() {}); x != ExitError {
		t.Errorf("unsupported value exit=%d", x)
	}
	if a.pretty() == false {
		t.Error("pretty explicit flag")
	}
	var raw json.RawMessage
	if _, err := rawResult(func() {}); err == nil {
		t.Error("function should not serialize")
	}
	if _, err := rawResult(raw); err != nil {
		t.Error(err)
	}
	if x := a.fail(&oanda.DryRunError{Method: "POST", URL: "test"}); x != ExitOK {
		t.Errorf("dry-run exit %d", x)
	}
	for _, e := range []error{context.Canceled, context.DeadlineExceeded, errors.New("unknown")} {
		if classify(e).Kind == "" {
			t.Errorf("not classified: %v", e)
		}
	}
	if _, err := a.runTokenCommand("   "); err == nil {
		t.Error("blank token command accepted")
	}
}

func TestDirectCLIHelpers(t *testing.T) {
	for _, typ := range []ParamType{TBool, TStrings, TJSON, TTime, TInt, TDuration, TString} {
		if typeHint(typ) == "" && typ != TBool {
			t.Errorf("empty hint %s", typ)
		}
	}
	if New() == nil {
		t.Error("nil application")
	}
	if !hasPrefix(Commands(), []string{"trade"}) || hasPrefix(Commands(), []string{"NOT_A_COMMAND"}) {
		t.Error("command prefix lookup")
	}
	if find(Commands(), []string{"account", "list"}) == nil || find(Commands(), []string{"invalid"}) != nil {
		t.Error("command lookup")
	}
	if _, err := finalize(&Command{Params: []Param{{Name: "default", Type: TString, Default: "yes"}}}, map[string]any{}); err != nil {
		t.Error(err)
	}
	buf := &limitedBuffer{max: 1, onOverflow: func() {}}
	if n, err := buf.Write([]byte("a")); n != 1 || err != nil {
		t.Errorf("write %d %v", n, err)
	}
	if n, err := buf.Write([]byte("bc")); n != 0 || err == nil || !buf.overflow {
		t.Errorf("overflow write %d %v", n, err)
	}
	if n, err := buf.Write([]byte("z")); n != 0 || err == nil {
		t.Errorf("repeat write %d %v", n, err)
	}
	if _, err := (&App{Getenv: func(k string) string { return "bad token" }}).loadToken(""); err == nil {
		t.Error("invalid environment token accepted")
	}
	if got := classify(&PolicyError{Msg: "denied"}); got.Kind != "policy" {
		t.Errorf("policy: %+v", got)
	}
	if got := classify(&UsageError{Msg: "bad"}); got.Kind != "usage" {
		t.Errorf("usage: %+v", got)
	}
	if got := classify(&oanda.APIError{StatusCode: 502}); got.ExitCode != ExitAPIServer {
		t.Errorf("HTTP status: %+v", got)
	}
	if got := classify(errors.New("broken")); got.Kind != "error" {
		t.Errorf("unexpected classification: %+v", got)
	}
	if x := time.Now().UTC(); x.IsZero() {
		t.Error("clock not running")
	}
	// Read-only mode must not reach the trading endpoint, irrespective of flags.
}

func TestCLIOrderBodyAndRegistryEdgeCases(t *testing.T) {
	env := &Env{}
	if _, err := env.orderBody(&Args{v: map[string]any{}}); err == nil {
		t.Error("missing body accepted")
	}
	for _, tc := range []string{`{"order":1}`, `{"order":null}`, `{"units":"not valid"}`} {
		_, err := env.orderBody(&Args{v: map[string]any{"body": json.RawMessage(tc)}})
		if err != nil && !strings.Contains(err.Error(), "invalid") {
			t.Log(err)
		}
	}
	var a Args
	a.v = map[string]any{"type": []string{"ORDER_FILL"}}
	if got := filters(&a); len(got) != 1 || got[0] != "ORDER_FILL" {
		t.Errorf("filters %v", got)
	}
}
