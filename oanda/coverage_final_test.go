package oanda

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestDoAndStreamRequestFailures(t *testing.T) {
	c, _ := newTestClient(t, streamHandler(nil, true), WithMaxRetries(2))
	bad := &request{method: "BAD METHOD", path: "/v3/accounts"}
	if err := c.do(context.Background(), bad, nil); err == nil || !strings.Contains(err.Error(), "build request") {
		t.Errorf("do build error: %v", err)
	}
	if err := c.stream(context.Background(), bad, func(StreamMessage) error { return nil }); err == nil || !strings.Contains(err.Error(), "build request") {
		t.Errorf("stream build error: %v", err)
	}
	if err := c.stream(context.Background(), &request{method: http.MethodGet, path: "/v3/accounts"}, nil); err == nil {
		t.Error("nil stream handler accepted")
	}
}

func TestDoCancelledWhileWaiting(t *testing.T) {
	get := &request{method: http.MethodGet, path: "/v3/accounts"}

	// The rate limiter observes a cancelled context before sending.
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {}, WithRateLimit(1, 1))
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.do(cancelled, get, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("limiter cancel: %v", err)
	}

	// Cancellation during a transport failure wins over the retry.
	ctx, cancelTransport := context.WithCancel(context.Background())
	tc, err := NewClient(mustToken(t), WithBaseURLs("http://127.0.0.1:1", "http://127.0.0.1:1"), WithRateLimit(0, 0), WithMaxRetries(2),
		WithHTTPClient(&http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			cancelTransport()
			return nil, errors.New("offline")
		})}))
	if err != nil {
		t.Fatal(err)
	}
	if err := tc.do(ctx, get, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("transport cancel: %v", err)
	}

	// Cancellation while sleeping before a transport retry.
	ctx, cancelSleep := context.WithCancel(context.Background())
	sc, err := NewClient(mustToken(t), WithBaseURLs("http://127.0.0.1:1", "http://127.0.0.1:1"), WithRateLimit(0, 0), WithMaxRetries(2),
		WithHTTPClient(&http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			time.AfterFunc(20*time.Millisecond, cancelSleep)
			return nil, errors.New("offline")
		})}))
	if err != nil {
		t.Fatal(err)
	}
	if err := sc.do(ctx, get, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("transport retry sleep cancel: %v", err)
	}

	// Cancellation while sleeping before a retryable status retry.
	ctx, cancelStatus := context.WithCancel(context.Background())
	rc, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		time.AfterFunc(20*time.Millisecond, cancelStatus)
		w.WriteHeader(http.StatusServiceUnavailable)
	}, WithMaxRetries(2))
	if err := rc.do(ctx, get, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("status retry sleep cancel: %v", err)
	}
}

func TestStreamCancelAndStall(t *testing.T) {
	handler := func(StreamMessage) error { return nil }

	// Parent cancelled while the dial fails.
	ctx, cancel := context.WithCancel(context.Background())
	dc, err := NewClient(mustToken(t), WithBaseURLs("http://127.0.0.1:1", "http://127.0.0.1:1"), WithRateLimit(0, 0),
		WithHTTPClient(&http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			cancel()
			return nil, errors.New("offline")
		})}))
	if err != nil {
		t.Fatal(err)
	}
	if err := dc.StreamTransactions(ctx, testAccount, handler); !errors.Is(err, context.Canceled) {
		t.Errorf("dial cancel: %v", err)
	}

	// Stall detected while dialing (headers never arrive).
	sc, err := NewClient(mustToken(t), WithBaseURLs("http://127.0.0.1:1", "http://127.0.0.1:1"), WithRateLimit(0, 0), WithStreamStallTimeout(20*time.Millisecond),
		WithHTTPClient(&http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
			<-r.Context().Done()
			return nil, r.Context().Err()
		})}))
	if err != nil {
		t.Fatal(err)
	}
	if err := sc.StreamTransactions(context.Background(), testAccount, handler); !errors.Is(err, ErrStreamStalled) {
		t.Errorf("dial stall: %v", err)
	}

	// Stall detected while reading an open stream.
	hc, _ := newTestClient(t, streamHandler([]string{`{"type":"HEARTBEAT"}`}, true), WithStreamStallTimeout(50*time.Millisecond))
	if err := hc.StreamTransactions(context.Background(), testAccount, handler); !errors.Is(err, ErrStreamStalled) {
		t.Errorf("read stall: %v", err)
	}
}

func TestModelUnmarshalErrorsAndRawMarshal(t *testing.T) {
	var p PriceBucket
	if err := p.UnmarshalJSON([]byte(`{`)); err == nil {
		t.Error("invalid price bucket accepted")
	}
	var r TransactionResult
	if err := r.UnmarshalJSON([]byte(`[`)); err == nil {
		t.Error("invalid transaction result accepted")
	}
	r.raw = []byte(`{"lastTransactionID":"9"}`)
	if b, err := r.MarshalJSON(); err != nil || string(b) != `{"lastTransactionID":"9"}` {
		t.Errorf("raw marshal %s %v", b, err)
	}
}

func TestStreamPrepareErrorAndParentCancelWhileReading(t *testing.T) {
	denied := errors.New("mutation denied")
	c, _ := newTestClient(t, streamHandler(nil, false), WithMutationCheck(func(string, string, []byte) error { return denied }))
	if err := c.stream(context.Background(), &request{method: http.MethodPost, path: "/v3/accounts"}, func(StreamMessage) error { return nil }); !errors.Is(err, denied) {
		t.Errorf("prepare error: %v", err)
	}

	hc, _ := newTestClient(t, streamHandler([]string{`{"type":"HEARTBEAT"}`}, true), WithStreamStallTimeout(time.Hour))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := hc.StreamTransactions(ctx, testAccount, func(StreamMessage) error {
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("parent cancel while reading: %v", err)
	}
}
