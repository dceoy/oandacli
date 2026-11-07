package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"

	"github.com/dceoy/oandactl/oanda"
)

func strs[T ~string](list []T) []string {
	out := make([]string, len(list))
	for i, v := range list {
		out[i] = string(v)
	}
	return out
}

var (
	pInstrument  = Param{Name: "instrument", Type: TString, Required: true, Desc: "Instrument name, e.g. EUR_USD"}
	pInstruments = Param{Name: "instruments", Type: TStrings, Required: true, Desc: "Instrument names, e.g. EUR_USD,USD_JPY"}
	pOrder       = Param{Name: "order", Type: TString, Required: true, Desc: "Order ID or @clientOrderID"}
	pTrade       = Param{Name: "trade", Type: TString, Required: true, Desc: "Trade ID or @clientTradeID"}
	pTxTypes     = Param{Name: "type", Type: TStrings, Desc: "Transaction type filters, e.g. ORDER,FUNDING"}
	pCount500    = Param{Name: "count", Type: TInt, Desc: "Maximum number of results (1-500, default 50)"}
	pBeforeID    = Param{Name: "before-id", Type: TString, Desc: "Only return items with an ID less than this"}
	pIDs         = Param{Name: "ids", Type: TStrings, Desc: "IDs to fetch"}

	pGranularity  = Param{Name: "granularity", Type: TString, Desc: "Candle granularity (default S5)", Enum: strs(oanda.Granularities)}
	pPrice        = Param{Name: "price", Type: TString, Desc: "Price components: any combination of M (mid), B (bid), A (ask); default M"}
	pDailyAlign   = Param{Name: "daily-alignment", Type: TInt, Desc: "Hour of day (0-23) used for daily candle alignment"}
	pAlignTZ      = Param{Name: "alignment-timezone", Type: TString, Desc: "IANA time zone for daily alignment, e.g. America/New_York"}
	pWeeklyAlign  = Param{Name: "weekly-alignment", Type: TString, Desc: "Day of week used for weekly alignment", Enum: strs(oanda.WeeklyAlignments)}
	pSmooth       = Param{Name: "smooth", Type: TBool, Desc: "Use the previous candle's close as the open"}
	pClientID     = Param{Name: "client-id", Type: TString, Desc: "Client extension ID (not for MT4 accounts)"}
	pClientTag    = Param{Name: "client-tag", Type: TString, Desc: "Client extension tag"}
	pClientCmt    = Param{Name: "client-comment", Type: TString, Desc: "Client extension comment"}
	pTradeClientI = Param{Name: "trade-client-id", Type: TString, Desc: "Client extension ID for the resulting trade"}
	pTradeClientT = Param{Name: "trade-client-tag", Type: TString, Desc: "Client extension tag for the resulting trade"}
	pTradeClientC = Param{Name: "trade-client-comment", Type: TString, Desc: "Client extension comment for the resulting trade"}

	pTPPrice    = Param{Name: "tp-price", Type: TString, Desc: "Take-profit price"}
	pSLPrice    = Param{Name: "sl-price", Type: TString, Desc: "Stop-loss price"}
	pSLDistance = Param{Name: "sl-distance", Type: TString, Desc: "Stop-loss distance in price units"}
	pTSLDist    = Param{Name: "tsl-distance", Type: TString, Desc: "Trailing stop-loss distance in price units"}
	pGSLPrice   = Param{Name: "gsl-price", Type: TString, Desc: "Guaranteed stop-loss price"}
	pGSLDist    = Param{Name: "gsl-distance", Type: TString, Desc: "Guaranteed stop-loss distance in price units"}

	pBody     = Param{Name: "body", Type: TJSON, Desc: `Order request JSON, either {"order":{...}} or the order object itself`}
	pBodyFile = Param{Name: "body-file", Type: TString, Desc: "Read the order request JSON from a file (- for stdin)"}

	pMaxMessages = Param{Name: "max-messages", Type: TInt, Desc: "Stop after this many messages (0 = unlimited)"}
	pDuration    = Param{Name: "duration", Type: TDuration, Desc: "Stop after this long (0 = unlimited)"}
	pHeartbeats  = Param{Name: "include-heartbeats", Type: TBool, Desc: "Also emit HEARTBEAT messages"}
)

