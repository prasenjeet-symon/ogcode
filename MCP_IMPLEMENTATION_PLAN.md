# MCP Support — Implementation Plan for ogcode

> Research base: OpenCode (`github.com/anomalyco/opencode`, TypeScript) cloned to `/tmp/opencode-src`, plus the official Go MCP SDK (`github.com/modelcontextprotocol/go-sdk`) cloned to `/tmp/go-sdk`. This plan adapts OpenCode's proven architecture to ogcode's existing Go tool system.

---

## ⚠️ Review Notes (2026-08-26)

Re-verified against the codebase and the actual Go MCP SDK **v1.7.0**. The plan is well-researched and fundamentally sound, but see the caveats below. Inline `> **⚠️ REVIEW**` annotations are placed at each affected location.

### Provenance caveat

The header above says this plan was "verified from the cloned source" at `/tmp/go-sdk` and `/tmp/opencode-src`, but **neither clone exists on this machine anymore** (`/tmp` was cleaned). The SDK-level claims were made against source that is no longer inspectable, and a few of them drift from what the current SDK actually does. They were re-fetched against SDK `v1.7.0` and re-checked fresh.

### Concrete bugs (inline where they occur)

1. **`ImageContent.Data` type mismatch** — the Phase 3 adapter sketch won't compile. See annotation in Phase 3.
2. **`NewInMemoryTransports` is plural, not singular** — the testing snippet is wrong. See annotation in §5 Testing.
3. **`InputSchema` is `any`, not `json.RawMessage`** — `Parameters()` needs a `json.Marshal` round-trip. See annotation in Phase 3.
4. **`Servers` map merge is new code, not a "mirror" of Skills** — see annotation in Phase 1.
5. **Missing CLI shutdown path** — stdio servers launched in CLI mode are orphaned on exit. See annotations in Phase 2 & §4.

### Omissions worth flagging

6. **Standalone-SSE for remote** — `StreamableClientTransport` opens a persistent standalone SSE GET stream (for server-initiated notifications) held for the whole session. For a long-running ogcode server with many remote MCP connections that's one held-open connection per remote server, indefinitely. The plan makes no deliberate choice here — consider `DisableStandaloneSSE` and reconnect/`MaxRetries` behavior rather than inheriting the default.
7. **`CommandTransport.Env` / `Dir`** — the SDK runs your `*exec.Cmd` **as-is**. If `cfg.Env` is meant to *augment* (not replace) the parent environment, you must set `cmd.Env = append(os.Environ(), kv...)` yourself. The plan states this correctly in Phase 2; just don't invert it.

### Verified as solid

- The core claim — `ToolDef` / `Registry` / `ForAgent` / `capToolOutput` are transport-agnostic and need **no interface change** — is accurate (`tool.go:104`, `loop.go:2121`).
- `capToolOutput`'s "future or MCP-style tool" comment genuinely exists (`loop.go:2117`), so the "set `Truncated=true` to avoid double-truncate" advice (Phase 3, step 6) is right.
- `codingAgentTools` is shared by Build and Task agents (`agent.go:112,124`), so adding `"mcp_*"` covers both (Phase 4 Option B is sound).
- The prompt-cache-invariant warning and the glob-`ForAgent` sketch are consistent with the actual `ForAgent` (exact-match-only today) and the AGENT.md cache rules.

---

## 1. How OpenCode Handles MCP (Research Summary)

OpenCode's MCP implementation lives in `packages/opencode/src/mcp/` and integrates at `packages/opencode/src/session/tools.ts`. The design is a **client-side service** that discovers external tools at runtime and merges them into the agent's tool palette alongside the built-in tools.

### Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                      OpenCode Agent Loop                      │
│                                                              │
│  session/tools.ts (resolve)                                  │
│  ├── Built-in tools (bash, read, edit, grep, …)             │
│  ├── MCP resource tools (list/read resources)                │
│  └── MCP server tools (converted via catalog.convertTool)   │
│                                                              │
│  mcp/index.ts (Service)  ◄── single stateful manager          │
│  ├── create(key, config)                                     │
│  │   ├── connectLocal → CommandTransport (stdio)            │
│  │   └── connectRemote → StreamableHTTP / SSE fallback       │
│  ├── watch() → onclose, ToolListChanged, logging notifs      │
│  ├── tools() → aggregated {toolName: {def, client, timeout}}│
│  └── close() → kill subprocesses + descendants              │
│                                                              │
│  mcp/catalog.ts          ◄── tool discovery + conversion     │
│  ├── listTools (with pagination)                             │
│  ├── convertTool (MCP def → AI SDK dynamicTool)             │
│  ├── prompts / resources / resourceTemplates                │
│  └── toolName() → "server_tool" sanitised naming             │
│                                                              │
│  mcp/auth.ts             ◄── token storage (~/.mcp-auth.json)│
│  mcp/oauth-provider.ts   ◄── OAuthClientProvider impl         │
│  mcp/browser.ts          ◄── opens browser for OAuth redirect│
└─────────────────────────────────────────────────────────────┘
```

### Key Design Decisions

| Decision | How OpenCode Does It | Rationale |
|---|---|---|
| **Config location** | `mcp` key in `opencode.json` (local + global) | Mirrors existing config structure; per-project + global merge |
| **Server types** | `local` (stdio subprocess) + `remote` (HTTP/SSE) | Covers both self-hosted and hosted MCP servers |
| **Transport fallback** | Remote: tries StreamableHTTP first, falls back to SSE | Maximises compatibility with servers of varying spec vintage |
| **Tool naming** | `{sanitizedServerName}_{sanitizedToolName}` | Avoids collisions; the LLM references tools by this composite name |
| **Tool discovery** | `listTools` with pagination (cursors), tolerant schema parsing | Handles large tool sets + servers with non-standard output schemas |
| **Lifecycle** | Concurrency: all servers connect in parallel at session start | Fast startup; one slow server doesn't block the others |
| **Teardown** | Kills subprocess + descendants (pgrep walk), then closes | Prevents orphaned child processes (e.g. npx-spawned node) |
| **Change notifications** | Subscribes to `ToolListChanged` + re-lists tools live | Hot-reload when an MCP server adds/removes tools at runtime |
| **Permissions** | Glob patterns on tool names; per-agent enable/disable | Fine-grained control; disable noisy servers globally, enable per agent |
| **OAuth** | Full `OAuthClientProvider` impl with dynamic client registration (RFC 7591), PKCE, token persistence, browser redirect | Works with hosted MCP servers (Sentry, GitHub, etc.) without manual token juggling |
| **Timeouts** | Per-server `timeout` (ms), with `resetTimeoutOnProgress` | Long-running tools don't prematurely abort |
| **Output handling** | Text content → output string; images → attachments; resources → text/binary | Full MCP content-type coverage |
| **Truncation** | MCP tool output passes through the same truncation pipeline as built-in tools | Bounds context growth (already anticipated by `capToolOutput` in ogcode's loop.go) |

### Config Schema (OpenCode v1, what ogcode should mirror)

```jsonc
{
  "mcp": {
    "penpot": {
      "type": "remote",
      "url": "https://design.penpot.app/mcp/stream",
      "headers": { "Authorization": "Bearer xxx" },
      "enabled": true,
      "timeout": 10000
    },
    "filesystem": {
      "type": "local",
      "command": ["npx", "-y", "@modelcontextprotocol/server-filesystem", "/tmp"],
      "environment": { "NODE_ENV": "production" },
      "enabled": true,
      "timeout": 5000
    }
  }
}
```

---

## 2. ogcode's Current Tool System (Integration Surface)

The existing system is a clean, closed set of statically-compiled Go tools. MCP fits in with **no changes to the core interfaces** — only additions.

### What exists

| Component | File | Role |
|---|---|---|
| `tool.ToolDef` interface | `internal/tool/tool.go:10-16` | `ID()`, `Description()`, `Parameters()`, `Execute()` — the contract every tool implements |
| `tool.Registry` | `internal/tool/tool.go:79-112` | `map[string]ToolDef`; `Register`, `Get`, `List`, `ForAgent(toolIDs)` |
| Agent definitions | `internal/agent/agent.go` | Each agent has a `Tools []string` allowlist; `ForAgent` filters the registry |
| Tool registration | `internal/server/server.go:168-354` | Static `toolRegistry.Register(...)` calls at startup |
| Config | `internal/config/config.go` | `Config{Providers, Skills}` loaded from `ogcode.json` |
| `capToolOutput` | `internal/agent/loop.go:2114` | Global backstop that already says "any future or MCP-style tool" |
| Per-agent tool filtering | `internal/agent/loop.go:420-442` | `lr.Tools.ForAgent(toolIDs)` → `providerTools` |

### Why it fits

The `ToolDef` interface is transport-agnostic. An MCP-backed tool just needs to implement `Execute(ctx, args) → (Result, error)` that proxies to the remote server. The `Registry` already accepts any `ToolDef`. The `ForAgent` allowlist already filters by tool ID string. The `capToolOutput` backstop already anticipates MCP tools. **No existing interface needs to change.**

---

## 3. Implementation Plan

### Phase 0 — Dependency: Go MCP SDK

Add the official Go SDK (already verified at `github.com/modelcontextprotocol/go-sdk`):

```bash
go get github.com/modelcontextprotocol/go-sdk/mcp
```

Key types we'll use (verified from the cloned source):

```go
import "github.com/modelcontextprotocol/go-sdk/mcp"

