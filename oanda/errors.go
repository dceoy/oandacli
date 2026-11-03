package oanda

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

var (
	// ErrReadOnly is returned when a state-changing request is attempted on a
	// client created with WithReadOnly(true).
	ErrReadOnly = errors.New("oanda: client is read-only; state-changing request refused")

	// ErrInvalidArgument is matched (via errors.Is) by every *ValidationError.
	ErrInvalidArgument = errors.New("oanda: invalid argument")

	// ErrStopStream can be returned by a stream handler to end a stream
	// cleanly. The stream function then returns nil.
	ErrStopStream = errors.New("oanda: stop stream")

	// ErrStreamStalled is returned when a stream delivers no data (not even
	// heartbeats) for longer than the configured stall timeout.
	ErrStreamStalled = errors.New("oanda: stream stalled (no data or heartbeat received)")
)

// ValidationError reports an argument that failed client-side validation.
// No request is sent when validation fails.
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("oanda: invalid %s: %s", e.Field, e.Reason)
}

// Is makes errors.Is(err, ErrInvalidArgument) true.
func (e *ValidationError) Is(target error) bool { return target == ErrInvalidArgument }

func invalid(field, format string, args ...any) error {
	return &ValidationError{Field: field, Reason: fmt.Sprintf(format, args...)}
}

// APIError is returned when OANDA answers with a non-2xx status code.
type APIError struct {
	// StatusCode is the HTTP status code.
	StatusCode int `json:"statusCode"`
	// ErrorCode is OANDA's machine-readable error code, when present.
	ErrorCode string `json:"errorCode,omitempty"`
	// ErrorMessage is OANDA's human-readable error message, when present.
	ErrorMessage string `json:"errorMessage,omitempty"`
	// RequestID is the value of the RequestID response header.
	RequestID string `json:"requestID,omitempty"`
	// Body is the raw response body. For rejected orders it contains the
	// reject transaction (e.g. orderRejectTransaction).
	Body json.RawMessage `json:"body,omitempty"`
}

func (e *APIError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "oanda: HTTP %d %s", e.StatusCode, http.StatusText(e.StatusCode))
	if e.ErrorCode != "" {
		fmt.Fprintf(&b, " [%s]", e.ErrorCode)
	}
	if e.ErrorMessage != "" {
		fmt.Fprintf(&b, ": %s", e.ErrorMessage)
	}
	if e.RequestID != "" {
		fmt.Fprintf(&b, " (request %s)", e.RequestID)
	}
	return b.String()
}

// Temporary reports whether retrying the same request later may succeed.
func (e *APIError) Temporary() bool { return isRetryableStatus(e.StatusCode) }

func newAPIError(status int, requestID string, body []byte) *APIError {
	e := &APIError{StatusCode: status, RequestID: requestID}
	if json.Valid(body) {
		e.Body = json.RawMessage(body)
		var payload struct {
			ErrorCode    string `json:"errorCode"`
			ErrorMessage string `json:"errorMessage"`
		}
		if json.Unmarshal(body, &payload) == nil {
			e.ErrorCode = payload.ErrorCode
			e.ErrorMessage = payload.ErrorMessage
		}
	} else if len(body) > 0 {
		msg := strings.TrimSpace(string(body))
		const maxLen = 512
		if len(msg) > maxLen {
			msg = msg[:maxLen] + "..."
		}
		e.ErrorMessage = msg
	}
	return e
}

func hasStatus(err error, status int) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == status
}

// IsNotFound reports whether err is an APIError with status 404.
func IsNotFound(err error) bool { return hasStatus(err, http.StatusNotFound) }

// IsUnauthorized reports whether err is an APIError with status 401.
func IsUnauthorized(err error) bool { return hasStatus(err, http.StatusUnauthorized) }

// IsForbidden reports whether err is an APIError with status 403.
func IsForbidden(err error) bool { return hasStatus(err, http.StatusForbidden) }

// IsRateLimited reports whether err is an APIError with status 429.
func IsRateLimited(err error) bool { return hasStatus(err, http.StatusTooManyRequests) }

// DryRunError is returned instead of sending a request when the client was
// created with WithDryRun(true). It describes the request that would have
// been sent. The Authorization header is never included.
type DryRunError struct {
	Method string          `json:"method"`
	URL    string          `json:"url"`
	Body   json.RawMessage `json:"body,omitempty"`
}

func (e *DryRunError) Error() string {
	return fmt.Sprintf("oanda: dry run: %s %s", e.Method, e.URL)
}
