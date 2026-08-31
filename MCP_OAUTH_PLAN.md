# MCP OAuth Authorization — Implementation Plan

Status: **implemented** (steps 1–5). This document scoped and sequenced the
work to add OAuth 2.0 Authorization Code (with PKCE) support to ogcode's MCP
client, so servers like Cal.com that require an OAuth handshake connect without
the user pasting a static token.

## Open questions — resolved

1. **Manual-copy in headless** → **implemented** (`manualCodeFetch` in
   `oauth.go`): when no display is detected, the auth URL is printed to stderr
   and the user pastes the full redirected URL on stdin.
2. **Token store location** → `~/.ogcode/mcp-tokens/` (runtime state, alongside
   the embed-model cache), overridable via `OGCODE_MCP_TOKEN_DIR`.
3. **`auth` field name** → `auth` (leaves room for non-OAuth schemes later).

## Why

Today (`internal/mcp/manager.go`) ogcode's HTTP MCP transport supports only two
auth paths: unauthenticated, or a **static** bearer token injected via the
`headers` map (`headerRoundTripper`). When a server responds `401` with a
`WWW-Authenticate: Bearer resource_metadata=…` challenge — the MCP Authorization
spec — `client.Connect` simply fails and the server contributes zero tools. The
user has no way to authorize.

The good news: the Go SDK we already depend on (`go-sdk v1.7.0`) ships a
**complete** authorization-code handler. We do not reimplement the protocol; we
wire it into our transport and supply the three things the SDK leaves to the
application:

1. A `AuthorizationCodeFetcher` — opens the user's browser and captures the
   `localhost` redirect carrying the code.
2. Dynamic Client Registration defaults — so a server with no pre-registered
   client "just works".
3. A token store on disk — so authorization survives restarts instead of
   re-prompting every launch.

## What the SDK already gives us

Confirmed by reading the vendored sources in
`$GOPATH/pkg/mod/github.com/modelcontextprotocol/go-sdk@v1.7.0`:

| Piece | Where | What it does |
|---|---|---|
| `auth.OAuthHandler` interface | `auth/client.go` | `TokenSource(ctx)` (set bearer on every request) + `Authorize(ctx, req, resp)` (run the flow on 401/403). The transport calls these. |
| `auth.AuthorizationCodeHandler` | `auth/authorization_code.go` | Full implementation of `OAuthHandler`. Parses `WWW-Authenticate`, fetches Protected Resource Metadata (`oauthex.GetProtectedResourceMetadata`), fetches Authorization Server Metadata (`auth.GetAuthServerMetadata`), does DCR or uses preregistered client, builds the `oauth2.Config` with PKCE, calls our fetcher, exchanges the code, stores the token in an `oauth2.TokenSource`. |
| `StreamableClientTransport.OAuthHandler` | `mcp/streamable.go:1960` | Field on the transport struct. The connection layer (`streamable_client.go:2276`) intercepts 401/403 → calls `Authorize` → **retries the request once**. Every request goes through `setMCPHeaders` (`:2380`) which pulls the bearer from `TokenSource`. |
| `oauthex.*` | `oauthex/` | `ProtectedResourceMetadata`, `AuthServerMeta`, `ParseWWWAuthenticate`, `RegisterClient` (DCR), `ClientCredentials`, `ClientRegistrationMetadata`. |

**The transport drives the flow.** We attach an `OAuthHandler` to the
`StreamableClientTransport` and the SDK does the rest — including the retry after
`Authorize` succeeds. Our only job is constructing the handler with a fetcher,
registration config, and (optionally) an `InitialTokenSource` loaded from disk.

### Constraint: SSE transport has no OAuth support

`SSEClientTransport` (`mcp/sse.go`) has **no** `OAuthHandler` field (confirmed
by grep). OAuth works only on `streamable-http`. This is acceptable: the modern
MCP Authorization spec is defined against streamable-HTTP, and our default
auto-detect (`transportFor` with `sc.URL` set → `httpTransport`) already
produces a streamable transport. Servers that force `"transport": "sse"` are
legacy and out of scope for OAuth in this iteration. We will **document** this
limitation rather than work around it.

## Design

### Config — minimal, opt-out via static headers

No new required field. The rule:

- A server with a `url` **and no `headers`** gets an `OAuthHandler` attached
  automatically. The first `401` triggers the flow. This makes Cal.com work with
  the bare config the user already wrote:
  ```json
  { "mcp": { "calcom": { "url": "https://mcp.cal.com/mcp" } } }
  ```
- A server with a `url` **and `headers`** keeps the current static-token path —
  no `OAuthHandler`. Users who already have a personal API token are unaffected.
- An **optional** `auth` block is added for advanced cases (pre-registered
  client, forcing DCR off, custom scopes). Empty `auth` = DCR with defaults.

```go
// config.go — additions to MCPServerConfig
type MCPServerConfig struct {
    // ... existing fields ...
    Auth *MCPAuthConfig `json:"auth,omitempty"`
}

// MCPAuthConfig configures OAuth for a URL-based server. When nil (the
// default for a server with a url and no headers), DCR is used with a
// localhost redirect — the "just works" path. Setting any field opts into
// an explicit client.
type MCPAuthConfig struct {
    // ClientID + ClientSecret for a pre-registered client. When set, DCR is
    // skipped. Leave empty to use Dynamic Client Registration (the default).
    ClientID     string `json:"clientId,omitempty"`
    ClientSecret string `json:"clientSecret,omitempty"`
    // Scopes requested beyond what the server advertises. Empty = server's
    // scopes_supported + offline_access.
    Scopes []string `json:"scopes,omitempty"`
    // SkipOAuth disables the OAuth handler for this server even with no
    // headers (e.g. a server that returns 401 but is not an OAuth server).
    SkipOAuth bool `json:"skipOAuth,omitempty"`
}
```

`config.Load`'s per-name wholesale replacement (`config.go:113-118`) already
handles the `auth` field correctly — a project re-stating a server owns its
full definition. No merge logic to add.

### Token store — `~/.ogcode/mcp-tokens/<server>.json`

New file `internal/mcp/tokenstore.go`. One JSON file per server, storing enough
to rebuild the `oauth2.TokenSource` after restart:

```go
type storedToken struct {
    AccessToken  string    `json:"access_token"`
    RefreshToken string    `json:"refresh_token,omitempty"`
    TokenType    string    `json:"token_type,omitempty"`
    Expiry       time.Time `json:"expiry,omitempty"`
    // Configuration captured at authorization time so a refresh after
    // restart hits the same token endpoint with the same client identity.
    TokenURL     string    `json:"token_url"`
    ClientID     string    `json:"client_id"`
    ClientSecret string    `json:"client_secret,omitempty"`
    Scopes       []string   `json:"scopes,omitempty"`
}
```

- `loadToken(name) (*storedToken, error)` — returns `nil, nil` when the file
  is absent (first run). Missing/invalid file is not an error; we re-authorize.
- `saveToken(name, t)` — atomic write (temp file + rename, `0600` perms) so a
  crash mid-write never corrupts the store. The token is a credential —
  restrictive perms, stored under `~/.ogcode` (not the project, never committed).
- Location: `os.UserConfigDir()/ogcode/mcp-tokens/` — mirrors how the embed
  model cache lives under `~/.ogcode/`. Override via `OGCODE_MCP_TOKEN_DIR`
  for tests, matching the `OGCODE_EMBED_MODEL_DIR` precedent.

The persistence hook is the SDK's `NewTokenSource` config field plus a
`savingTokenSource` wrapper (the pattern from `auth/auth_example_test.go`):
every time the access token changes (initial grant **and** each refresh), the
wrapper writes the new token to disk. This means refreshes — which the SDK
performs transparently when the access token expires — are persisted too,
without us intercepting every request.

### The fetcher — browser + localhost redirect

New file `internal/mcp/oauth.go`. One `codeReceiver` per Manager (shared
listener, not per-server — a single localhost port serves all servers, the
`state` param distinguishes flows). Constructed once in `New`, reused across
the parallel `connect` goroutines.

```go
type codeReceiver struct {
    listener net.Listener
    server   *http.Server
    // state -> chan, set in getAuthorizationCode, drained by the handler.
    mu       sync.Mutex
    pending  map[string]chan *auth.AuthorizationResult
}
```

