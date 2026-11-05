package oanda

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
)

// Version is the SDK version reported in the User-Agent header.
var Version = "0.1.0"

// Environment selects an OANDA trading environment.
type Environment string

const (
	// Practice is the fxTrade Practice (demo) environment.
	Practice Environment = "practice"
	// Live is the fxTrade (real money) environment.
	Live Environment = "live"
)

// Base URLs of the OANDA REST-V20 API.
const (
	PracticeAPIURL    = "https://api-fxpractice.oanda.com"
	PracticeStreamURL = "https://stream-fxpractice.oanda.com"
	LiveAPIURL        = "https://api-fxtrade.oanda.com"
	LiveStreamURL     = "https://stream-fxtrade.oanda.com"
)

// ParseEnvironment parses "practice" or "live" (case-insensitive). The aliases
// "demo"/"fxpractice" and "trade"/"fxtrade" are also accepted.
func ParseEnvironment(s string) (Environment, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "practice", "demo", "fxpractice":
		return Practice, nil
	case "live", "trade", "fxtrade":
		return Live, nil
	}
	return "", invalid("environment", "%q is not one of practice, live", s)
}

// Token is an OANDA personal access token. Its value is never revealed by
// String, GoString, Format, MarshalJSON, MarshalText or slog.
type Token struct{ value string }

const redacted = "[REDACTED]"

// NewToken validates and wraps a raw token string.
func NewToken(raw string) (Token, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return Token{}, invalid("token", "empty")
	}
	if len(v) > 1024 {
		return Token{}, invalid("token", "too long")
	}
	for _, r := range v {
		if r > unicode.MaxASCII || !unicode.IsPrint(r) || unicode.IsSpace(r) {
			return Token{}, invalid("token", "contains whitespace, control or non-ASCII characters")
		}
	}
	return Token{value: v}, nil
}

func (t Token) String() string               { return redacted }
func (t Token) GoString() string             { return redacted }
func (t Token) Format(f fmt.State, _ rune)   { _, _ = io.WriteString(f, redacted) }
func (t Token) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }
func (t Token) MarshalText() ([]byte, error) { return []byte(redacted), nil }
func (t Token) LogValue() slog.Value         { return slog.StringValue(redacted) }
func (t Token) IsZero() bool                 { return t.value == "" }
func (t Token) authorizationHeader() string  { return "Bearer " + t.value }

// Client is a client for the OANDA REST-V20 API. It is safe for concurrent use.
type Client struct {
	token            Token
	env              Environment
	apiURL           *url.URL
	streamURL        *url.URL
	httpClient       *http.Client
	streamHTTPClient *http.Client
	userAgent        string
	maxRetries       int
	maxResponseBytes int64
	stallTimeout     time.Duration
	readOnly         bool
	dryRun           bool
	mutationCheck    func(method, url string, body []byte) error
	limiter          *limiter
	logger           *slog.Logger
}

type options struct {
	env              Environment
	apiURL           string
	streamURL        string
	httpClient       *http.Client
	timeout          time.Duration
	userAgent        string
	maxRetries       int
	maxResponseBytes int64
	stallTimeout     time.Duration
	readOnly         bool
	dryRun           bool
	mutationCheck    func(method, url string, body []byte) error
	rps              float64
	burst            int
	logger           *slog.Logger
	connectionWait   func(context.Context) error
}

// Option configures a Client.
type Option func(*options) error

// WithEnvironment selects the practice (default) or live environment.
func WithEnvironment(env Environment) Option {
	return func(o *options) error {
		if env != Practice && env != Live {
			return invalid("environment", "%q is not one of practice, live", env)
		}
		o.env = env
		return nil
	}
}

// WithBaseURLs overrides the REST and streaming base URLs (e.g. for a test
// server or an egress proxy). URLs must use https, except for loopback hosts.
// An empty string keeps the environment default.
func WithBaseURLs(apiURL, streamURL string) Option {
	return func(o *options) error {
		o.apiURL, o.streamURL = apiURL, streamURL
		return nil
	}
}

// WithHTTPClient sets the underlying HTTP client. Its CheckRedirect policy is
// always replaced so that redirects are never followed.
func WithHTTPClient(c *http.Client) Option {
	return func(o *options) error {
		if c == nil {
			return invalid("http client", "nil")
		}
		o.httpClient = c
		return nil
	}
}

