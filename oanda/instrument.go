package oanda

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// CandlesParams are the query parameters of GetCandles. Zero values are
// omitted so that OANDA's defaults apply.
type CandlesParams struct {
	// Price components: any combination of "M" (mid), "B" (bid), "A" (ask).
	// Defaults to "M".
	Price string
	// Granularity defaults to S5.
	Granularity CandlestickGranularity
	// Count is the number of candles (1-5000, default 500). It cannot be
	// combined with both From and To.
	Count int
	From  time.Time
	To    time.Time
	// Smooth uses the previous candle's close as the open.
	Smooth bool
	// IncludeFirst controls whether the candle at From is included.
	IncludeFirst *bool
	// DailyAlignment is the hour (0-23) used for daily candles.
	DailyAlignment *int
	// AlignmentTimezone is the IANA timezone used for DailyAlignment.
	AlignmentTimezone string
	// WeeklyAlignment is the day of week used for weekly candles.
	WeeklyAlignment WeeklyAlignment
}

func (p CandlesParams) values() (url.Values, error) {
	q := url.Values{}
	if p.Price != "" {
		if err := validatePriceComponents(p.Price); err != nil {
			return nil, err
		}
		q.Set("price", p.Price)
	}
	if err := validateEnum("granularity", p.Granularity, Granularities); err != nil {
		return nil, err
	}
	if p.Granularity != "" {
		q.Set("granularity", string(p.Granularity))
	}
	if err := validateCount("count", p.Count, 5000); err != nil {
		return nil, err
	}
	if p.Count > 0 {
		if !p.From.IsZero() && !p.To.IsZero() {
			return nil, invalid("count", "cannot be combined with both from and to")
		}
		q.Set("count", strconv.Itoa(p.Count))
	}
	if !p.From.IsZero() {
		q.Set("from", formatTime(p.From))
	}
	if !p.To.IsZero() {
		q.Set("to", formatTime(p.To))
	}
	if !p.From.IsZero() && !p.To.IsZero() && p.To.Before(p.From) {
		return nil, invalid("to", "must not be before from")
	}
	if p.Smooth {
		q.Set("smooth", "true")
	}
	if p.IncludeFirst != nil {
		q.Set("includeFirst", strconv.FormatBool(*p.IncludeFirst))
	}
	if p.DailyAlignment != nil {
		if *p.DailyAlignment < 0 || *p.DailyAlignment > 23 {
			return nil, invalid("dailyAlignment", "must be between 0 and 23")
		}
		q.Set("dailyAlignment", strconv.Itoa(*p.DailyAlignment))
	}
	if p.AlignmentTimezone != "" {
		if _, err := time.LoadLocation(p.AlignmentTimezone); err != nil || strings.ContainsAny(p.AlignmentTimezone, " \t") {
			return nil, invalid("alignmentTimezone", "%q is not a known IANA time zone", p.AlignmentTimezone)
		}
		q.Set("alignmentTimezone", p.AlignmentTimezone)
	}
	if err := validateEnum("weeklyAlignment", p.WeeklyAlignment, WeeklyAlignments); err != nil {
		return nil, err
	}
	if p.WeeklyAlignment != "" {
		q.Set("weeklyAlignment", string(p.WeeklyAlignment))
	}
	return q, nil
}

func validatePriceComponents(s string) error {
	if s == "" || len(s) > 3 {
		return invalid("price", "%q must be a combination of M, B and A", s)
	}
	seen := map[rune]bool{}
	for _, r := range s {
		if (r != 'M' && r != 'B' && r != 'A') || seen[r] {
			return invalid("price", "%q must be a combination of M, B and A", s)
		}
		seen[r] = true
	}
	return nil
}

// CandlesResponse is returned by GetCandles.
type CandlesResponse struct {
	ResponseMeta
	Instrument  string                 `json:"instrument"`
	Granularity CandlestickGranularity `json:"granularity"`
	Candles     []Candlestick          `json:"candles"`
}

// BookResponse is returned by GetOrderBook and GetPositionBook.
type BookResponse struct {
	ResponseMeta
	OrderBook    *Book `json:"orderBook,omitempty"`
	PositionBook *Book `json:"positionBook,omitempty"`
}

// GetCandles fetches candlestick data for an instrument.
// GET /v3/instruments/{instrument}/candles
func (c *Client) GetCandles(ctx context.Context, instrument string, params CandlesParams) (*CandlesResponse, error) {
	if err := ValidateInstrument(instrument); err != nil {
		return nil, err
	}
	q, err := params.values()
	if err != nil {
		return nil, err
	}
	out := new(CandlesResponse)
	return out, c.do(ctx, &request{method: http.MethodGet, path: "/v3/instruments/" + seg(instrument) + "/candles", query: q}, out)
}

// GetOrderBook fetches an order book snapshot (the latest when at is zero).
// GET /v3/instruments/{instrument}/orderBook
func (c *Client) GetOrderBook(ctx context.Context, instrument string, at time.Time) (*BookResponse, error) {
	return c.getBook(ctx, instrument, "orderBook", at)
}

// GetPositionBook fetches a position book snapshot (the latest when at is
// zero).
// GET /v3/instruments/{instrument}/positionBook
func (c *Client) GetPositionBook(ctx context.Context, instrument string, at time.Time) (*BookResponse, error) {
	return c.getBook(ctx, instrument, "positionBook", at)
}

func (c *Client) getBook(ctx context.Context, instrument, kind string, at time.Time) (*BookResponse, error) {
	if err := ValidateInstrument(instrument); err != nil {
		return nil, err
	}
	q := url.Values{}
	if !at.IsZero() {
		q.Set("time", formatTime(at))
	}
	out := new(BookResponse)
	return out, c.do(ctx, &request{method: http.MethodGet, path: "/v3/instruments/" + seg(instrument) + "/" + kind, query: q}, out)
}
