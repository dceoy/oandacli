package oanda

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
)

// OrderRequest is the body of an order creation or replacement. It is the
// union of all order request types; set Type and the fields that type uses.
// Use the New...Order constructors for the common cases.
type OrderRequest struct {
	Type                     OrderType                  `json:"type"`
	Instrument               string                     `json:"instrument,omitempty"`
	Units                    Decimal                    `json:"units,omitempty"`
	Price                    Decimal                    `json:"price,omitempty"`
	PriceBound               Decimal                    `json:"priceBound,omitempty"`
	Distance                 Decimal                    `json:"distance,omitempty"`
	TradeID                  string                     `json:"tradeID,omitempty"`
	ClientTradeID            string                     `json:"clientTradeID,omitempty"`
	TimeInForce              TimeInForce                `json:"timeInForce,omitempty"`
	GtdTime                  DateTime                   `json:"gtdTime,omitempty"`
	PositionFill             OrderPositionFill          `json:"positionFill,omitempty"`
	TriggerCondition         OrderTriggerCondition      `json:"triggerCondition,omitempty"`
	ClientExtensions         *ClientExtensions          `json:"clientExtensions,omitempty"`
	TakeProfitOnFill         *TakeProfitDetails         `json:"takeProfitOnFill,omitempty"`
	StopLossOnFill           *StopLossDetails           `json:"stopLossOnFill,omitempty"`
	GuaranteedStopLossOnFill *GuaranteedStopLossDetails `json:"guaranteedStopLossOnFill,omitempty"`
	TrailingStopLossOnFill   *TrailingStopLossDetails   `json:"trailingStopLossOnFill,omitempty"`
	TradeClientExtensions    *ClientExtensions          `json:"tradeClientExtensions,omitempty"`
}

// NewMarketOrder returns a market order request. Positive units buy, negative
// units sell.
func NewMarketOrder(instrument string, units Decimal) *OrderRequest {
	return &OrderRequest{Type: OrderTypeMarket, Instrument: instrument, Units: units, TimeInForce: FOK, PositionFill: PositionFillDefault}
}

// NewLimitOrder returns a GTC limit order request.
func NewLimitOrder(instrument string, units, price Decimal) *OrderRequest {
	return &OrderRequest{Type: OrderTypeLimit, Instrument: instrument, Units: units, Price: price, TimeInForce: GTC, PositionFill: PositionFillDefault}
}

// NewStopOrder returns a GTC stop order request.
func NewStopOrder(instrument string, units, price Decimal) *OrderRequest {
	return &OrderRequest{Type: OrderTypeStop, Instrument: instrument, Units: units, Price: price, TimeInForce: GTC, PositionFill: PositionFillDefault}
}

// NewMarketIfTouchedOrder returns a GTC market-if-touched order request.
func NewMarketIfTouchedOrder(instrument string, units, price Decimal) *OrderRequest {
	return &OrderRequest{Type: OrderTypeMarketIfTouched, Instrument: instrument, Units: units, Price: price, TimeInForce: GTC, PositionFill: PositionFillDefault}
}

// Validate performs client-side checks of the order request.
func (o *OrderRequest) Validate() error {
	if o == nil {
		return invalid("order", "nil")
	}
	if !slices.Contains(OrderTypes, o.Type) {
		return invalid("order type", "%q is not a creatable order type", o.Type)
	}
	entry := o.Type == OrderTypeMarket || o.Type == OrderTypeLimit || o.Type == OrderTypeStop || o.Type == OrderTypeMarketIfTouched
	if entry {
		if err := ValidateInstrument(o.Instrument); err != nil {
			return err
		}
		if err := ValidateUnits("units", string(o.Units)); err != nil {
			return err
		}
		if o.TradeID != "" || o.ClientTradeID != "" {
			return invalid("tradeID", "not allowed for %s orders", o.Type)
		}
	} else {
		if o.TradeID == "" && o.ClientTradeID == "" {
			return invalid("tradeID", "required for %s orders", o.Type)
		}
		if o.TradeID != "" {
			if err := ValidateTransactionID(o.TradeID); err != nil {
				return invalid("tradeID", "%q is not numeric", o.TradeID)
			}
		}
	}
	needsPrice := o.Type == OrderTypeLimit || o.Type == OrderTypeStop || o.Type == OrderTypeMarketIfTouched
	if needsPrice && o.Price == "" {
		return invalid("price", "required for %s orders", o.Type)
	}
	if o.Type == OrderTypeMarket && o.Price != "" {
		return invalid("price", "not allowed for MARKET orders (use priceBound)")
	}
	if o.Type == OrderTypeTrailingStopLoss && o.Distance == "" {
		return invalid("distance", "required for TRAILING_STOP_LOSS orders")
	}
	for name, v := range map[string]Decimal{"price": o.Price, "priceBound": o.PriceBound, "distance": o.Distance} {
		if v != "" {
			if err := validatePositiveDecimal(name, string(v)); err != nil {
				return err
			}
		}
	}
	if err := validateEnum("timeInForce", o.TimeInForce, TimeInForces); err != nil {
		return err
	}
	if o.Type == OrderTypeMarket && o.TimeInForce != "" && o.TimeInForce != FOK && o.TimeInForce != IOC {
		return invalid("timeInForce", "MARKET orders accept only FOK or IOC")
	}
	if o.TimeInForce == GTD && o.GtdTime == "" {
		return invalid("gtdTime", "required when timeInForce is GTD")
	}
	if err := validateEnum("positionFill", o.PositionFill, PositionFills); err != nil {
		return err
	}
	if err := validateEnum("triggerCondition", o.TriggerCondition, TriggerConditions); err != nil {
		return err
	}
	if d := o.TakeProfitOnFill; d != nil {
		if err := validateDependent("takeProfitOnFill", d.Price, "", d.TimeInForce); err != nil {
			return err
		}
	}
	if d := o.StopLossOnFill; d != nil {
		if err := validateDependent("stopLossOnFill", d.Price, d.Distance, d.TimeInForce); err != nil {
			return err
		}
	}
	if d := o.GuaranteedStopLossOnFill; d != nil {
		if err := validateDependent("guaranteedStopLossOnFill", d.Price, d.Distance, d.TimeInForce); err != nil {
			return err
		}
	}
	if d := o.TrailingStopLossOnFill; d != nil {
		if d.Distance == "" {
			return invalid("trailingStopLossOnFill.distance", "required")
		}
		if err := validateDependent("trailingStopLossOnFill", "", d.Distance, d.TimeInForce); err != nil {
			return err
		}
	}
	return nil
}

