package oanda

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestOrderValidationCases(t *testing.T) {
	valid := func() OrderRequest { return *NewMarketOrder("EUR_USD", "10") }
	cases := []struct {
		name   string
		mutate func(*OrderRequest)
	}{
		{"invalid type", func(o *OrderRequest) { o.Type = "UNKNOWN" }},
		{"invalid instrument", func(o *OrderRequest) { o.Instrument = "X" }},
		{"invalid units", func(o *OrderRequest) { o.Units = "" }},
		{"entry trade ID not allowed", func(o *OrderRequest) { o.TradeID = "1" }},
		{"entry client ID not allowed", func(o *OrderRequest) { o.ClientTradeID = "client" }},
		{"dependent missing trade ID", func(o *OrderRequest) { o.Type = OrderTypeStopLoss }},
		{"dependent invalid trade ID", func(o *OrderRequest) { o.Type = OrderTypeStopLoss; o.TradeID = "not-numeric" }},
		{"limit missing price", func(o *OrderRequest) { o.Type = OrderTypeLimit }},
		{"stop missing price", func(o *OrderRequest) { o.Type = OrderTypeStop }},
		{"market if touched missing price", func(o *OrderRequest) { o.Type = OrderTypeMarketIfTouched }},
		{"market price disallowed", func(o *OrderRequest) { o.Price = "1" }},
		{"trailing stop missing distance", func(o *OrderRequest) { o.Type = OrderTypeTrailingStopLoss; o.TradeID = "1" }},
		{"negative price", func(o *OrderRequest) { o.PriceBound = "-1" }},
		{"nondecimal price", func(o *OrderRequest) { o.PriceBound = "bad" }},
		{"bad time in force", func(o *OrderRequest) { o.TimeInForce = "INVALID" }},
		{"bad market time in force", func(o *OrderRequest) { o.TimeInForce = GTC }},
		{"GTD missing time", func(o *OrderRequest) { o.TimeInForce = GTD }},
		{"bad position fill", func(o *OrderRequest) { o.PositionFill = "BAD" }},
		{"bad trigger", func(o *OrderRequest) { o.TriggerCondition = "BAD" }},
		{"take profit missing price", func(o *OrderRequest) { o.TakeProfitOnFill = &TakeProfitDetails{} }},
		{"stop loss missing price", func(o *OrderRequest) { o.StopLossOnFill = &StopLossDetails{} }},
		{"guaranteed stop missing price", func(o *OrderRequest) { o.GuaranteedStopLossOnFill = &GuaranteedStopLossDetails{} }},
		{"trailing stop empty distance", func(o *OrderRequest) { o.TrailingStopLossOnFill = &TrailingStopLossDetails{} }},
		{"trailing stop negative distance", func(o *OrderRequest) { o.TrailingStopLossOnFill = &TrailingStopLossDetails{Distance: "-1"} }},
	}
	var nilOrder *OrderRequest
	if err := nilOrder.Validate(); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("nil order: %v", err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := valid()
			tc.mutate(&o)
			if err := o.Validate(); !errors.Is(err, ErrInvalidArgument) {
				t.Errorf("Validate=%v, want validation error", err)
			}
		})
	}
	good := []OrderRequest{
		*NewMarketOrder("EUR_USD", "1"), *NewLimitOrder("EUR_USD", "1", "1.05"),
		*NewStopOrder("EUR_USD", "1", "1.05"), *NewMarketIfTouchedOrder("EUR_USD", "1", "1.05"),
		{Type: OrderTypeStopLoss, TradeID: "123", Price: "1.05"},
		{Type: OrderTypeStopLoss, ClientTradeID: "client-1", Price: "1.05"},
		{Type: OrderTypeTrailingStopLoss, TradeID: "3", Distance: "0.1"},
		{Type: OrderTypeMarket, Instrument: "EUR_USD", Units: "1", TakeProfitOnFill: &TakeProfitDetails{Price: "1.2"}, StopLossOnFill: &StopLossDetails{Distance: "0.1"}, GuaranteedStopLossOnFill: &GuaranteedStopLossDetails{Distance: "0.2"}, TrailingStopLossOnFill: &TrailingStopLossDetails{Distance: "0.3"}},
	}
	for i := range good {
		if err := good[i].Validate(); err != nil {
			t.Errorf("valid order %d: %v", i, err)
		}
	}
}

