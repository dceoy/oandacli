package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Exercise every non-stream command through its flag parser, local policy,
// request construction and response handler using a loopback OANDA server.
func TestAllCLICommandRoutes(t *testing.T) {
	cases := [][]string{
		{"account", "list"}, {"account", "get"}, {"account", "summary"},
		{"account", "instruments", "--instruments", "EUR_USD,USD_JPY"},
		{"account", "changes", "--since-transaction-id", "9"},
		{"account", "configure", "--alias", "test"},
		{"instrument", "candles", "--instrument", "EUR_USD", "--granularity", "M1", "--price", "MBA", "--count", "10", "--include-first", "--daily-alignment", "5", "--weekly-alignment", "Friday", "--alignment-timezone", "UTC", "--smooth"},
		{"instrument", "order-book", "--instrument", "EUR_USD", "--time", "2025-01-01T00:00:00Z"},
		{"instrument", "position-book", "--instrument", "EUR_USD", "--time", "2025-01-01T00:00:00Z"},
		{"pricing", "get", "--instruments", "EUR_USD", "--since", "2025-01-01T00:00:00Z", "--include-home-conversions"},
		{"pricing", "latest-candles", "--specs", "EUR_USD:M1:BA", "--units", "2", "--daily-alignment", "4", "--alignment-timezone", "UTC", "--weekly-alignment", "Friday", "--smooth"},
		{"pricing", "candles", "--instrument", "EUR_USD", "--units", "2", "--count", "2", "--include-first", "--price", "BA"},
		{"order", "list", "--ids", "1,2", "--state", "PENDING", "--before-id", "99", "--instrument", "EUR_USD", "--count", "2"},
		{"order", "pending"}, {"order", "get", "--order", "12"},
		{"order", "market", "--instrument", "EUR_USD", "--units", "1", "--price-bound", "1.2", "--sl-price", "0.5", "--tp-price", "1.5", "--client-id", "x", "--client-tag", "test", "--client-comment", "c", "--trade-client-id", "t"},
		{"order", "limit", "--instrument", "EUR_USD", "--units", "1", "--price", "1.5", "--time-in-force", "GTD", "--gtd-time", "2027-01-01T00:00:00Z", "--trigger-condition", "DEFAULT", "--sl-distance", "0.01", "--gsl-price", "0.7", "--tsl-distance", "0.3"},
		{"order", "stop", "--instrument", "EUR_USD", "--units", "1", "--price", "1.5", "--price-bound", "2"},
		{"order", "market-if-touched", "--instrument", "EUR_USD", "--units", "1", "--price", "1.5"},
		{"order", "create", "--body", `{"order":{"type":"MARKET","instrument":"EUR_USD","units":"1"}}`},
		{"order", "replace", "--order", "12", "--body", `{"order":{"type":"MARKET","instrument":"EUR_USD","units":"1"}}`},
		{"order", "cancel", "--order", "12"},
		{"order", "client-extensions", "--order", "12", "--client-id", "x", "--trade-client-tag", "tag"},
		{"trade", "list", "--ids", "1,2", "--state", "OPEN", "--instrument", "EUR_USD", "--count", "3", "--before-id", "55"},
		{"trade", "open"}, {"trade", "get", "--trade", "12"}, {"trade", "close", "--trade", "12", "--units", "5"},
		{"trade", "client-extensions", "--trade", "12", "--client-comment", "note"},
		{"trade", "orders", "--trade", "12", "--tp-price", "1.5", "--sl-distance", "0.2", "--tsl-distance", "0.3", "--gsl-price", "0.7"},
		{"trade", "orders", "--trade", "12", "--cancel", "tp,sl,tsl,gsl"},
		{"position", "list"}, {"position", "open"}, {"position", "get", "--instrument", "EUR_USD"},
		{"position", "close", "--instrument", "EUR_USD", "--long-units", "ALL", "--short-units", "NONE"},
		{"transaction", "list", "--from", "2025-01-01T00:00:00Z", "--to", "2025-01-02T00:00:00Z", "--page-size", "10", "--type", "ORDER_FILL"},
		{"transaction", "get", "--id", "12"}, {"transaction", "range", "--from-id", "12", "--to-id", "22", "--type", "ORDER_FILL"},
		{"transaction", "since", "--id", "12", "--type", "ORDER_FILL"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args[:2], "/"), func(t *testing.T) {
			h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			})
			if code := h.run(args...); code != ExitOK {
				t.Errorf("exit %d: %s", code, h.stderr.String())
			}
			if h.calls.Load() != 1 {
				t.Errorf("HTTP requests %d, want exactly 1", h.calls.Load())
			}
			var out any
			if err := json.Unmarshal(h.stdout.Bytes(), &out); err != nil {
				t.Errorf("JSON result %q: %v", h.stdout.String(), err)
			}
		})
	}
}

func TestParamConversionsAndDefaults(t *testing.T) {
	cases := []struct {
		name  string
		p     Param
		raw   any
		valid bool
	}{
		{"boolean", Param{Name: "x", Type: TBool}, true, true},
		{"boolean wrong type", Param{Name: "x", Type: TBool}, "true", false},
		{"nonbool raw", Param{Name: "x", Type: TInt}, 3, false},
		{"string too long", Param{Name: "x", Type: TString}, strings.Repeat("z", 4097), false},
		{"valid int", Param{Name: "x", Type: TInt}, "42", true},
		{"invalid int", Param{Name: "x", Type: TInt}, "abc", false},
		{"array", Param{Name: "x", Type: TStrings}, "a, b, ,c", true},
		{"array bad enum", Param{Name: "x", Type: TStrings, Enum: []string{"a", "b"}}, "a,c", false},
		{"object", Param{Name: "x", Type: TJSON}, `{"a":1}`, true},
		{"bad object", Param{Name: "x", Type: TJSON}, `[]`, false},
		{"duration", Param{Name: "x", Type: TDuration}, "3s", true},
		{"duration bad", Param{Name: "x", Type: TDuration}, "-3s", false},
		{"time", Param{Name: "x", Type: TTime}, "2025-01-01T00:00:00Z", true},
		{"time bad", Param{Name: "x", Type: TTime}, "yesterday", false},
		{"unsupported", Param{Name: "x", Type: "other"}, "x", false},
		{"enum valid", Param{Name: "x", Type: TString, Enum: []string{"y"}}, "y", true},
		{"enum invalid", Param{Name: "x", Type: TString, Enum: []string{"y"}}, "z", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.p.convert(tc.raw)
			if (err == nil) != tc.valid {
				t.Errorf("convert error %v, want valid=%v", err, tc.valid)
			}
		})
	}
	if _, err := finalize(&Command{Params: []Param{{Name: "required", Required: true}}}, map[string]any{}); err == nil {
		t.Error("missing required param")
	}
	if _, err := finalize(&Command{Params: []Param{{Name: "default", Type: TBool, Default: "bad"}}}, map[string]any{}); err == nil {
		t.Error("invalid default param")
	}
}
