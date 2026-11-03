package oanda

import (
	"encoding/json"
	"strings"
)

// Decimal is a decimal number encoded as a JSON string, as used by OANDA for
// prices, units and amounts. It is kept as a string to avoid float rounding.
type Decimal string

// DateTime is a timestamp string. The client requests RFC 3339 formatting.
type DateTime string

// CandlestickGranularity is a candlestick period.
type CandlestickGranularity string

// Candlestick granularities.
const (
	S5  CandlestickGranularity = "S5"
	S10 CandlestickGranularity = "S10"
	S15 CandlestickGranularity = "S15"
	S30 CandlestickGranularity = "S30"
	M1  CandlestickGranularity = "M1"
	M2  CandlestickGranularity = "M2"
	M4  CandlestickGranularity = "M4"
	M5  CandlestickGranularity = "M5"
	M10 CandlestickGranularity = "M10"
	M15 CandlestickGranularity = "M15"
	M30 CandlestickGranularity = "M30"
	H1  CandlestickGranularity = "H1"
	H2  CandlestickGranularity = "H2"
	H3  CandlestickGranularity = "H3"
	H4  CandlestickGranularity = "H4"
	H6  CandlestickGranularity = "H6"
	H8  CandlestickGranularity = "H8"
	H12 CandlestickGranularity = "H12"
	D   CandlestickGranularity = "D"
	W   CandlestickGranularity = "W"
	M   CandlestickGranularity = "M"
)

// Granularities lists every valid CandlestickGranularity.
var Granularities = []CandlestickGranularity{S5, S10, S15, S30, M1, M2, M4, M5, M10, M15, M30, H1, H2, H3, H4, H6, H8, H12, D, W, M}

// WeeklyAlignment is the day of week used for weekly candle alignment.
type WeeklyAlignment string

// WeeklyAlignments lists the valid weekly alignment days.
var WeeklyAlignments = []WeeklyAlignment{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}

// OrderType is the type of an order.
type OrderType string

// Order types.
const (
	OrderTypeMarket             OrderType = "MARKET"
	OrderTypeLimit              OrderType = "LIMIT"
	OrderTypeStop               OrderType = "STOP"
	OrderTypeMarketIfTouched    OrderType = "MARKET_IF_TOUCHED"
	OrderTypeTakeProfit         OrderType = "TAKE_PROFIT"
	OrderTypeStopLoss           OrderType = "STOP_LOSS"
	OrderTypeGuaranteedStopLoss OrderType = "GUARANTEED_STOP_LOSS"
	OrderTypeTrailingStopLoss   OrderType = "TRAILING_STOP_LOSS"
	OrderTypeFixedPrice         OrderType = "FIXED_PRICE"
)

// OrderTypes lists the order types that can be created through the API.
var OrderTypes = []OrderType{OrderTypeMarket, OrderTypeLimit, OrderTypeStop, OrderTypeMarketIfTouched, OrderTypeTakeProfit, OrderTypeStopLoss, OrderTypeGuaranteedStopLoss, OrderTypeTrailingStopLoss}

// TimeInForce specifies how long an order remains pending.
type TimeInForce string

// Time-in-force values.
const (
	GTC TimeInForce = "GTC"
	GTD TimeInForce = "GTD"
	GFD TimeInForce = "GFD"
	FOK TimeInForce = "FOK"
	IOC TimeInForce = "IOC"
)

// TimeInForces lists every valid TimeInForce.
var TimeInForces = []TimeInForce{GTC, GTD, GFD, FOK, IOC}

// OrderPositionFill specifies how positions are modified when an order fills.
type OrderPositionFill string

// Position fill values.
const (
	PositionFillOpenOnly    OrderPositionFill = "OPEN_ONLY"
	PositionFillReduceFirst OrderPositionFill = "REDUCE_FIRST"
	PositionFillReduceOnly  OrderPositionFill = "REDUCE_ONLY"
	PositionFillDefault     OrderPositionFill = "DEFAULT"
)

// PositionFills lists every valid OrderPositionFill.
var PositionFills = []OrderPositionFill{PositionFillOpenOnly, PositionFillReduceFirst, PositionFillReduceOnly, PositionFillDefault}