func entryParams(kind oanda.OrderType) []Param {
	tif := Param{Name: "time-in-force", Type: TString, Desc: "Time in force (default GTC)", Enum: strs(oanda.TimeInForces)}
	if kind == oanda.OrderTypeMarket {
		tif = Param{Name: "time-in-force", Type: TString, Desc: "Time in force (default FOK)", Enum: []string{"FOK", "IOC"}}
	}
	ps := []Param{
		pInstrument,
		{Name: "units", Type: TString, Required: true, Desc: "Units to trade; positive buys (long), negative sells (short)"},
	}
	if kind != oanda.OrderTypeMarket {
		ps = append(ps,
			Param{Name: "price", Type: TString, Required: true, Desc: "Trigger price"},
			Param{Name: "gtd-time", Type: TString, Desc: "Expiry time (RFC 3339) when time-in-force is GTD"},
			Param{Name: "trigger-condition", Type: TString, Desc: "Price component that triggers the order", Enum: strs(oanda.TriggerConditions)},
		)
	}
	if kind != oanda.OrderTypeLimit {
		ps = append(ps, Param{Name: "price-bound", Type: TString, Desc: "Worst acceptable fill price"})
	}
	return append(ps, tif,
		Param{Name: "position-fill", Type: TString, Desc: "How the fill affects positions (default DEFAULT)", Enum: strs(oanda.PositionFills)},
		pTPPrice, pSLPrice, pSLDistance, pTSLDist, pGSLPrice, pGSLDist,
		pClientID, pClientTag, pClientCmt, pTradeClientI, pTradeClientT, pTradeClientC,
	)
}

func clientExt(a *Args, prefix string) *oanda.ClientExtensions {
	ext := &oanda.ClientExtensions{ID: a.Str(prefix + "client-id"), Tag: a.Str(prefix + "client-tag"), Comment: a.Str(prefix + "client-comment")}
	if ext.IsZero() {
		return nil
	}
	return ext
}

func buildEntryOrder(kind oanda.OrderType, a *Args) *oanda.OrderRequest {
	instr, units, price := a.Str("instrument"), oanda.Decimal(a.Str("units")), oanda.Decimal(a.Str("price"))
	var o *oanda.OrderRequest
	switch kind {
	case oanda.OrderTypeMarket:
		o = oanda.NewMarketOrder(instr, units)
	case oanda.OrderTypeLimit:
		o = oanda.NewLimitOrder(instr, units, price)
	case oanda.OrderTypeStop:
		o = oanda.NewStopOrder(instr, units, price)
	default:
		o = oanda.NewMarketIfTouchedOrder(instr, units, price)
	}
	if v := a.Str("time-in-force"); v != "" {
		o.TimeInForce = oanda.TimeInForce(v)
	}
	if v := a.Str("position-fill"); v != "" {
		o.PositionFill = oanda.OrderPositionFill(v)
	}
	o.GtdTime = oanda.DateTime(a.Str("gtd-time"))
	o.TriggerCondition = oanda.OrderTriggerCondition(a.Str("trigger-condition"))
	o.PriceBound = oanda.Decimal(a.Str("price-bound"))
	if v := a.Str("tp-price"); v != "" {
		o.TakeProfitOnFill = &oanda.TakeProfitDetails{Price: oanda.Decimal(v)}
	}
	if p, d := a.Str("sl-price"), a.Str("sl-distance"); p != "" || d != "" {
		o.StopLossOnFill = &oanda.StopLossDetails{Price: oanda.Decimal(p), Distance: oanda.Decimal(d)}
	}
	if v := a.Str("tsl-distance"); v != "" {
		o.TrailingStopLossOnFill = &oanda.TrailingStopLossDetails{Distance: oanda.Decimal(v)}
	}
	if p, d := a.Str("gsl-price"), a.Str("gsl-distance"); p != "" || d != "" {
		o.GuaranteedStopLossOnFill = &oanda.GuaranteedStopLossDetails{Price: oanda.Decimal(p), Distance: oanda.Decimal(d)}
	}
	o.ClientExtensions = clientExt(a, "")
	o.TradeClientExtensions = clientExt(a, "trade-")
	return o
}

