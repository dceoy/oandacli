package oanda

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type entropyFail struct{}

func (entropyFail) Read([]byte) (int, error) { return 0, errors.New("no entropy") }

func TestAdditionalValidatorFailures(t *testing.T) {
	order := NewLimitOrder("EUR_USD", "1", "1.2")
	order.TimeInForce = GTD
	if err := order.Validate(); err == nil {
		t.Fatal("missing GTD expiry accepted")
	}
	if err := (ClosePositionRequest{LongUnits: "1", ShortUnits: "bad"}).validate(); err == nil {
		t.Fatal("bad short units accepted")
	}
	instruments := make([]string, 101)
	for i := range instruments {
		instruments[i] = "EUR_USD"
	}
	if err := validateInstruments(instruments, true); err == nil {
		t.Fatal("too many instruments accepted")
	}
}

func TestModelAdditionalMalformedFields(t *testing.T) {
	var p PriceBucket
	if err := json.Unmarshal([]byte(`{"price":"1","liquidity":"1"}`), &p); err != nil || p.Liquidity != "1" {
		t.Errorf("price bucket: %+v %v", p, err)
	}
	for _, raw := range []string{`{"relatedTransactionIDs":4}`, `{"lastTransactionID":3}`, `{"orderFillTransaction":false}`} {
		var r TransactionResult
		if err := json.Unmarshal([]byte(raw), &r); err == nil {
			t.Errorf("accepted bad transaction field %s", raw)
		}
	}
}

func TestTransportRetryErrorBranches(t *testing.T) {
	ctx := context.Background()
	attempts := 0
	c, err := NewClient(mustToken(t), WithBaseURLs("http://127.0.0.1:1", "http://127.0.0.1:1"), WithRateLimit(0, 0), WithMaxRetries(1), WithHTTPClient(&http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return nil, fmt.Errorf("intermittent failure")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
	})}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListAccounts(ctx); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if attempts != 2 {
		t.Errorf("attempts=%d", attempts)
	}
	previous := rand.Reader
	rand.Reader = entropyFail{}
	defer func() { rand.Reader = previous }()
	if got := backoff(1); got != 250*time.Millisecond {
		t.Errorf("entropy failure backoff=%v", got)
	}
}

func TestTransportBeforeRequestAndRetryCancellation(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{}`) }, WithConnectionWait(func(ctx context.Context) error { return errors.New("blocked") }), WithMaxRetries(0))
	if _, err := c.ListAccounts(context.Background()); err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Errorf("connection limiter error=%v", err)
	}
	c2, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{}`) })
	c2.apiURL = &url.URL{Scheme: "http", Host: "example.com", Opaque: "bad host"}
	if _, err := c2.ListAccounts(context.Background()); err == nil {
		t.Errorf("malformed built request accepted")
	}
}

func TestStreamAdditionalFailureBranches(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{}`) }, WithConnectionWait(func(ctx context.Context) error { return errors.New("stream blocked") }))
	if err := c.StreamTransactions(ctx, testAccount, func(StreamMessage) error { return nil }); err == nil {
		t.Error("stream connection gate bypassed")
	}
	c2, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{}`) })
	c2.streamURL = &url.URL{Scheme: "http", Host: "example.com", Opaque: "bad host"}
	if err := c2.StreamTransactions(ctx, testAccount, func(StreamMessage) error { return nil }); err == nil {
		t.Error("invalid stream request accepted")
	}
}