// OrderTriggerCondition specifies which price component triggers an order.
type OrderTriggerCondition string

// TriggerConditions lists every valid OrderTriggerCondition.
var TriggerConditions = []OrderTriggerCondition{"DEFAULT", "INVERSE", "BID", "ASK", "MID"}

// OrderStateFilter filters orders by state.
type OrderStateFilter string

// OrderStateFilters lists every valid OrderStateFilter.
var OrderStateFilters = []OrderStateFilter{"PENDING", "FILLED", "TRIGGERED", "CANCELLED", "ALL"}

// TradeStateFilter filters trades by state.
type TradeStateFilter string

// TradeStateFilters lists every valid TradeStateFilter.
var TradeStateFilters = []TradeStateFilter{"OPEN", "CLOSED", "CLOSE_WHEN_TRADEABLE", "ALL"}

// TransactionFilter filters transactions by type (e.g. "ORDER", "ORDER_FILL",
// "FUNDING").
type TransactionFilter string

// ClientExtensions attach client-side metadata to orders and trades.
// Do not set client extensions on MT4-linked accounts.
type ClientExtensions struct {
	ID      string `json:"id,omitempty"`
	Tag     string `json:"tag,omitempty"`
	Comment string `json:"comment,omitempty"`
}

// IsZero reports whether no field is set.
func (c *ClientExtensions) IsZero() bool { return c == nil || *c == ClientExtensions{} }

// TakeProfitDetails specifies a take-profit order to create on fill.
type TakeProfitDetails struct {
	Price            Decimal           `json:"price,omitempty"`
	TimeInForce      TimeInForce       `json:"timeInForce,omitempty"`
	GtdTime          DateTime          `json:"gtdTime,omitempty"`
	ClientExtensions *ClientExtensions `json:"clientExtensions,omitempty"`
}

// StopLossDetails specifies a stop-loss order to create on fill.
type StopLossDetails struct {
	Price            Decimal           `json:"price,omitempty"`
	Distance         Decimal           `json:"distance,omitempty"`
	TimeInForce      TimeInForce       `json:"timeInForce,omitempty"`
	GtdTime          DateTime          `json:"gtdTime,omitempty"`
	ClientExtensions *ClientExtensions `json:"clientExtensions,omitempty"`
}

// GuaranteedStopLossDetails specifies a guaranteed stop-loss order to create
// on fill.
type GuaranteedStopLossDetails struct {
	Price            Decimal           `json:"price,omitempty"`
	Distance         Decimal           `json:"distance,omitempty"`
	TimeInForce      TimeInForce       `json:"timeInForce,omitempty"`
	GtdTime          DateTime          `json:"gtdTime,omitempty"`
	ClientExtensions *ClientExtensions `json:"clientExtensions,omitempty"`
}

// TrailingStopLossDetails specifies a trailing stop-loss order to create on
// fill.
type TrailingStopLossDetails struct {
	Distance         Decimal           `json:"distance,omitempty"`
	TimeInForce      TimeInForce       `json:"timeInForce,omitempty"`
	GtdTime          DateTime          `json:"gtdTime,omitempty"`
	ClientExtensions *ClientExtensions `json:"clientExtensions,omitempty"`
}

// AccountProperties identifies an account the token can access.
type AccountProperties struct {
	ID           string   `json:"id"`
	MT4AccountID int      `json:"mt4AccountID,omitempty"`
	Tags         []string `json:"tags,omitempty"`
}

