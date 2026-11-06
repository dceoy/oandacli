// Package cli implements the oandactl command-line interface.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dceoy/oandactl/oanda"
)

// Version is set at build time with -ldflags "-X github.com/dceoy/oandactl/internal/cli.Version=...".
var Version = "dev"

// Exit codes.
const (
	ExitOK        = 0
	ExitError     = 1 // transport or unexpected error
	ExitUsage     = 2 // invalid command line or arguments
	ExitAPIClient = 3 // OANDA returned 4xx
	ExitAPIServer = 4 // OANDA returned 5xx
	ExitPolicy    = 5 // refused by local safety policy
)

// Environment variables.
const (
	EnvToken         = "OANDA_API_TOKEN"         // #nosec G101 -- variable name, not a credential
	EnvTokenFile     = "OANDA_API_TOKEN_FILE"    // #nosec G101 -- variable name, not a credential
	EnvTokenCommand  = "OANDA_API_TOKEN_COMMAND" // #nosec G101 -- variable name, not a credential
	EnvAccount       = "OANDA_ACCOUNT_ID"
	EnvEnvironment   = "OANDA_ENVIRONMENT"
	EnvAllowLive     = "OANDA_ALLOW_LIVE"
	EnvReadOnly      = "OANDA_READ_ONLY"
	EnvRequirePlan   = "OANDA_REQUIRE_CONFIRM_PLAN"
	EnvPlanTTL       = "OANDA_PLAN_TTL"
	EnvMaxOrderUnits = "OANDA_MAX_ORDER_UNITS"
	EnvAPIURL        = "OANDA_API_URL"
	EnvStreamURL     = "OANDA_STREAM_URL"
)

// tokenCommandTimeout bounds how long OANDA_API_TOKEN_COMMAND may run.
var tokenCommandTimeout = 10 * time.Second

// App is the CLI application. Fields are injectable for tests.
type App struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Getenv func(string) string
	// Pretty forces pretty or compact JSON; nil means auto (pretty on a TTY).
	Pretty *bool
}

// New returns an App bound to the process's stdio and environment.
func New() *App {
	return &App{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.Getenv}
}

// globals are flags accepted by every command.
type globals struct {
	account   string
	env       string
	tokenFile string
	confirm   string
	planTTL   string
	timeout   time.Duration
	pretty    bool
	compact   bool
	dryRun    bool
	allowLive bool
	readOnly  bool
	verbose   bool
}

func (a *App) envBool(name string) bool {
	b, _ := strconv.ParseBool(strings.TrimSpace(a.Getenv(name)))
	return b
}

func (a *App) registerGlobals(fs *flag.FlagSet, g *globals) {
	fs.StringVar(&g.account, "account", a.Getenv(EnvAccount), "Account ID (env "+EnvAccount+")")
	fs.StringVar(&g.env, "env", envOr(a.Getenv(EnvEnvironment), "practice"), "Environment: practice or live (env "+EnvEnvironment+")")
	fs.StringVar(&g.tokenFile, "token-file", a.Getenv(EnvTokenFile), "Read the API token from this file (env "+EnvTokenFile+"); otherwise "+EnvToken+" is used")
	fs.DurationVar(&g.timeout, "timeout", 30*time.Second, "Per-request timeout")
	fs.BoolVar(&g.pretty, "pretty", false, "Pretty-print JSON output")
	fs.BoolVar(&g.compact, "compact", false, "Compact JSON output")
	fs.BoolVar(&g.dryRun, "dry-run", false, "Print the request that would be sent instead of sending it")
	fs.BoolVar(&g.allowLive, "allow-live", a.envBool(EnvAllowLive), "Allow state-changing requests in the live environment (env "+EnvAllowLive+")")
	// The flag can only add protection: OANDA_READ_ONLY=true is enforced in newEnv
	// and cannot be revoked with --read-only=false.
	fs.BoolVar(&g.readOnly, "read-only", false, "Refuse state-changing execution; --dry-run is allowed (env "+EnvReadOnly+"=true always applies)")
	fs.StringVar(&g.confirm, "confirm-plan", "", "Send the state-changing request only if it matches this planHash from --dry-run (env "+EnvRequirePlan+"=true makes it mandatory)")
	fs.StringVar(&g.planTTL, "plan-ttl", envOr(a.Getenv(EnvPlanTTL), planTTL.String()), "Dry-run plan lifetime: 1m to 60m, default 10m (env "+EnvPlanTTL+")")
	fs.BoolVar(&g.verbose, "verbose", false, "Log requests to stderr (tokens are never logged)")
}