// Client creation
client := mcp.NewClient(&mcp.Implementation{Name: "ogcode", Version: version.Version}, nil)

// Transports
stdioT := &mcp.CommandTransport{Command: exec.Command("npx", "-y", "@some/server")}
httpT  := &mcp.StreamableClientTransport{Endpoint: "https://example.com/mcp", HTTPClient: customClient}
sseT   := &mcp.SSEClientTransport{Endpoint: "https://example.com/sse"}

// Connect → session
session, err := client.Connect(ctx, transport, nil)
defer session.Close()

// Discovery (paginated)
result, err := session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
// result.Tools []*mcp.Tool{Name, Description, InputSchema}
// result.NextCursor → next page

// Invocation
callResult, err := session.CallTool(ctx, &mcp.CallToolParams{
    Name:      tool.Name,
    Arguments: args,  // map[string]any
})
// callResult.Content []mcp.Content (TextContent, ImageContent, EmbeddedResource)
// callResult.IsError bool
```

---

### Phase 1 — Config Schema

**File:** `internal/config/config.go`

Add an `MCPServers` field to `Config`:

```go
type Config struct {
    Providers map[string]ProviderConfig `json:"providers,omitempty"`
    Skills    SkillsConfig              `json:"skills,omitempty"`
    MCP       MCPConfig                 `json:"mcp,omitempty"`  // NEW
}

// MCPConfig holds user-configured MCP servers.
type MCPConfig struct {
    Servers map[string]MCPServerConfig `json:"servers,omitempty"`
}

// MCPServerConfig defines one MCP server connection.
type MCPServerConfig struct {
    Type    string            `json:"type"`              // "local" | "remote"
    Command []string          `json:"command,omitempty"` // local: ["npx", "-y", "server"]
    URL     string            `json:"url,omitempty"`     // remote: https://...
    Headers map[string]string  `json:"headers,omitempty"`// remote: auth headers
    Env     map[string]string  `json:"env,omitempty"`    // local: env vars
    Cwd     string            `json:"cwd,omitempty"`     // local: working dir
    Enabled *bool             `json:"enabled,omitempty"` // default true
    Timeout int               `json:"timeout,omitempty"` // ms, default 30000
}
```

Update `projectFileTemplate` to include a commented `mcp` section so users discover the feature.

**Config merge:** `config.Load` already merges global + project-local. MCP servers from both layers merge by name (project-local overrides global for the same name), mirroring how `SkillsConfig` is handled.

> **⚠️ REVIEW #4 — "mirroring how `SkillsConfig` is handled" is slightly off.** `config.Load` has **no existing `Servers`-style map merge**. `SkillsConfig` uses *union* for paths/urls and per-key override for permissions — it does not do per-name override of a keyed map. The semantics described here (per-name override) are correct and are what MCP needs, but the merge loop is **new code that Phase 1 must write** — it isn't a reuse of existing machinery. Don't assume the loop already exists.

---

### Phase 2 — MCP Client Manager

**New package:** `internal/mcp/`

This is the heart — a long-lived manager that owns all MCP server connections, discovers tools, and proxies calls. Modelled on OpenCode's `mcp/index.ts` Service.

#### `internal/mcp/manager.go`

```go
package mcp

// Manager owns the lifecycle of all configured MCP server connections.
type Manager struct {
    servers map[string]*ServerConn  // keyed by config name
    mu      sync.RWMutex
}

// ServerConn wraps one connected MCP server.
type ServerConn struct {
    Name    string
    Config  config.MCPServerConfig
    Client  *mcp.ClientSession
    Tools   []*mcp.Tool          // cached tool list
    Status  Status               // connected | disabled | failed | needs_auth
    Err     error
}