Flow inside `getAuthorizationCode(ctx, args)`:
1. Generate a `state` value, register a channel under it.
2. Open the user's browser to `args.URL` (which already embeds `state` +
   PKCE challenge from the SDK):
   - macOS: `open "<url>"`
   - Linux: `xdg-open "<url>"`
   - Windows: `rundll32 url.dll,FileProtocolHandler "<url>"` (or `cmd /c start`)
   - Fallback: print the URL to stdout and ask the user to open it.
3. Wait on the channel (the redirect), the context, or a short hard timeout.
4. Return the `AuthorizationResult{Code, State, Iss}`.

The redirect handler validates `state` matches a pending flow (prevents CSRF),
sends the result on the channel, and responds with a simple "you can close this
tab" HTML page.

**Port selection:** bind `localhost:0` to let the OS pick a free port, then use
that port as the `RedirectURL`. Avoids hardcoding a port that may be taken. The
redirect URI is advertised to the AS via DCR, so any free port works. (The SDK
example hardcodes a port; we improve on it.)

### Wiring it into `manager.go`

`httpTransport` gains the handler. The decision lives in a new
`maybeOAuthHandler(ctx, name, sc)` returning `auth.OAuthHandler` or nil:

```go
func httpTransport(ctx context.Context, name string, sc config.MCPServerConfig) (*mcp.StreamableClientTransport, error) {
    client := &http.Client{}
    var handler auth.OAuthHandler
    if len(sc.Headers) == 0 && (sc.Auth == nil || !sc.Auth.SkipOAuth) {
        h, err := newOAuthHandler(ctx, name, sc)   // builds handler w/ DCR + fetcher + stored token
        if err != nil {
            return nil, fmt.Errorf("oauth: %w", err)
        }
        handler = h
    } else if len(sc.Headers) > 0 {
        client.Transport = headerRoundTripper{headers: sc.Headers}  // unchanged
    }
    return &mcp.StreamableClientTransport{
        Endpoint:             sc.URL,
        HTTPClient:           client,
        DisableStandaloneSSE: true,
        OAuthHandler:         handler,
    }, nil
}
```

`newOAuthHandler` builds the `AuthorizationCodeHandlerConfig`:
- `DynamicClientRegistrationConfig` with `ClientRegistrationMetadata{
    ClientName: "ogcode", RedirectURIs: []string{redirectURL},
    GrantTypes: []string{"authorization_code","refresh_token"},
  }` — **the default when no `auth.clientId`** is given. This is what makes
  Cal.com work with zero extra config.
- OR `PreregisteredClient{ClientID, ClientSecretAuth}` when `auth.clientId` is set.
- `RedirectURL` = the receiver's `http://localhost:<port>/callback`.
- `AuthorizationCodeFetcher` = the receiver's `getAuthorizationCode`.
- `RequestRefreshToken: true` — so we get a refresh token and don't re-prompt.
- `InitialTokenSource` = a token source rebuilt from the stored token, if one
  exists. When present **and** valid (not expired, or refreshable), the first
  request succeeds without ever hitting 401 — the user is not re-prompted on
  restart. When absent, the first request 401s and `Authorize` runs once.
- `NewTokenSource` wrapped in `savingTokenSource` → `saveToken(name, ...)`.

`sseTransport` is unchanged (no OAuth support in the SDK for SSE).

`transportFor`'s signature gains the `name` (already passed to `connect`) so
the handler can key the token store. `connect` passes `name` through.

### Headless mode (`ogcode run` / `internal/cli/run.go`)

`run.go` is the non-server entry point — often invoked headless (CI, SSH).
There is no guarantee of a browser or a reachable `localhost`. Two options:

1. **Detect a display.** If `os.Getenv("DISPLAY")` is set (Linux) or we're on
   macOS/Windows (where a browser is effectively always available), use the
   browser fetcher. This covers `ogcode run` on a laptop.
2. **Manual-copy fallback.** When no display is detected, print the auth URL
   and prompt the user to paste the **full redirected URL** (the one the AS
   redirects to, containing `code` + `state`) back into stdin. Parse the code
   from the pasted URL. This is the standard device-less OAuth manual flow.

A single `fetcherFor(ctx)` picks the implementation based on environment. The
receiver's interface (`getAuthorizationCode`) is the same; only the transport
of the code differs. Both paths write to the same token store.

### Startup behavior and error handling

The SDK's transport calls `Authorize` **lazily** on the first 401, not at
`Connect` time. So `mcp.New`'s parallel connect does **not** block on OAuth — it
blocks on the `initialize` JSON-RPC, which is the first request and the one
that 401s. The practical effect:

- A server needing OAuth: the first `connect` goroutine hits 401 → opens a
  browser → user authorizes → token stored → retry succeeds → tools listed.
  During this window `mcp.New` is waiting. **The browser prompt is on the
  critical path of startup for that one server.** Other servers connect in
  parallel and are not blocked.
- A server with a stored, valid token: no prompt, connects immediately.
- A server where the user **never** authorizes (closes the browser): the
  `connect` goroutine times out (we add a 5-minute hard cap on `Authorize`)
  → that server contributes zero tools → `mcp.New` returns a joined error for
  it → the server path logs `slog.Warn("mcp: …", mcpErr)` and continues, the
  headless path logs the same. No deadlock, no hang.

This matches the existing "an unreachable server simply contributes no tools"
contract (`manager.go:50-54`). OAuth failure is just another flavor of
unreachable.

### Security

- **State param** — generated per-flow, validated on redirect, prevents CSRF.
- **PKCE** — the SDK's `oauth2.Config` uses PKCE automatically for the
  authorization-code flow; we rely on it.
- **Token file perms** — `0600`, under `~/.ogcode/`, never under the project.
- **Redirect URI** — `localhost` only (loopback), as the spec requires for
  native apps. Never a remote callback.
- **SSRF** — the SDK's `AuthorizationCodeHandlerConfig.Client` field accepts a
  custom `*http.Client` for PRM/AS-metadata fetches. For v1 we use the default
  (no SSRF hardening); documented as a follow-up if we expose untrusted
  servers. Cal.com and similar first-party servers are not an SSRF concern.

## File-by-file change list

### New files

- **`internal/mcp/oauth.go`** (~180 lines)
  - `codeReceiver` struct + `newCodeReceiver(ctx)` (binds localhost:0, starts
    `http.Server`).
  - `getAuthorizationCode(ctx, *auth.AuthorizationArgs) (*auth.AuthorizationResult, error)`
    — registers state, opens browser (or prints for manual copy), waits.
  - `fetcherFor(ctx)` — picks browser vs manual-copy based on `DISPLAY`/OS.
  - `newOAuthHandler(ctx, name, sc, receiver)` — assembles
    `AuthorizationCodeHandlerConfig` (DCR or preregistered), loads stored
    token as `InitialTokenSource`, wraps `NewTokenSource` with `savingTokenSource`.
  - `close()` on the receiver — called from `Manager.Close`.

- **`internal/mcp/tokenstore.go`** (~90 lines)
  - `storedToken` struct, `loadToken(name)`, `saveToken(name, t)` (atomic,
    `0600`).
  - `tokenDir()` — `os.UserConfigDir()/ogcode/mcp-tokens/`, overridable via
    `OGCODE_MCP_TOKEN_DIR`.
  - `savingTokenSource` — wraps an `oauth2.TokenSource`, calls `saveToken` on
    access-token change (from `auth_example_test.go`).
  - `tokenSourceFromStored(t)` — rebuilds an `oauth2.TokenSource` from a
    stored token for `InitialTokenSource`.

### Modified files

- **`internal/config/config.go`**
  - Add `MCPAuthConfig` struct + `Auth *MCPAuthConfig` field on
    `MCPServerConfig`. Doc comment update on `MCPServerConfig` noting the
    OAuth auto-enable rule (url + no headers → OAuth).

- **`internal/mcp/manager.go`**
  - `Manager` gains a `receiver *codeReceiver` field, closed in `Manager.Close`.
  - `New` constructs one shared `codeReceiver` (if any server is HTTP + no
    headers), passes it down.
  - `connect` / `transportFor` signatures gain `name string`.
  - `httpTransport` becomes `httpTransport(ctx, name, sc, receiver)` and
    calls `newOAuthHandler` per the rule above. `sseTransport` and
    `stdioTransport` unchanged.

- **`internal/cli/run.go`** — no change beyond what `mcp.New` already does
  (the headless fetcher is selected inside `oauth.go`). The existing
  `slog.Warn("mcp: …", mcpErr)` already handles the per-server failure.

- **`internal/server/server.go`** — likewise no change; `mcp.New` absorbs the
  OAuth wiring.

### New test files

