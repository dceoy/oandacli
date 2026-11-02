package oanda

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// AccountsResponse is returned by ListAccounts.
type AccountsResponse struct {
	ResponseMeta
	Accounts []AccountProperties `json:"accounts"`
}

// AccountResponse is returned by GetAccount.
type AccountResponse struct {
	ResponseMeta
	Account           Account `json:"account"`
	LastTransactionID string  `json:"lastTransactionID"`
}

// AccountSummaryResponse is returned by GetAccountSummary.
type AccountSummaryResponse struct {
	ResponseMeta
	Account           AccountSummary `json:"account"`
	LastTransactionID string         `json:"lastTransactionID"`
}

// AccountInstrumentsResponse is returned by GetAccountInstruments.
type AccountInstrumentsResponse struct {
	ResponseMeta
	Instruments       []Instrument `json:"instruments"`
	LastTransactionID string       `json:"lastTransactionID"`
}

// AccountChangesResponse is returned by GetAccountChanges. Changes and State
// are kept as raw JSON.
type AccountChangesResponse struct {
	ResponseMeta
	Changes           json.RawMessage `json:"changes"`
	State             json.RawMessage `json:"state"`
	LastTransactionID string          `json:"lastTransactionID"`
}

// AccountConfiguration is the body of ConfigureAccount. Empty fields are not
// changed.
type AccountConfiguration struct {
	Alias      string  `json:"alias,omitempty"`
	MarginRate Decimal `json:"marginRate,omitempty"`
}

// ListAccounts lists the accounts the token is authorized to access.
// GET /v3/accounts
func (c *Client) ListAccounts(ctx context.Context) (*AccountsResponse, error) {
	out := new(AccountsResponse)
	return out, c.do(ctx, &request{method: http.MethodGet, path: "/v3/accounts"}, out)
}

// GetAccount returns the full details of an account.
// GET /v3/accounts/{accountID}
func (c *Client) GetAccount(ctx context.Context, accountID string) (*AccountResponse, error) {
	p, err := accountPath(accountID)
	if err != nil {
		return nil, err
	}
	out := new(AccountResponse)
	return out, c.do(ctx, &request{method: http.MethodGet, path: p}, out)
}

// GetAccountSummary returns the summary of an account.
// GET /v3/accounts/{accountID}/summary
func (c *Client) GetAccountSummary(ctx context.Context, accountID string) (*AccountSummaryResponse, error) {
	p, err := accountPath(accountID, "summary")
	if err != nil {
		return nil, err
	}
	out := new(AccountSummaryResponse)
	return out, c.do(ctx, &request{method: http.MethodGet, path: p}, out)
}

// GetAccountInstruments lists the tradeable instruments of an account,
// optionally restricted to the given instruments.
// GET /v3/accounts/{accountID}/instruments
func (c *Client) GetAccountInstruments(ctx context.Context, accountID string, instruments ...string) (*AccountInstrumentsResponse, error) {
	p, err := accountPath(accountID, "instruments")
	if err != nil {
		return nil, err
	}
	if err := validateInstruments(instruments, false); err != nil {
		return nil, err
	}
	q := url.Values{}
	if len(instruments) > 0 {
		q.Set("instruments", joinStrings(instruments))
	}
	out := new(AccountInstrumentsResponse)
	return out, c.do(ctx, &request{method: http.MethodGet, path: p, query: q}, out)
}

// ConfigureAccount sets the alias and/or default margin rate of an account.
// PATCH /v3/accounts/{accountID}/configuration
func (c *Client) ConfigureAccount(ctx context.Context, accountID string, cfg AccountConfiguration) (*TransactionResult, error) {
	p, err := accountPath(accountID, "configuration")
	if err != nil {
		return nil, err
	}
	if cfg.Alias == "" && cfg.MarginRate == "" {
		return nil, invalid("configuration", "set alias and/or marginRate")
	}
	if len(cfg.Alias) > 256 {
		return nil, invalid("alias", "too long")
	}
	if cfg.MarginRate != "" {
		if err := validatePositiveDecimal("marginRate", string(cfg.MarginRate)); err != nil {
			return nil, err
		}
	}
	out := new(TransactionResult)
	return out, c.do(ctx, &request{method: http.MethodPatch, path: p, body: cfg}, out)
}

// GetAccountChanges polls for changes to an account since a transaction ID.
// GET /v3/accounts/{accountID}/changes
func (c *Client) GetAccountChanges(ctx context.Context, accountID, sinceTransactionID string) (*AccountChangesResponse, error) {
	p, err := accountPath(accountID, "changes")
	if err != nil {
		return nil, err
	}
	if err := ValidateTransactionID(sinceTransactionID); err != nil {
		return nil, err
	}
	q := url.Values{"sinceTransactionID": {sinceTransactionID}}
	out := new(AccountChangesResponse)
	return out, c.do(ctx, &request{method: http.MethodGet, path: p, query: q}, out)
}
