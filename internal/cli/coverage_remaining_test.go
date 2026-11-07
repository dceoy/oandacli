package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMoreAppErrorAndFlagPaths(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{}`) })
	if code := h.run("account", "list", "--pretty", "stray"); code != ExitUsage {
		t.Errorf("unexpected trailing arg accepted: %d", code)
	}
	if code := h.run("account", "list", "--verbose"); code != ExitOK {
		t.Errorf("verbose code=%d", code)
	}
	h.env[EnvAPIURL] = "invalid URL"
	if code := h.run("account", "list"); code == ExitOK {
		t.Error("bad endpoint URL accepted")
	}
	a := h.app("")
	if code := a.writeResult(make(chan int)); code != ExitError {
		t.Errorf("marshal error result code %d", code)
	}
	if code := a.writeJSON(json.RawMessage(`{bad`)); code != ExitError {
		t.Errorf("compact bad raw JSON result code %d", code)
	}
	a.Stdout = os.Stdout // cover file status path without producing output
	_ = a.pretty()
	f, err := os.CreateTemp(t.TempDir(), "terminal")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	a.Stdout = f
	_ = a.pretty()
	a.printCommandHelp(io.Discard, &Command{Path: []string{"example"}, Params: []Param{{Name: "number", Type: TInt, Default: 12, Desc: "int"}}}, flag.NewFlagSet("example", flag.ContinueOnError))
}

func TestMoreCommandErrors(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{}`) })
	cases := [][]string{
		{"instrument", "candles", "--instrument", "EUR_USD", "--count", "0"},
		{"instrument", "candles", "--instrument", "EUR_USD", "--count", "0", "--units", "1"},
		{"order", "replace", "--order", "1", "--body", "[]"},
		{"trade", "client-extensions", "--trade", "1"},
		{"trade", "client-extensions", "--trade", "1", "--client-id", ""},
		{"order", "market", "--instrument", "EUR_USD", "--units", "1", "--time-in-force", "GTC"},
	}
	for _, args := range cases {
		if code := h.run(args...); code == ExitOK {
			t.Errorf("unexpected success: %v", args)
		}
	}
	if p := precision(new(big.Rat).SetFrac64(1, 3)); p != 20 {
		t.Errorf("inexact precision %d", p)
	}
}

func TestMorePlanAndPacingFailurePaths(t *testing.T) {
	badParent := filepath.Join(t.TempDir(), "blocking-file")
	if err := os.WriteFile(badParent, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := consumePlan(filepath.Join(badParent, "plans"), "someplan", time.Now().Unix()); err == nil {
		t.Error("plan directory unexpectedly created")
	}
	if err := consumePlan(badParent, "hash", time.Now().Unix()); err == nil {
		t.Error("plan marker unexpectedly created")
	}
	if err := paceConnection(context.Background(), filepath.Join(badParent, "connections")); err == nil {
		t.Error("pacing dir unexpectedly created")
	}
	base := t.TempDir()
	if err := os.Mkdir(filepath.Join(base, "connections"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := paceConnection(context.Background(), base); err == nil {
		t.Error("pacing file unexpectedly opened")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := paceConnection(ctx, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled pacing: %v", err)
	}
}

func TestMoreParamAndStreamingPaths(t *testing.T) {
	if _, err := (Param{Name: "ids", Type: TStrings}).convert(strings.Repeat("a,", 1001)); err == nil {
		t.Error("oversize list accepted")
	}
	// Raw JSON success and explicit compact result both exercise formatting paths.
	var b bytes.Buffer
	a := &App{Stdout: &b, Stderr: io.Discard}
	if code := a.writeJSON(json.RawMessage(`{"ok":true}`)); code != ExitOK {
		t.Errorf("raw json result %d", code)
	}
	if !strings.Contains(b.String(), `"ok"`) {
		t.Errorf("json output=%s", b.String())
	}
}
