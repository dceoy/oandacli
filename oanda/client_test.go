package oanda

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testAccount = "101-004-1234567-001"

func mustToken(t *testing.T) Token {
	t.Helper()
	tok, err := NewToken("secret-token-value")
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func newTestClient(t *testing.T, h http.HandlerFunc, opts ...Option) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	opts = append([]Option{WithBaseURLs(srv.URL, srv.URL), WithRateLimit(0, 0)}, opts...)
	c, err := NewClient(mustToken(t), opts...)
	if err != nil {
		t.Fatal(err)
	}
	return c, srv
}

func TestTokenIsNeverRevealed(t *testing.T) {
	tok := mustToken(t)
	var logBuf bytes.Buffer
	slog.New(slog.NewJSONHandler(&logBuf, nil)).Info("x", "token", tok)
	j, _ := json.Marshal(struct{ T Token }{tok})
	outputs := map[string]string{
		"%v":   fmt.Sprintf("%v", tok),
		"%s":   fmt.Sprintf("%s", tok),
		"%#v":  fmt.Sprintf("%#v", tok),
		"%+v":  fmt.Sprintf("%+v", struct{ T Token }{tok}),
		"json": string(j),
		"slog": logBuf.String(),
	}
	for name, out := range outputs {
		if strings.Contains(out, "secret-token-value") {
			t.Errorf("%s leaked the token: %s", name, out)
		}
	}
}

func TestNewTokenRejectsBadInput(t *testing.T) {
	for _, in := range []string{"", "   ", "abc def", "abc\ndef", "tökén"} {
		if _, err := NewToken(in); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("NewToken(%q) = %v, want ErrInvalidArgument", in, err)
		}
	}
}

func TestBaseURLValidation(t *testing.T) {
	tests := []struct {
		url string
		ok  bool
	}{
		{"https://api-fxpractice.oanda.com", true},
		{"http://127.0.0.1:8080", true},
		{"http://localhost:1", true},
		{"http://api-fxpractice.oanda.com", false},
		{"https://user:pw@example.com", false},
		{"https://example.com/?q=1", false},
		{"ftp://example.com", false},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			_, err := NewClient(mustToken(t), WithBaseURLs(tt.url, ""))
			if (err == nil) != tt.ok {
				t.Errorf("err = %v, want ok=%v", err, tt.ok)
			}
		})
	}
}

func TestRequestHeadersAndRawJSON(t *testing.T) {
	body := `{"account":{"id":"101-004-1234567-001","currency":"USD","balance":"1000.5","unknownField":42},"lastTransactionID":"7"}`
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/v3/accounts/"+testAccount+"/summary" {
			t.Errorf("path = %s", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret-token-value" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("Accept-Datetime-Format"); got != "RFC3339" {
			t.Errorf("Accept-Datetime-Format = %q", got)
		}
		w.Header().Set("RequestID", "req-1")
		_, _ = io.WriteString(w, body)
	})
	res, err := c.GetAccountSummary(context.Background(), testAccount)
	if err != nil {
		t.Fatal(err)
	}
	if res.Account.Balance != "1000.5" || res.LastTransactionID != "7" || res.RequestID() != "req-1" {
		t.Errorf("unexpected response: %+v", res)
	}
	if string(res.RawJSON()) != body {
		t.Errorf("RawJSON = %s", res.RawJSON())
	}
}

func TestRetries(t *testing.T) {
	tests := []struct {
		name      string
		call      func(*Client) error
		wantCalls int32
		wantErr   bool
	}{
		{"GET retried until success", func(c *Client) error { _, err := c.ListAccounts(context.Background()); return err }, 3, false},
		{"POST never retried", func(c *Client) error {
			_, err := c.CreateOrder(context.Background(), testAccount, NewMarketOrder("EUR_USD", "1"))
			return err
		}, 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) < 3 {
					w.Header().Set("Retry-After", "0")
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				_, _ = io.WriteString(w, `{"accounts":[]}`)
			})
			err := tt.call(c)
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got := calls.Load(); got != tt.wantCalls {
				t.Errorf("calls = %d, want %d", got, tt.wantCalls)
			}
		})
	}
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	var leaked atomic.Bool
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			leaked.Store(true)
		}
	}))
	defer evil.Close()
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evil.URL+"/steal", http.StatusFound)
	})
	_, err := c.ListAccounts(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusFound {
		t.Fatalf("err = %v, want APIError 302", err)
	}
	if leaked.Load() {
		t.Fatal("token was sent to the redirect target")
	}
}

