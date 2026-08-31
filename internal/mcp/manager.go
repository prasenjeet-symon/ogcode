// Package mcp connects ogcode to external Model Context Protocol servers
// declared in the project's ogcode.json under the "mcp" key. The Manager owns
// every connection's lifecycle — establishing them in parallel at startup,
// discovering the tools each server exposes, adapting those tools into
// ogcode's tool.ToolDef, and tearing down the subprocesses and HTTP sessions
// on Close.
package mcp

import (
	"context"
	"fmt"
	"hash/fnv"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prasenjeet-symon/ogcode/internal/config"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// clientImpl is the implementation string sent to MCP servers to identify
// ogcode. It is fixed for the process lifetime so a server's logs can
// attribute connections.
var clientImpl = &mcp.Implementation{
	Name:    "ogcode",
	Title:   "ogcode",
	Version: "1.0",
}

// Manager owns all MCP server connections and the tools discovered from them.
type Manager struct {
	// cfg is the config passed to New; Connect uses it to dial servers. nil
	// when New was given no MCP servers.
	cfg *config.Config

	// tools are all MCP tools, namespaced as "<server>/<tool>", registered with
	// the host's tool.Registry. Guarded by mu because Connect (background) and
	// Tools (agent loop) may run concurrently.
	tools []tool.ToolDef

	// sessions are the live client sessions, one per configured server.
	sessions []*mcp.ClientSession
	// procs are the subprocess servers started for stdio transports; Close
	// terminates them.
	procs []*exec.Cmd

	// receiver serves the localhost OAuth callback for any URL-based server
	// without static headers. It is nil when no server needs OAuth.
	receiver *codeReceiver

	// mu guards tools (and the sessions/procs slices during Connect).
	mu sync.RWMutex
	// connectOnce makes Connect idempotent — only the first call dials servers.
	connectOnce sync.Once
	// closed is set by Close; Connect checks it after each dial so a shutdown
	// racing with a lazy connect does not register a session Close never sees.
	closed bool

	closeOnce sync.Once
	closeErr  error
}

// New builds a Manager for the configured MCP servers but does NOT connect to
// any of them — connection is deferred to Connect. Constructing the Manager is
// cheap: it only scans the config to decide whether the shared OAuth callback
// receiver needs to be bound. If cfg is nil or has no MCP servers, New returns
// a zero-value Manager and no error.
//
// Deferring connection keeps server startup from blocking on MCP servers —
// historically a slow or OAuth-requiring server could hold startup for up to
// authTimeout (5 min), during which no HTTP server was listening and the UI
// could not surface the OAuth prompt. Connect is instead launched after the
// HTTP server is up (server.go) or before the loop runs (run.go).
func New(ctx context.Context, cfg *config.Config) (*Manager, error) {
	m := &Manager{}
	if cfg == nil || len(cfg.MCP) == 0 {
		return m, nil
	}
	m.cfg = cfg

	// One shared localhost OAuth callback receiver, built only if at least one
	// URL-based server without static headers is configured. Binding eagerly
	// (but cheaply, here in the constructor) means the receiver is ready before
	// Connect runs; Connect only holds the dial, not the listener bind.
	var receiver *codeReceiver
	for _, sc := range cfg.MCP {
		if sc.URL != "" && len(sc.Headers) == 0 && (sc.Auth == nil || !sc.Auth.SkipOAuth) {
			r, err := newCodeReceiver()
			if err != nil {
				// A listener failure does not block startup: servers requiring
				// OAuth will fail to connect and contribute no tools, like any
				// other unreachable server.
				break
			}
			receiver = r
			break
		}
	}
	m.receiver = receiver
	return m, nil
}

// Connect dials every configured MCP server in parallel, discovers its tools,
// and adapts them into ogcode tool definitions. It is idempotent: the first
// call performs the connections; subsequent calls are no-ops. A non-nil error
// describes per-server failures (a joined error listing each) but never
// prevents the servers that did connect from exposing their tools — an
// unreachable or OAuth-rejected server simply contributes no tools.
//
// Tools discovered here are appended to m.tools and also returned so the caller
// can register them into the host tool.Registry. Callers that registered tools
// from a previous Connect need not re-register on a later call (there will be
// none, by the idempotency contract).
func (m *Manager) Connect(ctx context.Context) ([]tool.ToolDef, error) {
	connected := false
	m.connectOnce.Do(func() {
		connected = true
	})
	if !connected {
		return nil, nil
	}
	if m.cfg == nil || len(m.cfg.MCP) == 0 {
		return nil, nil
	}

	type result struct {
		name    string
		session *mcp.ClientSession
		tools   []*mcp.Tool
		proc    *exec.Cmd
		err     error
	}

	results := make(chan result, len(m.cfg.MCP))
	for name, sc := range m.cfg.MCP {
		go func(name string, sc config.MCPServerConfig) {
			res := result{name: name}
			res.session, res.tools, res.proc, res.err = connect(ctx, name, sc, m.receiver)
			results <- res
		}(name, sc)
	}

	var errs []string
	var newTools []tool.ToolDef
	usedIDs := make(map[string]bool)
	m.mu.Lock()
	for i := 0; i < len(m.cfg.MCP); i++ {
		res := <-results
		if res.err != nil {
			errs = append(errs, fmt.Sprintf("mcp server %q: %v", res.name, res.err))
			continue
		}
		// Close raced in: tear down this freshly-dialled session rather than
		// registering it where Close would never see it.
		if m.closed {
			_ = res.session.Close()
			if res.proc != nil {
				killProcessTree(res.proc)
			}
			continue
		}
		m.sessions = append(m.sessions, res.session)
		if res.proc != nil {
			m.procs = append(m.procs, res.proc)
		}
		for _, t := range res.tools {
			// Ids are "mcp_<server>_<tool>", sanitised to the character set
			// providers allow in a function name — see toolID.
			id := uniqueToolID(usedIDs, res.name, t.Name)
			tool := newMCPTool(id, res.name, t, res.session)
			m.tools = append(m.tools, tool)
			newTools = append(newTools, tool)
		}
	}
	m.mu.Unlock()

	if len(errs) > 0 {
		return newTools, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return newTools, nil
}

// Tools returns the adapted MCP tool definitions discovered so far. The slice
// grows as Connect discovers tools; it is empty before Connect runs or when no
// server exposed any tools. Safe to call concurrently with Connect.
func (m *Manager) Tools() []tool.ToolDef {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]tool.ToolDef(nil), m.tools...)
}