// AccountSummary is the summary state of an account.
type AccountSummary struct {
	ID                          string   `json:"id"`
	Alias                       string   `json:"alias,omitempty"`
	Currency                    string   `json:"currency"`
	Balance                     Decimal  `json:"balance"`
	CreatedByUserID             int      `json:"createdByUserID,omitempty"`
	CreatedTime                 DateTime `json:"createdTime,omitempty"`
	GuaranteedStopLossOrderMode string   `json:"guaranteedStopLossOrderMode,omitempty"`
	PL                          Decimal  `json:"pl,omitempty"`
	ResettablePL                Decimal  `json:"resettablePL,omitempty"`
	ResettablePLTime            DateTime `json:"resettablePLTime,omitempty"`
	Financing                   Decimal  `json:"financing,omitempty"`
	Commission                  Decimal  `json:"commission,omitempty"`
	DividendAdjustment          Decimal  `json:"dividendAdjustment,omitempty"`
	GuaranteedExecutionFees     Decimal  `json:"guaranteedExecutionFees,omitempty"`
	MarginRate                  Decimal  `json:"marginRate,omitempty"`
	MarginCallEnterTime         DateTime `json:"marginCallEnterTime,omitempty"`
	MarginCallExtensionCount    int      `json:"marginCallExtensionCount,omitempty"`
	LastMarginCallExtensionTime DateTime `json:"lastMarginCallExtensionTime,omitempty"`
	OpenTradeCount              int      `json:"openTradeCount"`
	OpenPositionCount           int      `json:"openPositionCount"`
	PendingOrderCount           int      `json:"pendingOrderCount"`
	HedgingEnabled              bool     `json:"hedgingEnabled"`
	UnrealizedPL                Decimal  `json:"unrealizedPL,omitempty"`
	NAV                         Decimal  `json:"NAV,omitempty"`
	MarginUsed                  Decimal  `json:"marginUsed,omitempty"`
	MarginAvailable             Decimal  `json:"marginAvailable,omitempty"`
	PositionValue               Decimal  `json:"positionValue,omitempty"`
	MarginCloseoutUnrealizedPL  Decimal  `json:"marginCloseoutUnrealizedPL,omitempty"`
	MarginCloseoutNAV           Decimal  `json:"marginCloseoutNAV,omitempty"`
	MarginCloseoutMarginUsed    Decimal  `json:"marginCloseoutMarginUsed,omitempty"`
	MarginCloseoutPercent       Decimal  `json:"marginCloseoutPercent,omitempty"`
	MarginCloseoutPositionValue Decimal  `json:"marginCloseoutPositionValue,omitempty"`
	WithdrawalLimit             Decimal  `json:"withdrawalLimit,omitempty"`
	MarginCallMarginUsed        Decimal  `json:"marginCallMarginUsed,omitempty"`
	MarginCallPercent           Decimal  `json:"marginCallPercent,omitempty"`
	LastTransactionID           string   `json:"lastTransactionID,omitempty"`
}

// Account is the full state of an account, including open trades, positions
// and pending orders.
type Account struct {
	AccountSummary
	Trades    []Trade    `json:"trades,omitempty"`
	Positions []Position `json:"positions,omitempty"`
	Orders    []Order    `json:"orders,omitempty"`
}

// Instrument describes a tradeable instrument.
type Instrument struct {
	Name                              string  `json:"name"`
	Type                              string  `json:"type"`
	DisplayName                       string  `json:"displayName"`
	PipLocation                       int     `json:"pipLocation"`
	DisplayPrecision                  int     `json:"displayPrecision"`
	TradeUnitsPrecision               int     `json:"tradeUnitsPrecision"`
	MinimumTradeSize                  Decimal `json:"minimumTradeSize"`
	MaximumTrailingStopDistance       Decimal `json:"maximumTrailingStopDistance"`
	MinimumGuaranteedStopLossDistance Decimal `json:"minimumGuaranteedStopLossDistance,omitempty"`
	MinimumTrailingStopDistance       Decimal `json:"minimumTrailingStopDistance"`
	MaximumPositionSize               Decimal `json:"maximumPositionSize"`
	MaximumOrderUnits                 Decimal `json:"maximumOrderUnits"`
	MarginRate                        Decimal `json:"marginRate"`
	GuaranteedStopLossOrderMode       string  `json:"guaranteedStopLossOrderMode,omitempty"`
}

// CandlestickData holds open/high/low/close prices.
type CandlestickData struct {
	O Decimal `json:"o"`
	H Decimal `json:"h"`
	L Decimal `json:"l"`
	C Decimal `json:"c"`
}