func TestDependentOrderConstraints(t *testing.T) {
	tests := []struct {
		name        string
		price, dist Decimal
		tif         TimeInForce
		valid       bool
	}{
		{"both", "1", "2", GTC, false}, {"empty", "", "", GTC, false},
		{"bad price", "0", "", GTC, false}, {"bad distance", "", "0", GTC, false},
		{"bad tif", "1", "", "BAD", false},
		{"valid price", "1", "", GTC, true}, {"valid distance", "", "0.5", GFD, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDependent("example", tc.price, tc.dist, tc.tif)
			if (err == nil) != tc.valid {
				t.Errorf("unexpected result: %v", err)
			}
		})
	}
}

func TestTradeOrderValidationCases(t *testing.T) {
	cases := []struct {
		name  string
		req   TradeOrdersRequest
		valid bool
	}{
		{"empty", TradeOrdersRequest{}, false},
		{"cancel take", TradeOrdersRequest{CancelTakeProfit: true}, true},
		{"cancel stop", TradeOrdersRequest{CancelStopLoss: true}, true},
		{"cancel trailing", TradeOrdersRequest{CancelTrailingStopLoss: true}, true},
		{"cancel guaranteed", TradeOrdersRequest{CancelGuaranteedStopLoss: true}, true},
		{"conflicting take", TradeOrdersRequest{CancelTakeProfit: true, TakeProfit: &TakeProfitDetails{Price: "1"}}, false},
		{"conflicting stop", TradeOrdersRequest{CancelStopLoss: true, StopLoss: &StopLossDetails{Price: "1"}}, false},
		{"conflicting trailing", TradeOrdersRequest{CancelTrailingStopLoss: true, TrailingStopLoss: &TrailingStopLossDetails{Distance: "1"}}, false},
		{"conflicting guaranteed", TradeOrdersRequest{CancelGuaranteedStopLoss: true, GuaranteedStopLoss: &GuaranteedStopLossDetails{Price: "1"}}, false},
		{"invalid take", TradeOrdersRequest{TakeProfit: &TakeProfitDetails{}}, false},
		{"invalid stop", TradeOrdersRequest{StopLoss: &StopLossDetails{}}, false},
		{"invalid guaranteed", TradeOrdersRequest{GuaranteedStopLoss: &GuaranteedStopLossDetails{}}, false},
		{"invalid trailing", TradeOrdersRequest{TrailingStopLoss: &TrailingStopLossDetails{}}, false},
		{"valid take", TradeOrdersRequest{TakeProfit: &TakeProfitDetails{Price: "1"}}, true},
		{"valid stop", TradeOrdersRequest{StopLoss: &StopLossDetails{Distance: "0.1"}}, true},
		{"valid guaranteed", TradeOrdersRequest{GuaranteedStopLoss: &GuaranteedStopLossDetails{Price: "1"}}, true},
		{"valid trailing", TradeOrdersRequest{TrailingStopLoss: &TrailingStopLossDetails{Distance: "0.1"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.req.validate()
			if (err == nil) != tc.valid {
				t.Errorf("validate %v", err)
			}
		})
	}
}

func TestCandlesOptionsValidation(t *testing.T) {
	start := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	before := start.Add(-time.Hour)
	tests := []struct {
		name  string
		p     CandlesParams
		valid bool
	}{
		{"empty", CandlesParams{}, true},
		{"count and range", CandlesParams{Count: 1, From: start, To: start.Add(time.Hour)}, false},
		{"reversed dates", CandlesParams{From: start, To: before}, false},
		{"bad price", CandlesParams{Price: "MBX"}, false},
		{"repeated price", CandlesParams{Price: "MMM"}, false},
		{"price too long", CandlesParams{Price: "MBAM"}, false},
		{"bad timezone", CandlesParams{AlignmentTimezone: "not/a-zone"}, false},
		{"space in timezone", CandlesParams{AlignmentTimezone: "UTC "}, false},
		{"bad week", CandlesParams{WeeklyAlignment: "BAD"}, false},
		{"bad hour", CandlesParams{DailyAlignment: func() *int { x := 24; return &x }()}, false},
		{"all fields", CandlesParams{Price: "BM", Granularity: "H1", Count: 5, From: start, Smooth: true, IncludeFirst: func() *bool { x := true; return &x }(), DailyAlignment: func() *int { x := 0; return &x }(), AlignmentTimezone: "UTC", WeeklyAlignment: "Friday"}, true},
		{"to only", CandlesParams{To: start}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.p.values()
			if (err == nil) != tc.valid {
				t.Errorf("values: %v", err)
			}
		})
	}
	for _, x := range []string{"", "ABCD", "MBA", "MM", "A"} {
		err := validatePriceComponents(x)
		if x == "MBA" || x == "A" {
			if err != nil {
				t.Errorf("valid price %s: %v", x, err)
			}
		} else if err == nil {
			t.Errorf("expected invalid price %s", x)
		}
	}
}

