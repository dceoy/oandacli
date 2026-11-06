package oanda

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Every endpoint must dispatch to the expected account-scoped route using an
// isolated test server. These tests never contact the OANDA service.
func TestAllEndpointRoutes(t *testing.T) {
	ctx := context.Background()
	day := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	account := testAccount
	base := "/v3/accounts/" + account
	orders := NewMarketOrder("EUR_USD", "1")
	ext := ClientExtensions{ID: "demo"}
	tests := []struct {
		name, method, path string
		query              url.Values
		call               func(*Client) error
	}{
		{"accounts", "GET", "/v3/accounts", nil, func(c *Client) error { _, e := c.ListAccounts(ctx); return e }},
		{"account", "GET", base, nil, func(c *Client) error { _, e := c.GetAccount(ctx, account); return e }},
		{"summary", "GET", base + "/summary", nil, func(c *Client) error { _, e := c.GetAccountSummary(ctx, account); return e }},
		{"instruments", "GET", base + "/instruments", url.Values{"instruments": {"EUR_USD,USD_JPY"}}, func(c *Client) error { _, e := c.GetAccountInstruments(ctx, account, "EUR_USD", "USD_JPY"); return e }},
		{"configure", "PATCH", base + "/configuration", nil, func(c *Client) error {
			_, e := c.ConfigureAccount(ctx, account, AccountConfiguration{Alias: "my account", MarginRate: "0.02"})
			return e
		}},
		{"changes", "GET", base + "/changes", url.Values{"sinceTransactionID": {"12"}}, func(c *Client) error { _, e := c.GetAccountChanges(ctx, account, "12"); return e }},
		{"create order", "POST", base + "/orders", nil, func(c *Client) error { _, e := c.CreateOrder(ctx, account, orders); return e }},
		{"create raw order", "POST", base + "/orders", nil, func(c *Client) error {
			_, e := c.CreateOrderRaw(ctx, account, json.RawMessage(`{"type":"MARKET","instrument":"EUR_USD","units":"1"}`))
			return e
		}},
		{"list orders", "GET", base + "/orders", url.Values{"ids": {"1,2"}, "state": {"PENDING"}, "instrument": {"EUR_USD"}, "count": {"10"}, "beforeID": {"99"}}, func(c *Client) error {
			_, e := c.ListOrders(ctx, account, OrdersParams{IDs: []string{"1", "2"}, State: OrderStateFilter("PENDING"), Instrument: "EUR_USD", Count: 10, BeforeID: "99"})
			return e
		}},
		{"pending orders", "GET", base + "/pendingOrders", nil, func(c *Client) error { _, e := c.ListPendingOrders(ctx, account); return e }},
		{"get order", "GET", base + "/orders/@mine", nil, func(c *Client) error { _, e := c.GetOrder(ctx, account, "@mine"); return e }},
		{"replace order", "PUT", base + "/orders/17", nil, func(c *Client) error { _, e := c.ReplaceOrder(ctx, account, "17", orders); return e }},
		{"replace raw order", "PUT", base + "/orders/17", nil, func(c *Client) error {
			_, e := c.ReplaceOrderRaw(ctx, account, "17", json.RawMessage(`{"type":"MARKET","units":"1","instrument":"EUR_USD"}`))
			return e
		}},
		{"cancel order", "PUT", base + "/orders/17/cancel", nil, func(c *Client) error { _, e := c.CancelOrder(ctx, account, "17"); return e }},
		{"order extensions", "PUT", base + "/orders/17/clientExtensions", nil, func(c *Client) error {
			_, e := c.SetOrderClientExtensions(ctx, account, "17", ClientExtensionsRequest{ClientExtensions: &ext})
			return e
		}},
		{"trades", "GET", base + "/trades", url.Values{"ids": {"1,2"}, "state": {"OPEN"}, "instrument": {"EUR_USD"}, "count": {"10"}, "beforeID": {"99"}}, func(c *Client) error {
			_, e := c.ListTrades(ctx, account, TradesParams{IDs: []string{"1", "2"}, State: TradeStateFilter("OPEN"), Instrument: "EUR_USD", Count: 10, BeforeID: "99"})
			return e
		}},
		{"open trades", "GET", base + "/openTrades", nil, func(c *Client) error { _, e := c.ListOpenTrades(ctx, account); return e }},
		{"get trade", "GET", base + "/trades/17", nil, func(c *Client) error { _, e := c.GetTrade(ctx, account, "17"); return e }},
		{"close trade", "PUT", base + "/trades/17/close", nil, func(c *Client) error { _, e := c.CloseTrade(ctx, account, "17", "1.5"); return e }},
		{"trade extensions", "PUT", base + "/trades/17/clientExtensions", nil, func(c *Client) error { _, e := c.SetTradeClientExtensions(ctx, account, "17", ext); return e }},
		{"trade orders", "PUT", base + "/trades/17/orders", nil, func(c *Client) error {
			_, e := c.SetTradeOrders(ctx, account, "17", TradeOrdersRequest{CancelTakeProfit: true})
			return e
		}},
		{"positions", "GET", base + "/positions", nil, func(c *Client) error { _, e := c.ListPositions(ctx, account); return e }},
		{"open positions", "GET", base + "/openPositions", nil, func(c *Client) error { _, e := c.ListOpenPositions(ctx, account); return e }},
		{"position", "GET", base + "/positions/EUR_USD", nil, func(c *Client) error { _, e := c.GetPosition(ctx, account, "EUR_USD"); return e }},
		{"close position", "PUT", base + "/positions/EUR_USD/close", nil, func(c *Client) error {
			_, e := c.ClosePosition(ctx, account, "EUR_USD", ClosePositionRequest{LongUnits: "ALL", ShortUnits: "NONE"})
			return e
		}},
		{"pricing", "GET", base + "/pricing", url.Values{"instruments": {"EUR_USD,USD_JPY"}, "since": {formatTime(day)}, "includeHomeConversions": {"true"}}, func(c *Client) error {
			_, e := c.GetPricing(ctx, account, PricingParams{Instruments: []string{"EUR_USD", "USD_JPY"}, Since: day, IncludeHomeConversions: true})
			return e
		}},
		{"latest candles", "GET", base + "/candles/latest", url.Values{"candleSpecifications": {"EUR_USD:S10:BM"}, "smooth": {"true"}, "units": {"5"}, "dailyAlignment": {"3"}, "alignmentTimezone": {"UTC"}, "weeklyAlignment": {"Friday"}}, func(c *Client) error {
			h := 3
			_, e := c.GetLatestCandles(ctx, account, LatestCandlesParams{Specifications: []string{"EUR_USD:S10:BM"}, Units: "5", Smooth: true, DailyAlignment: &h, AlignmentTimezone: "UTC", WeeklyAlignment: WeeklyAlignment("Friday")})
			return e
		}},
		{"account candles", "GET", base + "/instruments/EUR_USD/candles", url.Values{"units": {"2"}, "count": {"3"}, "price": {"BM"}}, func(c *Client) error {
			_, e := c.GetAccountCandles(ctx, account, "EUR_USD", CandlesParams{Price: "BM", Count: 3}, "2")
			return e
		}},
		{"transaction list", "GET", base + "/transactions", url.Values{"from": {formatTime(day)}, "to": {formatTime(day.Add(time.Hour))}, "pageSize": {"10"}, "type": {"ORDER_FILL"}}, func(c *Client) error {
			_, e := c.ListTransactions(ctx, account, TransactionsParams{From: day, To: day.Add(time.Hour), PageSize: 10, Type: []TransactionFilter{"ORDER_FILL"}})
			return e
		}},
		{"get transaction", "GET", base + "/transactions/9", nil, func(c *Client) error { _, e := c.GetTransaction(ctx, account, "9"); return e }},
		{"transaction range", "GET", base + "/transactions/idrange", url.Values{"from": {"1"}, "to": {"9"}, "type": {"ORDER_FILL"}}, func(c *Client) error {
			_, e := c.GetTransactionRange(ctx, account, "1", "9", TransactionFilter("ORDER_FILL"))
			return e
		}},
		{"transactions since ID", "GET", base + "/transactions/sinceid", url.Values{"id": {"9"}, "type": {"ORDER_FILL"}}, func(c *Client) error {
			_, e := c.GetTransactionsSinceID(ctx, account, "9", TransactionFilter("ORDER_FILL"))
			return e
		}},
		{"instrument candles", "GET", "/v3/instruments/EUR_USD/candles", url.Values{"price": {"MBA"}, "granularity": {"H1"}, "smooth": {"true"}, "includeFirst": {"false"}, "dailyAlignment": {"5"}, "weeklyAlignment": {"Friday"}, "alignmentTimezone": {"UTC"}, "from": {formatTime(day)}, "to": {formatTime(day.Add(time.Hour))}}, func(c *Client) error {
			first := false
			hour := 5
			_, e := c.GetCandles(ctx, "EUR_USD", CandlesParams{Price: "MBA", Granularity: "H1", From: day, To: day.Add(time.Hour), Smooth: true, IncludeFirst: &first, DailyAlignment: &hour, WeeklyAlignment: WeeklyAlignment("Friday"), AlignmentTimezone: "UTC"})
			return e
		}},
		{"order book", "GET", "/v3/instruments/EUR_USD/orderBook", url.Values{"time": {formatTime(day)}}, func(c *Client) error { _, e := c.GetOrderBook(ctx, "EUR_USD", day); return e }},
		{"position book", "GET", "/v3/instruments/EUR_USD/positionBook", url.Values{"time": {formatTime(day)}}, func(c *Client) error { _, e := c.GetPositionBook(ctx, "EUR_USD", day); return e }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hits := 0
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				hits++
				if r.Method != tc.method || r.URL.EscapedPath() != tc.path {
					t.Errorf("request %s %s, want %s %s", r.Method, r.URL.EscapedPath(), tc.method, tc.path)
				}
				if tc.query != nil && !reflect.DeepEqual(r.URL.Query(), tc.query) {
					t.Errorf("query %v, want %v", r.URL.Query(), tc.query)
				}
				if tc.method != http.MethodGet && r.ContentLength > 0 && r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("missing JSON content type")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, `{}`)
			})
			if err := tc.call(c); err != nil {
				t.Errorf("%s: %v", tc.name, err)
			}
			if hits != 1 {
				t.Errorf("requests %d, want 1", hits)
			}
		})
	}
}