func envOr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// Run executes the CLI with args (excluding the program name) and returns the
// process exit code.
func (a *App) Run(ctx context.Context, args []string) int {
	cmds := Commands()
	var path []string
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			break
		}
		path = append(path, arg)
	}
	rest := args[len(path):]

	if len(path) == 0 {
		if len(rest) > 0 && (rest[0] == "--version" || rest[0] == "-version") {
			_, _ = fmt.Fprintln(a.Stdout, Version)
			return ExitOK
		}
		if len(rest) > 0 && rest[0] != "-h" && rest[0] != "--help" && rest[0] != "-help" {
			return a.fail(usagef("flags must follow the command, e.g. 'oandactl account summary %s'", rest[0]))
		}
		a.printUsage(a.Stdout, cmds, nil)
		if len(rest) == 0 {
			return ExitUsage
		}
		return ExitOK
	}
	switch path[0] {
	case "version":
		_, _ = fmt.Fprintln(a.Stdout, Version)
		return ExitOK
	case "help":
		a.printUsage(a.Stdout, cmds, path[1:])
		return ExitOK
	case "schema":
		return a.writeJSON(a.schema(cmds))
	}

	cmd := find(cmds, path)
	if cmd == nil {
		if hasPrefix(cmds, path) {
			a.printUsage(a.Stdout, cmds, path)
			if slices.Contains(rest, "-h") || slices.Contains(rest, "--help") {
				return ExitOK
			}
			return ExitUsage
		}
		return a.fail(usagef("unknown command %q; run 'oandactl help'", strings.Join(path, " ")))
	}

	var g globals
	fs := flag.NewFlagSet(cmd.Name(), flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	a.registerGlobals(fs, &g)
	raw := map[string]*string{}
	bools := map[string]*bool{}
	for _, p := range cmd.Params {
		if p.Type == TBool {
			bools[p.Name] = fs.Bool(p.Name, false, p.Desc)
		} else {
			raw[p.Name] = fs.String(p.Name, "", p.Desc)
		}
	}
	if err := fs.Parse(rest); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			a.printCommandHelp(a.Stdout, cmd, fs)
			return ExitOK
		}
		return a.fail(usagef("%v", err))
	}
	if fs.NArg() > 0 {
		return a.fail(usagef("unexpected argument %q (all parameters are --flags)", fs.Arg(0)))
	}
	ttl, err := time.ParseDuration(g.planTTL)
	if err != nil || ttl < minPlanTTL || ttl > maxPlanTTL {
		return a.fail(usagef("--plan-ttl / %s must be a duration between 1m and 60m", EnvPlanTTL))
	}
	if g.pretty || g.compact {
		p := g.pretty && !g.compact
		a.Pretty = &p
	}

	values := map[string]any{}
	var convErr error
	fs.Visit(func(f *flag.Flag) {
		if convErr != nil {
			return
		}
		for _, p := range cmd.Params {
			if p.Name != f.Name {
				continue
			}
			var v any
			if p.Type == TBool {
				v = *bools[p.Name]
			} else {
				v = *raw[p.Name]
			}
			values[p.Name], convErr = p.convert(v)
		}
	})
	if convErr != nil {
		return a.fail(convErr)
	}
	if path, ok := values["body-file"].(string); ok {
		if _, dup := values["body"]; dup {
			return a.fail(usagef("use either --body or --body-file, not both"))
		}
		b, err := a.readBodyFile(path)
		if err != nil {
			return a.fail(err)
		}
		if values["body"], err = pBody.convert(string(b)); err != nil {
			return a.fail(err)
		}
	}
	args2, err := finalize(cmd, values)
	if err != nil {
		return a.fail(err)
	}

	env, err := a.newEnv(cmd, g)
	if err != nil {
		return a.fail(err)
	}
	if cmd.Stream {
		var buf bytes.Buffer
		env.Emit = func(m json.RawMessage) error {
			buf.Reset()
			err := json.Compact(&buf, m)
			if err == nil {
				buf.WriteByte('\n')
				_, err = a.Stdout.Write(buf.Bytes())
			}
			return err
		}
	}
	res, err := cmd.Run(ctx, env, args2)
	var dry *oanda.DryRunError
	if cmd.Mutating && errors.As(err, &dry) {
		exp := time.Now().Add(ttl).Unix()
		return a.writeJSON(map[string]any{
			"dryRun":        true,
			"request":       dry,
			"planHash":      planHash(env.Client.Environment(), env.AccountID, dry.Method, dry.URL, dry.Body, exp),
			"planExpiresAt": time.Unix(exp, 0).UTC().Format(time.RFC3339),
		})
	}
	if err != nil {
		return a.fail(err)
	}
	if cmd.Stream && res == nil {
		return ExitOK
	}
	return a.writeResult(res)
}