var reUnits = regexp.MustCompile(`^-?[0-9]{1,20}(?:\.[0-9]{1,20})?$`)

// parseUnitLimit parses OANDA_MAX_ORDER_UNITS as a positive plain decimal.
// Values such as NaN, Inf, exponents or zero are rejected so that a typo
// cannot silently disable the guard.
func parseUnitLimit(v string) (*big.Rat, error) {
	limit, ok := new(big.Rat).SetString(v)
	if !reUnits.MatchString(v) || strings.HasPrefix(v, "-") || !ok || limit.Sign() <= 0 {
		return nil, usagef("%s must be a positive decimal number such as 10000 (got %q); unset it to disable the limit", EnvMaxOrderUnits, v)
	}
	return limit, nil
}

// checkUnits enforces Policy.MaxOrderUnits with exact decimal arithmetic.
// Units that cannot be verified are refused while a limit is configured.
func (e *Env) checkUnits(units string) error {
	limit := e.Policy.MaxOrderUnits
	if limit == nil || units == "" {
		return nil
	}
	u, ok := new(big.Rat).SetString(units)
	if !reUnits.MatchString(units) || !ok {
		return &PolicyError{Msg: fmt.Sprintf("order units %q cannot be checked against %s; use a plain decimal number", units, EnvMaxOrderUnits)}
	}
	if u.Abs(u).Cmp(limit) > 0 {
		return &PolicyError{Msg: fmt.Sprintf("order units %s exceed the configured limit of %s (%s)", units, limit.FloatString(precision(limit)), EnvMaxOrderUnits)}
	}
	return nil
}

func precision(r *big.Rat) int {
	n, exact := r.FloatPrec()
	if !exact {
		return 20
	}
	return n
}

// orderBody extracts the order object from --body / --body-file input and
// applies policy checks.
func (e *Env) orderBody(a *Args) (json.RawMessage, error) {
	body := a.JSON("body")
	if body == nil {
		return nil, usagef("--body or --body-file is required")
	}
	var wrapper struct {
		Order json.RawMessage `json:"order"`
	}
	// --body is already a validated JSON object, so a failed decode only means
	// there is no wrapper; the order object check below reports real errors.
	if json.Unmarshal(body, &wrapper) == nil && len(wrapper.Order) > 0 {
		body = wrapper.Order
	}
	var head struct {
		Units json.RawMessage `json:"units"`
	}
	if err := json.Unmarshal(body, &head); err != nil {
		return nil, usagef("invalid order object: %v", err)
	}
	if len(head.Units) > 0 {
		var units string
		if json.Unmarshal(head.Units, &units) != nil {
			units = string(head.Units)
		}
		if err := e.checkUnits(units); err != nil {
			return nil, err
		}
	}
	return body, nil
}

func optInt(a *Args, name string) *int {
	if !a.Has(name) {
		return nil
	}
	n := a.Int(name)
	return &n
}

func filters(a *Args) []oanda.TransactionFilter {
	var out []oanda.TransactionFilter
	for _, s := range a.List("type") {
		out = append(out, oanda.TransactionFilter(s))
	}
	return out
}