func TestEndpointValidationRejectsRequests(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		call func(*Client) error
	}{
		{"bad account", func(c *Client) error { _, e := c.GetAccount(ctx, "bad/account"); return e }},
		{"bad account summary", func(c *Client) error { _, e := c.GetAccountSummary(ctx, "bad/account"); return e }},
		{"bad account instruments", func(c *Client) error { _, e := c.GetAccountInstruments(ctx, testAccount, "bad"); return e }},
		{"account instruments bad account", func(c *Client) error { _, e := c.GetAccountInstruments(ctx, "bad/account", "EUR_USD"); return e }},
		{"empty configuration", func(c *Client) error { _, e := c.ConfigureAccount(ctx, testAccount, AccountConfiguration{}); return e }},
		{"long alias", func(c *Client) error {
			_, e := c.ConfigureAccount(ctx, testAccount, AccountConfiguration{Alias: strings.Repeat("a", 257)})
			return e
		}},
		{"invalid margin", func(c *Client) error {
			_, e := c.ConfigureAccount(ctx, testAccount, AccountConfiguration{MarginRate: "bad"})
			return e
		}},
		{"configuration bad account", func(c *Client) error {
			_, e := c.ConfigureAccount(ctx, "bad/account", AccountConfiguration{Alias: "ok"})
			return e
		}},
		{"changes bad ID", func(c *Client) error { _, e := c.GetAccountChanges(ctx, testAccount, "bad"); return e }},
		{"changes bad account", func(c *Client) error { _, e := c.GetAccountChanges(ctx, "bad/account", "1"); return e }},
		{"create order bad account", func(c *Client) error {
			_, e := c.CreateOrder(ctx, "bad/account", NewMarketOrder("EUR_USD", "1"))
			return e
		}},
		{"create raw invalid", func(c *Client) error { _, e := c.CreateOrderRaw(ctx, testAccount, json.RawMessage(`bad`)); return e }},
		{"create raw bad account", func(c *Client) error {
			_, e := c.CreateOrderRaw(ctx, "bad/account", json.RawMessage(`{"type":"MARKET"}`))
			return e
		}},
		{"orders invalid state", func(c *Client) error { _, e := c.ListOrders(ctx, testAccount, OrdersParams{State: "WHAT"}); return e }},
		{"orders bad account", func(c *Client) error { _, e := c.ListOrders(ctx, "bad/account", OrdersParams{}); return e }},
		{"orders invalid ID", func(c *Client) error {
			_, e := c.ListOrders(ctx, testAccount, OrdersParams{IDs: []string{"bad"}})
			return e
		}},
		{"orders invalid count", func(c *Client) error { _, e := c.ListOrders(ctx, testAccount, OrdersParams{Count: 501}); return e }},
		{"orders invalid before ID", func(c *Client) error { _, e := c.ListOrders(ctx, testAccount, OrdersParams{BeforeID: "x"}); return e }},
		{"orders invalid instrument", func(c *Client) error {
			_, e := c.ListOrders(ctx, testAccount, OrdersParams{Instrument: "wrong"})
			return e
		}},
		{"pending orders bad account", func(c *Client) error { _, e := c.ListPendingOrders(ctx, "bad/account"); return e }},
		{"get order bad ID", func(c *Client) error { _, e := c.GetOrder(ctx, testAccount, "bad%id"); return e }},
		{"replace order bad account", func(c *Client) error {
			_, e := c.ReplaceOrder(ctx, "bad/account", "1", NewMarketOrder("EUR_USD", "1"))
			return e
		}},
		{"replace order bad order", func(c *Client) error { _, e := c.ReplaceOrder(ctx, testAccount, "1", nil); return e }},
		{"replace raw bad ID", func(c *Client) error {
			_, e := c.ReplaceOrderRaw(ctx, testAccount, "bad%id", json.RawMessage(`{"type":"MARKET"}`))
			return e
		}},
		{"replace raw invalid", func(c *Client) error {
			_, e := c.ReplaceOrderRaw(ctx, testAccount, "1", json.RawMessage(`{}`))
			return e
		}},
		{"cancel order bad account", func(c *Client) error { _, e := c.CancelOrder(ctx, "bad/account", "1"); return e }},
		{"order extensions empty", func(c *Client) error {
			_, e := c.SetOrderClientExtensions(ctx, testAccount, "1", ClientExtensionsRequest{})
			return e
		}},
		{"order extensions bad ID", func(c *Client) error {
			_, e := c.SetOrderClientExtensions(ctx, testAccount, "bad%id", ClientExtensionsRequest{})
			return e
		}},
		{"trades bad account", func(c *Client) error { _, e := c.ListTrades(ctx, "bad/account", TradesParams{}); return e }},
		{"trades invalid state", func(c *Client) error { _, e := c.ListTrades(ctx, testAccount, TradesParams{State: "WHAT"}); return e }},
		{"trades invalid ID", func(c *Client) error {
			_, e := c.ListTrades(ctx, testAccount, TradesParams{IDs: []string{"bad"}})
			return e
		}},
		{"open trades bad account", func(c *Client) error { _, e := c.ListOpenTrades(ctx, "bad/account"); return e }},
		{"get trade bad spec", func(c *Client) error { _, e := c.GetTrade(ctx, testAccount, "bad%id"); return e }},
		{"get trade bad account", func(c *Client) error { _, e := c.GetTrade(ctx, "bad/account", "1"); return e }},
		{"close trade bad account", func(c *Client) error { _, e := c.CloseTrade(ctx, "bad/account", "1", ""); return e }},
		{"close trade invalid units", func(c *Client) error { _, e := c.CloseTrade(ctx, testAccount, "1", "bad!"); return e }},
		{"trade extensions empty", func(c *Client) error {
			_, e := c.SetTradeClientExtensions(ctx, testAccount, "1", ClientExtensions{})
			return e
		}},
		{"trade extensions bad account", func(c *Client) error {
			_, e := c.SetTradeClientExtensions(ctx, "bad/account", "1", ClientExtensions{})
			return e
		}},
		{"trade orders empty", func(c *Client) error { _, e := c.SetTradeOrders(ctx, testAccount, "1", TradeOrdersRequest{}); return e }},
		{"trade orders bad account", func(c *Client) error {
			_, e := c.SetTradeOrders(ctx, "bad/account", "1", TradeOrdersRequest{})
			return e
		}},
		{"positions bad account", func(c *Client) error { _, e := c.ListPositions(ctx, "bad/account"); return e }},
		{"open positions bad account", func(c *Client) error { _, e := c.ListOpenPositions(ctx, "bad/account"); return e }},
		{"get position bad instrument", func(c *Client) error { _, e := c.GetPosition(ctx, testAccount, "bad"); return e }},
		{"get position bad account", func(c *Client) error { _, e := c.GetPosition(ctx, "bad/account", "EUR_USD"); return e }},
		{"close position bad instrument", func(c *Client) error {
			_, e := c.ClosePosition(ctx, testAccount, "bad", ClosePositionRequest{LongUnits: "ALL"})
			return e
		}},
		{"close position bad account", func(c *Client) error {
			_, e := c.ClosePosition(ctx, "bad/account", "EUR_USD", ClosePositionRequest{LongUnits: "ALL"})
			return e
		}},
		{"close position empty", func(c *Client) error {
			_, e := c.ClosePosition(ctx, testAccount, "EUR_USD", ClosePositionRequest{})
			return e
		}},
		{"close position bad units", func(c *Client) error {
			_, e := c.ClosePosition(ctx, testAccount, "EUR_USD", ClosePositionRequest{LongUnits: "BAD"})
			return e
		}},
		{"pricing bad account", func(c *Client) error {
			_, e := c.GetPricing(ctx, "bad/account", PricingParams{Instruments: []string{"EUR_USD"}})
			return e
		}},
		{"pricing missing instruments", func(c *Client) error { _, e := c.GetPricing(ctx, testAccount, PricingParams{}); return e }},
		{"latest candles bad account", func(c *Client) error {
			_, e := c.GetLatestCandles(ctx, "bad/account", LatestCandlesParams{Specifications: []string{"EUR_USD:S10:BM"}})
			return e
		}},
		{"latest candles no spec", func(c *Client) error { _, e := c.GetLatestCandles(ctx, testAccount, LatestCandlesParams{}); return e }},
		{"latest candles bad spec", func(c *Client) error {
			_, e := c.GetLatestCandles(ctx, testAccount, LatestCandlesParams{Specifications: []string{"bad"}})
			return e
		}},
		{"latest candles bad granularity", func(c *Client) error {
			_, e := c.GetLatestCandles(ctx, testAccount, LatestCandlesParams{Specifications: []string{"EUR_USD:BAD:BM"}})
			return e
		}},
		{"latest candles bad price", func(c *Client) error {
			_, e := c.GetLatestCandles(ctx, testAccount, LatestCandlesParams{Specifications: []string{"EUR_USD:S10:BB"}})
			return e
		}},
		{"latest candles bad units", func(c *Client) error {
			_, e := c.GetLatestCandles(ctx, testAccount, LatestCandlesParams{Specifications: []string{"EUR_USD:S10:BM"}, Units: "BAD"})
			return e
		}},
		{"latest candles bad alignment", func(c *Client) error {
			h := 25
			_, e := c.GetLatestCandles(ctx, testAccount, LatestCandlesParams{Specifications: []string{"EUR_USD:S10:BM"}, DailyAlignment: &h})
			return e
		}},
		{"latest candles bad timezone", func(c *Client) error {
			_, e := c.GetLatestCandles(ctx, testAccount, LatestCandlesParams{Specifications: []string{"EUR_USD:S10:BM"}, AlignmentTimezone: "Invalid/TimeZone"})
			return e
		}},
		{"latest candles bad week", func(c *Client) error {
			_, e := c.GetLatestCandles(ctx, testAccount, LatestCandlesParams{Specifications: []string{"EUR_USD:S10:BM"}, WeeklyAlignment: "Bad"})
			return e
		}},
		{"account candles bad account", func(c *Client) error {
			_, e := c.GetAccountCandles(ctx, "bad/account", "EUR_USD", CandlesParams{}, "")
			return e
		}},
		{"account candles bad instrument", func(c *Client) error {
			_, e := c.GetAccountCandles(ctx, testAccount, "bad", CandlesParams{}, "")
			return e
		}},
		{"account candles invalid units", func(c *Client) error {
			_, e := c.GetAccountCandles(ctx, testAccount, "EUR_USD", CandlesParams{}, "bad")
			return e
		}},
		{"transactions bad account", func(c *Client) error { _, e := c.ListTransactions(ctx, "bad/account", TransactionsParams{}); return e }},
		{"transactions invalid size", func(c *Client) error {
			_, e := c.ListTransactions(ctx, testAccount, TransactionsParams{PageSize: 1001})
			return e
		}},
		{"transactions invalid type", func(c *Client) error {
			_, e := c.ListTransactions(ctx, testAccount, TransactionsParams{Type: []TransactionFilter{"invalid!"}})
			return e
		}},
		{"get transaction invalid ID", func(c *Client) error { _, e := c.GetTransaction(ctx, testAccount, "bad"); return e }},
		{"get transaction invalid account", func(c *Client) error { _, e := c.GetTransaction(ctx, "bad/account", "1"); return e }},
		{"range bad account", func(c *Client) error { _, e := c.GetTransactionRange(ctx, "bad/account", "1", "2"); return e }},
		{"range bad from", func(c *Client) error { _, e := c.GetTransactionRange(ctx, testAccount, "bad", "2"); return e }},
		{"range bad to", func(c *Client) error { _, e := c.GetTransactionRange(ctx, testAccount, "1", "bad"); return e }},
		{"range bad type", func(c *Client) error { _, e := c.GetTransactionRange(ctx, testAccount, "1", "2", "bad!"); return e }},
		{"since bad account", func(c *Client) error { _, e := c.GetTransactionsSinceID(ctx, "bad/account", "1"); return e }},
		{"since bad ID", func(c *Client) error { _, e := c.GetTransactionsSinceID(ctx, testAccount, "bad"); return e }},
		{"since bad type", func(c *Client) error { _, e := c.GetTransactionsSinceID(ctx, testAccount, "1", "bad!"); return e }},
		{"candles bad instrument", func(c *Client) error { _, e := c.GetCandles(ctx, "bad/account", CandlesParams{}); return e }},
		{"candles invalid count", func(c *Client) error { _, e := c.GetCandles(ctx, "EUR_USD", CandlesParams{Count: 5001}); return e }},
		{"book bad instrument", func(c *Client) error { _, e := c.GetOrderBook(ctx, "bad/account", time.Time{}); return e }},
		{"position book bad instrument", func(c *Client) error { _, e := c.GetPositionBook(ctx, "bad/account", time.Time{}); return e }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hits := 0
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { hits++; w.WriteHeader(http.StatusInternalServerError) })
			if err := tc.call(c); err == nil {
				t.Error("expected input validation failure")
			}
			if hits != 0 {
				t.Errorf("invalid input caused %d HTTP requests", hits)
			}
		})
	}
}