func (a *App) readBodyFile(path string) ([]byte, error) {
	const limit = 1 << 20
	var r io.Reader
	if path == "-" {
		r = a.Stdin
	} else {
		// The path is chosen by the local user running the command.
		f, err := os.Open(filepath.Clean(path)) // #nosec G304 -- user-selected input file
		if err != nil {
			return nil, usagef("cannot read --body-file: %v", err)
		}
		defer func() { _ = f.Close() }()
		r = f
	}
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, usagef("cannot read --body-file: %v", err)
	}
	if len(b) > limit {
		return nil, usagef("--body-file exceeds 1 MiB")
	}
	return b, nil
}

// newEnv builds the API client and applies the safety policy for cmd.
func (a *App) newEnv(cmd *Command, cfg globals) (*Env, error) {
	env := &Env{}
	// Only an empty or unset variable disables the limit; whitespace-only
	// values are rejected so that a typo cannot fail open.
	if raw := a.Getenv(EnvMaxOrderUnits); raw != "" {
		limit, err := parseUnitLimit(strings.TrimSpace(raw))
		if err != nil {
			return nil, err
		}
		env.Policy.MaxOrderUnits = limit
	}
	oenv, err := oanda.ParseEnvironment(cfg.env)
	if err != nil {
		return nil, usagef("%v", err)
	}
	if cmd.Mutating {
		switch {
		case (cfg.readOnly || a.envBool(EnvReadOnly)) && !cfg.dryRun:
			return nil, &PolicyError{Msg: "read-only mode is enabled (--read-only / " + EnvReadOnly + "); state-changing commands are refused"}
		case oenv == oanda.Live && !cfg.allowLive && !cfg.dryRun:
			return nil, &PolicyError{Msg: "refusing a state-changing request in the live environment; pass --allow-live or set " + EnvAllowLive + "=true"}
		case cfg.confirm == "" && !cfg.dryRun && a.envBool(EnvRequirePlan):
			return nil, &PolicyError{Msg: EnvRequirePlan + " is set; preview with --dry-run and pass its planHash with --confirm-plan"}
		}
	} else if cfg.confirm != "" {
		return nil, usagef("--confirm-plan only applies to state-changing commands")
	}
	if cmd.Account {
		env.AccountID = strings.TrimSpace(cfg.account)
		if env.AccountID == "" {
			return nil, usagef("an account ID is required: pass --account or set %s", EnvAccount)
		}
		if err := oanda.ValidateAccountID(env.AccountID); err != nil {
			return nil, usagef("%v", err)
		}
	}
	var extra []oanda.Option
	if cfg.confirm != "" && !cfg.dryRun {
		dir, err := os.UserCacheDir()
		if err != nil {
			return nil, &PolicyError{Msg: "--confirm-plan needs a cache directory to record used plans: " + err.Error()}
		}
		extra = append(extra, oanda.WithMutationCheck(planCheck(cfg.confirm, oenv, env.AccountID, filepath.Join(dir, "oandactl", "plans"), time.Now)))
	}
	c, err := a.newClient(cfg, oenv, !cmd.Mutating, extra...)
	if err != nil {
		return nil, err
	}
	env.Client = c
	return env, nil
}

