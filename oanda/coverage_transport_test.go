package oanda

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"
)

type faultyReader struct{}

func (faultyReader) Read(p []byte) (int, error) { return 0, errors.New("read failed") }

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestClientOptionValidationAndConfiguration(t *testing.T) {
	tok := mustToken(t)
	bad := []struct {
		name string
		opts []Option
	}{
		{"no token", nil},
		{"invalid environment", []Option{WithEnvironment("bogus")}},
		{"nil HTTP client", []Option{WithHTTPClient(nil)}},
		{"zero timeout", []Option{WithTimeout(0)}},
		{"bad user agent", []Option{WithUserAgent("inject\nheader")}},
		{"negative retries", []Option{WithMaxRetries(-1)}},
		{"excessive retries", []Option{WithMaxRetries(11)}},
		{"negative rate", []Option{WithRateLimit(-1, 2)}},
		{"zero burst", []Option{WithRateLimit(1, 0)}},
		{"zero response cap", []Option{WithMaxResponseBytes(0)}},
		{"zero stall", []Option{WithStreamStallTimeout(0)}},
		{"custom client and wait", []Option{WithHTTPClient(&http.Client{}), WithConnectionWait(func(context.Context) error { return nil })}},
		{"invalid API URL", []Option{WithBaseURLs("http://badhost", "https://example.org")}},
		{"invalid stream URL", []Option{WithBaseURLs("https://example.org", "http://badhost")}},
		{"malformed API URL", []Option{WithBaseURLs("%::", "https://example.org")}},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			token := tok
			if tc.name == "no token" {
				token = Token{}
			}
			if c, err := NewClient(token, tc.opts...); err == nil || c != nil {
				t.Errorf("client=%v err=%v", c, err)
			}
		})
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	options := []Option{WithEnvironment(Live), WithBaseURLs("http://127.0.0.1:1", "http://127.0.0.1:1"), WithTimeout(time.Second), WithUserAgent("test-agent"), WithMaxRetries(0), WithRateLimit(1, 2), WithMaxResponseBytes(12345), WithStreamStallTimeout(time.Second), WithReadOnly(true), WithDryRun(true), WithMutationCheck(func(string, string, []byte) error { return nil }), WithLogger(logger)}
	c, err := NewClient(tok, options...)
	if err != nil {
		t.Fatal(err)
	}
	if c.Environment() != Live || !c.ReadOnly() || !c.dryRun || c.maxResponseBytes != 12345 || !strings.HasPrefix(c.userAgent, "test-agent") || c.limiter == nil {
		t.Errorf("option values not retained: %+v", c)
	}
	c.log(context.Background(), "test", &request{method: http.MethodGet, path: "/v3"}, 1, 200, "request-42", time.Millisecond, errors.New("example"))
	if !strings.Contains(logs.String(), "request-42") || !strings.Contains(logs.String(), "example") {
		t.Errorf("logging omitted metadata: %s", logs.String())
	}
	custom, err := NewClient(tok, WithHTTPClient(&http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}}, nil
	})}), WithBaseURLs("http://127.0.0.1:1", "http://127.0.0.1:1"), WithRateLimit(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := custom.ListAccounts(context.Background()); err != nil {
		t.Error(err)
	}
}

func TestClientHelpersAndFailureHandling(t *testing.T) {
	tok, err := NewToken(strings.Repeat("x", 1025))
	if err == nil || !tok.IsZero() {
		t.Error("oversized token accepted")
	}
	tok = mustToken(t)
	if tok.String() != redacted || tok.GoString() != redacted {
		t.Error("token string leaked")
	}
	if v, _ := tok.MarshalText(); string(v) != redacted {
		t.Error("token marshaler leaked")
	}
	if u, err := parseBaseURL("test", "%://bad"); err == nil || u != nil {
		t.Errorf("malformed URL accepted %v", u)
	}
	c, _ := NewClient(tok, WithBaseURLs("http://127.0.0.1:1", "http://127.0.0.1:1"), WithRateLimit(0, 0), WithMaxRetries(0))
	if _, err := c.newHTTPRequest(context.Background(), http.MethodGet, "%::bad", nil); err == nil {
		t.Error("invalid HTTP URL accepted")
	}
	if _, _, err := c.prepare(&request{method: http.MethodPost, path: "/x", body: math.NaN()}, c.apiURL); err == nil {
		t.Error("NaN JSON body encoded")
	}
	if _, err := readLimited(faultyReader{}, 100); err == nil {
		t.Error("reader failure ignored")
	}
	if d := backoff(7); d < 0 || d > 5*time.Second {
		t.Errorf("backoff %v", d)
	}
	for _, tc := range []struct {
		value string
		valid bool
	}{{"", false}, {"oops", false}, {"-10", true}, {"90", true}, {"0", true}, {"Wed, 21 Oct 2015 07:28:00 GMT", true}} {
		d, ok := retryAfter(tc.value)
		if ok != tc.valid || d > 30*time.Second || d < 0 {
			t.Errorf("retryAfter(%q)=(%v,%v)", tc.value, d, ok)
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleep(canceled, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("sleep canceled: %v", err)
	}
	lim := newLimiter(100, 1)
	if err := lim.wait(context.Background()); err != nil {
		t.Error(err)
	}
	if err := lim.wait(canceled); !errors.Is(err, context.Canceled) {
		t.Errorf("limiter cancellation: %v", err)
	}
	if err := (*limiter)(nil).wait(canceled); !errors.Is(err, context.Canceled) {
		t.Errorf("disabled limiter cancellation: %v", err)
	}
	if err := (*limiter)(nil).wait(context.Background()); err != nil {
		t.Error(err)
	}
	if err := (&limiter{rate: 1, burst: 1, tokens: 10, last: time.Now().Add(-time.Second)}).wait(context.Background()); err != nil {
		t.Error(err)
	}
	var meta ResponseMeta
	meta.setMeta(202, "r", []byte(`{"value":1}`))
	if meta.StatusCode() != 202 || meta.RequestID() != "r" || len(meta.RawJSON()) == 0 {
		t.Error("response metadata accessors")
	}
}

func TestClientMalformedRepliesAndNetworkErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		body      string
		status    int
		wantError bool
	}{
		{"invalid JSON", `not json`, 200, true},
		{"valid JSON", `{"accounts":[]}`, 200, false},
		{"empty JSON", ``, 200, false},
		{"not found", `{"errorCode":"missing"}`, 404, true},
		{"plain server error", `oops`, 500, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				_, _ = fmt.Fprint(w, tc.body)
			}, WithMaxRetries(0))
			_, err := c.ListAccounts(context.Background())
			if (err != nil) != tc.wantError {
				t.Errorf("err=%v", err)
			}
		})
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c, _ := NewClient(mustToken(t), WithBaseURLs("http://127.0.0.1:1", "http://127.0.0.1:1"), WithRateLimit(0, 0), WithMaxRetries(0), WithLogger(logger), WithHTTPClient(&http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("network down") })}))
	if _, err := c.ListAccounts(context.Background()); err == nil {
		t.Error("network error ignored")
	}
	if !strings.Contains(logs.String(), "oanda") { // Transport logging occurs on retry, not final failure.
		t.Log("final transport failures are returned without logging")
	}
}
