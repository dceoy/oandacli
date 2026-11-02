package oanda

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// TransactionsParams are the query parameters of ListTransactions.
type TransactionsParams struct {
	From time.Time
	To   time.Time
	// PageSize is the number of transactions per page (1-1000, default 100).
	PageSize int
	Type     []TransactionFilter
}

// TransactionPagesResponse is returned by ListTransactions. Pages contains
// URLs that can be fetched with GetTransactionPage.
type TransactionPagesResponse struct {
	ResponseMeta
	From              DateTime `json:"from"`
	To                DateTime `json:"to"`
	PageSize          int      `json:"pageSize"`
	Type              []string `json:"type,omitempty"`
	Count             int      `json:"count"`
	Pages             []string `json:"pages"`
	LastTransactionID string   `json:"lastTransactionID"`
}

// TransactionsResponse is returned by the range, since-ID and page endpoints.
type TransactionsResponse struct {
	ResponseMeta
	Transactions      []Transaction `json:"transactions"`
	LastTransactionID string        `json:"lastTransactionID"`
}

// TransactionResponse is returned by GetTransaction.
type TransactionResponse struct {
	ResponseMeta
	Transaction       Transaction `json:"transaction"`
	LastTransactionID string      `json:"lastTransactionID"`
}

// ListTransactions returns transaction page URLs for a time range.
// GET /v3/accounts/{accountID}/transactions
func (c *Client) ListTransactions(ctx context.Context, accountID string, params TransactionsParams) (*TransactionPagesResponse, error) {
	p, err := accountPath(accountID, "transactions")
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	if !params.From.IsZero() {
		q.Set("from", formatTime(params.From))
	}
	if !params.To.IsZero() {
		q.Set("to", formatTime(params.To))
	}
	if err := validateCount("pageSize", params.PageSize, 1000); err != nil {
		return nil, err
	}
	if params.PageSize > 0 {
		q.Set("pageSize", strconv.Itoa(params.PageSize))
	}
	if err := validateTransactionTypes(params.Type); err != nil {
		return nil, err
	}
	if len(params.Type) > 0 {
		q.Set("type", joinStrings(params.Type))
	}
	out := new(TransactionPagesResponse)
	return out, c.do(ctx, &request{method: http.MethodGet, path: p, query: q}, out)
}

// GetTransactionPage fetches one of the page URLs returned by
// ListTransactions. To prevent leaking the token, the URL must point to the
// client's own API host and to the transactions/idrange endpoint.
func (c *Client) GetTransactionPage(ctx context.Context, pageURL string) (*TransactionsResponse, error) {
	u, err := url.Parse(pageURL)
	if err != nil {
		return nil, invalid("page URL", "%v", err)
	}
	if u.Scheme != c.apiURL.Scheme || u.Host != c.apiURL.Host || u.User != nil || u.Fragment != "" {
		return nil, invalid("page URL", "must point to %s", c.apiURL.String())
	}
	rel := strings.TrimPrefix(u.Path, c.apiURL.Path)
	parts := strings.Split(strings.TrimPrefix(rel, "/"), "/")
	if len(parts) != 5 || parts[0] != "v3" || parts[1] != "accounts" || parts[3] != "transactions" || parts[4] != "idrange" {
		return nil, invalid("page URL", "must be a /v3/accounts/{id}/transactions/idrange URL")
	}
	q := u.Query()
	return c.GetTransactionRange(ctx, parts[2], q.Get("from"), q.Get("to"), splitFilters(q.Get("type"))...)
}

func splitFilters(s string) []TransactionFilter {
	if s == "" {
		return nil
	}
	var out []TransactionFilter
	for _, f := range strings.Split(s, ",") {
		out = append(out, TransactionFilter(f))
	}
	return out
}

// GetTransaction returns one transaction.
// GET /v3/accounts/{accountID}/transactions/{transactionID}
func (c *Client) GetTransaction(ctx context.Context, accountID, transactionID string) (*TransactionResponse, error) {
	if err := ValidateTransactionID(transactionID); err != nil {
		return nil, err
	}
	p, err := accountPath(accountID, "transactions", seg(transactionID))
	if err != nil {
		return nil, err
	}
	out := new(TransactionResponse)
	return out, c.do(ctx, &request{method: http.MethodGet, path: p}, out)
}

// GetTransactionRange returns transactions in an inclusive ID range.
// GET /v3/accounts/{accountID}/transactions/idrange
func (c *Client) GetTransactionRange(ctx context.Context, accountID, fromID, toID string, types ...TransactionFilter) (*TransactionsResponse, error) {
	p, err := accountPath(accountID, "transactions", "idrange")
	if err != nil {
		return nil, err
	}
	if err := ValidateTransactionID(fromID); err != nil {
		return nil, err
	}
	if err := ValidateTransactionID(toID); err != nil {
		return nil, err
	}
	if err := validateTransactionTypes(types); err != nil {
		return nil, err
	}
	q := url.Values{"from": {fromID}, "to": {toID}}
	if len(types) > 0 {
		q.Set("type", joinStrings(types))
	}
	out := new(TransactionsResponse)
	return out, c.do(ctx, &request{method: http.MethodGet, path: p, query: q}, out)
}

// GetTransactionsSinceID returns transactions after the given ID.
// GET /v3/accounts/{accountID}/transactions/sinceid
func (c *Client) GetTransactionsSinceID(ctx context.Context, accountID, id string, types ...TransactionFilter) (*TransactionsResponse, error) {
	p, err := accountPath(accountID, "transactions", "sinceid")
	if err != nil {
		return nil, err
	}
	if err := ValidateTransactionID(id); err != nil {
		return nil, err
	}
	if err := validateTransactionTypes(types); err != nil {
		return nil, err
	}
	q := url.Values{"id": {id}}
	if len(types) > 0 {
		q.Set("type", joinStrings(types))
	}
	out := new(TransactionsResponse)
	return out, c.do(ctx, &request{method: http.MethodGet, path: p, query: q}, out)
}