- **`internal/mcp/oauth_test.go`**
  - `TestNewOAuthHandler_DCRDefaults` — config with no `auth` → handler built
    with a `DynamicClientRegistrationConfig` and a localhost redirect.
  - `TestNewOAuthHandler_Preregistered` — `auth.clientId` set → no DCR.
  - `TestTokenStore_RoundTrip` — `saveToken` → `loadToken` returns the same
    token; `0600` perms; missing file returns nil.
  - `TestSavingTokenSource_PersistsOnRefresh` — wrapped source produces a new
    token → `saveToken` called with it.
  - `TestCodeReceiver_StateValidation` — redirect with wrong `state` is
    rejected; correct `state` delivers the result.
  - `TestMaybeOAuthHandler_Rules` — headers set → nil handler; no headers →
    non-nil; `auth.skipOAuth` → nil.

  OAuth **end-to-end** (a real AS + MCP server returning 401) is left to the
  SDK's own `authorization_code_test.go`; we test our glue, not the protocol.
  An integration test against a live Cal.com endpoint is explicitly **out of
  scope** (network/flaky, needs a real account).

## Dependencies

- `golang.org/x/oauth2 v0.35.0` — already in `go.mod` as `// indirect` (pulled
  by the SDK). We use it directly in `oauth.go`/`tokenstore.go`, so promote it
  to a direct dep: `go mod tidy` after the imports land. No version bump.
- The `auth` and `oauthex` packages — part of the SDK module already in
  `go.sum`. No new module.

No `CGO_ENABLED=1` implications — pure Go.

## Sequencing

1. **Config + token store** — `MCPAuthConfig`, `tokenstore.go` + tests. No
   behavior change yet (nothing reads the store). Land first; it's the
   foundation everything hangs off and is independently testable.
2. **Receiver + handler construction** — `oauth.go` + tests. Still no wiring;
   build the pieces, test them in isolation.
3. **Wire into `manager.go`** — the `httpTransport` change. Now a real server
   with no headers gets OAuth. Test manually against Cal.com (or a local AS).
4. **Headless manual-copy fetcher** — the `fetcherFor` branch + stdin prompt.
   Test by running `ogcode run` with `DISPLAY` unset.
5. **Docs** — update `MEMORY.md` (the MCP section) with the new behavior, the
   auto-enable rule, the token store location, and the SSE limitation.
   Update the `MCPServerConfig` doc comment and the project `ogcode.json`
   template (`projectFileTemplate`) to show the `auth` block as a comment.

Steps 1–2 are safe to merge with no behavioral risk. Step 3 is the
behavioral cutover. Steps 4–5 are polish.

## What we are explicitly NOT doing (v1)

- **SSE transport OAuth** — the SDK's `SSEClientTransport` has no
  `OAuthHandler` field. Out of scope.
- **SSRF hardening** on PRM/AS-metadata fetches — default `http.Client`. A
  follow-up if we ever treat untrusted MCP servers.
- **Token revocation** — we store tokens; we don't call a revocation endpoint
  on `mcp ... remove` or config change. A follow-up.
- **Per-server token inspection in the UI** — the web settings UI does not
  surface stored MCP tokens. The files are on disk for power users; a UI is a
  separate feature.
- **Client Credentials grant** (`extauth/client_credentials.go`) — for
  machine-to-machine servers that don't have a user. The SDK supports it; we
  defer until a server needs it (none known today).
- **Live integration test against Cal.com** — flaky and account-bound. The SDK
  covers the protocol; we cover our glue.

## Open questions for the developer

1. **Manual-copy in headless** — is printing the URL and prompting for the
   pasted redirect URL acceptable, or should headless simply refuse OAuth
   ("set a static token in headers" / "run on a machine with a browser once")?
   The former is more useful; the latter is simpler and safer.
2. **Token store location** — `~/.ogcode/mcp-tokens/` (alongside the embed
   model cache) or `~/.config/ogcode/mcp-tokens/` (XDG-ish)? MEMORY.md uses
   `~/.ogcode/` for the embed model and `~/.config/ogcode/` for config. Tokens
   are runtime state like the model cache → I lean `~/.ogcode/`, but it's a
   judgment call.
3. **`auth` field name** — `auth` (what I've used) vs `oauth` (more specific)
   vs folding into `headers` semantics. `auth` reads cleanly and leaves room
   for non-OAuth auth schemes later.