// WithConnectionWait calls wait immediately before each new TCP connection on
// the default transport, including stream connections and GET retry reconnects.
// Reused connections and dry runs do not call it. It cannot be combined with
// WithHTTPClient, whose transport and connection lifecycle belong to the caller.
func WithConnectionWait(wait func(context.Context) error) Option {
	return func(o *options) error { o.connectionWait = wait; return nil }
}

// WithTimeout sets the per-request timeout for non-streaming requests
// (default 30s). Streams are bounded by their context instead.
func WithTimeout(d time.Duration) Option {
	return func(o *options) error {
		if d <= 0 {
			return invalid("timeout", "must be positive")
		}
		o.timeout = d
		return nil
	}
}

// WithUserAgent prepends a product token to the User-Agent header.
func WithUserAgent(ua string) Option {
	return func(o *options) error {
		if strings.ContainsAny(ua, "\r\n") {
			return invalid("user agent", "contains newline")
		}
		o.userAgent = ua
		return nil
	}
}

// WithMaxRetries sets how many times idempotent GET requests are retried on
// transport errors, HTTP 429 and HTTP 5xx (default 3). State-changing
// requests are never retried.
func WithMaxRetries(n int) Option {
	return func(o *options) error {
		if n < 0 || n > 10 {
			return invalid("max retries", "must be between 0 and 10")
		}
		o.maxRetries = n
		return nil
	}
}

// WithRateLimit sets the client-side request rate limit (default 100 requests
// per second with a burst of 20; OANDA allows 120 per second). A
// requestsPerSecond of 0 disables client-side limiting.
func WithRateLimit(requestsPerSecond float64, burst int) Option {
	return func(o *options) error {
		if requestsPerSecond < 0 || (requestsPerSecond > 0 && burst < 1) {
			return invalid("rate limit", "requires rps >= 0 and burst >= 1")
		}
		o.rps, o.burst = requestsPerSecond, burst
		return nil
	}
}

// WithMaxResponseBytes caps the size of a non-streaming response body
// (default 32 MiB).
func WithMaxResponseBytes(n int64) Option {
	return func(o *options) error {
		if n <= 0 {
			return invalid("max response bytes", "must be positive")
		}
		o.maxResponseBytes = n
		return nil
	}
}

// WithStreamStallTimeout sets how long a stream may go without receiving any
// line (OANDA sends heartbeats every 5 seconds) before it is aborted with
// ErrStreamStalled (default 30s).
func WithStreamStallTimeout(d time.Duration) Option {
	return func(o *options) error {
		if d <= 0 {
			return invalid("stream stall timeout", "must be positive")
		}
		o.stallTimeout = d
		return nil
	}
}

// WithReadOnly makes the client refuse every non-GET request with ErrReadOnly.
func WithReadOnly(readOnly bool) Option {
	return func(o *options) error { o.readOnly = readOnly; return nil }
}

// WithDryRun makes the client return a *DryRunError describing each request
// instead of sending it.
func WithDryRun(dryRun bool) Option {
	return func(o *options) error { o.dryRun = dryRun; return nil }
}

// WithMutationCheck calls check with the exact method, URL and body of every
// state-changing request before it is sent; a non-nil error aborts the
// request and is returned unchanged. It is not called in dry-run mode.
func WithMutationCheck(check func(method, url string, body []byte) error) Option {
	return func(o *options) error { o.mutationCheck = check; return nil }
}

// WithLogger enables debug logging of requests (method, path, status,
// request ID and duration). Tokens and bodies are never logged.
func WithLogger(l *slog.Logger) Option {
	return func(o *options) error { o.logger = l; return nil }
}

