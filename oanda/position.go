package oanda

import (
	"context"
	"net/http"
)

// PositionsResponse is returned by ListPositions and ListOpenPositions.
type PositionsResponse struct {
	ResponseMeta
	Positions         []Position `json:"positions"`
	LastTransactionID string     `json:"lastTransactionID"`
}

// PositionResponse is returned by GetPosition.
type PositionResponse struct {
	ResponseMeta
	Position          Position `json:"position"`
	LastTransactionID string   `json:"lastTransactionID"`
}

// ClosePositionRequest is the body of ClosePosition. Units are "ALL", "NONE"
// or a positive decimal number; empty means "do not close this side".
type ClosePositionRequest struct {
	LongUnits             Decimal           `json:"longUnits,omitempty"`
	LongClientExtensions  *ClientExtensions `json:"longClientExtensions,omitempty"`
	ShortUnits            Decimal           `json:"shortUnits,omitempty"`
	ShortClientExtensions *ClientExtensions `json:"shortClientExtensions,omitempty"`
}

func (r ClosePositionRequest) validate() error {
	check := func(field string, v Decimal) error {
		if v == "" || v == "ALL" || v == "NONE" {
			return nil
		}
		return validatePositiveDecimal(field, string(v))
	}
	if err := check("longUnits", r.LongUnits); err != nil {
		return err
	}
	if err := check("shortUnits", r.ShortUnits); err != nil {
		return err
	}
	closes := func(v Decimal) bool { return v != "" && v != "NONE" }
	if !closes(r.LongUnits) && !closes(r.ShortUnits) {
		return invalid("units", "set longUnits and/or shortUnits")
	}
	return nil
}

// ListPositions lists all positions (open and previously held) of an account.
// GET /v3/accounts/{accountID}/positions
func (c *Client) ListPositions(ctx context.Context, accountID string) (*PositionsResponse, error) {
	p, err := accountPath(accountID, "positions")
	if err != nil {
		return nil, err
	}
	out := new(PositionsResponse)
	return out, c.do(ctx, &request{method: http.MethodGet, path: p}, out)
}

// ListOpenPositions lists the open positions of an account.
// GET /v3/accounts/{accountID}/openPositions
func (c *Client) ListOpenPositions(ctx context.Context, accountID string) (*PositionsResponse, error) {
	p, err := accountPath(accountID, "openPositions")
	if err != nil {
		return nil, err
	}
	out := new(PositionsResponse)
	return out, c.do(ctx, &request{method: http.MethodGet, path: p}, out)
}

// GetPosition returns the position in one instrument.
// GET /v3/accounts/{accountID}/positions/{instrument}
func (c *Client) GetPosition(ctx context.Context, accountID, instrument string) (*PositionResponse, error) {
	if err := ValidateInstrument(instrument); err != nil {
		return nil, err
	}
	p, err := accountPath(accountID, "positions", seg(instrument))
	if err != nil {
		return nil, err
	}
	out := new(PositionResponse)
	return out, c.do(ctx, &request{method: http.MethodGet, path: p}, out)
}

// ClosePosition closes the long and/or short side of a position.
// PUT /v3/accounts/{accountID}/positions/{instrument}/close
func (c *Client) ClosePosition(ctx context.Context, accountID, instrument string, req ClosePositionRequest) (*TransactionResult, error) {
	if err := ValidateInstrument(instrument); err != nil {
		return nil, err
	}
	p, err := accountPath(accountID, "positions", seg(instrument), "close")
	if err != nil {
		return nil, err
	}
	if err := req.validate(); err != nil {
		return nil, err
	}
	out := new(TransactionResult)
	return out, c.do(ctx, &request{method: http.MethodPut, path: p, body: req}, out)
}
