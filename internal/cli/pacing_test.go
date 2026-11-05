//go:build linux || darwin

package cli

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPacingState(t *testing.T) {
	for _, tt := range []struct {
		name    string
		state   string
		wantErr bool
	}{
		{"first connection", "", false},
		{"old timestamp", strconv.FormatInt(time.Now().Add(-time.Hour).UnixNano(), 10), false},
		{"malformed", "bad", true},
		{"negative", "-1", true},
		{"zero", "0", true},
		{"oversized", strings.Repeat("1", 65), true},
		{"future", strconv.FormatInt(time.Now().Add(time.Hour).UnixNano(), 10), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.state != "" {
				if err := os.WriteFile(filepath.Join(dir, "connections"), []byte(tt.state), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			// A first/old connection must not pay the 500ms interval.
			ctx, cancel := context.WithTimeout(context.Background(), connectionInterval/2)
			defer cancel()
			err := paceConnection(ctx, dir)
			if (err != nil) != tt.wantErr {
				t.Fatalf("pacing error = %v, want error %v", err, tt.wantErr)
			}
		})
	}
}

func TestPacingCancellation(t *testing.T) {
	for _, mode := range []string{"recent timestamp", "held lock", "already cancelled"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "connections")
			state := strconv.FormatInt(time.Now().UnixNano(), 10) + "\n"
			if err := os.WriteFile(path, []byte(state), 0o600); err != nil {
				t.Fatal(err)
			}
			if mode == "held lock" {
				f, err := os.OpenFile(path, os.O_RDWR, 0o600)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = f.Close() }()
				if err := lockConnection(context.Background(), f); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			if mode == "already cancelled" {
				cancel()
			}
			if err := paceConnection(ctx, dir); !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
				t.Fatalf("expected cancellation, got %v", err)
			}
			b, err := os.ReadFile(path)
			if err != nil || string(b) != state {
				t.Fatalf("cancelled wait changed state: %q, %v", b, err)
			}
		})
	}
}

func TestPacingCacheFailure(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(dir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := paceConnection(context.Background(), dir); err == nil {
		t.Fatal("unavailable cache allowed a connection")
	}
}

// The child runs the actual CLI in a separate process, with only fixture
// credentials and loopback URLs supplied by its parent (never a real API).
func TestConnectionPacingProcess(t *testing.T) {
	if os.Getenv("OANDA_PACING_TEST_CHILD") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Exit(New().Run(context.Background(), os.Args[i+1:]))
		}
	}
	t.Fatal("missing child arguments")
}

func TestCrossInvocationPacing(t *testing.T) {
	commands := [][]string{
		{"account", "summary"},
		{"transaction", "stream", "--max-messages", "1", "--include-heartbeats", "--duration", "5s"},
		{"order", "cancel", "--order", "123", "--env", "live", "--allow-live"},
	}
	for _, parallel := range []bool{false, true} {
		name := "sequential"
		if parallel {
			name = "concurrent"
		}
		t.Run(name, func(t *testing.T) {
			arrivals := make(chan time.Time, len(commands))
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				arrivals <- time.Now()
				_, _ = io.WriteString(w, "{\"type\":\"HEARTBEAT\"}\n")
			}))
			defer srv.Close()
			cache := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			var children []*exec.Cmd
			for _, args := range commands {
				childArgs := append([]string{"-test.run=^TestConnectionPacingProcess$", "--"}, args...)
				cmd := exec.CommandContext(ctx, os.Args[0], childArgs...) // #nosec G204 -- this test binary, fixture arguments
				cmd.Env = []string{
					"OANDA_PACING_TEST_CHILD=1", "GORACE=atexit_sleep_ms=0", "HOME=" + cache, "XDG_CACHE_HOME=" + cache,
					EnvAPIURL + "=" + srv.URL, EnvStreamURL + "=" + srv.URL,
					EnvAccount + "=" + testAccount, EnvToken + "=fixture-token",
				}
				if parallel {
					if err := cmd.Start(); err != nil {
						t.Fatal(err)
					}
					children = append(children, cmd)
				} else if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("CLI child failed: %v: %s", err, out)
				}
			}
			for _, cmd := range children {
				if err := cmd.Wait(); err != nil {
					t.Fatalf("CLI child failed: %v", err)
				}
			}
			var last time.Time
			for range commands {
				select {
				case now := <-arrivals:
					// Small tolerance for scheduler/network delay between dial and
					// server arrival; the timestamp reservation itself is 500ms.
					if !last.IsZero() && now.Sub(last) < connectionInterval-50*time.Millisecond {
						t.Fatalf("connections only %v apart", now.Sub(last))
					}
					last = now
				default:
					t.Fatal("missing CLI request")
				}
			}
		})
	}
}

func TestNoConnectionDoesNotTouchPacingState(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want int
	}{
		{"dry run", []string{"order", "cancel", "--order", "123", "--dry-run"}, ExitOK},
		{"help", []string{"--help"}, ExitOK},
		{"policy refusal", []string{"order", "cancel", "--order", "123", "--read-only"}, ExitPolicy},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, okJSON(`{}`))
			if code := h.run(tt.args...); code != tt.want {
				t.Fatalf("exit %d: %s", code, h.stderr.String())
			}
			dir, err := os.UserCacheDir()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(dir, "oandactl", "connections")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("touched pacing state: %v", err)
			}
			if h.calls.Load() != 0 {
				t.Fatal("reached server")
			}
		})
	}
}

func TestPacingFailureSendsNothing(t *testing.T) {
	for _, args := range [][]string{
		{"order", "cancel", "--order", "123"},
		{"transaction", "stream", "--duration", "5s"},
	} {
		t.Run(strings.Join(args[:2], " "), func(t *testing.T) {
			h := newHarness(t, okJSON(`{}`))
			dir, err := os.UserCacheDir()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(dir, "oandactl"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "oandactl", "connections"), []byte("bad"), 0o600); err != nil {
				t.Fatal(err)
			}
			if code := h.run(args...); code != ExitError || !strings.Contains(h.stderr.String(), "invalid connection pacing timestamp") {
				t.Fatalf("exit %d: %s", code, h.stderr.String())
			}
			if h.calls.Load() != 0 {
				t.Fatal("pacing failure reached server")
			}
		})
	}
}