// NewClient creates a client authenticated with token.
func NewClient(token Token, opts ...Option) (*Client, error) {
	if token.IsZero() {
		return nil, invalid("token", "empty")
	}
	o := options{
		env:              Practice,
		timeout:          30 * time.Second,
		maxRetries:       3,
		maxResponseBytes: 32 << 20,
		stallTimeout:     30 * time.Second,
		rps:              100,
		burst:            20,
	}
	for _, opt := range opts {
		if err := opt(&o); err != nil {
			return nil, err
		}
	}
	apiDefault, streamDefault := PracticeAPIURL, PracticeStreamURL
	if o.env == Live {
		apiDefault, streamDefault = LiveAPIURL, LiveStreamURL
	}
	if o.apiURL == "" {
		o.apiURL = apiDefault
	}
	if o.streamURL == "" {
		o.streamURL = streamDefault
	}
	apiURL, err := parseBaseURL("api url", o.apiURL)
	if err != nil {
		return nil, err
	}
	streamURL, err := parseBaseURL("stream url", o.streamURL)
	if err != nil {
		return nil, err
	}

	var base http.Client
	if o.httpClient != nil {
		if o.connectionWait != nil {
			return nil, invalid("connection wait", "cannot be combined with a custom HTTP client")
		}
		base = *o.httpClient
	} else {
		t := newTransport()
		if o.connectionWait != nil {
			t.DialContext = newConnectionDialer(o.connectionWait).DialContext
		}
		base = http.Client{Transport: t}
	}
	base.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	hc := base
	hc.Timeout = o.timeout
	shc := base
	shc.Timeout = 0

	ua := "oanda-go/" + Version
	if o.userAgent != "" {
		ua = o.userAgent + " " + ua
	}
	c := &Client{
		token:            token,
		env:              o.env,
		apiURL:           apiURL,
		streamURL:        streamURL,
		httpClient:       &hc,
		streamHTTPClient: &shc,
		userAgent:        ua,
		maxRetries:       o.maxRetries,
		maxResponseBytes: o.maxResponseBytes,
		stallTimeout:     o.stallTimeout,
		readOnly:         o.readOnly,
		dryRun:           o.dryRun,
		mutationCheck:    o.mutationCheck,
		logger:           o.logger,
	}
	if o.rps > 0 {
		c.limiter = newLimiter(o.rps, o.burst)
	}
	return c, nil
}

// Environment returns the environment the client was created for.
func (c *Client) Environment() Environment { return c.env }

// ReadOnly reports whether the client refuses state-changing requests.
func (c *Client) ReadOnly() bool { return c.readOnly }

// newConnectionDialer preserves the default transport's dial timeouts. ControlContext
// runs after DNS resolution for every address attempt, including fallback attempts.
func newConnectionDialer(wait func(context.Context) error) *net.Dialer {
	return &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		ControlContext: func(ctx context.Context, _, _ string, _ syscall.RawConn) error {
			return wait(ctx)
		},
	}
}

func newTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	t.ForceAttemptHTTP2 = true
	t.MaxIdleConnsPerHost = 16
	t.ResponseHeaderTimeout = 30 * time.Second
	return t
}