func TestReadOnlyAndDryRun(t *testing.T) {
	var calls atomic.Int32
	h := func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }
	ro, _ := newTestClient(t, h, WithReadOnly(true))
	if _, err := ro.CancelOrder(context.Background(), testAccount, "12"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("read-only err = %v", err)
	}
	dry, _ := newTestClient(t, h, WithDryRun(true))
	_, err := dry.CloseTrade(context.Background(), testAccount, "@my-trade", "")
	var d *DryRunError
	if !errors.As(err, &d) {
		t.Fatalf("dry-run err = %v", err)
	}
	if d.Method != http.MethodPut || !strings.HasSuffix(d.URL, "/trades/@my-trade/close") || string(d.Body) != `{"units":"ALL"}` {
		t.Errorf("dry run = %+v body=%s", d, d.Body)
	}
	if calls.Load() != 0 {
		t.Errorf("server was called %d times", calls.Load())
	}
}

func TestValidationPreventsRequests(t *testing.T) {
	c, err := NewClient(mustToken(t), WithBaseURLs("http://127.0.0.1:1", "http://127.0.0.1:1"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tests := map[string]func() error{
		"account traversal": func() error { _, err := c.GetAccount(ctx, "../../v3/accounts"); return err },
		"bad instrument":    func() error { _, err := c.GetCandles(ctx, "EUR/USD", CandlesParams{}); return err },
		"lowercase instr":   func() error { _, err := c.GetPosition(ctx, testAccount, "eur_usd"); return err },
		"bad specifier":     func() error { _, err := c.GetOrder(ctx, testAccount, "12/cancel"); return err },
		"bad granularity":   func() error { _, err := c.GetCandles(ctx, "EUR_USD", CandlesParams{Granularity: "X1"}); return err },
		"count too large":   func() error { _, err := c.GetCandles(ctx, "EUR_USD", CandlesParams{Count: 5001}); return err },
		"bad price comps":   func() error { _, err := c.GetCandles(ctx, "EUR_USD", CandlesParams{Price: "MMX"}); return err },
		"zero units":        func() error { _, err := c.CreateOrder(ctx, testAccount, NewMarketOrder("EUR_USD", "0")); return err },
		"limit no price": func() error {
			_, err := c.CreateOrder(ctx, testAccount, NewLimitOrder("EUR_USD", "10", ""))
			return err
		},
		"market GTC": func() error {
			o := NewMarketOrder("EUR_USD", "10")
			o.TimeInForce = GTC
			_, err := c.CreateOrder(ctx, testAccount, o)
			return err
		},
		"sl price and distance": func() error {
			o := NewMarketOrder("EUR_USD", "10")
			o.StopLossOnFill = &StopLossDetails{Price: "1.1", Distance: "0.01"}
			_, err := c.CreateOrder(ctx, testAccount, o)
			return err
		},
		"raw order bad type": func() error {
			_, err := c.CreateOrderRaw(ctx, testAccount, json.RawMessage(`{"type":"NOPE"}`))
			return err
		},
		"close nothing": func() error {
			_, err := c.ClosePosition(ctx, testAccount, "EUR_USD", ClosePositionRequest{})
			return err
		},
		"close bad units":    func() error { _, err := c.CloseTrade(ctx, testAccount, "1", "-5"); return err },
		"trade orders empty": func() error { _, err := c.SetTradeOrders(ctx, testAccount, "1", TradeOrdersRequest{}); return err },
		"bad tx type":        func() error { _, err := c.GetTransactionsSinceID(ctx, testAccount, "1", "order;drop"); return err },
		"pricing no instr":   func() error { _, err := c.GetPricing(ctx, testAccount, PricingParams{}); return err },
		"bad candle spec": func() error {
			_, err := c.GetLatestCandles(ctx, testAccount, LatestCandlesParams{Specifications: []string{"EUR_USD:X9:M"}})
			return err
		},
	}
	for name, fn := range tests {
		t.Run(name, func(t *testing.T) {
			if err := fn(); !errors.Is(err, ErrInvalidArgument) {
				t.Errorf("err = %v, want ErrInvalidArgument", err)
			}
		})
	}
}

func TestOrderRejectionAPIError(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Order OrderRequest `json:"order"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Order.Instrument != "EUR_USD" {
			t.Errorf("body = %+v, %v", body, err)
		}
		w.Header().Set("RequestID", "r9")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"orderRejectTransaction":{"id":"5","type":"MARKET_ORDER_REJECT","rejectReason":"INSUFFICIENT_MARGIN"},"errorCode":"INSUFFICIENT_MARGIN","errorMessage":"Insufficient margin"}`)
	})
	_, err := c.CreateOrder(context.Background(), testAccount, NewMarketOrder("EUR_USD", "1000000"))
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v", err)
	}
	if apiErr.ErrorCode != "INSUFFICIENT_MARGIN" || apiErr.RequestID != "r9" || !bytes.Contains(apiErr.Body, []byte("orderRejectTransaction")) {
		t.Errorf("apiErr = %+v", apiErr)
	}
}