// newClient builds an API client from the global configuration.
func (a *App) newClient(cfg globals, oenv oanda.Environment, readOnly bool, extra ...oanda.Option) (*oanda.Client, error) {
	token, err := a.loadToken(cfg.tokenFile)
	if err != nil {
		if !cfg.dryRun {
			return nil, err
		}
		token, _ = oanda.NewToken("dry-run-placeholder")
	}
	opts := []oanda.Option{
		oanda.WithEnvironment(oenv),
		oanda.WithBaseURLs(a.Getenv(EnvAPIURL), a.Getenv(EnvStreamURL)),
		oanda.WithTimeout(cfg.timeout),
		oanda.WithUserAgent("oandactl/" + Version),
		oanda.WithConnectionWait(waitForConnection),
		// Defense in depth: the client itself refuses writes for read-only commands.
		oanda.WithReadOnly(readOnly),
		oanda.WithDryRun(cfg.dryRun),
	}
	opts = append(opts, extra...)
	if cfg.timeout <= 0 {
		return nil, usagef("--timeout must be positive")
	}
	if cfg.verbose {
		opts = append(opts, oanda.WithLogger(slog.New(slog.NewTextHandler(a.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))))
	}
	c, err := oanda.NewClient(token, opts...)
	if err != nil {
		return nil, usagef("%v", err)
	}
	return c, nil
}

// loadToken reads the token from a file (preferred), a credential helper
// command or the environment.
// Tokens are never accepted as command-line arguments because those are
// visible to other users via the process list.
func (a *App) loadToken(file string) (oanda.Token, error) {
	if file != "" {
		f, err := os.Open(filepath.Clean(file)) // #nosec G304 -- user-configured token file, permission-checked below
		if err != nil {
			return oanda.Token{}, usagef("cannot open token file: %v", err)
		}
		defer func() { _ = f.Close() }()
		st, err := f.Stat()
		if err != nil || !st.Mode().IsRegular() {
			return oanda.Token{}, usagef("token file must be a regular file")
		}
		if runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0 {
			return oanda.Token{}, &PolicyError{Msg: fmt.Sprintf("token file %s is accessible by other users (mode %04o); run: chmod 600 %s", file, st.Mode().Perm(), file)}
		}
		b, err := io.ReadAll(io.LimitReader(f, 4096))
		if err != nil {
			return oanda.Token{}, usagef("cannot read token file: %v", err)
		}
		t, err := oanda.NewToken(string(b))
		if err != nil {
			return oanda.Token{}, usagef("token file: %v", err)
		}
		return t, nil
	}
	if command := a.Getenv(EnvTokenCommand); command != "" {
		return a.runTokenCommand(command)
	}
	v := a.Getenv(EnvToken)
	if v == "" {
		return oanda.Token{}, usagef("no API token: set %s, %s or %s (or pass --token-file)", EnvToken, EnvTokenFile, EnvTokenCommand)
	}
	t, err := oanda.NewToken(v)
	if err != nil {
		return oanda.Token{}, usagef("%s: %v", EnvToken, err)
	}
	return t, nil
}