// Candlestick is one candle.
type Candlestick struct {
	Time     DateTime         `json:"time"`
	Bid      *CandlestickData `json:"bid,omitempty"`
	Ask      *CandlestickData `json:"ask,omitempty"`
	Mid      *CandlestickData `json:"mid,omitempty"`
	Volume   int              `json:"volume"`
	Complete bool             `json:"complete"`
}

// PriceBucket is one level of price/liquidity.
type PriceBucket struct {
	Price     Decimal `json:"price"`
	Liquidity Decimal `json:"liquidity"`
}

// UnmarshalJSON accepts liquidity encoded as a number or a string.
func (p *PriceBucket) UnmarshalJSON(b []byte) error {
	var v struct {
		Price     Decimal         `json:"price"`
		Liquidity json.RawMessage `json:"liquidity"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	p.Price = v.Price
	p.Liquidity = Decimal(strings.Trim(string(v.Liquidity), `"`))
	return nil
}

// ClientPrice is a price for an instrument.
type ClientPrice struct {
	Type        string        `json:"type"`
	Instrument  string        `json:"instrument"`
	Time        DateTime      `json:"time"`
	Tradeable   bool          `json:"tradeable"`
	Bids        []PriceBucket `json:"bids"`
	Asks        []PriceBucket `json:"asks"`
	CloseoutBid Decimal       `json:"closeoutBid"`
	CloseoutAsk Decimal       `json:"closeoutAsk"`
}

// HomeConversions are conversion factors to the account home currency.
type HomeConversions struct {
	Currency      string  `json:"currency"`
	AccountGain   Decimal `json:"accountGain"`
	AccountLoss   Decimal `json:"accountLoss"`
	PositionValue Decimal `json:"positionValue"`
}

// OrderBookBucket is one bucket of an order or position book.
type OrderBookBucket struct {
	Price             Decimal `json:"price"`
	LongCountPercent  Decimal `json:"longCountPercent"`
	ShortCountPercent Decimal `json:"shortCountPercent"`
}

// Book is an order book or position book snapshot.
type Book struct {
	Instrument  string            `json:"instrument"`
	Time        DateTime          `json:"time"`
	Price       Decimal           `json:"price"`
	BucketWidth Decimal           `json:"bucketWidth"`
	Buckets     []OrderBookBucket `json:"buckets"`
}

// Order is the union of the fields of every OANDA order type. Use Type to
// tell order kinds apart.
type Order struct {
	ID                       string                     `json:"id"`
	CreateTime               DateTime                   `json:"createTime,omitempty"`
	State                    string                     `json:"state,omitempty"`
	ClientExtensions         *ClientExtensions          `json:"clientExtensions,omitempty"`
	Type                     OrderType                  `json:"type"`
	Instrument               string                     `json:"instrument,omitempty"`
	Units                    Decimal                    `json:"units,omitempty"`
	TradeID                  string                     `json:"tradeID,omitempty"`
	ClientTradeID            string                     `json:"clientTradeID,omitempty"`
	Price                    Decimal                    `json:"price,omitempty"`
	PriceBound               Decimal                    `json:"priceBound,omitempty"`
	Distance                 Decimal                    `json:"distance,omitempty"`
	TrailingStopValue        Decimal                    `json:"trailingStopValue,omitempty"`
	TimeInForce              TimeInForce                `json:"timeInForce,omitempty"`
	GtdTime                  DateTime                   `json:"gtdTime,omitempty"`
	PositionFill             OrderPositionFill          `json:"positionFill,omitempty"`
	TriggerCondition         OrderTriggerCondition      `json:"triggerCondition,omitempty"`
	TakeProfitOnFill         *TakeProfitDetails         `json:"takeProfitOnFill,omitempty"`
	StopLossOnFill           *StopLossDetails           `json:"stopLossOnFill,omitempty"`
	GuaranteedStopLossOnFill *GuaranteedStopLossDetails `json:"guaranteedStopLossOnFill,omitempty"`
	TrailingStopLossOnFill   *TrailingStopLossDetails   `json:"trailingStopLossOnFill,omitempty"`
	TradeClientExtensions    *ClientExtensions          `json:"tradeClientExtensions,omitempty"`
	FillingTransactionID     string                     `json:"fillingTransactionID,omitempty"`
	FilledTime               DateTime                   `json:"filledTime,omitempty"`
	TradeOpenedID            string                     `json:"tradeOpenedID,omitempty"`
	TradeReducedID           string                     `json:"tradeReducedID,omitempty"`
	TradeClosedIDs           []string                   `json:"tradeClosedIDs,omitempty"`
	CancellingTransactionID  string                     `json:"cancellingTransactionID,omitempty"`
	CancelledTime            DateTime                   `json:"cancelledTime,omitempty"`
	ReplacesOrderID          string                     `json:"replacesOrderID,omitempty"`
	ReplacedByOrderID        string                     `json:"replacedByOrderID,omitempty"`
}

// Trade is an open or closed trade.
type Trade struct {
	ID                        string            `json:"id"`
	Instrument                string            `json:"instrument"`
	Price                     Decimal           `json:"price"`
	OpenTime                  DateTime          `json:"openTime"`
	State                     string            `json:"state"`
	InitialUnits              Decimal           `json:"initialUnits"`
	InitialMarginRequired     Decimal           `json:"initialMarginRequired,omitempty"`
	CurrentUnits              Decimal           `json:"currentUnits"`
	RealizedPL                Decimal           `json:"realizedPL"`
	UnrealizedPL              Decimal           `json:"unrealizedPL,omitempty"`
	MarginUsed                Decimal           `json:"marginUsed,omitempty"`
	AverageClosePrice         Decimal           `json:"averageClosePrice,omitempty"`
	ClosingTransactionIDs     []string          `json:"closingTransactionIDs,omitempty"`
	Financing                 Decimal           `json:"financing,omitempty"`
	DividendAdjustment        Decimal           `json:"dividendAdjustment,omitempty"`
	CloseTime                 DateTime          `json:"closeTime,omitempty"`
	ClientExtensions          *ClientExtensions `json:"clientExtensions,omitempty"`
	TakeProfitOrderID         string            `json:"takeProfitOrderID,omitempty"`
	StopLossOrderID           string            `json:"stopLossOrderID,omitempty"`
	GuaranteedStopLossOrderID string            `json:"guaranteedStopLossOrderID,omitempty"`
	TrailingStopLossOrderID   string            `json:"trailingStopLossOrderID,omitempty"`
	TakeProfitOrder           *Order            `json:"takeProfitOrder,omitempty"`
	StopLossOrder             *Order            `json:"stopLossOrder,omitempty"`
	TrailingStopLossOrder     *Order            `json:"trailingStopLossOrder,omitempty"`
	GuaranteedStopLossOrder   *Order            `json:"guaranteedStopLossOrder,omitempty"`
}

// PositionSide is the long or short side of a position.
type PositionSide struct {
	Units                   Decimal  `json:"units"`
	AveragePrice            Decimal  `json:"averagePrice,omitempty"`
	TradeIDs                []string `json:"tradeIDs,omitempty"`
	PL                      Decimal  `json:"pl"`
	UnrealizedPL            Decimal  `json:"unrealizedPL,omitempty"`
	ResettablePL            Decimal  `json:"resettablePL,omitempty"`
	Financing               Decimal  `json:"financing,omitempty"`
	DividendAdjustment      Decimal  `json:"dividendAdjustment,omitempty"`
	GuaranteedExecutionFees Decimal  `json:"guaranteedExecutionFees,omitempty"`
}

// Position is the aggregate position in one instrument.
type Position struct {
	Instrument              string       `json:"instrument"`
	PL                      Decimal      `json:"pl"`
	UnrealizedPL            Decimal      `json:"unrealizedPL,omitempty"`
	MarginUsed              Decimal      `json:"marginUsed,omitempty"`
	ResettablePL            Decimal      `json:"resettablePL,omitempty"`
	Financing               Decimal      `json:"financing,omitempty"`
	Commission              Decimal      `json:"commission,omitempty"`
	DividendAdjustment      Decimal      `json:"dividendAdjustment,omitempty"`
	GuaranteedExecutionFees Decimal      `json:"guaranteedExecutionFees,omitempty"`
	Long                    PositionSide `json:"long"`
	Short                   PositionSide `json:"short"`
}

// Transaction holds the fields common to all OANDA transaction types plus the
// most frequently used type-specific ones. Raw holds the complete JSON object,
// and MarshalJSON re-emits it unchanged.
type Transaction struct {
	ID             string   `json:"id"`
	Time           DateTime `json:"time"`
	UserID         int      `json:"userID,omitempty"`
	AccountID      string   `json:"accountID,omitempty"`
	BatchID        string   `json:"batchID,omitempty"`
	RequestID      string   `json:"requestID,omitempty"`
	Type           string   `json:"type"`
	Instrument     string   `json:"instrument,omitempty"`
	Units          Decimal  `json:"units,omitempty"`
	Price          Decimal  `json:"price,omitempty"`
	Reason         string   `json:"reason,omitempty"`
	OrderID        string   `json:"orderID,omitempty"`
	TradeID        string   `json:"tradeID,omitempty"`
	PL             Decimal  `json:"pl,omitempty"`
	Financing      Decimal  `json:"financing,omitempty"`
	Commission     Decimal  `json:"commission,omitempty"`
	AccountBalance Decimal  `json:"accountBalance,omitempty"`
	RejectReason   string   `json:"rejectReason,omitempty"`

	Raw json.RawMessage `json:"-"`
}

// UnmarshalJSON decodes the common fields and keeps the full object in Raw.
func (t *Transaction) UnmarshalJSON(b []byte) error {
	type plain Transaction
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*t = Transaction(p)
	t.Raw = append(json.RawMessage(nil), b...)
	return nil
}

// MarshalJSON re-emits Raw when available.
func (t Transaction) MarshalJSON() ([]byte, error) {
	if len(t.Raw) > 0 {
		return t.Raw, nil
	}
	type plain Transaction
	return json.Marshal(plain(t))
}

// TransactionResult is returned by state-changing endpoints. OANDA returns a
// varying set of "...Transaction" fields depending on the endpoint and
// outcome (e.g. orderCreateTransaction, orderFillTransaction,
// orderCancelTransaction, longOrderFillTransaction); all of them are
// collected in Transactions keyed by their JSON field name.
type TransactionResult struct {
	ResponseMeta
	Transactions          map[string]*Transaction `json:"-"`
	RelatedTransactionIDs []string                `json:"relatedTransactionIDs,omitempty"`
	LastTransactionID     string                  `json:"lastTransactionID,omitempty"`
}

// UnmarshalJSON collects every field whose name ends in "Transaction".
func (r *TransactionResult) UnmarshalJSON(b []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		return err
	}
	r.Transactions = map[string]*Transaction{}
	for k, v := range fields {
		switch {
		case k == "relatedTransactionIDs":
			if err := json.Unmarshal(v, &r.RelatedTransactionIDs); err != nil {
				return err
			}
		case k == "lastTransactionID":
			if err := json.Unmarshal(v, &r.LastTransactionID); err != nil {
				return err
			}
		case strings.HasSuffix(k, "Transaction") && string(v) != "null":
			tx := new(Transaction)
			if err := json.Unmarshal(v, tx); err != nil {
				return err
			}
			r.Transactions[k] = tx
		}
	}
	return nil
}

// Transaction returns the transaction stored under the given JSON field name,
// e.g. "orderFillTransaction", or nil.
func (r *TransactionResult) Transaction(name string) *Transaction { return r.Transactions[name] }

// MarshalJSON re-emits the raw response when available.
func (r TransactionResult) MarshalJSON() ([]byte, error) {
	if len(r.raw) > 0 {
		return r.raw, nil
	}
	m := map[string]any{}
	for k, v := range r.Transactions {
		m[k] = v
	}
	if len(r.RelatedTransactionIDs) > 0 {
		m["relatedTransactionIDs"] = r.RelatedTransactionIDs
	}
	if r.LastTransactionID != "" {
		m["lastTransactionID"] = r.LastTransactionID
	}
	return json.Marshal(m)
}