func parseBaseURL(field, raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil {
		return nil, invalid(field, "%v", err)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Host == "" {
		return nil, invalid(field, "must be a bare scheme://host[:port][/path] URL")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopback(u.Hostname()) {
			return nil, invalid(field, "plain http is only allowed for loopback hosts")
		}
	default:
		return nil, invalid(field, "scheme must be https")
	}
	return u, nil
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ResponseMeta carries metadata common to every response. It is embedded in
// all response types.
type ResponseMeta struct {
	statusCode int
	requestID  string
	raw        json.RawMessage
}

// StatusCode returns the HTTP status code of the response.
func (m *ResponseMeta) StatusCode() int { return m.statusCode }

// RequestID returns the value of OANDA's RequestID response header.
func (m *ResponseMeta) RequestID() string { return m.requestID }

// RawJSON returns the unmodified JSON response body.
func (m *ResponseMeta) RawJSON() json.RawMessage { return m.raw }

func (m *ResponseMeta) setMeta(status int, requestID string, raw []byte) {
	m.statusCode, m.requestID, m.raw = status, requestID, raw
}

type metaSetter interface {
	setMeta(status int, requestID string, raw []byte)
}

// request describes one API call. path segments must already be escaped.
type request struct {
	method string
	path   string
	query  url.Values
	body   any
}

func (r *request) mutating() bool { return r.method != http.MethodGet && r.method != http.MethodHead }

func (c *Client) buildURL(base *url.URL, r *request) string {
	s := base.String() + r.path
	if len(r.query) > 0 {
		s += "?" + r.query.Encode()
	}
	return s
}

func (c *Client) newHTTPRequest(ctx context.Context, method, u string, payload []byte) (*http.Request, error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", c.token.authorizationHeader())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Datetime-Format", "RFC3339")
	req.Header.Set("User-Agent", c.userAgent)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (c *Client) prepare(r *request, base *url.URL) (string, []byte, error) {
	if c.readOnly && r.mutating() {
		return "", nil, ErrReadOnly
	}
	var payload []byte
	if r.body != nil {
		b, err := json.Marshal(r.body)
		if err != nil {
			return "", nil, fmt.Errorf("oanda: encode request body: %w", err)
		}
		payload = b
	}
	u := c.buildURL(base, r)
	if c.dryRun {
		return "", nil, &DryRunError{Method: r.method, URL: u, Body: payload}
	}
	if c.mutationCheck != nil && r.mutating() {
		if err := c.mutationCheck(r.method, u, payload); err != nil {
			return "", nil, err
		}
	}
	return u, payload, nil
}

// do sends r and decodes a successful JSON response into out.
func (c *Client) do(ctx context.Context, r *request, out any) error {
	u, payload, err := c.prepare(r, c.apiURL)
	if err != nil {
		return err
	}
	attempts := 1
	if !r.mutating() {
		attempts += c.maxRetries
	}
	for attempt := 1; ; attempt++ {
		if err := c.limiter.wait(ctx); err != nil {
			return err
		}
		req, err := c.newHTTPRequest(ctx, r.method, u, payload)
		if err != nil {
			return fmt.Errorf("oanda: build request: %w", err)
		}
		start := time.Now()
		resp, err := c.httpClient.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if attempt < attempts {
				c.log(ctx, "retrying after transport error", r, attempt, 0, "", time.Since(start), err)
				if err := sleep(ctx, backoff(attempt)); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("oanda: %s %s: %w", r.method, r.path, err)
		}
		body, readErr := readLimited(resp.Body, c.maxResponseBytes)
		_ = resp.Body.Close()
		requestID := resp.Header.Get("RequestID")
		c.log(ctx, "request", r, attempt, resp.StatusCode, requestID, time.Since(start), readErr)
		if readErr != nil {
			return fmt.Errorf("oanda: read response: %w", readErr)
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			apiErr := newAPIError(resp.StatusCode, requestID, body)
			if attempt < attempts && isRetryableStatus(resp.StatusCode) {
				wait := backoff(attempt)
				if ra, ok := retryAfter(resp.Header.Get("Retry-After")); ok {
					wait = ra
				}
				if err := sleep(ctx, wait); err != nil {
					return err
				}
				continue
			}
			return apiErr
		}
		if out != nil {
			if len(bytes.TrimSpace(body)) > 0 {
				if err := json.Unmarshal(body, out); err != nil {
					return fmt.Errorf("oanda: decode response: %w", err)
				}
			}
			if m, ok := out.(metaSetter); ok {
				m.setMeta(resp.StatusCode, requestID, body)
			}
		}
		return nil
	}
}

func (c *Client) log(ctx context.Context, msg string, r *request, attempt, status int, requestID string, d time.Duration, err error) {
	if c.logger == nil {
		return
	}
	attrs := []slog.Attr{
		slog.String("method", r.method),
		slog.String("path", r.path),
		slog.Int("attempt", attempt),
		slog.Duration("duration", d),
	}
	if status != 0 {
		attrs = append(attrs, slog.Int("status", status))
	}
	if requestID != "" {
		attrs = append(attrs, slog.String("requestID", requestID))
	}
	if err != nil {
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	c.logger.LogAttrs(ctx, slog.LevelDebug, "oanda: "+msg, attrs...)
}

var errResponseTooLarge = errors.New("response body exceeds size limit")

func readLimited(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errResponseTooLarge
	}
	return b, nil
}

func isRetryableStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

func backoff(attempt int) time.Duration {
	d := 250 * time.Millisecond << (attempt - 1)
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	// Jitter in [d/2, d], drawn from crypto/rand (no math/rand needed).
	half := int64(d / 2)
	n, err := rand.Int(rand.Reader, big.NewInt(half+1))
	if err != nil {
		return d
	}
	return time.Duration(half + n.Int64())
}

func retryAfter(v string) (time.Duration, bool) {
	if v == "" {
		return 0, false
	}
	var d time.Duration
	if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
		d = time.Duration(secs) * time.Second
	} else if t, err := http.ParseTime(v); err == nil {
		d = time.Until(t)
	} else {
		return 0, false
	}
	if d < 0 {
		d = 0
	}
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d, true
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// limiter is a token-bucket rate limiter.
type limiter struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

func newLimiter(rps float64, burst int) *limiter {
	return &limiter{rate: rps, burst: float64(burst), tokens: float64(burst), last: time.Now()}
}

func (l *limiter) wait(ctx context.Context) error {
	if l == nil {
		return ctx.Err()
	}
	l.mu.Lock()
	now := time.Now()
	l.tokens += now.Sub(l.last).Seconds() * l.rate
	if l.tokens > l.burst {
		l.tokens = l.burst
	}
	l.last = now
	l.tokens--
	var wait time.Duration
	if l.tokens < 0 {
		wait = time.Duration(-l.tokens / l.rate * float64(time.Second))
	}
	l.mu.Unlock()
	if wait == 0 {
		return ctx.Err()
	}
	return sleep(ctx, wait)
}