// runTokenCommand runs a credential helper without a shell, with a minimal
// environment and a timeout, and uses its stdout as the token. Its output is
// never echoed: failures report only the exit status.
func (a *App) runTokenCommand(command string) (oanda.Token, error) {
	args := strings.Fields(command)
	if len(args) == 0 {
		return oanda.Token{}, usagef("%s is blank", EnvTokenCommand)
	}
	ctx, cancel := context.WithTimeout(context.Background(), tokenCommandTimeout)
	defer cancel()
	// Trusted by design: the command comes only from the invoking user's or
	// runtime's environment, never from repository content, and runs without a shell.
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	c := exec.CommandContext(ctx, args[0], args[1:]...) // #nosec G204 -- trusted credential helper
	c.Env = []string{}
	for _, k := range []string{"PATH", "HOME", "TMPDIR", "SYSTEMROOT"} {
		if v := a.Getenv(k); v != "" {
			c.Env = append(c.Env, k+"="+v)
		}
	}
	// Stdout is not an *os.File, so exec copies it in a goroutine that
	// WaitDelay bounds even if a grandchild keeps the pipe open.
	out := &limitedBuffer{max: 4096, onOverflow: cancel}
	c.Stdout = out
	c.WaitDelay = time.Second
	err := c.Run()
	var exitErr *exec.ExitError
	switch {
	case out.overflow:
		return oanda.Token{}, usagef("%s output exceeds %d bytes", EnvTokenCommand, out.max)
	case errors.Is(err, exec.ErrWaitDelay):
		return oanda.Token{}, usagef("%s exited but left a process holding its stdout open", EnvTokenCommand)
	case ctx.Err() != nil:
		return oanda.Token{}, usagef("%s timed out after %s", EnvTokenCommand, tokenCommandTimeout)
	case errors.As(err, &exitErr):
		return oanda.Token{}, usagef("%s failed: %s", EnvTokenCommand, exitErr.ProcessState)
	case err != nil:
		return oanda.Token{}, usagef("cannot run %s: %v", EnvTokenCommand, err)
	}
	t, err := oanda.NewToken(out.buf.String())
	if err != nil {
		return oanda.Token{}, usagef("%s: %v", EnvTokenCommand, err)
	}
	return t, nil
}

// limitedBuffer collects up to max bytes and calls onOverflow once more is
// written. It must not expose ReadFrom, so that io.Copy goes through Write.
type limitedBuffer struct {
	buf        bytes.Buffer
	max        int
	overflow   bool
	onOverflow func()
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.overflow || b.buf.Len()+len(p) > b.max {
		b.overflow = true
		b.onOverflow()
		return 0, io.ErrShortWrite
	}
	return b.buf.Write(p)
}

func find(cmds []*Command, path []string) *Command {
	for _, c := range cmds {
		if slices.Equal(c.Path, path) {
			return c
		}
	}
	return nil
}

func hasPrefix(cmds []*Command, prefix []string) bool {
	for _, c := range cmds {
		if len(c.Path) > len(prefix) && slices.Equal(c.Path[:len(prefix)], prefix) {
			return true
		}
	}
	return false
}

// rawResult returns the exact JSON OANDA returned when available.
func rawResult(v any) (json.RawMessage, error) {
	if r, ok := v.(interface{ RawJSON() json.RawMessage }); ok {
		if raw := r.RawJSON(); len(raw) > 0 {
			return raw, nil
		}
	}
	if r, ok := v.(json.RawMessage); ok {
		return r, nil
	}
	return json.Marshal(v)
}

func (a *App) pretty() bool {
	if a.Pretty != nil {
		return *a.Pretty
	}
	if f, ok := a.Stdout.(*os.File); ok {
		if st, err := f.Stat(); err == nil {
			return st.Mode()&os.ModeCharDevice != 0
		}
	}
	return false
}

func (a *App) writeResult(v any) int {
	raw, err := rawResult(v)
	if err != nil {
		return a.fail(err)
	}
	return a.writeJSON(raw)
}