// Close terminates every server connection and subprocess exactly once. It is
// safe to call from multiple goroutines (e.g. a deferred Close racing with a
// shutdown handler). It also signals any in-flight Connect that it should not
// register further sessions, so a shutdown racing with a lazy connect cannot
// leak a freshly-dialled session that Close then misses. Calls to
// ListTools/CallTool on already-closed sessions return errors from the SDK;
// ogcode treats any post-Close tool result as a failure.
func (m *Manager) Close() error {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		sessions := m.sessions
		procs := m.procs
		receiver := m.receiver
		m.mu.Unlock()

		var errs []string
		for _, s := range sessions {
			if err := s.Close(); err != nil {
				errs = append(errs, err.Error())
			}
		}
		for _, p := range procs {
			killProcessTree(p)
		}
		receiver.close()
		if len(errs) > 0 {
			m.closeErr = fmt.Errorf("%s", strings.Join(errs, "; "))
		}
	})
	return m.closeErr
}

// connect establishes a single server connection per the transport implied by
// sc, lists its tools (paginating until the server's cursor is exhausted), and
// returns the live session, discovered tools, and any started subprocess.
func connect(ctx context.Context, name string, sc config.MCPServerConfig, receiver *codeReceiver) (*mcp.ClientSession, []*mcp.Tool, *exec.Cmd, error) {
	transport, transportErr := transportFor(ctx, name, sc, receiver)
	if transportErr != nil {
		return nil, nil, nil, fmt.Errorf("transport: %w", transportErr)
	}
	client := mcp.NewClient(clientImpl, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("connect: %w", err)
	}

	var all []*mcp.Tool
	cursor := ""
	for {
		out, err := session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			_ = session.Close()
			return nil, nil, nil, fmt.Errorf("list tools: %w", err)
		}
		all = append(all, out.Tools...)
		if out.NextCursor == "" {
			break
		}
		cursor = out.NextCursor
	}

	// Return the command if transportFor started one so Manager.Close can kill
	// it; for HTTP transports proc is nil and Close is handled by session.Close.
	var proc *exec.Cmd
	if t, ok := transport.(*mcp.CommandTransport); ok && t.Command != nil {
		proc = t.Command
	}
	return session, all, proc, nil
}

// transportFor builds the MCP transport for one server config. A forced
// Transport takes precedence; otherwise stdio is inferred from Command and
// streamable-http from URL. The server name and OAuth receiver are passed
// through so URL-based servers without static headers can attach an OAuth
// handler keyed to the per-server token store.
func transportFor(ctx context.Context, name string, sc config.MCPServerConfig, receiver *codeReceiver) (mcp.Transport, error) {
	switch strings.ToLower(sc.Transport) {
	case "stdio":
		if sc.Command == "" {
			return nil, fmt.Errorf("transport stdio requires a command")
		}
		return stdioTransport(sc), nil
	case "streamable-http", "http", "https":
		if sc.URL == "" {
			return nil, fmt.Errorf("transport %q requires a url", sc.Transport)
		}
		return httpTransport(ctx, name, sc, receiver)
	case "sse":
		if sc.URL == "" {
			return nil, fmt.Errorf("transport sse requires a url")
		}
		return sseTransport(sc), nil
	case "":
		if sc.Command != "" {
			return stdioTransport(sc), nil
		}
		if sc.URL != "" {
			return httpTransport(ctx, name, sc, receiver)
		}
		return nil, fmt.Errorf("neither command nor url set")
	default:
		return nil, fmt.Errorf("unknown transport %q", sc.Transport)
	}
}

