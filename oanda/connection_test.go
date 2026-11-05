package oanda

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

// connectionResolver provides deterministic DNS answers without external network
// access. A net.Pipe uses the resolver's length-prefixed DNS-over-TCP framing.
func connectionResolver(t *testing.T, before func(context.Context) error, ips ...[4]byte) *net.Resolver {
	t.Helper()
	return &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		if before != nil {
			if err := before(ctx); err != nil {
				return nil, err
			}
		}
		client, server := net.Pipe()
		go func() {
			defer func() { _ = server.Close() }()
			var size [2]byte
			if _, err := io.ReadFull(server, size[:]); err != nil {
				return
			}
			query := make([]byte, binary.BigEndian.Uint16(size[:]))
			if _, err := io.ReadFull(server, query); err != nil {
				return
			}
			// Retain only the question (omit the query's optional EDNS record).
			end := 12
			for query[end] != 0 {
				end += int(query[end]) + 1
			}
			end += 5 // root label, QTYPE, QCLASS
			response := append([]byte(nil), query[:end]...)
			response[2], response[3] = 0x81, 0x80
			clear(response[6:12])
			if binary.BigEndian.Uint16(query[end-4:end-2]) == 1 { // A
				binary.BigEndian.PutUint16(response[6:8], uint16(len(ips))) // #nosec G115 -- test supplies at most two addresses
				for _, ip := range ips {
					response = append(response, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4)
					response = append(response, ip[:]...)
				}
			}
			binary.BigEndian.PutUint16(size[:], uint16(len(response))) // #nosec G115 -- bounded test DNS response
			_, _ = server.Write(append(size[:], response...))
		}()
		return client, nil
	}}
}

func TestConnectionWaitAfterDNS(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	const spacing = 150 * time.Millisecond
	var mu sync.Mutex
	var last time.Time
	wait := func(ctx context.Context) error {
		mu.Lock()
		defer mu.Unlock()
		timer := time.NewTimer(time.Until(last.Add(spacing)))
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			last = time.Now()
			return nil
		}
	}
	lookupStarted, releaseDNS := make(chan struct{}), make(chan struct{})
	var once sync.Once
	slow := newConnectionDialer(wait)
	slow.Resolver = connectionResolver(t, func(ctx context.Context) error {
		once.Do(func() { close(lookupStarted) })
		select {
		case <-releaseDNS:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}, [4]byte{127, 0, 0, 1})
	fast := newConnectionDialer(wait)
	fast.Resolver = connectionResolver(t, nil, [4]byte{127, 0, 0, 1})
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	addr := net.JoinHostPort("pacing.test", port)
	results := make(chan error, 2)
	dial := func(d *net.Dialer) {
		conn, dialErr := d.DialContext(ctx, "tcp4", addr)
		if conn != nil {
			_ = conn.Close()
		}
		results <- dialErr
	}
	accepted := make(chan time.Time, 2)
	go func() {
		for range 2 {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			accepted <- time.Now()
			_ = conn.Close()
		}
	}()
	go dial(slow)
	select {
	case <-lookupStarted:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	go dial(fast)
	var first time.Time
	select {
	case first = <-accepted:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// Release the earlier lookup only after the later invocation connected.
	// A gate before DNS would now allow these connections to arrive in a burst.
	close(releaseDNS)
	select {
	case second := <-accepted:
		if gap := second.Sub(first); gap < spacing*4/5 {
			t.Fatalf("post-DNS connection gap = %v, want at least %v", gap, spacing*4/5)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
}

func TestConnectionWaitEachAddress(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	blocked := errors.New("stop fallback")
	for _, tt := range []struct {
		name string
		stop bool
	}{
		{"fallback connects", false},
		{"fallback gate error", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var waits int
			d := newConnectionDialer(func(context.Context) error {
				waits++
				if tt.stop && waits == 2 {
					return blocked
				}
				return nil
			})
			// The first loopback address refuses; the second has a listener.
			d.Resolver = connectionResolver(t, nil, [4]byte{127, 0, 0, 2}, [4]byte{127, 0, 0, 1})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, port, _ := net.SplitHostPort(listener.Addr().String())
			conn, err := d.DialContext(ctx, "tcp4", net.JoinHostPort("pacing.test", port))
			if conn != nil {
				_ = conn.Close()
			}
			// Dialer returns the first address's error when all attempts fail.
			if (err != nil) != tt.stop || waits != 2 {
				t.Fatalf("dial error = %v, waits = %d; want error %t and 2 waits", err, waits, tt.stop)
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
