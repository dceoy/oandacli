package oanda

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// PricingParams are the query parameters of GetPricing.
type PricingParams struct {
	// Instruments to price (required).
	Instruments []string
	// Since restricts results to prices that changed after this time.
	Since time.Time
	// IncludeHomeConversions adds home-currency conversion factors.
	IncludeHomeConversions bool
}

// PricingResponse is returned by GetPricing.
type PricingResponse struct {
	ResponseMeta
	Prices          []ClientPrice     `json:"prices"`
	HomeConversions []HomeConversions `json:"homeConversions,omitempty"`
	Time            DateTime          `json:"time"`
}

// LatestCandlesParams are the query parameters of GetLatestCandles.
type LatestCandlesParams struct {
	// Specifications such as "EUR_USD:S10:BM" (instrument:granularity:price).
	Specifications    []string
	Units             Decimal
	Smooth            bool
	DailyAlignment    *int
	AlignmentTimezone string
	WeeklyAlignment   WeeklyAlignment
}

// LatestCandlesResponse is returned by GetLatestCandles.
type LatestCandlesResponse struct {
	ResponseMeta
	LatestCandles []struct {
		Instrument  string                 `json:"instrument"`
		Granularity CandlestickGranularity `json:"granularity"`
		Candles     []Candlestick          `json:"candles"`
	} `json:"latestCandles"`
}

// GetPricing returns current prices for instruments.
// GET /v3/accounts/{accountID}/pricing
func (c *Client) GetPricing(ctx context.Context, accountID string, params PricingParams) (*PricingResponse, error) {
	p, err := accountPath(accountID, "pricing")
	if err != nil {
		return nil, err
	}
	if err := validateInstruments(params.Instruments, true); err != nil {
		return nil, err
	}
	q := url.Values{"instruments": {joinStrings(params.Instruments)}}
	if !params.Since.IsZero() {
		q.Set("since", formatTime(params.Since))
	}
	if params.IncludeHomeConversions {
		q.Set("includeHomeConversions", "true")
	}
	out := new(PricingResponse)
	return out, c.do(ctx, &request{method: http.MethodGet, path: p, query: q}, out)
}

// GetLatestCandles returns the latest candles for several
// instrument/granularity/price specifications at once.
// GET /v3/accounts/{accountID}/candles/latest
func (c *Client) GetLatestCandles(ctx context.Context, accountID string, params LatestCandlesParams) (*LatestCandlesResponse, error) {
	p, err := accountPath(accountID, "candles", "latest")
	if err != nil {
		return nil, err
	}
	if len(params.Specifications) == 0 || len(params.Specifications) > 100 {
		return nil, invalid("candleSpecifications", "between 1 and 100 specifications are required")
	}
	for _, s := range params.Specifications {
		if !reCandleSpec.MatchString(s) {
			return nil, invalid("candleSpecifications", "%q must look like EUR_USD:S10:BM", s)
		}
		parts := strings.Split(s, ":")
		if err := validateEnum("granularity", CandlestickGranularity(parts[1]), Granularities); err != nil {
			return nil, err
		}
		if err := validatePriceComponents(parts[2]); err != nil {
			return nil, err
		}
	}
	q := url.Values{"candleSpecifications": {strings.Join(params.Specifications, ",")}}
	if params.Units != "" {
		if err := ValidateUnits("units", string(params.Units)); err != nil {
			return nil, err
		}
		q.Set("units", string(params.Units))
	}
	if params.Smooth {
		q.Set("smooth", "true")
	}
	if params.DailyAlignment != nil {
		if *params.DailyAlignment < 0 || *params.DailyAlignment > 23 {
			return nil, invalid("dailyAlignment", "must be between 0 and 23")
		}
		q.Set("dailyAlignment", strconv.Itoa(*params.DailyAlignment))
	}
	if params.AlignmentTimezone != "" {
		if _, err := time.LoadLocation(params.AlignmentTimezone); err != nil {
			return nil, invalid("alignmentTimezone", "%q is not a known IANA time zone", params.AlignmentTimezone)
		}
		q.Set("alignmentTimezone", params.AlignmentTimezone)
	}
	if err := validateEnum("weeklyAlignment", params.WeeklyAlignment, WeeklyAlignments); err != nil {
		return nil, err
	}
	if params.WeeklyAlignment != "" {
		q.Set("weeklyAlignment", string(params.WeeklyAlignment))
	}
	out := new(LatestCandlesResponse)
	return out, c.do(ctx, &request{method: http.MethodGet, path: p, query: q}, out)
}

// GetAccountCandles fetches account-specific candlesticks, optionally using
// units to calculate volume-weighted bid and ask prices.
// GET /v3/accounts/{accountID}/instruments/{instrument}/candles
func (c *Client) GetAccountCandles(ctx context.Context, accountID, instrument string, params CandlesParams, units Decimal) (*CandlesResponse, error) {
	if err := ValidateInstrument(instrument); err != nil {
		return nil, err
	}
	p, err := accountPath(accountID, "instruments", seg(instrument), "candles")
	if err != nil {
		return nil, err
	}
	q, err := params.values()
	if err != nil {
		return nil, err
	}
	if units != "" {
		if err := ValidateUnits("units", string(units)); err != nil {
			return nil, err
		}
		q.Set("units", string(units))
	}
	out := new(CandlesResponse)
	return out, c.do(ctx, &request{method: http.MethodGet, path: p, query: q}, out)
}
