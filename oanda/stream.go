package oanda

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"
)

// StreamMessage is one line of a pricing or transaction stream.
type StreamMessage struct {
	// Type is "PRICE" or "HEARTBEAT" for pricing streams, and a transaction
	// type or "HEARTBEAT" for transaction streams.
	Type string
	// Raw is the complete JSON line.
	Raw json.RawMessage
}

// IsHeartbeat reports whether the message is a heartbeat.
func (m StreamMessage) IsHeartbeat() bool { return m.Type == "HEARTBEAT" }

// Price decodes a PRICE message.
func (m StreamMessage) Price() (*ClientPrice, error) {
	if m.Type != "PRICE" {
		return nil, fmt.Errorf("oanda: stream message is %q, not PRICE", m.Type)
	}
	p := new(ClientPrice)
	return p, json.Unmarshal(m.Raw, p)
}

// Transaction decodes a transaction message.
func (m StreamMessage) Transaction() (*Transaction, error) {
	if m.IsHeartbeat() {
		return nil, errors.New("oanda: stream message is a heartbeat")
	}
	t := new(Transaction)
	return t, json.Unmarshal(m.Raw, t)
}

// StreamHandler receives stream messages. Returning ErrStopStream ends the
// stream cleanly; returning any other error aborts it with that error.
type StreamHandler func(StreamMessage) error

// PricingStreamParams are the query parameters of StreamPricing.
type PricingStreamParams struct {
	Instruments []string
	// NoSnapshot disables the initial snapshot of current prices.
	NoSnapshot             bool
	IncludeHomeConversions bool
}

// StreamPricing streams prices until ctx is cancelled, the handler returns an
// error, or the connection fails. It is not reconnected automatically.
// GET /v3/accounts/{accountID}/pricing/stream
func (c *Client) StreamPricing(ctx context.Context, accountID string, params PricingStreamParams, fn StreamHandler) error {
	p, err := accountPath(accountID, "pricing", "stream")
	if err != nil {
		return err
	}
	if err := validateInstruments(params.Instruments, true); err != nil {
		return err
	}
	q := url.Values{"instruments": {joinStrings(params.Instruments)}}
	if params.NoSnapshot {
		q.Set("snapshot", "false")
	}
	if params.IncludeHomeConversions {
		q.Set("includeHomeConversions", "true")
	}
	return c.stream(ctx, &request{method: http.MethodGet, path: p, query: q}, fn)
}

// StreamTransactions streams account transactions.
// GET /v3/accounts/{accountID}/transactions/stream
func (c *Client) StreamTransactions(ctx context.Context, accountID string, fn StreamHandler) error {
	p, err := accountPath(accountID, "transactions", "stream")
	if err != nil {
		return err
	}
	return c.stream(ctx, &request{method: http.MethodGet, path: p}, fn)
}

const maxStreamLine = 1 << 20

func (c *Client) stream(parent context.Context, r *request, fn StreamHandler) error {
	if fn == nil {
		return invalid("handler", "nil")
	}
	u, _, err := c.prepare(r, c.streamURL)
	if err != nil {
		return err
	}
	if err := c.limiter.wait(parent); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	var stalled atomic.Bool
	watchdog := time.AfterFunc(c.stallTimeout, func() { stalled.Store(true); cancel() })
	defer watchdog.Stop()

	req, err := c.newHTTPRequest(ctx, r.method, u, nil)
	if err != nil {
		return fmt.Errorf("oanda: build request: %w", err)
	}
	start := time.Now()
	resp, err := c.streamHTTPClient.Do(req)
	if err != nil {
		switch {
		case parent.Err() != nil:
			return parent.Err()
		case stalled.Load():
			return ErrStreamStalled
		}
		return fmt.Errorf("oanda: %s %s: %w", r.method, r.path, err)
	}
	defer resp.Body.Close()
	requestID := resp.Header.Get("RequestID")
	c.log(ctx, "stream opened", r, 1, resp.StatusCode, requestID, time.Since(start), nil)
	if resp.StatusCode != http.StatusOK {
		body, _ := readLimited(resp.Body, 1<<20)
		return newAPIError(resp.StatusCode, requestID, body)
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64<<10), maxStreamLine)
	for sc.Scan() {
		watchdog.Reset(c.stallTimeout)
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &head); err != nil {
			return fmt.Errorf("oanda: malformed stream message: %w", err)
		}
		msg := StreamMessage{Type: head.Type, Raw: append(json.RawMessage(nil), line...)}
		if err := fn(msg); err != nil {
			if errors.Is(err, ErrStopStream) {
				return nil
			}
			return err
		}
	}
	switch {
	case parent.Err() != nil:
		return parent.Err()
	case stalled.Load():
		return ErrStreamStalled
	case sc.Err() != nil:
		return fmt.Errorf("oanda: read stream: %w", sc.Err())
	}
	return fmt.Errorf("oanda: stream closed by server: %w", io.ErrUnexpectedEOF)
}