func validateDependent(field string, price, distance Decimal, tif TimeInForce) error {
	if price != "" && distance != "" {
		return invalid(field, "set either price or distance, not both")
	}
	if price == "" && distance == "" {
		return invalid(field, "price or distance is required")
	}
	if price != "" {
		if err := validatePositiveDecimal(field+".price", string(price)); err != nil {
			return err
		}
	}
	if distance != "" {
		if err := validatePositiveDecimal(field+".distance", string(distance)); err != nil {
			return err
		}
	}
	if tif != "" && tif != GTC && tif != GTD && tif != GFD {
		return invalid(field+".timeInForce", "must be GTC, GTD or GFD")
	}
	return nil
}

// OrdersParams are the query parameters of ListOrders.
type OrdersParams struct {
	IDs        []string
	State      OrderStateFilter
	Instrument string
	// Count is the maximum number of orders (1-500, default 50).
	Count    int
	BeforeID string
}

// OrdersResponse is returned by ListOrders and ListPendingOrders.
type OrdersResponse struct {
	ResponseMeta
	Orders            []Order `json:"orders"`
	LastTransactionID string  `json:"lastTransactionID"`
}

// OrderResponse is returned by GetOrder.
type OrderResponse struct {
	ResponseMeta
	Order             Order  `json:"order"`
	LastTransactionID string `json:"lastTransactionID"`
}

// ClientExtensionsRequest is the body of SetOrderClientExtensions and
// SetTradeClientExtensions.
type ClientExtensionsRequest struct {
	ClientExtensions      *ClientExtensions `json:"clientExtensions,omitempty"`
	TradeClientExtensions *ClientExtensions `json:"tradeClientExtensions,omitempty"`
}

// CreateOrder submits an order. The response contains, depending on the
// outcome, orderCreateTransaction, orderFillTransaction,
// orderCancelTransaction, etc. Rejected orders return an *APIError whose Body
// contains the orderRejectTransaction. CreateOrder is never retried.
// POST /v3/accounts/{accountID}/orders
func (c *Client) CreateOrder(ctx context.Context, accountID string, order *OrderRequest) (*TransactionResult, error) {
	p, err := accountPath(accountID, "orders")
	if err != nil {
		return nil, err
	}
	if err := order.Validate(); err != nil {
		return nil, err
	}
	out := new(TransactionResult)
	return out, c.do(ctx, &request{method: http.MethodPost, path: p, body: map[string]any{"order": order}}, out)
}

// CreateOrderRaw submits an arbitrary order request object (the value of the
// "order" field) for order types or fields not modelled by OrderRequest.
// POST /v3/accounts/{accountID}/orders
func (c *Client) CreateOrderRaw(ctx context.Context, accountID string, order json.RawMessage) (*TransactionResult, error) {
	p, err := accountPath(accountID, "orders")
	if err != nil {
		return nil, err
	}
	if err := validateRawOrder(order); err != nil {
		return nil, err
	}
	out := new(TransactionResult)
	return out, c.do(ctx, &request{method: http.MethodPost, path: p, body: map[string]any{"order": order}}, out)
}

func validateRawOrder(order json.RawMessage) error {
	var head struct {
		Type OrderType `json:"type"`
	}
	if err := json.Unmarshal(order, &head); err != nil {
		return invalid("order", "must be a JSON object: %v", err)
	}
	if !slices.Contains(OrderTypes, head.Type) {
		return invalid("order type", "%q is not a creatable order type", head.Type)
	}
	return nil
}

