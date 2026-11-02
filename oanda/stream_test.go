package oanda

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func streamHandler(lines []string, hang bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		for _, l := range lines {
			fmt.Fprintln(w, l)
			w.(http.Flusher).Flush()
		}
		if hang {
			<-r.Context().Done()
		}
	}
}

func TestStreamPricing(t *testing.T) {
	lines := []string{
		`{"type":"HEARTBEAT","time":"2024-01-01T00:00:00Z"}`,
		`{"type":"PRICE","instrument":"EUR_USD","bids":[{"price":"1.1","liquidity":1000000}],"asks":[{"price":"1.2","liquidity":"500"}],"tradeable":true}`,
		`{"type":"PRICE","instrument":"USD_JPY","bids":[],"asks":[]}`,
	}
	tests := []struct {
		name     string
		hang     bool
		stopAt   int
		stall    time.Duration
		wantErr  error
		wantMsgs int
	}{
		{name: "stop via ErrStopStream", hang: true, stopAt: 2, stall: time.Second, wantMsgs: 2},
		{name: "server closes stream", hang: false, stall: time.Second, wantErr: errUnexpectedClose, wantMsgs: 3},
		{name: "stall detected", hang: true, stall: 200 * time.Millisecond, wantErr: ErrStreamStalled, wantMsgs: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v3/accounts/"+testAccount+"/pricing/stream" || r.URL.Query().Get("instruments") != "EUR_USD,USD_JPY" {
					t.Errorf("unexpected request %s", r.URL)
				}
				streamHandler(lines, tt.hang)(w, r)
			}, WithStreamStallTimeout(tt.stall))
			n := 0
			err := c.StreamPricing(context.Background(), testAccount, PricingStreamParams{Instruments: []string{"EUR_USD", "USD_JPY"}}, func(m StreamMessage) error {
				n++
				if m.Type == "PRICE" && n == 2 {
					p, err := m.Price()
					if err != nil || p.Bids[0].Liquidity != "1000000" || p.Asks[0].Liquidity != "500" {
						t.Errorf("price = %+v, %v", p, err)
					}
				}
				if tt.stopAt > 0 && n == tt.stopAt {
					return ErrStopStream
				}
				return nil
			})
			switch {
			case tt.wantErr == errUnexpectedClose:
				if err == nil {
					t.Error("expected an error when the server closes the stream")
				}
			case !errors.Is(err, tt.wantErr):
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
			if n != tt.wantMsgs {
				t.Errorf("messages = %d, want %d", n, tt.wantMsgs)
			}
		})
	}
}

var errUnexpectedClose = errors.New("unexpected close")

func TestStreamContextCancel(t *testing.T) {
	c, _ := newTestClient(t, streamHandler(nil, true))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := c.StreamTransactions(ctx, testAccount, func(StreamMessage) error { return nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v", err)
	}
}
