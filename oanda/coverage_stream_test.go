package oanda

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestStreamingErrorsAndMessageDecoding(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestClient(t, streamHandler(nil, false))
	if err := c.StreamPricing(ctx, "bad/account", PricingStreamParams{Instruments: []string{"EUR_USD"}}, func(StreamMessage) error { return nil }); err == nil {
		t.Error("invalid account accepted")
	}
	if err := c.StreamPricing(ctx, testAccount, PricingStreamParams{Instruments: []string{"invalid"}}, func(StreamMessage) error { return nil }); err == nil {
		t.Error("invalid instrument accepted")
	}
	if err := c.StreamTransactions(ctx, "bad/account", func(StreamMessage) error { return nil }); err == nil {
		t.Error("invalid account accepted")
	}
	if err := c.StreamTransactions(ctx, testAccount, nil); err == nil {
		t.Error("nil handler accepted")
	}
	m := StreamMessage{Type: "HEARTBEAT", Raw: []byte(`{"type":"HEARTBEAT"}`)}
	if !m.IsHeartbeat() {
		t.Error("expected heartbeat")
	}
	if _, err := m.Price(); err == nil {
		t.Error("heartbeat not price")
	}
	if _, err := m.Transaction(); err == nil {
		t.Error("heartbeat not transaction")
	}
	for _, tc := range []StreamMessage{{Type: "PRICE", Raw: []byte(`not json`)}, {Type: "ORDER_FILL", Raw: []byte(`not json`)}} {
		if tc.Type == "PRICE" {
			if _, err := tc.Price(); err == nil {
				t.Error("bad price JSON accepted")
			}
		} else {
			if _, err := tc.Transaction(); err == nil {
				t.Error("bad transaction JSON accepted")
			}
		}
	}
	msg := StreamMessage{Type: "ORDER_FILL", Raw: []byte(`{"type":"ORDER_FILL","id":"9"}`)}
	if got, err := msg.Transaction(); err != nil || got.ID != "9" {
		t.Errorf("transaction=%v err=%v", got, err)
	}
}

func TestStreamServerErrorPaths(t *testing.T) {
	for _, tc := range []struct {
		name       string
		body       string
		status     int
		handlerErr error
		want       string
	}{
		{"server 403", `{"errorCode":"DENIED"}`, 403, nil, "HTTP 403"},
		{"invalid json", `broken`, 200, nil, "malformed stream"},
		{"handler error", `{"type":"PRICE"}`, 200, errors.New("handler failed"), "handler failed"},
		{"empty lines", "\n   \n", 200, nil, "closed by server"},
		{"oversize", strings.Repeat("x", maxStreamLine+1), 200, nil, "read stream"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				fmt.Fprintln(w, tc.body)
			}, WithMaxRetries(0))
			err := c.StreamTransactions(context.Background(), testAccount, func(StreamMessage) error { return tc.handlerErr })
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err=%v, want %q", err, tc.want)
			}
		})
	}
}

func TestStreamDialErrorsAndOptions(t *testing.T) {
	ctx := context.Background()
	c, err := NewClient(mustToken(t), WithBaseURLs("http://127.0.0.1:1", "http://127.0.0.1:1"), WithRateLimit(0, 0), WithHTTPClient(&http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })}))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.StreamTransactions(ctx, testAccount, func(StreamMessage) error { return nil }); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Errorf("dial error %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := c.StreamTransactions(cancelled, testAccount, func(StreamMessage) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled stream: %v", err)
	}
	c2, _ := newTestClient(t, streamHandler([]string{`{"type":"HEARTBEAT"}`}, false))
	if err := c2.StreamPricing(ctx, testAccount, PricingStreamParams{Instruments: []string{"EUR_USD"}, NoSnapshot: true, IncludeHomeConversions: true}, func(m StreamMessage) error { return ErrStopStream }); err != nil {
		t.Errorf("stream options %v", err)
	}
	// Context deadline is enforced independently of the configured stall timer.
	h, _ := newTestClient(t, streamHandler(nil, true), WithStreamStallTimeout(time.Hour))
	deadline, done := context.WithTimeout(ctx, 50*time.Millisecond)
	defer done()
	if err := h.StreamTransactions(deadline, testAccount, func(StreamMessage) error { return nil }); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("deadline: %v", err)
	}
}