// ListOrders lists orders of an account.
// GET /v3/accounts/{accountID}/orders
func (c *Client) ListOrders(ctx context.Context, accountID string, params OrdersParams) (*OrdersResponse, error) {
	p, err := accountPath(accountID, "orders")
	if err != nil {
		return nil, err
	}
	q, err := listParams(params.IDs, string(params.State), params.Instrument, params.Count, params.BeforeID)
	if err != nil {
		return nil, err
	}
	if err := validateEnum("state", params.State, OrderStateFilters); err != nil {
		return nil, err
	}
	out := new(OrdersResponse)
	return out, c.do(ctx, &request{method: http.MethodGet, path: p, query: q}, out)
}

func listParams(ids []string, state, instrument string, count int, beforeID string) (url.Values, error) {
	q := url.Values{}
	for _, id := range ids {
		if err := ValidateTransactionID(id); err != nil {
			return nil, invalid("ids", "%q is not numeric", id)
		}
	}
	if len(ids) > 0 {
		q.Set("ids", joinStrings(ids))
	}
	if state != "" {
		q.Set("state", state)
	}
	if instrument != "" {
		if err := ValidateInstrument(instrument); err != nil {
			return nil, err
		}
		q.Set("instrument", instrument)
	}
	if err := validateCount("count", count, 500); err != nil {
		return nil, err
	}
	if count > 0 {
		q.Set("count", strconv.Itoa(count))
	}
	if beforeID != "" {
		if err := ValidateTransactionID(beforeID); err != nil {
			return nil, invalid("beforeID", "%q is not numeric", beforeID)
		}
		q.Set("beforeID", beforeID)
	}
	return q, nil
}

// ListPendingOrders lists all pending orders of an account.
// GET /v3/accounts/{accountID}/pendingOrders
func (c *Client) ListPendingOrders(ctx context.Context, accountID string) (*OrdersResponse, error) {
	p, err := accountPath(accountID, "pendingOrders")
	if err != nil {
		return nil, err
	}
	out := new(OrdersResponse)
	return out, c.do(ctx, &request{method: http.MethodGet, path: p}, out)
}

func orderPath(accountID, orderSpecifier string, rest ...string) (string, error) {
	if err := ValidateSpecifier("order specifier", orderSpecifier); err != nil {
		return "", err
	}
	return accountPath(accountID, append([]string{"orders", seg(orderSpecifier)}, rest...)...)
}

// GetOrder returns one order by ID or "@clientID".
// GET /v3/accounts/{accountID}/orders/{orderSpecifier}
func (c *Client) GetOrder(ctx context.Context, accountID, orderSpecifier string) (*OrderResponse, error) {
	p, err := orderPath(accountID, orderSpecifier)
	if err != nil {
		return nil, err
	}
	out := new(OrderResponse)
	return out, c.do(ctx, &request{method: http.MethodGet, path: p}, out)
}

// ReplaceOrder cancels an order and replaces it with a new one.
// PUT /v3/accounts/{accountID}/orders/{orderSpecifier}
func (c *Client) ReplaceOrder(ctx context.Context, accountID, orderSpecifier string, order *OrderRequest) (*TransactionResult, error) {
	p, err := orderPath(accountID, orderSpecifier)
	if err != nil {
		return nil, err
	}
	if err := order.Validate(); err != nil {
		return nil, err
	}
	out := new(TransactionResult)
	return out, c.do(ctx, &request{method: http.MethodPut, path: p, body: map[string]any{"order": order}}, out)
}

// ReplaceOrderRaw is like ReplaceOrder but takes a raw order object.
func (c *Client) ReplaceOrderRaw(ctx context.Context, accountID, orderSpecifier string, order json.RawMessage) (*TransactionResult, error) {
	p, err := orderPath(accountID, orderSpecifier)
	if err != nil {
		return nil, err
	}
	if err := validateRawOrder(order); err != nil {
		return nil, err
	}
	out := new(TransactionResult)
	return out, c.do(ctx, &request{method: http.MethodPut, path: p, body: map[string]any{"order": order}}, out)
}

// CancelOrder cancels a pending order.
// PUT /v3/accounts/{accountID}/orders/{orderSpecifier}/cancel
func (c *Client) CancelOrder(ctx context.Context, accountID, orderSpecifier string) (*TransactionResult, error) {
	p, err := orderPath(accountID, orderSpecifier, "cancel")
	if err != nil {
		return nil, err
	}
	out := new(TransactionResult)
	return out, c.do(ctx, &request{method: http.MethodPut, path: p}, out)
}

// SetOrderClientExtensions updates the client extensions of an order and/or
// of the trade it will open.
// PUT /v3/accounts/{accountID}/orders/{orderSpecifier}/clientExtensions
func (c *Client) SetOrderClientExtensions(ctx context.Context, accountID, orderSpecifier string, req ClientExtensionsRequest) (*TransactionResult, error) {
	p, err := orderPath(accountID, orderSpecifier, "clientExtensions")
	if err != nil {
		return nil, err
	}
	if req.ClientExtensions.IsZero() && req.TradeClientExtensions.IsZero() {
		return nil, invalid("clientExtensions", "nothing to set")
	}
	out := new(TransactionResult)
	return out, c.do(ctx, &request{method: http.MethodPut, path: p, body: req}, out)
}