func (a *App) writeJSON(v any) int {
	raw, err := rawResult(v)
	if err != nil {
		return a.fail(err)
	}
	var buf bytes.Buffer
	if a.pretty() {
		err = json.Indent(&buf, raw, "", "  ")
	} else {
		err = json.Compact(&buf, raw)
	}
	if err != nil {
		return a.fail(err)
	}
	buf.WriteByte('\n')
	if _, err := a.Stdout.Write(buf.Bytes()); err != nil {
		return ExitError
	}
	return ExitOK
}

// ErrorInfo is the machine-readable error document.
type ErrorInfo struct {
	Kind       string          `json:"kind"`
	Message    string          `json:"message"`
	StatusCode int             `json:"statusCode,omitempty"`
	ErrorCode  string          `json:"errorCode,omitempty"`
	RequestID  string          `json:"requestID,omitempty"`
	Response   json.RawMessage `json:"response,omitempty"`
	ExitCode   int             `json:"exitCode"`
}

// classify maps an error to an ErrorInfo.
func classify(err error) ErrorInfo {
	var (
		apiErr *oanda.APIError
		usage  *UsageError
		policy *PolicyError
	)
	switch {
	case errors.As(err, &apiErr):
		info := ErrorInfo{Kind: "api", Message: err.Error(), StatusCode: apiErr.StatusCode, ErrorCode: apiErr.ErrorCode, RequestID: apiErr.RequestID, Response: apiErr.Body, ExitCode: ExitAPIClient}
		if apiErr.StatusCode >= 500 {
			info.ExitCode = ExitAPIServer
		}
		return info
	case errors.As(err, &usage), errors.Is(err, oanda.ErrInvalidArgument):
		return ErrorInfo{Kind: "usage", Message: err.Error(), ExitCode: ExitUsage}
	case errors.As(err, &policy), errors.Is(err, oanda.ErrReadOnly):
		return ErrorInfo{Kind: "policy", Message: err.Error(), ExitCode: ExitPolicy}
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return ErrorInfo{Kind: "timeout", Message: err.Error(), ExitCode: ExitError}
	}
	return ErrorInfo{Kind: "error", Message: err.Error(), ExitCode: ExitError}
}

// fail reports err on stderr as JSON and returns the exit code. A dry-run
// result is printed to stdout and treated as success.
func (a *App) fail(err error) int {
	var dry *oanda.DryRunError
	if errors.As(err, &dry) {
		return a.writeJSON(map[string]any{"dryRun": true, "request": dry})
	}
	info := classify(err)
	b, _ := json.Marshal(map[string]any{"error": info})
	_, _ = fmt.Fprintln(a.Stderr, string(b))
	return info.ExitCode
}

func (a *App) printUsage(w io.Writer, cmds []*Command, prefix []string) {
	_, _ = fmt.Fprintf(w, "oandactl %s - CLI for the OANDA REST-V20 API\n\nUsage:\n  oandactl <group> <command> [--flags]\n\nCommands:\n", Version)
	for _, c := range cmds {
		if len(prefix) > 0 && (len(c.Path) < len(prefix) || !slices.Equal(c.Path[:len(prefix)], prefix)) {
			continue
		}
		mark := ""
		if c.Mutating {
			mark = " [trading]"
		}
		_, _ = fmt.Fprintf(w, "  %-34s %s%s\n", c.Name(), c.Summary, mark)
	}
	if len(prefix) == 0 {
		_, _ = fmt.Fprint(w, `  schema                             Print a JSON description of all commands
  version                            Print the version

Configuration (environment):
  OANDA_API_TOKEN / OANDA_API_TOKEN_FILE   API token (never passed as a flag)
  OANDA_API_TOKEN_COMMAND                  Credential helper that prints the token
  OANDA_ACCOUNT_ID                         Default account ID
  OANDA_ENVIRONMENT                        practice (default) or live
  OANDA_ALLOW_LIVE=true                    Allow trading in the live environment
  OANDA_READ_ONLY=true                     Refuse state-changing execution (not dry runs)
  OANDA_MAX_ORDER_UNITS                    Reject orders larger than this
  OANDA_REQUIRE_CONFIRM_PLAN=true          Require --confirm-plan for trading
  OANDA_PLAN_TTL                           Dry-run plan lifetime: 1m to 60m (default 10m)

Output is JSON on stdout; errors are JSON on stderr.
Exit codes: 0 ok, 1 error, 2 usage, 3 API 4xx, 4 API 5xx, 5 refused by policy.
Run 'oandactl <group> <command> --help' for flags.
`)
	}
}

