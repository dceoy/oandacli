package oanda

import (
	"context"
	"encoding/json"
	"net/http"
)

// TradesParams are the query parameters of ListTrades.
type TradesParams struct {
	IDs        []string
	State      TradeStateFilter
	Instrument string
	// Count is the maximum number of trades (1-500, default 50).
	Count    int
	BeforeID string
}

// TradesResponse is returned by ListTrades and ListOpenTrades.
type TradesResponse struct {
	ResponseMeta
	Trades            []Trade `json:"trades"`
	LastTransactionID string  `json:"lastTransactionID"`
}

// TradeResponse is returned by GetTrade.
type TradeResponse struct {
	ResponseMeta
	Trade             Trade  `json:"trade"`
	LastTransactionID string `json:"lastTransactionID"`
}

// TradeOrdersRequest creates, replaces or cancels the take-profit,
// stop-loss, trailing stop-loss and guaranteed stop-loss orders of a trade.
// Nil details with a false Cancel flag leave the existing order unchanged;
// a true Cancel flag cancels it.
type TradeOrdersRequest struct {
	TakeProfit               *TakeProfitDetails
	StopLoss                 *StopLossDetails
	TrailingStopLoss         *TrailingStopLossDetails
	GuaranteedStopLoss       *GuaranteedStopLossDetails
	CancelTakeProfit         bool
	CancelStopLoss           bool
	CancelTrailingStopLoss   bool
	CancelGuaranteedStopLoss bool
}

// MarshalJSON encodes cancellations as explicit nulls, as OANDA expects.
func (r TradeOrdersRequest) MarshalJSON() ([]byte, error) {
	m := map[string]any{}
	put := func(key string, cancel bool, v any, isNil bool) {
		switch {
		case cancel:
			m[key] = nil
		case !isNil:
			m[key] = v
		}
	}
	put("takeProfit", r.CancelTakeProfit, r.TakeProfit, r.TakeProfit == nil)
	put("stopLoss", r.CancelStopLoss, r.StopLoss, r.StopLoss == nil)
	put("trailingStopLoss", r.CancelTrailingStopLoss, r.TrailingStopLoss, r.TrailingStopLoss == nil)
	put("guaranteedStopLoss", r.CancelGuaranteedStopLoss, r.GuaranteedStopLoss, r.GuaranteedStopLoss == nil)
	return json.Marshal(m)
}

func (r TradeOrdersRequest) validate() error {
	n := 0
	check := func(name string, cancel, set bool) error {
		if cancel && set {
			return invalid(name, "cannot both set and cancel")
		}
		if cancel || set {
			n++
		}
		return nil
	}
	if err := check("takeProfit", r.CancelTakeProfit, r.TakeProfit != nil); err != nil {
		return err
	}
	if err := check("stopLoss", r.CancelStopLoss, r.StopLoss != nil); err != nil {
		return err
	}
	if err := check("trailingStopLoss", r.CancelTrailingStopLoss, r.TrailingStopLoss != nil); err != nil {
		return err
	}
	if err := check("guaranteedStopLoss", r.CancelGuaranteedStopLoss, r.GuaranteedStopLoss != nil); err != nil {
		return err
	}
	if n == 0 {
		return invalid("trade orders", "nothing to create, replace or cancel")
	}
	if d := r.TakeProfit; d != nil {
		if err := validateDependent("takeProfit", d.Price, "", d.TimeInForce); err != nil {
			return err
		}
	}
	if d := r.StopLoss; d != nil {
		if err := validateDependent("stopLoss", d.Price, d.Distance, d.TimeInForce); err != nil {
			return err
		}
	}
	if d := r.GuaranteedStopLoss; d != nil {
		if err := validateDependent("guaranteedStopLoss", d.Price, d.Distance, d.TimeInForce); err != nil {
			return err
		}
	}
	if d := r.TrailingStopLoss; d != nil {
		if err := validateDependent("trailingStopLoss", "", d.Distance, d.TimeInForce); err != nil {
			return err
		}
	}
	return nil
}