type Status string
const (
    StatusConnected  Status = "connected"
    StatusDisabled   Status = "disabled"
    StatusFailed     Status = "failed"
    StatusNeedsAuth  Status = "needs_auth"
)

// NewManager connects to all configured servers in parallel.
func NewManager(ctx context.Context, cfg config.MCPConfig) (*Manager, error)

// Tools returns all tools from all connected servers, with namespaced names.
func (m *Manager) Tools() []DiscoveredTool

// Close shuts down all server connections and kills subprocesses.
func (m *Manager) Close() error
```

#### Lifecycle (mirrors OpenCode's `create` + `state`):

1. **`NewManager`** reads `config.MCPConfig`, iterates servers **in parallel** (goroutine per server, like OpenCode's `concurrency: "unbounded"`).
2. For each enabled server:
   - **Local:** build `exec.Command(cfg.Command[0], cfg.Command[1:]...)`, set `Env` (merge `os.Environ()` + `cfg.Env`), set `Dir` (resolve `cfg.Cwd` against session dir). Wrap in `&mcp.CommandTransport{Command: cmd}`.
   - **Remote:** build `&mcp.StreamableClientTransport{Endpoint: cfg.URL, HTTPClient: httpClientWithHeaders(cfg.Headers)}`. If connect fails with a transport error (not auth), fall back to `&mcp.SSEClientTransport{Endpoint: cfg.URL}` — same fallback chain as OpenCode.
   - `client.Connect(ctx, transport, nil)` with a timeout (default 30s, configurable per-server).
   - On success: `session.ListTools` with pagination (loop on `NextCursor` until empty — same as OpenCode's `paginate`).
   - Cache tools on `ServerConn.Tools`.
   - On failure: record `Status=failed` + error, do NOT block other servers.
3. **Tool name sanitisation:** `sanitize(serverName) + "_" + sanitize(toolName)` — exactly OpenCode's `catalog.toolName` / `catalog.sanitize`. Strips non-`[a-zA-Z0-9_-]` to `_`.

#### Process teardown (mirrors OpenCode's `descendants` + finalizer):

On `Close()`, for each stdio server: send `session.Close()` (the Go SDK's `CommandTransport` closes stdin → waits `TerminateDuration` (5s) → SIGTERM). Additionally, walk child PIDs with `pgrep -P` (or syscall on macOS/Linux) and kill orphans — the same pattern OpenCode uses, because `npx` spawns `node` spawns the actual server and stdin-close alone may not cascade.

> **⚠️ REVIEW #5 — missing CLI shutdown path (genuine plan bug).** The §4 file-change table wires `Manager.Close()` only into `server.go`'s `Shutdown()`. The CLI run path (`internal/cli/run.go`) and the headless root command get a `NewManager` + register-tools call but **no `Close()`** → stdio MCP servers launched in CLI mode are **orphaned on exit** (this is exactly how you end up leaking `npx`/`node` MCP servers). Add an explicit `defer mcpManager.Close()` in every CLI path that constructs a Manager, not just the server.

> **⚠️ REVIEW #6 — standalone-SSE for remote.** `StreamableClientTransport` by default opens a **persistent standalone SSE GET stream** (for server-initiated notifications) that lives for the whole session. For a long-running ogcode server holding several remote MCP connections, that's one held-open connection per remote server, indefinitely. Make a deliberate choice here: set `DisableStandaloneSSE` if you don't need server-initiated notifications, and review `MaxRetries`/reconnect behavior — don't silently inherit the default.

#### HTTP headers for remote:

The Go SDK's `StreamableClientTransport` takes an `HTTPClient *http.Client`. To inject custom headers (auth), wrap with a `RoundTripper`:

```go
func httpClientWithHeaders(headers map[string]string) *http.Client {
    rt := &headerRoundTripper{base: http.DefaultTransport, headers: headers}
    return &http.Client{Transport: rt}
}
```

---

### Phase 3 — Tool Adapter

**New file:** `internal/mcp/tool.go`

A `tool.ToolDef` implementation that wraps one discovered MCP tool. This is the bridge between the MCP protocol and ogcode's tool system — the Go equivalent of OpenCode's `catalog.convertTool` + `session/tools.ts` wiring.

```go
// mcpTool implements tool.ToolDef for one MCP-discovered tool.
type mcpTool struct {
    id          string  // "serverName_toolName" (sanitised)
    description string
    params      json.RawMessage  // from mcp.Tool.InputSchema
    session     *mcp.ClientSession
    toolName    string  // original MCP tool name
    timeout     time.Duration
}

