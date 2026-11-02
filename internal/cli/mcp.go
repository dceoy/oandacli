package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/dceoy/oandacli/oanda"
)

const (
	mcpDefaultProtocol   = "2025-06-18"
	mcpMaxStreamMessages = 1000
	mcpDefaultMessages   = 10
	mcpMaxStreamDuration = 2 * time.Minute
	mcpDefaultDuration   = 10 * time.Second
	mcpMaxLine           = 4 << 20
)

var mcpProtocols = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// mcpExtraParams are the MCP-only arguments added to a command's tool.
func mcpExtraParams(c *Command) []Param {
	var ps []Param
	if c.Account {
		ps = append(ps, Param{Name: "account", Type: TString, Desc: "Account ID (defaults to the server's configured account)"})
	}
	if c.Mutating {
		ps = append(ps, Param{Name: "dry-run", Type: TBool, Desc: "Return the request that would be sent without sending it"})
	}
	return ps
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type mcpServer struct {
	app   *App
	cfg   runConfig
	tools map[string]*Command
	order []*Command

	mu       sync.Mutex // guards out, writeErr and pending
	out      *bufio.Writer
	writeErr error
	pending  map[string]context.CancelFunc
	wg       sync.WaitGroup
}

func (a *App) runMCP(ctx context.Context, cmds []*Command, args []string) int {
	var g globals
	var allowTrading bool
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	a.registerGlobals(fs, &g)
	fs.BoolVar(&allowTrading, "allow-trading", false, "Expose state-changing tools (orders, closes, configuration)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprint(a.Stdout, "Run an MCP server on stdio exposing OANDA tools.\n\nUsage:\n  oandacli mcp [--allow-trading] [--account ID] [--env practice|live] [--allow-live] [--read-only]\n\nBy default only read-only tools are exposed.\n")
			return ExitOK
		}
		return a.fail(usagef("%v", err))
	}
	if fs.NArg() > 0 {
		return a.fail(usagef("unexpected argument %q", fs.Arg(0)))
	}
	cfg := runConfig{globals: g}
	if !allowTrading {
		cfg.denyMutations = "trading tools are disabled; restart the MCP server with --allow-trading"
	}
	// Fail fast on configuration errors (missing token, bad environment).
	if _, err := a.newEnv(&Command{Path: []string{"probe"}, Local: true}, cfg); err != nil {
		return a.fail(err)
	}
	oenv, err := oanda.ParseEnvironment(g.env)
	if err != nil {
		return a.fail(usagef("%v", err))
	}
	// One client for the whole session: tool calls share its connection pool
	// and rate limiter (OANDA allows 2 new connections/s and 120 requests/s).
	// Per-call read-only and dry-run settings are applied with Client.Derive.
	base, err := a.newClient(cfg, oenv, true)
	if err != nil {
		return a.fail(err)
	}
	cfg.base = base
	exposeTrading := allowTrading && !g.readOnly && (oenv != oanda.Live || g.allowLive)

	s := &mcpServer{app: a, cfg: cfg, tools: map[string]*Command{}, out: bufio.NewWriter(a.Stdout), pending: map[string]context.CancelFunc{}}
	for _, c := range cmds {
		if c.Mutating && !exposeTrading {
			continue
		}
		s.tools[c.ToolName()] = c
		s.order = append(s.order, c)
	}
	if err := s.serve(ctx, a.Stdin); err != nil {
		return a.fail(err)
	}
	return ExitOK
}

func (s *mcpServer) serve(ctx context.Context, in io.Reader) error {
	// On end of input, let in-flight calls finish and deliver their results.
	// Cancellation of ctx (e.g. SIGINT/SIGTERM) aborts in-flight calls and
	// returns even while the client keeps stdin open without sending.
	defer s.wg.Wait()
	lines := make(chan string)
	scanErr := make(chan error, 1)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(in)
		sc.Buffer(make([]byte, 0, 64<<10), mcpMaxLine)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-ctx.Done():
				return
			}
		}
		scanErr <- sc.Err()
	}()
	for {
		var text string
		select {
		case <-ctx.Done():
			return nil
		case t, ok := <-lines:
			if !ok {
				select {
				case err := <-scanErr:
					if err != nil {
						return err
					}
				default:
					return nil // ctx was cancelled
				}
				s.wg.Wait()
				return s.failed()
			}
			text = t
		}
		if err := s.failed(); err != nil {
			return fmt.Errorf("write to MCP client: %w", err)
		}
		line := strings.TrimSpace(text)
		if line == "" {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil || req.JSONRPC != "2.0" || req.Method == "" {
			s.send(rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: "parse error: expected a JSON-RPC 2.0 request object"}})
			continue
		}
		s.handle(ctx, req)
	}
}

func (s *mcpServer) send(resp rpcResponse) {
	b, err := json.Marshal(resp)
	if err != nil {
		b, _ = json.Marshal(rpcResponse{JSONRPC: "2.0", ID: resp.ID, Error: &rpcError{Code: -32603, Message: "internal error: " + err.Error()}})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writeErr != nil {
		return
	}
	b = append(b, '\n')
	if _, err := s.out.Write(b); err != nil {
		s.writeErr = err
		return
	}
	s.writeErr = s.out.Flush()
}

func (s *mcpServer) failed() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeErr
}