// ListTrades lists trades of an account.
// GET /v3/accounts/{accountID}/trades
func (c *Client) ListTrades(ctx context.Context, accountID string, params TradesParams) (*TradesResponse, error) {
	p, err := accountPath(accountID, "trades")
	if err != nil {
		return nil, err
	}
	if err := validateEnum("state", params.State, TradeStateFilters); err != nil {
		return nil, err
	}
	q, err := listParams(params.IDs, string(params.State), params.Instrument, params.Count, params.BeforeID)
	if err != nil {
		return nil, err
	}
	out := new(TradesResponse)
	return out, c.do(ctx, &request{method: http.MethodGet, path: p, query: q}, out)
}

// ListOpenTrades lists all open trades of an account.
// GET /v3/accounts/{accountID}/openTrades
func (c *Client) ListOpenTrades(ctx context.Context, accountID string) (*TradesResponse, error) {
	p, err := accountPath(accountID, "openTrades")
	if err != nil {
		return nil, err
	}
	out := new(TradesResponse)
	return out, c.do(ctx, &request{method: http.MethodGet, path: p}, out)
}

func tradePath(accountID, tradeSpecifier string, rest ...string) (string, error) {
	if err := ValidateSpecifier("trade specifier", tradeSpecifier); err != nil {
		return "", err
	}
	return accountPath(accountID, append([]string{"trades", seg(tradeSpecifier)}, rest...)...)
}

// GetTrade returns one trade by ID or "@clientID".
// GET /v3/accounts/{accountID}/trades/{tradeSpecifier}
func (c *Client) GetTrade(ctx context.Context, accountID, tradeSpecifier string) (*TradeResponse, error) {
	p, err := tradePath(accountID, tradeSpecifier)
	if err != nil {
		return nil, err
	}
	out := new(TradeResponse)
	return out, c.do(ctx, &request{method: http.MethodGet, path: p}, out)
}

// CloseTrade closes a trade fully (units "ALL" or empty) or partially.
// PUT /v3/accounts/{accountID}/trades/{tradeSpecifier}/close
func (c *Client) CloseTrade(ctx context.Context, accountID, tradeSpecifier string, units Decimal) (*TransactionResult, error) {
	p, err := tradePath(accountID, tradeSpecifier, "close")
	if err != nil {
		return nil, err
	}
	if units == "" {
		units = "ALL"
	}
	if units != "ALL" {
		if err := validatePositiveDecimal("units", string(units)); err != nil {
			return nil, err
		}
	}
	out := new(TransactionResult)
	return out, c.do(ctx, &request{method: http.MethodPut, path: p, body: map[string]Decimal{"units": units}}, out)
}

// SetTradeClientExtensions updates the client extensions of a trade.
// PUT /v3/accounts/{accountID}/trades/{tradeSpecifier}/clientExtensions
func (c *Client) SetTradeClientExtensions(ctx context.Context, accountID, tradeSpecifier string, ext ClientExtensions) (*TransactionResult, error) {
	p, err := tradePath(accountID, tradeSpecifier, "clientExtensions")
	if err != nil {
		return nil, err
	}
	if ext.IsZero() {
		return nil, invalid("clientExtensions", "nothing to set")
	}
	out := new(TransactionResult)
	return out, c.do(ctx, &request{method: http.MethodPut, path: p, body: map[string]any{"clientExtensions": ext}}, out)
}

// SetTradeOrders creates, replaces or cancels a trade's dependent orders.
// PUT /v3/accounts/{accountID}/trades/{tradeSpecifier}/orders
func (c *Client) SetTradeOrders(ctx context.Context, accountID, tradeSpecifier string, req TradeOrdersRequest) (*TransactionResult, error) {
	p, err := tradePath(accountID, tradeSpecifier, "orders")
	if err != nil {
		return nil, err
	}
	if err := req.validate(); err != nil {
		return nil, err
	}
	out := new(TransactionResult)
	return out, c.do(ctx, &request{method: http.MethodPut, path: p, body: req}, out)
}