// runStream drives a streaming SDK call and applies max-messages, duration and
// heartbeat filtering.
func runStream(ctx context.Context, e *Env, a *Args, start func(context.Context, oanda.StreamHandler) error) (any, error) {
	if d := a.Duration("duration"); d > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}
	limit, heartbeats, n := a.Int("max-messages"), a.Bool("include-heartbeats"), 0
	if limit < 0 {
		return nil, usagef("--max-messages must not be negative")
	}
	err := start(ctx, func(m oanda.StreamMessage) error {
		if m.IsHeartbeat() && !heartbeats {
			return nil
		}
		if err := e.Emit(m.Raw); err != nil {
			return err
		}
		n++
		if limit > 0 && n >= limit {
			return oanda.ErrStopStream
		}
		return nil
	})
	if errors.Is(err, context.DeadlineExceeded) && a.Duration("duration") > 0 {
		err = nil
	}
	return nil, err
}

// Commands returns every CLI command.
func Commands() []*Command {
	return []*Command{
		// ---- account ----
		{
			Path: []string{"account", "list"}, Summary: "List the accounts the token can access",
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) { return e.Client.ListAccounts(ctx) },
		},
		{
			Path: []string{"account", "get"}, Summary: "Get full account details incl. open trades, positions and pending orders", Account: true,
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) { return e.Client.GetAccount(ctx, e.AccountID) },
		},
		{
			Path: []string{"account", "summary"}, Summary: "Get the account summary (balance, NAV, margin, P/L)", Account: true,
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) {
				return e.Client.GetAccountSummary(ctx, e.AccountID)
			},
		},
		{
			Path: []string{"account", "instruments"}, Summary: "List tradeable instruments and their trading rules", Account: true,
			Params: []Param{{Name: "instruments", Type: TStrings, Desc: "Restrict to these instruments"}},
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) {
				return e.Client.GetAccountInstruments(ctx, e.AccountID, a.List("instruments")...)
			},
		},
		{
			Path: []string{"account", "changes"}, Summary: "Get account changes and state since a transaction ID", Account: true,
			Params: []Param{{Name: "since-transaction-id", Type: TString, Required: true, Desc: "Transaction ID to poll from"}},
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) {
				return e.Client.GetAccountChanges(ctx, e.AccountID, a.Str("since-transaction-id"))
			},
		},
		{
			Path: []string{"account", "configure"}, Summary: "Set the account alias and/or default margin rate", Account: true, Mutating: true,
			Params: []Param{
				{Name: "alias", Type: TString, Desc: "New account alias"},
				{Name: "margin-rate", Type: TString, Desc: "New default margin rate, e.g. 0.05"},
			},
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) {
				return e.Client.ConfigureAccount(ctx, e.AccountID, oanda.AccountConfiguration{Alias: a.Str("alias"), MarginRate: oanda.Decimal(a.Str("margin-rate"))})
			},
		},

		// ---- instrument ----
		{
			Path: []string{"instrument", "candles"}, Summary: "Get candlestick (OHLC) data for an instrument",
			Params: []Param{
				pInstrument, pGranularity, pPrice,
				{Name: "count", Type: TInt, Desc: "Number of candles (1-5000, default 500)"},
				{Name: "from", Type: TTime, Desc: "Start time (RFC 3339)"},
				{Name: "to", Type: TTime, Desc: "End time (RFC 3339)"},
				pSmooth,
				{Name: "include-first", Type: TBool, Desc: "Include the candle at --from (default true)"},
				pDailyAlign, pAlignTZ, pWeeklyAlign,
			},
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) {
				p := oanda.CandlesParams{
					Price: a.Str("price"), Granularity: oanda.CandlestickGranularity(a.Str("granularity")), Count: a.Int("count"),
					From: a.Time("from"), To: a.Time("to"), Smooth: a.Bool("smooth"), DailyAlignment: optInt(a, "daily-alignment"),
					AlignmentTimezone: a.Str("alignment-timezone"), WeeklyAlignment: oanda.WeeklyAlignment(a.Str("weekly-alignment")),
				}
				if a.Has("include-first") {
					b := a.Bool("include-first")
					p.IncludeFirst = &b
				}
				if a.Has("count") && p.Count == 0 {
					return nil, usagef("--count must be between 1 and 5000")
				}
				return e.Client.GetCandles(ctx, a.Str("instrument"), p)
			},
		},
		{
			Path: []string{"instrument", "order-book"}, Summary: "Get the order book of an instrument",
			Params: []Param{pInstrument, {Name: "time", Type: TTime, Desc: "Snapshot time (RFC 3339); latest if omitted"}},
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) {
				return e.Client.GetOrderBook(ctx, a.Str("instrument"), a.Time("time"))
			},
		},
		{
			Path: []string{"instrument", "position-book"}, Summary: "Get the position book of an instrument",
			Params: []Param{pInstrument, {Name: "time", Type: TTime, Desc: "Snapshot time (RFC 3339); latest if omitted"}},
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) {
				return e.Client.GetPositionBook(ctx, a.Str("instrument"), a.Time("time"))
			},
		},

		// ---- pricing ----
		{
			Path: []string{"pricing", "get"}, Summary: "Get current bid/ask prices", Account: true,
			Params: []Param{
				pInstruments,
				{Name: "since", Type: TTime, Desc: "Only prices that changed after this time (RFC 3339)"},
				{Name: "include-home-conversions", Type: TBool, Desc: "Include home currency conversion factors"},
			},
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) {
				return e.Client.GetPricing(ctx, e.AccountID, oanda.PricingParams{
					Instruments: a.List("instruments"), Since: a.Time("since"), IncludeHomeConversions: a.Bool("include-home-conversions"),
				})
			},
		},
		{
			Path: []string{"pricing", "latest-candles"}, Summary: "Get the latest candles for several instrument:granularity:price specs", Account: true,
			Params: []Param{
				{Name: "specs", Type: TStrings, Required: true, Desc: "Candle specifications, e.g. EUR_USD:M1:BA,USD_JPY:H1:M"},
				{Name: "units", Type: TString, Desc: "Units used to calculate VWAP prices"},
				pSmooth, pDailyAlign, pAlignTZ, pWeeklyAlign,
			},
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) {
				return e.Client.GetLatestCandles(ctx, e.AccountID, oanda.LatestCandlesParams{
					Specifications: a.List("specs"), Units: oanda.Decimal(a.Str("units")), Smooth: a.Bool("smooth"),
					DailyAlignment: optInt(a, "daily-alignment"), AlignmentTimezone: a.Str("alignment-timezone"),
					WeeklyAlignment: oanda.WeeklyAlignment(a.Str("weekly-alignment")),
				})
			},
		},
		{
			Path: []string{"pricing", "stream"}, Summary: "Stream live prices as NDJSON", Account: true, Stream: true,
			Params: []Param{
				pInstruments,
				{Name: "no-snapshot", Type: TBool, Desc: "Do not send an initial snapshot of current prices"},
				{Name: "include-home-conversions", Type: TBool, Desc: "Include home currency conversion factors"},
				pMaxMessages, pDuration, pHeartbeats,
			},
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) {
				p := oanda.PricingStreamParams{Instruments: a.List("instruments"), NoSnapshot: a.Bool("no-snapshot"), IncludeHomeConversions: a.Bool("include-home-conversions")}
				return runStream(ctx, e, a, func(ctx context.Context, fn oanda.StreamHandler) error {
					return e.Client.StreamPricing(ctx, e.AccountID, p, fn)
				})
			},
		},

		{
			Path: []string{"pricing", "candles"}, Summary: "Get account-specific candlesticks for an instrument", Account: true,
			Params: []Param{
				pInstrument, pGranularity, pPrice,
				{Name: "count", Type: TInt, Desc: "Number of candles (1-5000, default 500)"},
				{Name: "from", Type: TTime, Desc: "Start time (RFC 3339)"},
				{Name: "to", Type: TTime, Desc: "End time (RFC 3339)"},
				{Name: "units", Type: TString, Desc: "Units used to calculate VWAP prices"},
				pSmooth,
				{Name: "include-first", Type: TBool, Desc: "Include the candle at --from (default true)"},
				pDailyAlign, pAlignTZ, pWeeklyAlign,
			},
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) {
				p := oanda.CandlesParams{
					Price: a.Str("price"), Granularity: oanda.CandlestickGranularity(a.Str("granularity")), Count: a.Int("count"),
					From: a.Time("from"), To: a.Time("to"), Smooth: a.Bool("smooth"), DailyAlignment: optInt(a, "daily-alignment"),
					AlignmentTimezone: a.Str("alignment-timezone"), WeeklyAlignment: oanda.WeeklyAlignment(a.Str("weekly-alignment")),
				}
				if a.Has("include-first") {
					b := a.Bool("include-first")
					p.IncludeFirst = &b
				}
				if a.Has("count") && p.Count == 0 {
					return nil, usagef("--count must be between 1 and 5000")
				}
				return e.Client.GetAccountCandles(ctx, e.AccountID, a.Str("instrument"), p, oanda.Decimal(a.Str("units")))
			},
		},

		// ---- order ----
		{
			Path: []string{"order", "list"}, Summary: "List orders", Account: true,
			Params: []Param{
				pIDs,
				{Name: "state", Type: TString, Desc: "Order state filter (default PENDING)", Enum: strs(oanda.OrderStateFilters)},
				{Name: "instrument", Type: TString, Desc: "Instrument filter"},
				pCount500, pBeforeID,
			},
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) {
				return e.Client.ListOrders(ctx, e.AccountID, oanda.OrdersParams{
					IDs: a.List("ids"), State: oanda.OrderStateFilter(a.Str("state")), Instrument: a.Str("instrument"), Count: a.Int("count"), BeforeID: a.Str("before-id"),
				})
			},
		},
		{
			Path: []string{"order", "pending"}, Summary: "List all pending orders", Account: true,
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) {
				return e.Client.ListPendingOrders(ctx, e.AccountID)
			},
		},
		{
			Path: []string{"order", "get"}, Summary: "Get one order", Account: true, Params: []Param{pOrder},
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) {
				return e.Client.GetOrder(ctx, e.AccountID, a.Str("order"))
			},
		},
		entryCommand("market", "Place a market order", oanda.OrderTypeMarket),
		entryCommand("limit", "Place a limit order", oanda.OrderTypeLimit),
		entryCommand("stop", "Place a stop order", oanda.OrderTypeStop),
		entryCommand("market-if-touched", "Place a market-if-touched order", oanda.OrderTypeMarketIfTouched),
		{
			Path: []string{"order", "create"}, Summary: "Place any order type from a raw JSON order request", Account: true, Mutating: true,
			Params: []Param{pBody, pBodyFile},
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) {
				body, err := e.orderBody(a)
				if err != nil {
					return nil, err
				}
				return e.Client.CreateOrderRaw(ctx, e.AccountID, body)
			},
		},
		{
			Path: []string{"order", "replace"}, Summary: "Cancel an order and replace it with a new one from raw JSON", Account: true, Mutating: true,
			Params: []Param{pOrder, pBody, pBodyFile},
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) {
				body, err := e.orderBody(a)
				if err != nil {
					return nil, err
				}
				return e.Client.ReplaceOrderRaw(ctx, e.AccountID, a.Str("order"), body)
			},
		},
		{
			Path: []string{"order", "cancel"}, Summary: "Cancel a pending order", Account: true, Mutating: true, Params: []Param{pOrder},
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) {
				return e.Client.CancelOrder(ctx, e.AccountID, a.Str("order"))
			},
		},
		{
			Path: []string{"order", "client-extensions"}, Summary: "Update client extensions of an order and/or its future trade", Account: true, Mutating: true,
			Params: []Param{pOrder, pClientID, pClientTag, pClientCmt, pTradeClientI, pTradeClientT, pTradeClientC},
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) {
				return e.Client.SetOrderClientExtensions(ctx, e.AccountID, a.Str("order"), oanda.ClientExtensionsRequest{
					ClientExtensions: clientExt(a, ""), TradeClientExtensions: clientExt(a, "trade-"),
				})
			},
		},

		// ---- trade ----
		{
			Path: []string{"trade", "list"}, Summary: "List trades", Account: true,
			Params: []Param{
				pIDs,
				{Name: "state", Type: TString, Desc: "Trade state filter (default OPEN)", Enum: strs(oanda.TradeStateFilters)},
				{Name: "instrument", Type: TString, Desc: "Instrument filter"},
				pCount500, pBeforeID,
			},
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) {
				return e.Client.ListTrades(ctx, e.AccountID, oanda.TradesParams{
					IDs: a.List("ids"), State: oanda.TradeStateFilter(a.Str("state")), Instrument: a.Str("instrument"), Count: a.Int("count"), BeforeID: a.Str("before-id"),
				})
			},
		},
		{
			Path: []string{"trade", "open"}, Summary: "List all open trades", Account: true,
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) {
				return e.Client.ListOpenTrades(ctx, e.AccountID)
			},
		},
		{
			Path: []string{"trade", "get"}, Summary: "Get one trade", Account: true, Params: []Param{pTrade},
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) {
				return e.Client.GetTrade(ctx, e.AccountID, a.Str("trade"))
			},
		},
		{
			Path: []string{"trade", "close"}, Summary: "Close a trade fully or partially", Account: true, Mutating: true,
			Params: []Param{pTrade, {Name: "units", Type: TString, Default: "ALL", Desc: "Units to close (positive number) or ALL"}},
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) {
				return e.Client.CloseTrade(ctx, e.AccountID, a.Str("trade"), oanda.Decimal(a.Str("units")))
			},
		},
		{
			Path: []string{"trade", "client-extensions"}, Summary: "Update client extensions of a trade", Account: true, Mutating: true,
			Params: []Param{pTrade, pClientID, pClientTag, pClientCmt},
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) {
				ext := clientExt(a, "")
				if ext == nil {
					return nil, usagef("set at least one of --client-id, --client-tag, --client-comment")
				}
				return e.Client.SetTradeClientExtensions(ctx, e.AccountID, a.Str("trade"), *ext)
			},
		},
		{
			Path: []string{"trade", "orders"}, Summary: "Create, replace or cancel a trade's take-profit / stop-loss orders", Account: true, Mutating: true,
			Params: []Param{
				pTrade, pTPPrice, pSLPrice, pSLDistance, pTSLDist, pGSLPrice, pGSLDist,
				{Name: "cancel", Type: TStrings, Desc: "Dependent orders to cancel", Enum: []string{"tp", "sl", "tsl", "gsl"}},
			},
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) {
				var r oanda.TradeOrdersRequest
				if v := a.Str("tp-price"); v != "" {
					r.TakeProfit = &oanda.TakeProfitDetails{Price: oanda.Decimal(v)}
				}
				if p, d := a.Str("sl-price"), a.Str("sl-distance"); p != "" || d != "" {
					r.StopLoss = &oanda.StopLossDetails{Price: oanda.Decimal(p), Distance: oanda.Decimal(d)}
				}
				if v := a.Str("tsl-distance"); v != "" {
					r.TrailingStopLoss = &oanda.TrailingStopLossDetails{Distance: oanda.Decimal(v)}
				}
				if p, d := a.Str("gsl-price"), a.Str("gsl-distance"); p != "" || d != "" {
					r.GuaranteedStopLoss = &oanda.GuaranteedStopLossDetails{Price: oanda.Decimal(p), Distance: oanda.Decimal(d)}
				}
				for _, c := range a.List("cancel") {
					switch c {
					case "tp":
						r.CancelTakeProfit = true
					case "sl":
						r.CancelStopLoss = true
					case "tsl":
						r.CancelTrailingStopLoss = true
					case "gsl":
						r.CancelGuaranteedStopLoss = true
					}
				}
				return e.Client.SetTradeOrders(ctx, e.AccountID, a.Str("trade"), r)
			},
		},

		// ---- position ----
		{
			Path: []string{"position", "list"}, Summary: "List all positions (including previously held)", Account: true,
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) {
				return e.Client.ListPositions(ctx, e.AccountID)
			},
		},
		{
			Path: []string{"position", "open"}, Summary: "List open positions", Account: true,
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) {
				return e.Client.ListOpenPositions(ctx, e.AccountID)
			},
		},
		{
			Path: []string{"position", "get"}, Summary: "Get the position in one instrument", Account: true, Params: []Param{pInstrument},
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) {
				return e.Client.GetPosition(ctx, e.AccountID, a.Str("instrument"))
			},
		},
		{
			Path: []string{"position", "close"}, Summary: "Close the long and/or short side of a position", Account: true, Mutating: true,
			Params: []Param{
				pInstrument,
				{Name: "long-units", Type: TString, Desc: "Long units to close: ALL, NONE or a positive number"},
				{Name: "short-units", Type: TString, Desc: "Short units to close: ALL, NONE or a positive number"},
			},
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) {
				return e.Client.ClosePosition(ctx, e.AccountID, a.Str("instrument"), oanda.ClosePositionRequest{
					LongUnits: oanda.Decimal(a.Str("long-units")), ShortUnits: oanda.Decimal(a.Str("short-units")),
				})
			},
		},

		// ---- transaction ----
		{
			Path: []string{"transaction", "list"}, Summary: "List transaction pages for a time range", Account: true,
			Params: []Param{
				{Name: "from", Type: TTime, Desc: "Start time (RFC 3339)"},
				{Name: "to", Type: TTime, Desc: "End time (RFC 3339)"},
				{Name: "page-size", Type: TInt, Desc: "Transactions per page (1-1000, default 100)"},
				pTxTypes,
			},
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) {
				return e.Client.ListTransactions(ctx, e.AccountID, oanda.TransactionsParams{
					From: a.Time("from"), To: a.Time("to"), PageSize: a.Int("page-size"), Type: filters(a),
				})
			},
		},
		{
			Path: []string{"transaction", "get"}, Summary: "Get one transaction", Account: true,
			Params: []Param{{Name: "id", Type: TString, Required: true, Desc: "Transaction ID"}},
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) {
				return e.Client.GetTransaction(ctx, e.AccountID, a.Str("id"))
			},
		},
		{
			Path: []string{"transaction", "range"}, Summary: "Get transactions in an inclusive ID range", Account: true,
			Params: []Param{
				{Name: "from-id", Type: TString, Required: true, Desc: "First transaction ID"},
				{Name: "to-id", Type: TString, Required: true, Desc: "Last transaction ID"},
				pTxTypes,
			},
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) {
				return e.Client.GetTransactionRange(ctx, e.AccountID, a.Str("from-id"), a.Str("to-id"), filters(a)...)
			},
		},
		{
			Path: []string{"transaction", "since"}, Summary: "Get transactions after a transaction ID", Account: true,
			Params: []Param{{Name: "id", Type: TString, Required: true, Desc: "Return transactions after this ID"}, pTxTypes},
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) {
				return e.Client.GetTransactionsSinceID(ctx, e.AccountID, a.Str("id"), filters(a)...)
			},
		},
		{
			Path: []string{"transaction", "stream"}, Summary: "Stream account transactions as NDJSON", Account: true, Stream: true,
			Params: []Param{pMaxMessages, pDuration, pHeartbeats},
			Run: func(ctx context.Context, e *Env, a *Args) (any, error) {
				return runStream(ctx, e, a, func(ctx context.Context, fn oanda.StreamHandler) error {
					return e.Client.StreamTransactions(ctx, e.AccountID, fn)
				})
			},
		},
	}
}

func entryCommand(name, summary string, kind oanda.OrderType) *Command {
	return &Command{
		Path: []string{"order", name}, Summary: summary + " (positive units buy, negative units sell)", Account: true, Mutating: true,
		Params: entryParams(kind),
		Run: func(ctx context.Context, e *Env, a *Args) (any, error) {
			if err := e.checkUnits(a.Str("units")); err != nil {
				return nil, err
			}
			return e.Client.CreateOrder(ctx, e.AccountID, buildEntryOrder(kind, a))
		},
	}
}