func (t *mcpTool) ID() string                     { return t.id }
func (t *mcpTool) Description() string            { return t.description }
func (t *mcpTool) Parameters() json.RawMessage    { return t.params }

func (t *mcpTool) Execute(ctx context.Context, args json.RawMessage, tctx tool.Context) (tool.Result, error) {
    // 1. Unmarshal args → map[string]any
    // 2. Apply timeout context
    // 3. session.CallTool(ctx, &mcp.CallToolParams{Name: t.toolName, Arguments: argMap})
    // 4. If result.IsError → return error with text content
    // 5. Convert result.Content → tool.Result:
    //    - TextContent → Result.Output (concatenate, then run through tool.TruncateOutput)
    //    - ImageContent → Result.Image (base64, if ModelSupportsImages)
    //    - EmbeddedResource → extract text/blob → attachments
    // 6. Set Result.Truncated = true so capToolOutput doesn't double-truncate
}
```

> **⚠️ REVIEW #3 — `InputSchema` is `any`, not `json.RawMessage`.** Client-side, `mcp.Tool.InputSchema` is typed **`any`** (SDK docs: "will hold the default JSON marshaling of the server's input schema (a `map[string]any`)"). You **cannot** assign it straight into the `params json.RawMessage` field above. `Parameters()` must `json.Marshal(tool.InputSchema)` back into raw bytes (cache the result at construction time, eagerly or lazily). As sketched, `Parameters()` won't compile/work without that marshal step.

#### Output conversion (mirrors `session/tools.ts:426-462`):

```go
var textParts []string
var images []tool.ResultImage
for _, content := range callResult.Content {
    switch c := content.(type) {
    case *mcp.TextContent:
        textParts = append(textParts, c.Text)
    case *mcp.ImageContent:
        if tctx.ModelSupportsImages {
            images = append(images, tool.ResultImage{MediaType: c.MimeType, Data: c.Data})
        }
```

> **⚠️ REVIEW #1 — `ImageContent` type mismatch (won't compile as written).** `mcp.ImageContent.Data` is **`[]byte`** (base64-bearing bytes), and the media-type field is **`c.MIMEType`** (capital `MIME`), not `c.MimeType`. But `tool.ResultImage.Data` is a **`string`** (base64). So the line above has **two compile errors**. The adapter needs:
> ```go
> images = append(images, tool.ResultImage{
>     MediaType: c.MIMEType,
>     Data:      base64.StdEncoding.EncodeToString(c.Data), // SDK []byte → base64 string
> })
> ```
> (The SDK's `[]byte` carries decoded bytes when unmarshalled; it re-marshals to base64 on the wire, so `EncodeToString` reproduces the payload `ResultImage` expects.)

```go
    case *mcp.EmbeddedResource:
        // extract c.Resource.Text or base64 blob
    }
}
output := strings.Join(textParts, "\n\n")
output, truncated := tool.TruncateOutput(output, tool.KeepHead)
```

---

### Phase 4 — Registration & Agent Wiring

**File:** `internal/server/server.go`

After the existing static tool registrations, add MCP:

```go
// MCP servers: connect to all configured servers and register their tools.
mcpCfg := config.Load(s.dir).MCP
mcpManager, err := mcp.NewManager(context.Background(), mcpCfg)
if err != nil {
    slog.Warn("MCP manager failed; some servers may be unavailable", "err", err)
}
if mcpManager != nil {
    s.mcpManager = mcpManager  // for Close() on shutdown
    for _, dt := range mcpManager.Tools() {
        toolRegistry.Register(dt)
    }
    slog.Info("MCP tools registered", "count", len(mcpManager.Tools()))
}
```

**File:** `internal/server/server.go` — add `mcpManager *mcp.Manager` to the server struct, call `mcpManager.Close()` in `Shutdown()`.

#### Agent tool allowlists

**File:** `internal/agent/agent.go`

This is the key design question. OpenCode uses **glob patterns** (`"my-mcp*": true/false`) so a server's tools are toggled as a group. ogcode currently uses **exact string allowlists** per agent (`codingAgentTools`).

Two options:

**Option A (recommended for v1): Auto-include all MCP tools in full-access agents.**
Add a sentinel or post-filter step. The cleanest approach: after `ForAgent(toolIDs)`, append all MCP-discovered tool IDs for agents that already have broad access. This requires the `LoopRunner` to know about MCP tools.

**Option B (cleaner, matches OpenCode): Glob support in `ForAgent`.**
Extend `Registry.ForAgent` to accept glob patterns, and add an `mcp_*` entry to `codingAgentTools`. This lets config-level `tools` overrides enable/disable MCP tools per agent, exactly like OpenCode.

**Recommended:** Option B. Add `mcp_*` glob to `codingAgentTools` and the task agent's tools. Update `ForAgent` to match globs:

```go
// internal/tool/tool.go
func (r *Registry) ForAgent(toolIDs []string) []ToolDef {
    var result []ToolDef
    for _, id := range toolIDs {
        if strings.ContainsAny(id, "*?") {
            // glob match against all registered tools
            for _, t := range r.tools {
                if matched, _ := filepath.Match(id, t.ID()); matched {
                    result = append(result, t)
                }
            }
        } else if t, ok := r.tools[id]; ok {
            result = append(result, t)
        }
    }
    return result
}
```

Update `codingAgentTools` to append `"mcp_*"`.

**⚠️ Prompt-cache invariant:** `internal/agent/loop.go:452-457` — the system prompt's static entry [0] must stay byte-identical. MCP tool names are dynamic, so they must NOT appear in the static prompt. They appear in the tool definitions block (sent per-turn, outside the cached prefix) — same as built-in tools. No change needed to the prompt builder.

---

### Phase 5 — Tool Reachability Test

**File:** `internal/agent/tool_reachability_test.go`

Per AGENT.md: *"A section that names a tool by id must only reach agents that hold it."* If the system prompt mentions MCP tools (e.g. an "MCP servers" section listing connected servers), add `mcp_*` to `mandatoryPromptTools` with a glob-aware check, or omit MCP from the prompt and let the tool definitions speak for themselves.

**Recommended:** Do NOT add MCP-specific prompt text for v1. The tool descriptions from the MCP server are already passed in the tool definitions block. The agent will discover and use them naturally (the `capToolOutput` comment already anticipated this). Add a brief line to the static prompt only if testing shows the agent needs a nudge.

---

### Phase 6 — CLI Support (optional, v2)

**File:** `internal/cli/mcp.go` (new)

Mirror OpenCode's `opencode mcp` subcommands:

```
ogcode mcp list              # show all configured servers + connection status
ogcode mcp add <name>        # interactively add a server to ogcode.json
ogcode mcp remove <name>     # remove a server
ogcode mcp auth <name>       # trigger OAuth flow (v2)
ogcode mcp debug <name>      # test connection + show diagnostics
```

For v1, config is purely file-based (edit `ogcode.json`). CLI management is a convenience layer.

---

### Phase 7 — OAuth Support (v2, deferred)

OpenCode has a full OAuth implementation (`oauth-provider.ts`, `auth.ts`, `oauth-callback.ts`, `browser.ts`). The Go SDK has `auth.OAuthHandler` on `StreamableClientTransport`.

For v1, **skip OAuth**. Remote servers that need auth use static `headers` (e.g. `"Authorization": "Bearer {token}"`) — this covers the Penpot use case (`?userToken=...` in the URL, or a header). OAuth is a separate, substantial feature for a later phase.

---

## 4. File Change Summary

| File | Change | Phase |
|---|---|---|
| `go.mod` / `go.sum` | Add `github.com/modelcontextprotocol/go-sdk` | 0 |
| `internal/config/config.go` | Add `MCPConfig`, `MCPServerConfig` to `Config`; update template | 1 |
| `internal/mcp/manager.go` | **New** — server lifecycle, parallel connect, teardown | 2 |
| `internal/mcp/tool.go` | **New** — `mcpTool` implementing `tool.ToolDef` | 3 |
| `internal/tool/tool.go` | Glob support in `ForAgent` | 4 |
| `internal/agent/agent.go` | Add `"mcp_*"` to `codingAgentTools` + task agent tools | 4 |
| `internal/server/server.go` | Instantiate `mcp.Manager`, register tools, close on shutdown | 4 |
| `internal/cli/run.go` | Same MCP registration for CLI mode **+ `defer mcpManager.Close()`** ⚠️ #5 | 4 |
| `internal/cli/root.go` | Same for the root command's minimal tool set **+ `defer mcpManager.Close()`** ⚠️ #5 (optional) | 4 |
| `internal/agent/tool_reachability_test.go` | Glob-aware check if MCP prompt text added | 5 |

---

## 5. Testing Strategy

### Unit tests

- **`internal/mcp/manager_test.go`**: Use `mcp.NewInMemoryTransport()` (the Go SDK provides paired in-memory transports — verified in `mcp/transport.go:153`) to spin up a test MCP server with known tools, connect a `Manager`, and assert discovered tools + call results.

  > **⚠️ REVIEW #2 — wrong identifier + citation.** The SDK exposes **`mcp.NewInMemoryTransports()`** (plural), which returns a **pair** `(*InMemoryTransport, *InMemoryTransport)` — one for the client, one for the server. There is no singular `NewInMemoryTransport`, and the "`mcp/transport.go:153`" "verified" cite is wrong (that clone no longer exists). Easy fix — just call the plural form and use both returned transports.
- **`internal/mcp/tool_test.go`**: Test `mcpTool.Execute` output conversion (text → output, image → attachment, error → error result, truncation).
- **`internal/tool/tool_test.go`**: Test `ForAgent` glob matching (`"mcp_*"` matches `"mcp_penpot_get_design"`).

### Integration test

- Spin up a real stdio MCP server (e.g. `@modelcontextprotocol/server-everything` via `npx`) in a test, connect through `Manager`, call a tool, verify the result flows through `capToolOutput` and into a mock provider.

### Manual test (Penpot)

- Configure the Penpot remote MCP in `ogcode.json`:
  ```json
  {
    "mcp": {
      "servers": {
        "penpot": {
          "type": "remote",
          "url": "https://design.penpot.app/mcp/stream?userToken=...",
          "enabled": true
        }
      }
    }
  }
  ```
- Start ogcode, verify `slog` shows "MCP tools registered, count=N", ask the agent to "use the penpot tool to list files".

---

## 6. Risks & Mitigations

| Risk | Mitigation |
|---|---|
| **Context bloat** — an MCP server (e.g. GitHub) exposes 50+ tools, each with a JSON schema, inflating every request | Per-server `enabled: false` in config; per-agent glob allow/disable (Phase 4 Option B). Document the caveat (OpenCode's docs warn about this explicitly). |
| **Subprocess orphans** — `npx` spawns `node` spawns the server; closing stdin may not cascade | Walk child PIDs and SIGTERM (OpenCode's `descendants` pattern). |
| **Startup latency** — a slow/unreachable remote server blocks agent startup | Parallel connect with per-server timeout (30s default). Failed servers are logged + skipped, not fatal. |
| **Prompt-cache invalidation** — MCP tools must not appear in the cached static system prompt | Tool definitions are sent per-turn outside the cached prefix. No prompt builder changes needed. Verified against `loop.go:452-457`. |
| **Tool name collisions** — two servers expose a tool named `search` | Namespacing: `server_tool` (sanitised). The LLM references the full composite name. |
| **CGO** — adding a pure-Go dependency shouldn't affect the cgo build | The Go MCP SDK is pure Go (`net/http`, `os/exec`, `jsonrpc2`). No new cgo. Build with `CGO_ENABLED=1` as usual. |

---

## 7. Build Verification

```bash
# After implementation:
CGO_ENABLED=1 go build ./...
CGO_ENABLED=1 go test ./...
gofmt -w internal/mcp/ internal/config/config.go internal/tool/tool.go internal/agent/agent.go internal/server/server.go
```

---

## 8. Phasing

| Phase | Scope | Effort |
|---|---|---|
| **0** | Add Go MCP SDK dependency | 30 min |
| **1** | Config schema | 1 hr |
| **2** | MCP client manager (local + remote, no OAuth) | 1-2 days |
| **3** | Tool adapter (`mcpTool`) | 0.5 day |
| **4** | Registration + agent wiring + glob `ForAgent` | 0.5 day |
| **5** | Tool reachability test | 1 hr |
| **6** | CLI management commands | 0.5 day (v2) |
| **7** | OAuth support | 1-2 days (v2, deferred) |

**v1 deliverable:** Phases 0-5. A user edits `ogcode.json`, adds local or remote MCP servers (with static headers for auth), and the agent can call their tools. This covers the Penpot remote MCP use case from the previous conversation.