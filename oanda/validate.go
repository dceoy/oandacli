package oanda

import (
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

var (
	reAccountID     = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z-]{0,63}$`)
	reInstrument    = regexp.MustCompile(`^[A-Z0-9]{1,20}_[A-Z0-9]{1,20}$`)
	reTransactionID = regexp.MustCompile(`^[0-9]{1,20}$`)
	reSpecifier     = regexp.MustCompile(`^(?:[0-9]{1,20}|@[A-Za-z0-9_.:~+=-]{1,128})$`)
	reDecimal       = regexp.MustCompile(`^-?[0-9]{1,20}(?:\.[0-9]{1,20})?$`)
	reTxType        = regexp.MustCompile(`^[A-Z_]{1,64}$`)
	reCandleSpec    = regexp.MustCompile(`^[A-Z0-9]{1,20}_[A-Z0-9]{1,20}:[A-Z0-9]{1,3}:[MBA]{1,3}$`)
)

// ValidateAccountID checks that id is a plausible OANDA account ID
// (e.g. "101-004-1234567-001").
func ValidateAccountID(id string) error {
	if !reAccountID.MatchString(id) {
		return invalid("account ID", "%q is not a valid account ID", id)
	}
	return nil
}

// ValidateInstrument checks that name is an instrument name such as "EUR_USD".
func ValidateInstrument(name string) error {
	if !reInstrument.MatchString(name) {
		return invalid("instrument", "%q is not a valid instrument name (expected e.g. EUR_USD)", name)
	}
	return nil
}

// ValidateTransactionID checks that id is a numeric transaction ID.
func ValidateTransactionID(id string) error {
	if !reTransactionID.MatchString(id) {
		return invalid("transaction ID", "%q is not a numeric transaction ID", id)
	}
	return nil
}

// ValidateSpecifier checks an order or trade specifier: either a numeric ID or
// "@" followed by a client ID.
func ValidateSpecifier(field, spec string) error {
	if !reSpecifier.MatchString(spec) {
		return invalid(field, "%q must be a numeric ID or @clientID", spec)
	}
	return nil
}

// ValidateDecimal checks that s is a plain decimal number such as "1.2345".
func ValidateDecimal(field, s string) error {
	if !reDecimal.MatchString(s) {
		return invalid(field, "%q is not a decimal number", s)
	}
	return nil
}

// ValidateUnits checks that s is a non-zero decimal number of units.
func ValidateUnits(field, s string) error {
	if err := ValidateDecimal(field, s); err != nil {
		return err
	}
	if f, _ := strconv.ParseFloat(s, 64); f == 0 {
		return invalid(field, "must not be zero")
	}
	return nil
}

func validatePositiveDecimal(field, s string) error {
	if err := ValidateDecimal(field, s); err != nil {
		return err
	}
	if f, _ := strconv.ParseFloat(s, 64); f <= 0 {
		return invalid(field, "must be greater than zero")
	}
	return nil
}

func validateEnum[T ~string](field string, v T, allowed []T) error {
	if v == "" || slices.Contains(allowed, v) {
		return nil
	}
	names := make([]string, len(allowed))
	for i, a := range allowed {
		names[i] = string(a)
	}
	return invalid(field, "%q is not one of %s", v, strings.Join(names, ", "))
}

func validateCount(field string, n, max int) error {
	if n < 0 || n > max {
		return invalid(field, "must be between 1 and %d", max)
	}
	return nil
}

func validateInstruments(list []string, required bool) error {
	if len(list) == 0 && required {
		return invalid("instruments", "at least one instrument is required")
	}
	if len(list) > 100 {
		return invalid("instruments", "at most 100 instruments may be requested")
	}
	for _, s := range list {
		if err := ValidateInstrument(s); err != nil {
			return err
		}
	}
	return nil
}

func validateTransactionTypes(list []TransactionFilter) error {
	for _, t := range list {
		if !reTxType.MatchString(string(t)) {
			return invalid("transaction type filter", "%q is not a valid filter", t)
		}
	}
	return nil
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// seg escapes one path segment.
func seg(s string) string { return url.PathEscape(s) }

func accountPath(accountID string, rest ...string) (string, error) {
	if err := ValidateAccountID(accountID); err != nil {
		return "", err
	}
	p := "/v3/accounts/" + seg(accountID)
	for _, r := range rest {
		p += "/" + r
	}
	return p, nil
}

func joinStrings[T ~string](list []T) string {
	s := make([]string, len(list))
	for i, v := range list {
		s[i] = string(v)
	}
	return strings.Join(s, ",")
}