func (a *App) printCommandHelp(w io.Writer, cmd *Command, fs *flag.FlagSet) {
	_, _ = fmt.Fprintf(w, "%s\n\nUsage:\n  oandactl %s [--flags]\n\n", cmd.Summary, cmd.Name())
	if cmd.Mutating {
		_, _ = fmt.Fprint(w, "This command changes account state. Execution is refused in read-only mode\nand, in the live environment, unless --allow-live is given. --dry-run is allowed.\n\n")
	}
	if len(cmd.Params) > 0 {
		_, _ = fmt.Fprintln(w, "Flags:")
		for _, p := range cmd.Params {
			req := ""
			if p.Required && p.Default == nil {
				req = " (required)"
			}
			extra := ""
			if len(p.Enum) > 0 {
				extra = " {" + strings.Join(p.Enum, "|") + "}"
			}
			if p.Default != nil {
				extra += fmt.Sprintf(" (default %v)", p.Default)
			}
			_, _ = fmt.Fprintf(w, "  --%-24s %s%s%s\n", p.Name+" "+typeHint(p.Type), p.Desc, extra, req)
		}
		_, _ = fmt.Fprintln(w)
	}
	_, _ = fmt.Fprintln(w, "Global flags:")
	names := []string{}
	fs.VisitAll(func(f *flag.Flag) {
		for _, p := range cmd.Params {
			if p.Name == f.Name {
				return
			}
		}
		names = append(names, f.Name)
	})
	sort.Strings(names)
	for _, n := range names {
		_, _ = fmt.Fprintf(w, "  --%-24s %s\n", n, fs.Lookup(n).Usage)
	}
}

func typeHint(t ParamType) string {
	switch t {
	case TBool:
		return ""
	case TStrings:
		return "a,b"
	case TJSON:
		return "json"
	case TTime:
		return "rfc3339"
	case TInt:
		return "n"
	case TDuration:
		return "dur"
	}
	return "str"
}

// schema describes every command for agents and tooling.
func (a *App) schema(cmds []*Command) any {
	type flag struct {
		Type        ParamType `json:"type"`
		Required    bool      `json:"required"`
		Description string    `json:"description"`
		Enum        []string  `json:"enum,omitempty"`
		Default     any       `json:"default,omitempty"`
	}
	type entry struct {
		Command     string          `json:"command"`
		Usage       string          `json:"usage"`
		Description string          `json:"description"`
		Mutating    bool            `json:"mutating"`
		Stream      bool            `json:"stream"`
		Account     bool            `json:"requiresAccount"`
		Flags       map[string]flag `json:"flags"`
	}
	out := []entry{}
	for _, c := range cmds {
		flags := map[string]flag{}
		for _, p := range c.Params {
			flags["--"+p.Name] = flag{Type: p.Type, Required: p.Required && p.Default == nil, Description: p.Desc, Enum: p.Enum, Default: p.Default}
		}
		out = append(out, entry{Command: c.Name(), Usage: "oandactl " + c.Name() + " [--flags]", Description: c.Summary, Mutating: c.Mutating, Stream: c.Stream, Account: c.Account, Flags: flags})
	}
	return map[string]any{"name": "oandactl", "version": Version, "commands": out}
}
