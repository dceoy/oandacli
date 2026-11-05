package oanda

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestConnectionWait(t *testing.T) {
	blocked := errors.New("connection blocked")
	for _, tt := range []struct {
		name      string
		close     bool
		dry       bool
		block     bool
		stream    bool
		wantWaits int32
		wantCalls int32
	}{
		{"reused REST connection", false, false, false, false, 1, 2},
		{"new REST connections", true, false, false, false, 2, 2},
		{"dry run", false, true, false, false, 0, 0},
		{"blocked dial", false, false, true, false, 2, 0},
		{"stream connection", true, false, false, true, 2, 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var waits, calls atomic.Int32
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if tt.close {
					w.Header().Set("Connection", "close")
				}
				if tt.stream {
					_, _ = io.WriteString(w, "{\"type\":\"HEARTBEAT\"}\n")
				} else {
					_, _ = io.WriteString(w, `{}`)
				}
			}, WithMaxRetries(0), WithDryRun(tt.dry), WithConnectionWait(func(context.Context) error {
				waits.Add(1)
				if tt.block {
					return blocked
				}
				return nil
			}))
			for range 2 {
				var err error
				if tt.stream {
					err = c.StreamTransactions(context.Background(), testAccount, func(StreamMessage) error { return ErrStopStream })
				} else {
					_, err = c.ListAccounts(context.Background())
				}
				var dry *DryRunError
				switch {
				case tt.dry && !errors.As(err, &dry):
					t.Fatalf("dry run error = %v", err)
				case tt.block && !errors.Is(err, blocked):
					t.Fatalf("blocked error = %v", err)
				case !tt.dry && !tt.block && err != nil:
					t.Fatal(err)
				}
			}
			if waits.Load() != tt.wantWaits || calls.Load() != tt.wantCalls {
				t.Fatalf("waits/calls = %d/%d, want %d/%d", waits.Load(), calls.Load(), tt.wantWaits, tt.wantCalls)
			}
		})
	}
}

func TestConnectionWaitRetries(t *testing.T) {
	for _, tt := range []struct {
		name  string
		write bool
		want  int32
	}{
		{"GET reconnect is paced", false, 2},
		{"write is not retried", true, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var waits, calls atomic.Int32
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Connection", "close")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = io.WriteString(w, `{}`)
			}, WithMaxRetries(1), WithConnectionWait(func(context.Context) error {
				waits.Add(1)
				return nil
			}))
			var err error
			if tt.write {
				_, err = c.CancelOrder(context.Background(), testAccount, "123")
			} else {
				_, err = c.ListAccounts(context.Background())
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("request error = %v", err)
			}
			if waits.Load() != tt.want || calls.Load() != tt.want {
				t.Fatalf("waits/calls = %d/%d, want %d", waits.Load(), calls.Load(), tt.want)
			}
		})
	}
}

func TestConnectionWaitCustomClient(t *testing.T) {
	for _, opts := range [][]Option{
		{WithHTTPClient(&http.Client{}), WithConnectionWait(func(context.Context) error { return nil })},
		{WithConnectionWait(func(context.Context) error { return nil }), WithHTTPClient(&http.Client{})},
	} {
		if _, err := NewClient(mustToken(t), opts...); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("custom client/connection wait combination error = %v", err)
		}
	}
}