func TestErrorsAndModelSerializationEdgeCases(t *testing.T) {
	e := &ValidationError{Field: "alias", Reason: "invalid"}
	if !strings.Contains(e.Error(), "alias") || !errors.Is(e, ErrInvalidArgument) {
		t.Error("validation error must be recognizable")
	}
	ap := newAPIError(429, "rid", []byte(`{"errorCode":"RATE","errorMessage":"slow down"}`))
	if ap.StatusCode != 429 || ap.ErrorCode != "RATE" || !strings.Contains(ap.Error(), "rid") || !ap.Temporary() || !IsRateLimited(ap) {
		t.Errorf("API error: %+v", ap)
	}
	if IsNotFound(ap) || IsUnauthorized(ap) || IsForbidden(ap) {
		t.Error("wrong status predicate")
	}
	for _, tc := range []struct {
		status int
		check  func(error) bool
	}{{404, IsNotFound}, {401, IsUnauthorized}, {403, IsForbidden}} {
		if !tc.check(&APIError{StatusCode: tc.status}) {
			t.Errorf("status %d", tc.status)
		}
	}
	if IsNotFound(errors.New("something")) {
		t.Error("plain errors must not match")
	}
	if (&APIError{StatusCode: 200}).Temporary() {
		t.Error("200 temporary")
	}
	if e := newAPIError(400, "", []byte(strings.Repeat("x", 600))); len(e.ErrorMessage) != 515 {
		t.Errorf("truncation len=%d", len(e.ErrorMessage))
	}
	if msg := (&DryRunError{Method: "PUT", URL: "example"}).Error(); !strings.Contains(msg, "PUT") {
		t.Error(msg)
	}
	if (&ClientExtensions{}).IsZero() == false || (*ClientExtensions)(nil).IsZero() == false || (&ClientExtensions{ID: "x"}).IsZero() {
		t.Error("zero-value extensions")
	}
	var pb PriceBucket
	if err := json.Unmarshal([]byte(`not-json`), &pb); err == nil {
		t.Error("bad liquidity must be rejected")
	}
	var tr Transaction
	if err := json.Unmarshal([]byte(`wrong`), &tr); err == nil {
		t.Error("bad transaction must be rejected")
	}
	if _, err := json.Marshal(Transaction{ID: "12"}); err != nil {
		t.Error(err)
	}
	var res TransactionResult
	for _, tc := range []string{`broken`, `{"relatedTransactionIDs":"bad"}`, `{"lastTransactionID":[]}`, `{"orderFillTransaction":[]}`} {
		if err := json.Unmarshal([]byte(tc), &res); err == nil {
			t.Errorf("accepted invalid transaction result %s", tc)
		}
	}
	populated := TransactionResult{Transactions: map[string]*Transaction{"orderFillTransaction": {ID: "12"}}, RelatedTransactionIDs: []string{"12"}, LastTransactionID: "12"}
	b, err := json.Marshal(populated)
	if err != nil || !strings.Contains(string(b), "orderFillTransaction") {
		t.Errorf("marshal %s: %v", b, err)
	}
	var decoded TransactionResult
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Error(err)
	}
	if tx := decoded.Transaction("orderFillTransaction"); tx == nil || tx.ID != "12" {
		t.Errorf("transaction %v", tx)
	}
	if b, err := json.Marshal(decoded); err != nil || len(b) == 0 {
		t.Errorf("re-marshal: %v", err)
	}
	if _, err := json.Marshal(Transaction{Raw: json.RawMessage(`{"id":"123"}`)}); err != nil {
		t.Error(err)
	}
	if e := fmt.Sprint(&APIError{StatusCode: 500}); !strings.Contains(e, "500") {
		t.Error(e)
	}
}

func TestTransactionPageURLsAndFilters(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{}`) })
	base := c.apiURL.String() + "/v3/accounts/" + testAccount + "/transactions/idrange"
	if _, err := c.GetTransactionPage(context.Background(), base+"?from=1&to=2&type=ORDER_FILL,STOP_LOSS_ORDER"); err != nil {
		t.Error(err)
	}
	if got := splitFilters(""); got != nil {
		t.Errorf("empty filters %v", got)
	}
	if len(splitFilters("ORDER_FILL,STOP_LOSS_ORDER")) != 2 {
		t.Error("filter split")
	}
	if _, err := c.GetTransactionPage(context.Background(), "%:/broken"); err == nil {
		t.Error("malformed URL must fail")
	}
}