// stdioTransport launches the server as a subprocess with an augmented
// environment and wraps it in an mcp.CommandTransport.
func stdioTransport(sc config.MCPServerConfig) *mcp.CommandTransport {
	cmd := exec.Command(sc.Command, sc.Args...)
	// Env augments the parent environment rather than replacing it — servers
	// commonly inherit PATH, HOME, etc.
	env := os.Environ()
	for k, v := range sc.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env
	return &mcp.CommandTransport{Command: cmd}
}

// httpTransport builds a streamable HTTP transport. When the server has static
// Headers, they are injected via a custom RoundTripper so the SDK's HTTP client
// carries them on every request (the existing static-token path). When it has
// no Headers, an OAuth handler is attached (see maybeOAuthHandler) so the first
// 401 triggers the authorization-code flow instead of failing outright.
// DisableStandaloneSSE is set so the client skips the optional persistent SSE
// stream and only reads responses to its own POSTs; servers that require the
// legacy SSE transport are reached via the explicit "sse" transport instead.
func httpTransport(ctx context.Context, name string, sc config.MCPServerConfig, receiver *codeReceiver) (*mcp.StreamableClientTransport, error) {
	client := &http.Client{}
	var handler auth.OAuthHandler
	if len(sc.Headers) > 0 {
		client.Transport = headerRoundTripper{headers: sc.Headers}
	} else {
		h, err := maybeOAuthHandler(ctx, name, sc, receiver)
		if err != nil {
			return nil, fmt.Errorf("oauth: %w", err)
		}
		handler = h
	}
	return &mcp.StreamableClientTransport{
		Endpoint:             sc.URL,
		HTTPClient:           client,
		DisableStandaloneSSE: true,
		OAuthHandler:         handler,
	}, nil
}

// sseTransport builds the legacy SSE transport for servers that require it.
func sseTransport(sc config.MCPServerConfig) *mcp.SSEClientTransport {
	client := &http.Client{}
	if len(sc.Headers) > 0 {
		client.Transport = headerRoundTripper{headers: sc.Headers}
	}
	return &mcp.SSEClientTransport{
		Endpoint:   sc.URL,
		HTTPClient: client,
	}
}

// headerRoundTripper injects static headers into every outbound request.
type headerRoundTripper struct {
	headers map[string]string
}

func (h headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	for k, v := range h.headers {
		clone.Header.Set(k, v)
	}
	return http.DefaultTransport.RoundTrip(clone)
}

// killProcessTree terminates the subprocess and any descendants it spawned.
// SIGTERM is sent first to allow graceful shutdown; after a short grace period
// any still-living process is SIGKILLed. On Windows the semantics differ but
// os/exec does not expose a process-group kill, so we fall back to Kill.
func killProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	// Best-effort graceful stop: closing stdin signals many stdio servers.
	if cmd.Stdin != nil {
		if c, ok := cmd.Stdin.(interface{ Close() error }); ok {
			_ = c.Close()
		}
	}
	_ = cmd.Process.Signal(os.Interrupt)
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}

// maxToolNameLen is the function-name length cap the Anthropic and OpenAI APIs
// both enforce. An over-long name is rejected exactly like an invalid one.
const maxToolNameLen = 64

// providerSafeName maps a string into the character set every major provider
// accepts in a function name — a-z, A-Z, 0-9, underscore, dash — replacing
// anything else with an underscore.
//
// MCP puts no such restriction on the names a server may expose, and neither
// did ogcode: tool ids were built as "mcp_<server>/<tool>", so every MCP tool
// carried a slash. Providers that validate the schema reject the whole request
// over it, naming a single offending function and nothing else — which surfaced
// as an unexplained 400 on a first "hello", since the tool list is sent with the
// very first message and one bad name invalidates the entire request.
func providerSafeName(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// toolID builds the provider-safe id for one discovered MCP tool. The "mcp_"
// prefix is what makes the id match the "mcp_*" glob in the coding agent's
// toolset — without it the tool silently never reaches the agent.
func toolID(server, name string) string {
	id := "mcp_" + providerSafeName(server) + "_" + providerSafeName(name)
	if len(id) <= maxToolNameLen {
		return id
	}
	// Truncating alone would collide two long names sharing a prefix, so the
	// tail carries a hash of the full id to keep them distinct.
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	suffix := "_" + strconv.FormatUint(uint64(h.Sum32()), 16)
	return id[:maxToolNameLen-len(suffix)] + suffix
}

// uniqueToolID returns toolID's result, disambiguated if some earlier tool in
// this Connect already claimed it. Sanitising is many-to-one — a dot and an
// underscore in the same position now map to the same character — and the host
// registry is a map keyed by id, so a collision would silently drop a tool
// rather than surface as an error.
func uniqueToolID(used map[string]bool, server, name string) string {
	id := toolID(server, name)
	for n := 2; used[id]; n++ {
		suffix := "_" + strconv.Itoa(n)
		base := toolID(server, name)
		if len(base)+len(suffix) > maxToolNameLen {
			base = base[:maxToolNameLen-len(suffix)]
		}
		id = base + suffix
	}
	used[id] = true
	return id
}
