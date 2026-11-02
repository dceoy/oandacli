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

	"github.com/dceoy/oandacli/oanda"
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

// Param is a named command parameter. Parameters are exposed as --kebab-case
// CLI flags and as snake_case MCP tool arguments.
type Param struct {
	Name     string
	Type     ParamType
	Required bool
	Desc     string
	Enum     []string
	Default  any
	// CLIOnly params (e.g. reading local files) are not exposed over MCP.
	CLIOnly bool
}

func (p Param) snake() string { return strings.ReplaceAll(p.Name, "-", "_") }

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

// ToolName returns the MCP tool name.
func (c *Command) ToolName() string {
	return "oanda_" + strings.ReplaceAll(strings.Join(c.Path, "_"), "-", "_")
}

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

// convert turns a raw value (string from a flag, or a JSON value from MCP)
// into the typed value for p.
func (p Param) convert(raw any) (any, error) {
	fail := func(format string, args ...any) error {
		return usagef("parameter %s: %s", p.Name, fmt.Sprintf(format, args...))
	}
	var v any
	switch p.Type {
	case TString:
		s, ok := raw.(string)
		if !ok {
			return nil, fail("expected a string")
		}
		if len(s) > 4096 {
			return nil, fail("value too long")
		}
		v = s
	case TInt:
		switch x := raw.(type) {
		case string:
			n, err := strconv.Atoi(x)
			if err != nil {
				return nil, fail("expected an integer")
			}
			v = n
		case float64:
			if x != float64(int(x)) {
				return nil, fail("expected an integer")
			}
			v = int(x)
		default:
			return nil, fail("expected an integer")
		}
	case TBool:
		switch x := raw.(type) {
		case bool:
			v = x
		case string:
			b, err := strconv.ParseBool(x)
			if err != nil {
				return nil, fail("expected true or false")
			}
			v = b
		default:
			return nil, fail("expected a boolean")
		}
	case TStrings:
		var out []string
		switch x := raw.(type) {
		case string:
			out = splitList(x)
		case []string:
			for _, s := range x {
				out = append(out, splitList(s)...)
			}
		case []any:
			for _, e := range x {
				s, ok := e.(string)
				if !ok {
					return nil, fail("expected an array of strings")
				}
				out = append(out, splitList(s)...)
			}
		default:
			return nil, fail("expected a list of strings")
		}
		if len(out) > 1000 {
			return nil, fail("too many values")
		}
		for _, s := range out {
			if len(p.Enum) > 0 && !slices.Contains(p.Enum, s) {
				return nil, fail("%q is not one of %s", s, strings.Join(p.Enum, ", "))
			}
		}
		return out, nil
	case TJSON:
		var b []byte
		if s, ok := raw.(string); ok {
			b = []byte(s)
		} else {
			var err error
			if b, err = json.Marshal(raw); err != nil {
				return nil, fail("%v", err)
			}
		}
		if !json.Valid(b) || !strings.HasPrefix(strings.TrimSpace(string(b)), "{") {
			return nil, fail("expected a JSON object")
		}
		v = json.RawMessage(b)
	case TDuration:
		s, ok := raw.(string)
		if !ok {
			if f, isNum := raw.(float64); isNum {
				s = strconv.FormatFloat(f, 'f', -1, 64) + "s"
			} else {
				return nil, fail("expected a duration such as 30s")
			}
		}
		d, err := time.ParseDuration(s)
		if err != nil || d < 0 {
			return nil, fail("expected a duration such as 30s or 5m")
		}
		v = d
	case TTime:
		s, ok := raw.(string)
		if !ok {
			return nil, fail("expected an RFC 3339 timestamp")
		}
		t, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return nil, fail("expected an RFC 3339 timestamp such as 2024-01-02T15:04:05Z")
		}
		v = t
	default:
		return nil, fail("unsupported type %s", p.Type)
	}
	if s, ok := v.(string); ok && len(p.Enum) > 0 && !slices.Contains(p.Enum, s) {
		return nil, fail("%q is not one of %s", s, strings.Join(p.Enum, ", "))
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
func finalize(cmd *Command, v map[string]any, includeCLIOnly bool) (*Args, error) {
	for _, p := range cmd.Params {
		if p.CLIOnly && !includeCLIOnly {
			continue
		}
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

// JSONSchema returns the JSON Schema of the command's MCP input.
func (c *Command) JSONSchema(extra ...Param) map[string]any {
	props := map[string]any{}
	required := []string{}
	for _, p := range append(slices.Clone(c.Params), extra...) {
		if p.CLIOnly {
			continue
		}
		s := map[string]any{"description": p.Desc}
		switch p.Type {
		case TStrings:
			item := map[string]any{"type": "string"}
			if len(p.Enum) > 0 {
				item["enum"] = p.Enum
			}
			s["type"] = "array"
			s["items"] = item
		case TJSON:
			s["type"] = "object"
		case TDuration:
			s["type"] = "string"
			s["description"] = p.Desc + " (Go duration, e.g. 30s, 5m)"
		case TTime:
			s["type"] = "string"
			s["format"] = "date-time"
		default:
			s["type"] = string(p.Type)
		}
		if len(p.Enum) > 0 && p.Type != TStrings {
			s["enum"] = p.Enum
		}
		if p.Default != nil {
			s["default"] = p.Default
		}
		props[p.snake()] = s
		if p.Required && p.Default == nil {
			required = append(required, p.snake())
		}
	}
	schema := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}