func TestTransactionResult(t *testing.T) {
	raw := `{"orderCreateTransaction":{"id":"10","type":"MARKET_ORDER","instrument":"EUR_USD","units":"100","extra":1},"orderFillTransaction":{"id":"11","type":"ORDER_FILL","price":"1.1"},"orderCancelTransaction":null,"relatedTransactionIDs":["10","11"],"lastTransactionID":"11"}`
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, raw) })
	res, err := c.CreateOrder(context.Background(), testAccount, NewMarketOrder("EUR_USD", "100"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Transactions) != 2 || res.Transaction("orderFillTransaction").Price != "1.1" || res.LastTransactionID != "11" {
		t.Errorf("res = %+v", res)
	}
	if got, _ := json.Marshal(res.Transaction("orderCreateTransaction")); !bytes.Contains(got, []byte(`"extra":1`)) {
		t.Errorf("transaction did not round-trip raw JSON: %s", got)
	}
}

func TestTradeOrdersRequestJSON(t *testing.T) {
	got, err := json.Marshal(TradeOrdersRequest{StopLoss: &StopLossDetails{Distance: "0.01"}, CancelTakeProfit: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"stopLoss":{"distance":"0.01"},"takeProfit":null}`; string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestResponseSizeLimit(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"accounts":[`+strings.Repeat(" ", 2048)+`]}`)
	}, WithMaxResponseBytes(1024), WithMaxRetries(0))
	if _, err := c.ListAccounts(context.Background()); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Errorf("err = %v", err)
	}
}

func TestQueryEncoding(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		want := map[string]string{"granularity": "H1", "price": "BA", "from": "2024-01-02T03:04:05Z", "includeFirst": "false", "dailyAlignment": "0"}
		for k, v := range want {
			if q.Get(k) != v {
				t.Errorf("%s = %q, want %q", k, q.Get(k), v)
			}
		}
		_, _ = io.WriteString(w, `{"instrument":"EUR_USD","granularity":"H1","candles":[{"time":"2024-01-02T03:00:00Z","bid":{"o":"1","h":"2","l":"0.5","c":"1.5"},"volume":5,"complete":true}]}`)
	})
	f, zero := false, 0
	res, err := c.GetCandles(context.Background(), "EUR_USD", CandlesParams{
		Granularity: H1, Price: "BA", From: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC), IncludeFirst: &f, DailyAlignment: &zero,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Candles) != 1 || res.Candles[0].Bid.C != "1.5" {
		t.Errorf("res = %+v", res)
	}
}

func TestGetTransactionPageRejectsForeignHosts(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/accounts/"+testAccount+"/transactions/idrange" || r.URL.Query().Get("from") != "1" {
			t.Errorf("unexpected request %s", r.URL)
		}
		_, _ = io.WriteString(w, `{"transactions":[{"id":"1","type":"CREATE"}],"lastTransactionID":"1"}`)
	})
	for _, u := range []string{
		"https://evil.example.com/v3/accounts/" + testAccount + "/transactions/idrange?from=1&to=2",
		srv.URL + "/v3/accounts/" + testAccount + "/orders",
	} {
		if _, err := c.GetTransactionPage(context.Background(), u); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("GetTransactionPage(%s) = %v", u, err)
		}
	}
	res, err := c.GetTransactionPage(context.Background(), srv.URL+"/v3/accounts/"+testAccount+"/transactions/idrange?from=1&to=2")
	if err != nil || len(res.Transactions) != 1 {
		t.Errorf("res = %+v, err = %v", res, err)
	}
}

func TestParseEnvironment(t *testing.T) {
	tests := map[string]Environment{"": Practice, "PRACTICE": Practice, "demo": Practice, "live": Live, "fxtrade": Live}
	for in, want := range tests {
		if got, err := ParseEnvironment(in); err != nil || got != want {
			t.Errorf("ParseEnvironment(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := ParseEnvironment("prod"); err == nil {
		t.Error("expected error for unknown environment")
	}
}
