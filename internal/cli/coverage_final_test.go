package cli

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFinalCommandValidationPaths(t *testing.T) {
	h := newHarness(t, okJSON(`{}`))
	if code := h.run("order", "market", "--instrument", "EUR_USD", "--units", "1", "--position-fill", "DEFAULT", "--dry-run"); code != ExitOK {
		t.Errorf("position-fill dry run code=%d: %s", code, h.stderr.String())
	}
	if code := h.run("order", "replace", "--order", "1", "--body", `{"order":{"units":[]}}`); code != ExitUsage && code != ExitOK {
		t.Errorf("array units code=%d", code)
	}
	if code := h.run("pricing", "candles", "--instrument", "EUR_USD", "--count", "0"); code != ExitUsage {
		t.Errorf("zero candle count code=%d", code)
	}
	if code := h.run("transaction", "stream", "--max-messages", "-1"); code != ExitUsage {
		t.Errorf("negative max-messages code=%d", code)
	}
	if code := h.run("order", "replace", "--order", "1"); code != ExitUsage {
		t.Errorf("missing body code=%d", code)
	}
}

func TestFinalStreamEmitFailureAndDeadline(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"type":"ORDER_FILL"}`+"\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	a := h.app("")
	a.Stdout = failWriter{}
	if code := a.Run(context.Background(), []string{"transaction", "stream", "--max-messages", "1"}); code != ExitError {
		t.Errorf("emit failure code=%d: %s", code, h.stderr.String())
	}
	if code := h.run("transaction", "stream", "--duration", "100ms"); code != ExitOK {
		t.Errorf("duration deadline code=%d: %s", code, h.stderr.String())
	}
}

func TestFinalMissingCacheDirectory(t *testing.T) {
	h := newHarness(t, okJSON(`{}`))
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "")
	if code := h.run("order", "market", "--instrument", "EUR_USD", "--units", "1", "--confirm-plan", "1.x"); code != ExitPolicy {
		t.Errorf("confirm-plan without cache dir code=%d: %s", code, h.stderr.String())
	}
	if err := waitForConnection(context.Background()); err == nil {
		t.Error("connection pacing without cache dir succeeded")
	}
}

func TestFinalTokenFileReadFailures(t *testing.T) {
	h := newHarness(t, okJSON(`{}`))
	empty := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(empty, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.app("").loadToken(empty); err == nil {
		t.Error("empty token file accepted")
	}
	if _, err := readTokenFile(failReader{}); err == nil || !strings.Contains(err.Error(), "cannot read token file") {
		t.Errorf("unreadable token file: %v", err)
	}
}

func TestFinalPrettyAndWriteJSONFailures(t *testing.T) {
	h := newHarness(t, okJSON(`{}`))
	a := h.app("")
	a.Pretty = nil
	f, err := os.CreateTemp(t.TempDir(), "closed")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	a.Stdout = f
	if a.pretty() {
		t.Error("closed file reported as a terminal")
	}
	dev, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dev.Close() }()
	a.Stdout = dev
	if !a.pretty() {
		t.Error("character device not reported as a terminal")
	}
	if code := a.writeJSON(make(chan int)); code != ExitError {
		t.Errorf("unmarshalable value code=%d", code)
	}
}

func TestFinalFilesystemFailures(t *testing.T) {
	// Force the plan-file creation error independently of user privileges.
	// The plan directory exists, but this nested marker parent does not.
	if err := consumePlan(t.TempDir(), filepath.Join("missing", "plan"), time.Now().Unix()); err == nil {
		t.Error("plan marker unexpectedly created")
	}

	f, err := os.CreateTemp(t.TempDir(), "lock")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err := lockConnection(context.Background(), f); err == nil || errors.Is(err, context.Canceled) {
		t.Errorf("lock on closed file: %v", err)
	}
	held, err := os.CreateTemp(t.TempDir(), "lock")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	if err := lockConnection(context.Background(), held); err != nil {
		t.Fatal(err)
	}
	waiter, err := os.Open(held.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = waiter.Close() }()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := lockConnection(cancelled, waiter); !errors.Is(err, context.Canceled) {
		t.Errorf("lock with cancelled context: %v", err)
	}
}

type failingPacingFile struct {
	readErr     error
	truncateErr error
	writeErr    error
}

func (f failingPacingFile) Read([]byte) (int, error) {
	return 0, f.readErr
}

func (f failingPacingFile) Truncate(int64) error {
	return f.truncateErr
}

func (f failingPacingFile) WriteAt([]byte, int64) (int, error) {
	return 0, f.writeErr
}

func TestPacingStateIOFailures(t *testing.T) {
	tests := []struct {
		name  string
		state failingPacingFile
		want  string
	}{
		{"read", failingPacingFile{readErr: errors.New("read failure")}, "connection pacing read"},
		{"truncate", failingPacingFile{readErr: io.EOF, truncateErr: errors.New("truncate failure")}, "connection pacing write"},
		{"write", failingPacingFile{readErr: io.EOF, writeErr: errors.New("write failure")}, "connection pacing write"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := paceLockedConnection(context.Background(), tc.state); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want %q", err, tc.want)
			}
		})
	}
}
