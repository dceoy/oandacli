package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dceoy/oandactl/oanda"
)

// ParamType is the type of a command parameter.
type ParamType string

const (
	TString   ParamType = "string"
	TInt      ParamType = "integer"
	TBool     ParamType = "boolean"
	TStrings  ParamType = "array"
	TJSON     ParamType = "object"
	TDuration ParamType = "duration"
	TTime     ParamType = "time"
)

// Param is a named command parameter, exposed as a --kebab-case CLI flag.
type Param struct {
	Name     string
	Type     ParamType
	Required bool
	Desc     string
	Enum     []string
	Default  any
}

// Command is one leaf command.
type Command struct {
	Path     []string
	Summary  string
	Params   []Param
	Account  bool // requires an account ID
	Mutating bool // changes account state
	Stream   bool // emits NDJSON messages
	Local    bool // does not need an API client
	Run      func(ctx context.Context, e *Env, a *Args) (any, error)
}

// Name returns the space-separated command path.
func (c *Command) Name() string { return strings.Join(c.Path, " ") }

// Env is the execution environment passed to commands.
type Env struct {
	Client    *oanda.Client
	AccountID string
	Policy    Policy
	// Emit writes one stream message.
	Emit func(json.RawMessage) error
}

// Policy holds safety limits applied before requests are sent.
type Policy struct {
	// MaxOrderUnits rejects orders whose absolute units exceed it (nil = off).
	// It is an exact decimal so that no float rounding can weaken the limit.
	MaxOrderUnits *big.Rat
}

// Args holds parsed, typed parameter values.
type Args struct{ v map[string]any }

func (a *Args) Has(name string) bool { _, ok := a.v[name]; return ok }

func (a *Args) Str(name string) string { s, _ := a.v[name].(string); return s }

func (a *Args) Int(name string) int { n, _ := a.v[name].(int); return n }

func (a *Args) Bool(name string) bool { b, _ := a.v[name].(bool); return b }

func (a *Args) List(name string) []string { l, _ := a.v[name].([]string); return l }

func (a *Args) JSON(name string) json.RawMessage { j, _ := a.v[name].(json.RawMessage); return j }

func (a *Args) Duration(name string) time.Duration { d, _ := a.v[name].(time.Duration); return d }

func (a *Args) Time(name string) time.Time { t, _ := a.v[name].(time.Time); return t }

// UsageError marks invalid invocations.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

func usagef(format string, args ...any) error { return &UsageError{Msg: fmt.Sprintf(format, args...)} }

// PolicyError marks requests refused by local safety policy.
type PolicyError struct{ Msg string }

func (e *PolicyError) Error() string { return e.Msg }

// convert turns a raw flag value (a string, or a bool for boolean flags)
// into the typed value for p.
func (p Param) convert(raw any) (any, error) {
	fail := func(format string, args ...any) error {
		return usagef("parameter --%s: %s", p.Name, fmt.Sprintf(format, args...))
	}
	if p.Type == TBool {
		b, ok := raw.(bool)
		if !ok {
			return nil, fail("expected true or false")
		}
		return b, nil
	}
	s, ok := raw.(string)
	if !ok {
		return nil, fail("expected a string value")
	}
	var v any
	switch p.Type {
	case TString:
		if len(s) > 4096 {
			return nil, fail("value too long")
		}
		v = s
	case TInt:
		n, err := strconv.Atoi(s)
		if err != nil {
			return nil, fail("expected an integer")
		}
		v = n
	case TStrings:
		out := splitList(s)
		if len(out) > 1000 {
			return nil, fail("too many values")
		}
		for _, e := range out {
			if len(p.Enum) > 0 && !slices.Contains(p.Enum, e) {
				return nil, fail("%q is not one of %s", e, strings.Join(p.Enum, ", "))
			}
		}
		return out, nil
	case TJSON:
		if !json.Valid([]byte(s)) || !strings.HasPrefix(strings.TrimSpace(s), "{") {
			return nil, fail("expected a JSON object")
		}
		v = json.RawMessage(s)
	case TDuration:
		d, err := time.ParseDuration(s)
		if err != nil || d < 0 {
			return nil, fail("expected a duration such as 30s or 5m")
		}
		v = d
	case TTime:
		t, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return nil, fail("expected an RFC 3339 timestamp such as 2024-01-02T15:04:05Z")
		}
		v = t
	default:
		return nil, fail("unsupported type %s", p.Type)
	}
	if str, ok := v.(string); ok && len(p.Enum) > 0 && !slices.Contains(p.Enum, str) {
		return nil, fail("%q is not one of %s", str, strings.Join(p.Enum, ", "))
	}
	return v, nil
}

func splitList(s string) []string {
	var out []string
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// finalize applies defaults and checks required parameters.
func finalize(cmd *Command, v map[string]any) (*Args, error) {
	for _, p := range cmd.Params {
		if _, ok := v[p.Name]; ok {
			continue
		}
		if p.Default != nil {
			conv, err := p.convert(p.Default)
			if err != nil {
				return nil, err
			}
			v[p.Name] = conv
		} else if p.Required {
			return nil, usagef("missing required parameter --%s", p.Name)
		}
	}
	return &Args{v: v}, nil
}