func (s *mcpServer) reply(id json.RawMessage, result any) {
	s.send(rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *mcpServer) replyErr(id json.RawMessage, code int, msg string) {
	s.send(rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
}

func (s *mcpServer) handle(ctx context.Context, req rpcRequest) {
	isNotification := len(req.ID) == 0
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := mcpDefaultProtocol
		for _, v := range mcpProtocols {
			if v == p.ProtocolVersion {
				version = v
			}
		}
		s.reply(req.ID, map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "oandacli", "version": Version},
			"instructions": "Tools for the OANDA REST-V20 trading API. Prices, units and amounts are decimal strings. " +
				"Positive units buy, negative units sell. Tools whose names start with oanda_order_, oanda_trade_close, oanda_trade_orders, " +
				"oanda_position_close or oanda_account_configure change account state; use dry_run=true to preview them.",
		})
	case "ping":
		if !isNotification {
			s.reply(req.ID, map[string]any{})
		}
	case "tools/list":
		tools := make([]map[string]any, 0, len(s.order))
		for _, c := range s.order {
			tools = append(tools, map[string]any{
				"name":        c.ToolName(),
				"title":       "oandacli " + c.Name(),
				"description": toolDescription(c),
				"inputSchema": c.JSONSchema(mcpExtraParams(c)...),
				"annotations": map[string]any{
					"readOnlyHint":    !c.Mutating,
					"destructiveHint": c.Mutating,
					"idempotentHint":  !c.Mutating,
					"openWorldHint":   true,
				},
			})
		}
		s.reply(req.ID, map[string]any{"tools": tools})
	case "tools/call":
		if isNotification {
			return
		}
		key := string(req.ID)
		cctx, cancel := context.WithCancel(ctx)
		s.mu.Lock()
		s.pending[key] = cancel
		s.mu.Unlock()
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() {
				s.mu.Lock()
				delete(s.pending, key)
				s.mu.Unlock()
				cancel()
			}()
			s.call(cctx, req)
		}()
	case "notifications/cancelled":
		var p struct {
			RequestID json.RawMessage `json:"requestId"`
		}
		if json.Unmarshal(req.Params, &p) == nil {
			s.mu.Lock()
			if c, ok := s.pending[string(p.RequestID)]; ok {
				c()
			}
			s.mu.Unlock()
		}
	default:
		if !isNotification {
			s.replyErr(req.ID, -32601, "method not found: "+req.Method)
		}
	}
}

func toolDescription(c *Command) string {
	d := c.Summary + "."
	if c.Mutating {
		d += " CHANGES ACCOUNT STATE (may place or close real trades)."
	}
	if c.Stream {
		d += fmt.Sprintf(" Returns a JSON array of stream messages, bounded by max_messages (default %d, max %d) and duration (default %s, max %s).",
			mcpDefaultMessages, mcpMaxStreamMessages, mcpDefaultDuration, mcpMaxStreamDuration)
	}
	return d
}

func (s *mcpServer) call(ctx context.Context, req rpcRequest) {
	var p struct {
		Name      string                     `json:"name"`
		Arguments map[string]json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		s.replyErr(req.ID, -32602, "invalid params: "+err.Error())
		return
	}
	cmd, ok := s.tools[p.Name]
	if !ok {
		s.replyErr(req.ID, -32602, "unknown tool: "+p.Name)
		return
	}
	res, err := s.invoke(ctx, cmd, p.Arguments)
	if err != nil {
		var dry *oanda.DryRunError
		if errors.As(err, &dry) {
			s.reply(req.ID, textResult(map[string]any{"dryRun": true, "request": dry}, false))
			return
		}
		s.reply(req.ID, textResult(map[string]any{"error": classify(err)}, true))
		return
	}
	s.reply(req.ID, textResult(res, false))
}

func textResult(v any, isError bool) map[string]any {
	raw, err := rawResult(v)
	if err != nil {
		raw, _ = json.Marshal(map[string]any{"error": map[string]string{"kind": "error", "message": err.Error()}})
		isError = true
	}
	return map[string]any{"content": []map[string]any{{"type": "text", "text": string(raw)}}, "isError": isError}
}

func (s *mcpServer) invoke(ctx context.Context, cmd *Command, arguments map[string]json.RawMessage) (any, error) {
	params := map[string]Param{}
	for _, p := range append(cmd.Params, mcpExtraParams(cmd)...) {
		if !p.CLIOnly {
			params[p.snake()] = p
		}
	}
	values := map[string]any{}
	cfg := s.cfg
	for k, raw := range arguments {
		p, ok := params[k]
		if !ok {
			return nil, usagef("unknown argument %q", k)
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, usagef("argument %s: %v", k, err)
		}
		if v == nil {
			continue
		}
		conv, err := p.convert(v)
		if err != nil {
			return nil, err
		}
		switch p.Name {
		case "account":
			cfg.account = conv.(string)
		case "dry-run":
			// A per-call dry_run can only enable previewing; it never
			// overrides a server started with --dry-run.
			cfg.dryRun = s.cfg.dryRun || conv.(bool)
		default:
			values[p.Name] = conv
		}
	}
	args, err := finalize(cmd, values, false)
	if err != nil {
		return nil, err
	}
	env, err := s.app.newEnv(cmd, cfg)
	if err != nil {
		return nil, err
	}
	if !cmd.Stream {
		return cmd.Run(ctx, env, args)
	}
	// Streams are always bounded over MCP.
	n := args.Int("max-messages")
	if n <= 0 {
		n = mcpDefaultMessages
	}
	args.v["max-messages"] = min(n, mcpMaxStreamMessages)
	d := args.Duration("duration")
	if d <= 0 {
		d = mcpDefaultDuration
	}
	args.v["duration"] = min(d, mcpMaxStreamDuration)
	msgs := []json.RawMessage{}
	env.Emit = func(m json.RawMessage) error {
		msgs = append(msgs, m)
		return nil
	}
	if _, err := cmd.Run(ctx, env, args); err != nil && len(msgs) == 0 {
		return nil, err
	}
	return map[string]any{"messages": msgs, "count": len(msgs)}, nil
}
