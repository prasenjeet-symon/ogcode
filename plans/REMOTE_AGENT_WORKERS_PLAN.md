# Remote Agent Workers — Implementation Plan for ogcode

> **Scope**: a **master** (the separate `ogcode-control-plane` daemon) that orchestrates long-lived **workers** (the `ogcode worker` run-mode) on other machines. A worker dials the master over ConnectRPC, publishes its local workspaces, and *hosts agent sessions* inside the worktrees it manages. Operators reach everything through the master: an apex console listing workers, and per-worker subdomains that reverse-proxy each worker's own ogcode UI over a yamux tunnel.

> **Status (re-baselined 2026-09-11)**: the transport, pairing, registration, registry, worker daemon, session relay, UI tunnel, and CLI shipped before this revision. §0 states exactly what exists; §5 plans only what remains. **Do not implement from §5 without reading §0 first** — the earlier revision of this document described a master-embedded-in-ogcode-server design that was superseded by the standalone control plane.

> **Architecture decision (confirmed by the developer, 2026-09-11)**: hosted sessions run on **one full standalone `ogcode` server process per worktree** — not a shared multi-dir server and not the headless `env` wiring. Each per-worktree server is a complete, isolated instance (own session DB, bus, MCP subprocesses, skills, providers, permission manager, port, tunnel). Everything the earlier review flagged as "must stay per-dir" (MCP, skills, tools, bus) is per-process **automatically** under this model — nothing to carve out. Machine-global state (`~/.ogcode/config.db` provider keys, `~/.ogcode/embed-model/`, `~/.ogcode/mcp-tokens/`) is already designed to be shared across processes and needs no sharing logic. The consequence: **`internal/server/server.go` becomes the hosted-session construction path** (`server.New(dir)` per worktree), superseding `buildEnv` for hosted sessions (Phase 2), and Phase F(a) is realized by the same mechanism rather than by folding `internal/server` into `buildEnv`'s wiring.

> This plan is deliberately **anti-framework-by-default**. The control plane is thin by design: transport via ConnectRPC, registration + workspaces + session hosting as hand-rolled application logic, stdlib for auth (pairing secret, bcrypt, HMAC cookie). No durable-workflow engine (Temporal), no message bus (NATS), no hosted runner provider for v1.

---

## 0. Re-baseline: shipped vs. remaining (2026-09-11)

The first revision of this plan (2026-09-09) was written before any control-plane code existed. The control plane has since landed as a **nested Go module** at `controlplane/` (its own `go.mod`; ogcode's `go.mod` carries `replace github.com/prasenjeet-symon/ogcode-control-plane => ./controlplane`; it imports none of ogcode's `internal/`, and ogcode imports only its `gen/` + `tunnel` packages). Both modules build and test separately. Worker reconnect + token persistence (Phase C) shipped 2026-09-11 alongside Phase A/B.

| Original phase | Status | Where it lives now |
|---|---|---|
| Phase 0 — ConnectRPC dep, shared proto, codegen, round-trip test | **Shipped** | `connectrpc.com/connect v1.21.0`; proto at `controlplane/proto/controlplane/v1/controlplane.proto`; codegen via `buf` (`buf.yaml`, `buf.gen.yaml`) into `controlplane/gen/` |
| Phase 1 — pairing, registration, token rotation, heartbeat, registry, reap | **Shipped** (in-memory) | `controlplane/internal/pairing/`, `registry/`, `master/server.go`; worker side `internal/worker/worker.go` |
| Phase 1 — worker-id persistence (stable identity across restarts) | **Shipped** | `internal/worker/workerid.go` + master adopt-or-reject (`server.go:118-129`) |
| Phase 2 — workspaces API + discovery | **Shipped** | `internal/worker/workspaces.go` (`discoverWorkspaces`, `git worktree list --porcelain`) + `ListWorkspaces` command |
| Phase 2 — git bootstrap (clone absent workspaces) | **Shipped** (Phase D) | `internal/worker/bootstrap.go` (`bootstrapWorkspace`/`cloneRepo`) + `startAgent` calls it (`worker.go`); no ogcode-level git auth, deferring to the host's credential helper |
| Phase 3 — session hosting on the worker | **Shipped (headless)** | `internal/worker/env.go` (mirrors `runPrompt`), `session.go`, relay in `worker.go:326-351` |
| Phase 3 — permission gating / guidance / abort over RPC | **Shipped** (Phase E) | `worker.go handleCommand` (Guidance/ApproveTool/RejectTool), env `permissions` manager, `WithPermissionGating` + `LoopControl` in `startAgent`, `replyToPermission` seam |
| Phase 3 — boot recovery (port `recoverInterruptedSessions`) | **Shipped** (Phase E) | `internal/worker/recover.go` (`recoverInterruptedSessions` runs per env build in `envFor`) |
| Phase 4 — master-as-proxy panel (REST proxy + SSE relay + `web/src` section) | **Superseded** | The shipped interaction model is the **UI tunnel**: master proxies each worker's own ogcode UI (`tunnel.go:101-158`). A master-side panel section is only worth building if operators must never reach worker UIs directly — see Phase F and §9 |
| Phase 5 — `ogcode worker` CLI | **Shipped** | `internal/cli/worker.go` (all flags incl. `--master-ca`, `--insecure`; `--serve-addr` removed — hosting is in-process) |
| — bbolt registry persistence (A) | **Shipped** | `controlplane/internal/registry/store.go` + `store_test.go` |
| — per-employee accounts (B) | **Shipped** | `controlplane/internal/auth/auth.go` (store-backed gate), `cmd/ogcode-control-plane/users.go` (add/rm/list), `master/accounts_test.go` |
| — worker reconnect + token persistence (C) | **Shipped** | `internal/worker/cred.go` + reconnect loop / re-pair fallback in `worker.go`; `internal/worker/reconnect_test.go` (h2c fake master) |
| — git bootstrap (D) | **Shipped** | `internal/worker/bootstrap.go` + `startAgent` branch; `internal/worker/bootstrap_test.go` |
| — interactive hosting (E) | **Shipped** | permission gating + guidance + approve/reject (`session_test.go`), boot recovery (`recover.go` + `recover_test.go`), worker-side titles (`title.go`) — note: E's RPC path now proxies into the per-dir server (see hosting row); the standalone env wiring (`env.go`, `recover.go`, `title.go`) compiles and is tested but is no longer on the hosted path |
| — standalone-server lifecycle (1) | **Shipped** | `internal/server/server.go` `Serve(ctx)`/`Stop`/`Port`/`Options`; `serve_lifecycle_test.go` |
| — per-worktree server hosting (2) | **Shipped** | `internal/worker/servers.go` (serverManager: one in-process `server.Server` per worktree dir, `NewWithOptions(0, dir, ModeBuild, {Loopback, NoBrowser})`, TCP-poll bind wait, ctx-cascade shutdown); `internal/server/host_session.go` (`HostSession` — creates the session row, publishes user message, starts the loop on that server; idempotent on re-delivery; exported `Guidance`/`ReplyPermission`/`SubscribeEvents`/`SessionRow` seams); `internal/worker/tunnel.go` rewritten — one tunnel per worktree, first-frame `TunnelChunk.route` = DNS-safe `worktreeLabel(dir)`, master keys `<workerID>-<route>` (`tunnelKey`, legacy bare id when route empty); `--serve-addr` removed from `ogcode worker` |
| — subdomain scheme `<worker>-<worktree>.<host>` (3, master side) | **Shipped** | `controlplane/internal/master/tunnel.go` — `tunnelRegistry` tracks `tunnelEntry{workerID, route, sess}` per key; `routes(workerID)` enumerates a worker's live route labels (sorted, excludes the legacy empty route); routing stays an **exact whole-label lookup** (`hostLabel` unchanged, no first-hyphen split — worker ids may themselves contain hyphens, pinned by `TestUIProxy_RoutesHyphenatedWorkerID`). `controlplane/internal/master/panel.go` — the apex console renders one "Open `<route>` UI" link per live worktree tunnel (`<id>-<route>.<host>`); workers with no tunnels keep the legacy bare `<id>.<host>` link (pinned by `TestRenderApex_WorktreeRoutes`). Worker side needed no change: `openTunnel` already sent the route frame (Phase 2) |
| — | **Remaining** | none — Phase G (security hardening) verified 2026-09-11; all rows shipped |

**Net effect**: none remaining — persistence, accounts, resilience, interactivity, and the security verification pass are all shipped and pinned.

---

## 1. Design decisions (carried over, updated to shipped reality)

| Decision | Choice | Why / status |
|---|---|---|
| **Transport / RPC** | **ConnectRPC over HTTP/2** (shipped) | Connect has no WebSocket transport; bidi rides HTTP/2 (`BidiStream` handler-side, `BidiStreamForClient` caller-side; `net/http` negotiates h2 via ALPN under TLS, and the master wraps cleartext dev mode in h2c — `main.go:131-134`). TLS terminates at the master; certs hot-reload via `tlsreload` so a renewed wildcard applies without restart. |
| **Control plane** | Hand-rolled, small, fully owned (shipped) | The master↔worker trust boundary is the code — it stays tiny and auditable. Current size: `master/` ≈ 1,000 lines total. |
| **Worker is the client** | Workers dial out; master never dials in (shipped, load-bearing) | Workers live behind NAT. Every master→worker command travels down the worker-opened `WorkerStream`; the `Tunnel` RPC reverses the direction for browser→worker UI traffic. |
| **Auth (worker↔master)** | Stdlib pairing secret + rotating tokens + TLS (shipped) | `crypto/subtle` constant-time secret check, 32-byte random tokens, TTL default 15 min, rotated via Heartbeat. |
| **Auth (browser-facing)** | **Per-employee accounts** (Phase B — **shipped**) | Replaces the single shared `operatorPassword`: `username → bcrypt(password)` in the bbolt `users` bucket; the signed cookie carries the logged-in user id. No auth framework, no identity provider. **Not stateless at validate time** — see Phase B (A1 fix). |
| **Session hosting** | **One standalone server process per worktree** (confirmed 2026-09-11; Phase 2 — **shipped in-process**) | `server.NewWithOptions(0, dir, ModeBuild, {Loopback, NoBrowser})` + `Serve(ctx)` per worktree, hosted **inside the worker daemon** (`internal/worker/servers.go`) — pure isolation, zero cross-process coupling beyond the filesystem. Sessions start via `Server.HostSession`; guidance/approvals proxy into the server's exported seams. Supersedes `buildEnv` for hosted sessions (`env.go`/`recover.go`/`title.go` compile but are off the hosted path). Phase 1 (lifecycle) shipped. |
| **Durability** | Registry state via **embedded bbolt** (Phase A — **shipped**) | `workers`/`tokenIndex`/`sessions` survive a master restart. Deliberately **not** session durability: an in-flight session still dies with its worker and is not replayable (Temporal stays rejected). |
| **Operator UI model** | Apex console + per-worker UI tunnel (shipped); worker serves its own UI (Phase F) | The master never hosts the ogcode web UI; it proxies each worker's. |
| **Managed provider** | Deferred (§9) | "Run sessions on Fly Machines / a hosted runner" is a different product question from "build your own worker pool." |

> **Invitation for a second opinion**: Temporal becomes worth its cost *only if* a future requirement makes sessions durable/replayable across master restarts. For v1 it is explicitly **not** adopted.

---

## 2. What already ships (the integration surface)

### 2.1 The control plane — `controlplane/` (nested module, builds/tests separately)

- **Proto** `proto/controlplane/v1/controlplane.proto` — service `ControlPlaneService`: `Register`, `Heartbeat`, `WorkerStream` (bidi), `Tunnel` (bidi). Stream commands (`MasterToWorker`): `StartAgent`, `StopAgent`, `AbortSession`, `Guidance`, `ApproveTool`, `RejectTool`, `ListWorkspaces`, `Ping`. Worker frames (`WorkerToMaster`): `Hello`, `CommandResult`, `SessionEvent`, `ResourceFrame`, `Pong`. Codegen: `buf generate` → `gen/controlplane/v1/`.
- **Pairing** `internal/pairing/pairing.go` — constant-time secret check (`CheckSecret`, :30), 32-byte random tokens (`Mint`, :51), rotation policy (`ShouldRotate`, :64). TTL default 15 min (`config.go:34-36`).
- **Registry** `internal/registry/registry.go` — `sync.RWMutex` over three maps: `workers`, `tokenIndex`, `sessions` (:58-64). `Add` (:77) registers/replaces a worker **Offline**; `Attach`/`Detach` bind the live stream; `Touch` refreshes on heartbeat; `RouteSession`/`UnrouteSession`/`WorkerForSession`/`SessionsForWorker` are the routing table; `ReapStale` (:268-292) marks missed-heartbeat workers Dead and returns their orphaned sessions. **Note**: `Add` replaces the worker entry and re-keys `tokenIndex` but never touches `sessions` — a re-pairing worker keeps its session routes. All state is **in-memory today** (Phase A adds bbolt).
- **Master server** `internal/master/server.go` — `Register` (:105, adopt-or-reject presented worker id), `Heartbeat` (:139, refresh + rotate), `WorkerStream` (:168, Hello-token bind → command/event loop), `Call` (:255, request-id correlated command/result with `DefaultCommandTimeout` 30 s), `ListWorkspaces` (:276), `StartRemoteAgent` (:292 — mints the global `ses_…` id, routes on success), `ReapLoop` (:328) + `failSession` (:352 — publishes a synthetic `session.failed` and unroutes). Relay: `dispatchFromWorker` (:210) re-publishes `SessionEvent`s onto the master bus via `PublishRaw` with a **fresh master seq** (`bus.go:71` — the worker's seq is intentionally not carried; `SessionEvent` has no seq field by design). `ResourceFrame` is consumed by a **no-op hook** (:246) — control frames never touch the bus.
- **UI proxy + tunnel** `internal/master/tunnel.go` — the worker opens `Tunnel` (raw `TunnelChunk` byte pipe, first frame = worker token); master wraps it in yamux (`tunnel.ClientSession`) and `UIProxyHandler` (:101) reverse-proxies any request whose host's first label names a tunneled worker to that worker's local ogcode UI (`FlushInterval: -1` so SSE flows live, :143-145). The **apex console** (`panel.go`) — a server-rendered list of connected workers — and the operator gate (`/…/login` under `/__operator/`, `auth.go`) live here.
- **Operator gate** `internal/auth/auth.go` — a store-backed gate with four modes (`ModeDisabled/Legacy/Accounts/Locked`). `NewGate` resolves the mode from a `UserChecker` (`users` bbolt bucket) + optional legacy password: bucket non-empty → **Accounts** (each login verifies `username` → bcrypt via `Authenticate`, A3 dummy-hash constant-time unknown-user path); bucket empty and `operatorPassword` set → **Legacy** (subtle compare); both empty → **Disabled** (runs open, warned); corrupt store at boot → **Locked**. Session cookie `base64url(expiry|userID).base64url(hmac(payload))` (`issue`, :281), random per-process `signingKey` (:30 — restarts log everyone out). `validate` (:289) is the **A1 fix**: HMAC + expiry **plus** one store lookup that fails closed when the user was deleted — so `users rm` breaks that operator's live sessions immediately. Defaults: session TTL 12 h, cookie `og_operator`.
- **`users` CLI** `cmd/ogcode-control-plane/users.go` — `add|rm|list` operating on the same bbolt store as the master (`register` wiring in main.go). Password from `$OGCODE_CONTROL_PLANE_USER_PASSWORD` or `--password-file` (A6 — never argv); only a bcrypt hash hits the DB. `add` also resets (re-hash) an existing user; `rm` needs `--force` to delete the **last** account so an operator can't accidentally make the gate fall back open.
- **Config** `internal/config/config.go` — `MasterConfig`: `listen`, `pairingSecret` (required), `tls`, `tokenTtlSeconds` (900), `workerTimeoutSeconds` (45), `operatorPassword` (legacy — ignored once a `users` account exists), `sessionTtlSeconds` (43200), `cookieDomain`, `dbPath` (Phase A/B store; empty ⇒ in-memory).
- **Wiring** `cmd/ogcode-control-plane/main.go` — cobra `serve`: load config → bbolt store (Phase A; a corrupt DB is moved aside + the gate comes up **Locked**, not open, main.go ErrCorrupt path) → registry + pairing + bus → operator Gate (Accounts/Legacy/Disabled/Locked) → Connect handler + `/healthz` → `UIProxyHandler` mux → TLS (hot-reload) or h2c → `ReapLoop` → serve until SIGTERM → graceful `http.Server.Shutdown`. `users add|rm|list` root subcommands share the same `usersDBPathConfig` default config path.

### 2.2 The worker — `internal/worker/` (ogcode module)

- **`ogcode worker`** `internal/cli/worker.go` — flags: `--master` (required), `--name`, `--workspace` (repeatable), `--pairing-secret-file`, `--serve-addr` (default `127.0.0.1:9595`), `--master-ca`, `--insecure`. The pairing secret comes from `$OGCODE_PAIRING_SECRET` or a file — **never the process arguments** (:36-37, the pattern to reuse elsewhere).
- **Daemon** `internal/worker/worker.go` — `Run`: build client (h2 over TLS with optional CA bundle, or h2c for dev; `buildClient`) → resume a **stored token** if present + unexpired else `register` (:191-212 — presents the persisted worker id + pairing secret + discovered workspaces; stores the returned token via `persistCred`) → open `WorkerStream` → `Hello{token}` → heartbeat loop + tunnel loop + receive loop dispatching commands. **Reconnect loop** (Phase C): on stream failure `Run` retries with capped backoff (1 s → 30 s) instead of exiting; on `Unauthenticated` it re-registers with the pairing secret (re-pair fallback); an auth-failed heartbeat sets the reauthenticate flag and tears the stream down rather than retrying forever. The token + its expiry persist to `~/.ogcode/worker-cred` (`cred.go`), so a master restart recognizes the worker without re-pairing as long as the token hasn't expired. Line anchors below moved with the Phase C rewrite — read the file for current positions.
- **Session host** `internal/worker/env.go` + `session.go` — one `env` per workspace dir (project DB `.ogcode/ogcode.db`, global config DB for provider keys, provider/tool/skill/MCP wiring mirroring `runPrompt`, one shared `LoopRunner`); `buildProviderRegistry` (:138-206) replicates the headless provider resolution. `startAgent` (`worker.go:273-308`) writes the session + initial user rows with the **master-supplied global session id** (`createSessionRows`, `session.go:62-107`), cancels via a per-session registry. `startRelay` (:326-351) forwards every bus event up the stream as a `SessionEvent` (session id parsed from the event payload; worker seq deliberately dropped). `shutdown` cancels all hosted sessions and tears down envs.
- **Workspace discovery** `internal/worker/workspaces.go` — `discoverWorkspaces(roots)`: each configured root plus every worktree linked from it (`git worktree list --porcelain`), absolute + de-duplicated, with `present` flag and branch. (`internal/git` has only `CreateTaskWorktree`/`RemoveTaskWorktree`, `git.go:26/71` — enumeration is deliberately worker-side code.)
- **Stable identity** `internal/worker/workerid.go` — mints + persists a DNS-safe worker id to `~/.ogcode/worker-id` once; the master adopts it verbatim or rejects a malformed one, so the panel subdomain `<id>.<host>` survives restarts.
- **Tunnel client** `internal/worker/tunnel.go` — opens one byte pipe per worktree server (first frame carries token + `route` = `worktreeLabel(dir)`; master keys `<workerID>-<route>`) and splices each yamux stream to that server's in-process port; `controlplane/tunnel/conn.go` is the shared yamux conn helper both sides import. Tunnels (and the servers they serve) open **eagerly at connect time**: `runSession` launches `ensureEagerTunnels` right after the Hello, which spawns a per-dir server for every discovered workspace and wires tunnel + event relay via the shared `tunnelAndRelayOnce` dedupe — `StartAgent` calls the same helper, so a dir wired eagerly is never wired twice, and a dir that failed the eager pass still gets wired when a session arrives.

### 2.3 What does not exist yet

bbolt persistence (registry + accounts), per-employee login, worker reconnect, git bootstrap, permission/guidance/abort over the stream (stubs), boot recovery, resource-frame emission (proto message exists; worker never sends one), worker-side title generation, and the per-worktree server hosting (Phase 2: today the tunnel proxies a *separately started* `ogcode serve`, which shares the workspace DB but cannot see the daemon's in-memory permission state or control its loops). (*Worker reconnect + token persistence shipped since this sentence was written — Phase C; standalone-server lifecycle shipped — Phase 1; per-worktree in-process hosting + per-worktree tunnels with route keying shipped 2026-09-11 — the "separately started serve" caveat no longer applies.*)

The Phase 2 hosting is the one full standalone `ogcode` server per worktree, hosted **in-process** by the worker daemon (`internal/worker/servers.go` serverManager) — not a `ogcode serve` subprocess. StartAgent does `bootstrapWorkspace` → `srvs.ensure(dir)` → first-tunnel-per-dir (`SubscribeEvents` relay + `openTunnel` with route label) → `srv.HostSession(...)` on that server. Guidance/ApproveTool/RejectTool commands resolve the session's server and call its exported `Guidance`/`ReplyPermission` methods; `loop.done` bus events drive the worker's session registry.

---

## 3. Architecture (as shipped)

```
┌─────────────────────────────────────────────────────────────────────────┐
│              ogcode-control-plane  (master, nested module)              │
│                                                                         │
│   Apex console (server-rendered, operator-gated)   panel.go             │
│      └─ list of connected workers → <id>.<host> links                  │
│                                                                         │
│   ConnectRPC service (worker-authenticated)        server.go            │
│      Register · Heartbeat · WorkerStream(bidi) · Tunnel(bidi)          │
│                                                                         │
│   Registry (in-memory; Phase A → bbolt)            registry.go          │
│      workers / tokenIndex / sessions                                   │
│                                                                         │
│   Operator gate (single password today;            auth.go              │
│   Phase B → per-employee accounts)  +  bus (relay target)              │
└───────────────▲──────────────────────────────────────────────▲──────────┘
                │ WorkerStream (commands ↓, events/results ↑)  │ Tunnel
                │ ConnectRPC over HTTPS/2 (h2c dev only)       │ (yamux)
┌───────────────┴──────────────────────────────────────────────┴──────────┐
│                          ogcode worker (run-mode)                       │
│                                                                         │
│   registers (pairing secret → token) · heartbeats (15 s)                │
│   hosts sessions per workspace: env = DB + bus + LoopRunner +           │
│   provider/tool/skill/MCP (mirrors runPrompt)                           │
│   relays bus events ↑ as SessionEvent (master re-stamps fresh seq)      │
│   tunnels browser traffic → local ogcode UI on 127.0.0.1:9595           │
└──────────────────────────────────────────────────────────────────────────┘
```

**Data flow for one remote session (shipped)**

```
Operator → apex console → StartAgent(workspace)
  master: StartRemoteAgent mints global ses_ id → Call(StartAgent) down the
          worker's stream → on ok, RouteSession(sessionID, workerID)
  worker: createSessionRows(master-supplied id) → LoopRunner.RunLoop(ctx, …)
  loop events → worker bus → startRelay → SessionEvent →
  master: PublishRaw onto its own bus with a FRESH master seq (bus.go:71);
          the worker's seq is intentionally not carried (proto SessionEvent
          comment) — one numbering must cover the mixed master-local +
          relayed stream or the panel's gap-resync would fire on every event
  operator UI: the browser talks to the WORKER's ogcode UI via the tunnel
          (<id>.<host>), which serves its own SSE — the master is not in
          that path (tunnel.go flushes immediately)
  worker dies: ReapLoop → ReapStale → failSession publishes a synthetic
          session.failed on the master bus and unrouts
```

**What the master bus is for today**: the relay lands on it, and it is the natural source for any *master-side* surface (an activity feed on the apex console is the obvious v2 use). Nothing subscribes yet; no SSE endpoint exists on the master. That is fine — the tunnel already gives operators a live UI per worker, and the relay keeps a single global event numbering available for whatever master-side surface comes later.

---

## 4. Configuration

### 4.1 Master — `control-plane.json` (actual schema; `controlplane/internal/config/config.go`)

```jsonc
{
  "master": {
    "listen": ":8443",                     // required
    "pairingSecret": "…",                  // required, constant-time compared
    "tls": { "cert": "…", "key": "…" },    // absent → h2c (dev only, loud warn)
    "tokenTtlSeconds": 900,                // worker-token TTL
    "workerTimeoutSeconds": 45,            // missed-heartbeat → dead
    "sessionTtlSeconds": 43200,            // operator login TTL (12 h)
    "cookieDomain": ".panel.example",      // one login covers worker subdomains
    "dbPath": "~/.ogcode-control-plane/controlplane.db"  // Phase A (bbolt)
    // "operatorPassword" is REMOVED in Phase B (per-employee accounts);
    // one release of coexistence — see Phase B.
  }
}
```

There is no `master`/`worker` key in ogcode's `ogcode.json` and none is planned: the master is a separate binary with its own config file, and the worker is configured entirely by CLI flags + env (`ogcode worker --master … --workspace …`). The earlier revision's `MasterConfig`/`WorkerConfig` structs in `internal/config/config.go` belong to the superseded design and are dropped.

### 4.2 Worker — CLI flags (shipped; `internal/cli/worker.go:42-51`)

`--master` (required) · `--name` · `--workspace` (repeatable) · `--pairing-secret-file` (or `$OGCODE_PAIRING_SECRET`) · `--serve-addr` (loopback UI for the tunnel; Phase F changes this to the worker's *built-in* UI) · `--master-ca` · `--insecure`.

Employee accounts are **not** worker configuration — they live in the master's bbolt `users` bucket, seeded by `ogcode-control-plane users add|rm|list` (Phase B). **A5 fix: there is no `--promote-admin` flag and no admin concept in v1** — v1 is identity-only (§9); if account administration itself is later restricted to admins, that is the RBAC future work, and it will need an enforcement surface this plan deliberately does not define.

---

## 5. Remaining phases

> Each phase lists the shipped code it builds on. **bbolt + `golang.org/x/crypto` are absent from `controlplane/go.mod`** — Phase A/B add them (everything else needed is already a dependency).

### Phase 1 — Standalone-server lifecycle (prerequisite for per-worktree hosting) — **SHIPPED (2026-09-11)**

Makes `internal/server` constructible and drivable N times per process, so the worker can host one full server per worktree (the confirmed architecture above). Everything else in this phase was already per-dir by construction in `server.New(dir)`/`Start` — no consolidation of per-dir state was needed or done.

- **`Serve(ctx)` replaces `Start()` as the lifecycle entry** (`internal/server/server.go`): runs until ctx cancel, SIGINT/SIGTERM, or `Stop()`, then performs the same graceful shutdown (10 s drain, MCP teardown, DB close). `Start()` is gone; the only prior call site (`internal/cli/root.go`, `serveWithMode`) now passes `context.Background()`, so standalone `ogcode serve` behavior — including dying on a signal — is unchanged. A second `Serve` on the same Server is rejected fast (pinned by test).
- **`Stop()`** — programmatic, goroutine-safe shutdown request; `Serve` returns nil after the graceful drain completes. Signal handling stays inside `Serve` (a `stopCh` separates it from the programmatic path so a signal meant for one component can't tear down another).
- **`NewWithOptions(port, dir, mode, Options)`** — `Options{NoBrowser, Loopback}`: `NoBrowser` suppresses `openBrowser` (the worker must not open a tab per worktree); `Loopback` binds `127.0.0.1` only (the master's tunnel is the only remote path to a hosted server — §9's loopback rule now enforced at the bind, not just by convention). Zero-value `Options` via `New` reproduces interactive `serve` exactly.
- **Actual bound port surfaced** (`Server.Port()`): the bind loop previously recorded `tryPort` — with `port: 0` the kernel-assigned value was never stored, and a worker that dials the tunnel per worktree needs the real number. Now read from `listener.Addr()` after bind. Found by the lifecycle test, not by review.
- **Tests** (`internal/server/serve_lifecycle_test.go`, run against the real server with a real listener): loopback bind + `/api/config` 200 + `Port() != 0` + clean ctx-cancel return; `Stop()` from another goroutine returns nil; double-`Serve` fails fast.

Files: `internal/server/server.go` (`Serve`/`Stop`/`Port`/`Options`/`NewWithOptions`), `internal/cli/root.go` (call site), `internal/server/serve_lifecycle_test.go` (new).

### Phase A — bbolt registry persistence

Persist the registry's three maps (`workers`, `tokenIndex`, `sessions` — `registry.go:58-64`) so a master restart restores the routing table and each worker's current token.

- **New** `controlplane/internal/registry/store.go`: a bbolt store with buckets `workers`, `tokens`, `sessions`, (Phase B adds `users`). One DB file at config `dbPath` (default `~/.ogcode-control-plane/controlplane.db`, mode `0600`, mirroring ogcode's own `~/.ogcode/ogcode.db`). API: `Load() (*Snapshot, error)`, `Put/Get/Delete` per bucket, `BackupTo` for graceful shutdown. Values are plain JSON blobs — no ORM, no schema migration machinery for v1.
- `Registry` threads a `*store.Store` through `Add`/`UpdateToken`/`RouteSession`/`UnrouteSession`: every mutation is written to bbolt **inside the same mutex** as the in-memory map (the maps stay the read path; bbolt is the write-behind). Live-only state — `status`, `lastSeen`, the `conn` handle — never persists.
- **Restore semantics (get these right — a wrong restore turns a restart into a mass session-failure)**: on boot, restore workers with `StatusOffline` and **`LastSeen = now` (boot time)**, not the persisted timestamp. The persisted `LastSeen` is in the past by definition; `ReapStale` (`registry.go:279`) would otherwise immediately mark every restored worker Dead and `failSession` every routed session before the workers re-register. `conn` restores nil.
- **Re-pair interaction**: a worker that re-registers after a master restart presents its **pairing secret** (there is no pre-restart-token path today — see Phase C). `Add` (`registry.go:77-95`) replaces the worker entry and re-keys `tokenIndex`; it leaves `sessions` untouched, so restored routes stay valid across re-pairing. Persist both effects: the new token index entry and the deletion of the replaced token's entry.
- **Boot policies for a corrupt file (A2 fix — buckets are not interchangeable)**:
  - **Registry buckets corrupt/lost → start empty, log loudly.** Correct and self-healing: workers re-register themselves and re-create registry state by connecting. (The registry is the one bucket whose contents re-derive from the field.) Move the corrupt file aside (`controlplane.db.corrupt-<unix>`) so it can be inspected, and `BackupTo` nothing from it.
  - **The `users` bucket lost/empty after corruption → refuse to serve the operator-gated surfaces** (worker UI proxy + apex console login) and say so at boot. Accounts cannot re-create themselves; silently degrading bcrypt-gated access to open access as a *recovery mode* is the failure mode this rule exists to prevent. Recovery is a human step: `users add` the first account.
  - **Healthy file, empty `users` bucket at true first boot → run open with a loud warning** (mirrors today's `operatorPassword == ""` path, `main.go:76-84`) so a fresh install isn't bricked. Distinguishing "first boot" from "lost accounts" is exactly the corrupt-file rule above; the flag-free version is acceptable because corruption is detectable and handled by the previous rule.
  - **`users rm` of the last account refuses without `--force`**, which prints the same "operator UI now open" warning the first-boot path prints.
- Shutdown: `BackupTo` a single `.bak` beside the DB after the registry drains (best-effort; bbolt transactions are the real durability story — the backup only shortens a forensic recovery).

Files: `controlplane/internal/registry/store.go` (new), `registry.go` (thread the store + `Restore`), `store_test.go` (persist→reload round-trip + restore/re-pair contracts), `cmd/ogcode-control-plane/main.go` (open at boot, restore, corrupt-file recovery, close + backup on shutdown), `internal/config/config.go` (`dbPath`, defaulted), `go.mod` (`go.etcd.io/bbolt`).

### Phase B — Per-employee accounts (operator login) — **SHIPPED**

Replace the single shared `operatorPassword` with accounts in the same bbolt store. Everything below is implemented and tested (`controlplane/internal/accounts_test.go`, `cmd/ogcode-control-plane/users_test.go`); the bullets are kept as the design record.

- **Bucket**: `users` — `username → {bcryptHash, createdAt}`. Seeded/managed by `ogcode-control-plane users add|rm|list`.
- **Gate rework** `controlplane/internal/auth/auth.go`: `Gate` takes a store-backed checker instead of a raw password. `Enabled()` = accounts exist (or accounts are explicitly not required). The login endpoint (`/…/login`, currently password-only in `master/auth.go`) accepts `username`+`password` and issues the existing HMAC cookie with the payload extended to `expiry|userID` (`base64url(expiry|userID).base64url(hmac(payload))`).
- **A1 fix — validate-time user-existence check (this is the correctness core of the feature)**: `validate` keeps the HMAC + expiry checks and adds **one bbolt `users` existence lookup for the cookie's userID**. Without it, a cookie signed under the old "stateless" design stays valid until expiry (default **12 h**) after `users rm` deletes the account — a fired employee keeps operator access for up to half a day. The "the cookie carries identity + expiry, this is still stateless" claim from the earlier revision is **retracted**: validate now consults the store, one read per request, which is exactly the price of "rm breaks that user's sessions *immediately*". If the lookup errors (store unavailable), **fail closed**. Do **not** "fix" restart-logout by persisting the signing key (next bullet) instead of this check — a persisted key without the existence check is precisely the A1 hole.
- **Signing key stays random per-process (A6)**: `Gate.signingKey` (`auth.go:30`) keeps its shipped behavior — every master restart logs every operator out. State this in the plan so nobody "improves" it by persisting the key and reopening the deleted-user window. Revisit only alongside a session-revocation design.
- **A3 fix — constant-time unknown-user path**: comparing bcrypt hashes costs real CPU; an unknown user with no hash to compare rejects instantly, so response timing enumerates valid usernames. On unknown user, compare the presented password against a **dummy bcrypt hash precomputed at Gate construction** (bcrypt of a random secret), then reject. The "wrong password or unknown user rejected in constant time" test is only honest with this in place.
- **A6 fix — the raw password never appears in argv**: `users add <name>` reads the password from `$OGCODE_CONTROL_PLANE_USER_PASSWORD` or `--password-file` (mirroring the worker's `OGCODE_PAIRING_SECRET`/`--pairing-secret-file` pattern, `internal/cli/worker.go:36-38`). Positional-argv passwords leak via `ps` and shell history.
- **`operatorPassword` removal with one release of coexistence**: if `operatorPassword` is set **and** the `users` bucket is empty, keep the legacy single-password gate and log a deprecation warning pointing at `users add`; once the bucket is non-empty, accounts win. Remove the field the release after. Until then the shipped warn path (`main.go:76-84`) stays the zero-account behavior.
- **`users rm` deletes and immediately breaks that user's sessions** — true *only because* of the A1 lookup; the §7 test asserts exactly that.

Files: `controlplane/internal/auth/auth.go` (store-backed gate), `auth_test.go`, `cmd/ogcode-control-plane/main.go` (`users add|rm|list` + boot policy), `internal/master/auth.go` + `tunnel.go` (login form carries username), `internal/config/config.go` (drop `operatorPassword` per the coexistence rule), `go.mod` (`golang.org/x/crypto` for bcrypt).

### Phase C — Worker reconnect + token persistence (honest scope) — **SHIPPED**

Everything below is implemented and tested (`internal/worker/cred.go` + `cred_test.go`, `internal/worker/reconnect_test.go` — the worker's reconnect/expire/re-pair transitions against an h2c fake master, run under `-race`); the bullets are kept as the design record. The design note still stands: worker *identity* already survived restarts via the persisted worker id + master adopt-or-reject; this phase adds the **routing-table benefit across a master restart** and resilience/backoff. No master/proto changes were needed — `Store.Restore` already repopulated `snap.Tokens`, and `WorkerStream`/`Heartbeat`/`Tunnel` resolve tokens via `WorkerIDForToken`.

- **Persist the current token + expiry** (`internal/worker/cred.go`, new — same pattern as `workerid.go`: atomic temp+rename, `0600`, under `~/.ogcode/`). On boot with a stored, unexpired token: skip `Register` and open the stream with `Hello{token}` directly.
- **Reconnect loop** wrapping `Run`: on stream failure (not ctx cancel), retry with capped backoff (1 s → 30 s) instead of exiting — so an ogcode-worker under a supervisor no longer treats every master blip as a process death.
- **Re-pair fallback**: on `Unauthenticated` (unknown **or expired** token) — from `Hello`, `Heartbeat`, or `Tunnel` — tear down and run full `register()` with the pairing secret. This is mandatory, not optional: `tokenTtlSeconds` defaults to **900 s**, so a master down longer than 15 minutes guarantees re-pairing regardless of persistence; and the master's `ReapStale`/expiry paths reject stale tokens by design.
- **Fix the heartbeat auth-failure hole**: `heartbeatLoop` currently logs auth failures and continues forever (:225-228) — correct only while the process exits on stream death anyway. With a reconnect loop, an auth-failed heartbeat must trigger the re-pair path, not an infinite retry.
- **Token store on the master**: `UpdateToken` writes through to bbolt (Phase A), so a *master* restart re-recognizes a worker presenting its pre-restart token — the one part of the original restart story that becomes true, and only when both sides ship.

Files: `internal/worker/cred.go` (new), `worker.go` (reconnect loop, re-pair fallback, heartbeat fix), tests for reconnect/expire/re-pair transitions.

### Phase D — Git bootstrap

**Shipped.** `StartAgent.ref` exists in the proto; the worker rejects absent workspaces (`worker.go`, "provide a git ref to clone it").

- When `workspace` is absent and `ref` is present: shell out to the worker's `git clone` — respect the host's credential helper entirely; **no ogcode-level git auth** (private repos defer to whatever the worker machine already has). The "or a configured repo URL" option in the plan is not wired: `Options` has no repo-url field, so bootstrap relies solely on `StartAgent.ref`.
- Absent workspace with no way to bootstrap → `CommandResult{ok:false}` with the clone hint (the proto already documents this slot, `CommandResult.error` comment).
- **Timing decision (pinned by the tests)**: the clone runs *synchronously* inside `startAgent` and replies `ok` only after it finishes, in the StartAgent handler goroutine (the receive loop is already insulated). This matches the plan's configurable-timeout option: the command timeout is already configurable master-side (`opts.CommandTimeout`). The async+stream-progress option (reply `ok` early, emit `SessionEvent`s) was rejected for v1 — it would require restructuring the synchronous reply pattern and risks routing sessions that never start.

Files: `internal/worker/bootstrap.go` (`bootstrapWorkspace`/`cloneRepo`), `worker.go` (startAgent branch), `internal/worker/bootstrap_test.go`.

### Phase E — Interactive worker-hosted sessions — **SHIPPED**

The worker hosts headless sessions today. The remaining gap is *control*: guidance, abort-of-turn, and tool approvals.

- **Permission gating** (**shipped**): hosted sessions now carry `agent.WithPermissionGating(ctx)` + a per-env `permission.Manager` (mirroring `server.go:352`), and the `ApproveTool`/`RejectTool` commands call the manager the loop blocks on — the same seam `permission_routes.go` uses locally (`Reply` on the `PermissionID`). `startAgent` registers the `LoopControl` on the `hostedSession`; the registry's `loopControl`/`dirOf` let the command handler reach it. `replyToPermission` (`worker.go:449`) resolves the env via the session's dir and calls `Manager.Reply(id, "once"|"reject")`.
- **Guidance** (**shipped**): `Guidance` command → the session's `LoopControl` (ephemeral, `loop_control.go:14`). Empty text is a no-op; populated text is pushed **first** then the in-flight stream/tool is cancelled so the loop advances to drain it. Unknown sessions get a distinguishing error ("session is hosted but not running a gated loop" vs "session not hosted here").
- **Abort** (**shipped**): `AbortSession`/`StopAgent` already routed to `sessions.cancel` (kills the loop ctx, which unwinds any in-flight tool call); unchanged by Phase E.
- **Boot recovery** (**shipped**): `recoverInterruptedSessions` (`internal/worker/recover.go`) — the server's `recoverInterruptedSessions` (`resume_routes.go:103-140`) ported to the worker. Runs once per env build in `envFor` (repairs each workspace's store via `LoopRunner.ReconcileSession`). Same repair-only semantics: a crashed turn (no finish reason, possibly an unanswered tool call) is marked resumable — **never auto-resumes**; that remains the operator's call via the tunneled UI's resume button.
- **Title generation** (**shipped**, `internal/worker/title.go`): the worker generates titles for hosted sessions worker-side (worker provider, worker DB) so remote sessions get titles at all, even though their DB never touches the master. Runs concurrently with the loop; failures are logged and it never clobbers a title changed since creation.
- **Resource frames (optional)**: left as-is — the proto carries `ResourceFrame` and the master consumes it as a no-op hook (`server.go:246-248`); the worker never emits one, and it stays that way until a master-side aggregate surface (apex activity feed) lands. The tunnel already delivers the worker UI's own resource view.

Files: `internal/worker/session.go` (registry gains `loopControl`/`has`/`dirOf` + `lc` on `hostedSession`), `internal/worker/recover.go` (new), `internal/worker/title.go` (new), `env.go` (per-env `permission.Manager`), `worker.go` (command stubs filled), `internal/worker/{session,recover}_test.go` (new).

### Phase F — Operator interaction model (decision point, recommendation included)

The original Phase 4 (master-side REST proxy + SSE relay + a `web/src` "Workers" section) was designed when the master *was* the ogcode server. It isn't: the master is the standalone control plane, and the shipped operator path is the **tunnel to each worker's own UI**. Two coherent ways to finish the interaction story:

- **(a) Plan of record — the worker hosts one full ogcode server per worktree** (realized by Phase 2; the mechanism Phase 1 shipped). Each worktree gets `server.New(worktreeDir, Options{Loopback, NoBrowser})` + `Serve(ctx)` in its own goroutine: approvals, aborts, guidance, titles, resume, and the resource view all work natively through the tunnel with **zero new RPCs and zero REST-proxy code** — the tunneled server IS the session host, not a second process sharing its DB. This eliminates today's two-writers-to-one-SQLite awkwardness entirely (the daemon env and the UI server were separate writers on the same workspace DB; now there is one server per dir, full stop). It also **supersedes most of Phase E's custom control RPCs** (`Guidance`/`ApproveTool`/`RejectTool`): the operator drives the real UI through the tunnel, which has native permission routes. The `SessionEvent` relay stays for master-side surfaces. Isolation is total: per-dir DB, bus, MCP, skills, providers, permission manager.
- **(b) Master-side panel** (the old Phase 4): build REST-read proxies and an SSE relay into the control plane plus a `web/src` section. Only justified if operators must **never** reach worker UIs directly (hostile-network posture, single aggregated panel requirement). The SSE relay's contracts are already proven (fresh master seqs, control-frame pass-through) — but the panel work is a second UI and a second API surface, for a need v1 does not have. **Deferred with (a) as the plan of record; promote (b) only on an explicit requirement** (§9, §11 Q1).

Files (option a, Phase 2 — **shipped as written, minus the buildAgentEnvironment note which is moot**): `internal/worker/servers.go` (per-worktree server ownership: spawn, `Port()` capture, tunnel dial target, ctx-cascade shutdown), `internal/worker/tunnel.go` (one tunnel per worktree server, first-frame `TunnelChunk.route`), master route keying `tunnelKey(workerID, route)` in `controlplane/internal/master/tunnel.go` (`<worker>-<label>`, split on first hyphen). One deliberate addition: the stream RPCs `Guidance`/`ApproveTool`/`RejectTool` were kept and now **proxy into the per-dir server's exported methods** (`Server.Guidance`/`Server.ReplyPermission` in `internal/server/host_session.go`) — this preserves master-side control surfaces (and future headless callers) without REST-proxy code; the tunneled UI still drives the same server natively.

**Phase F — SHIPPED 2026-09-11.** The remaining piece — "freshly connected worktrees should be visible and reachable before any session starts" — is the eager pass: `runSession` launches `ensureEagerTunnels` (worker.go) immediately after the Hello, which for each discovered workspace spawns the per-dir server (`serverManager.ensure`) and opens the tunnel + event relay through `tunnelAndRelayOnce` (the LoadOrStore-deduped wiring previously inline in `startAgent`; `startAgent` now calls the same helper, so late-created or eagerly-failed dirs still get wired on first StartAgent without double-wiring). The `internal/worker/uiserver.go` / `internal/cli/buildenv.go` bullets below are moot — Phase 2 made the per-dir server the UI host, and the wiring it needs already lives in `servers.go`. Acceptance pinned by `internal/worker/eager_tunnel_test.go`: `TestRun_OpensEagerTunnelsForDiscoveredWorkspaces` (server + exactly one authenticated tunnel per discovered dir at connect time, before any StartAgent), `TestTunnelSplicesToLocalWorktreeUI` (a real HTTP GET driven through the tunnel from the master side is answered by the worktree's **native** `/api/config` reporting that worktree's directory — the F(a) claim, no special-cased UI server), and `TestStartAgent_AfterEagerTunnelDoesNotDuplicateTunnel` (StartAgent on an eagerly-wired dir stays at one handshake).

### Phase G — Security hardening (spread across A–F, verified here) — SHIPPED 2026-09-11

- **Shipped and pinned by tests**: TLS with h2 via ALPN (h2c only with a loud dev warning, `main.go:131-134`); pairing secret constant-time compared (`pairing.go:30`); 32-byte random worker tokens, 15-min TTL, rotated near expiry (heartbeat rotation path in `server.go:156-164`); operator gate enforced on every proxied worker-subdomain request with the login/logout paths exempted (`tunnel.go:169-187`); RPC endpoints authenticated by pairing secret/worker token, deliberately **not** by the operator login (Tunnel/WorkerStream first-frame auth); control-plane DB `0600`; tokens persisted worker-side at `0600`.
- **Verification pass (2026-09-11) added the missing pins**:
  - Worker-subdomain operator gate, four tests in `tunnel_internal_test.go`: `TestUIProxy_WorkerSubdomainGated` (unauthenticated HTML → login page, non-HTML API fetch → 401 "operator login required", the tunneled origin is never contacted), `TestUIProxy_WorkerSubdomainLoginLogoutExempt` (login/logout reachable ON the subdomain while gated; the issued cookie authenticates the proxied UI; wrong password → 401 form; logout re-gates), `TestUIProxy_WorkerSubdomainAuthenticatedPassesThrough` (valid cookie → both navigation and API fetch reach the origin — the gate must not over-block), `TestUIProxy_WorkerSubdomainDisabledGatePassesThrough` (no operator configured → worker subdomains serve unauthenticated).
  - Concurrent two-worker routing under `-race`: `TestWorkerStreamRoundTrip_ConcurrentWorkers` (`server_test.go`) — two workers streaming at once, ListWorkspaces + StartRemoteAgent issued concurrently for both, each answered/routed by the right worker (per-request_id pending-command table holds under `-race`).
  - Control-plane DB privacy: `TestOpen_CreatesFilePrivate` (`registry/store_test.go`) — `Open` creates the bbolt file `0600` (token index inside is a bearer credential).
  - Tunnel first-frame auth at the wire boundary: `TestTunnel_FirstFrameRejects` (`server_test.go`) — a bogus token and a known-but-expired token are both refused with `Unauthenticated` before any yamux session is registered; valid-token happy path pinned worker-side in `eager_tunnel_test.go`.
- **Trust model, stated precisely (unchanged from the original plan)**: the worker is read-only w.r.t. the master — commands are start/stop/approve/guidance, never arbitrary execution. `bash.go` sets `cmd.Dir` to the session's working directory; that is **not containment**, and `bash_safety.go` says outright that the denylist is "NOT a sandbox." Inside a session, the agent is as powerful as a local ogcode on that machine; the worker boundary protects the *master and other workers*, not the worker's own filesystem from its own sessions.
- **New in A–F**: bcrypt + per-user salted hashes; login timing parity (A3); validate-time existence check (A1); corrupt-file fail-closed policy for `users` (A2); password never in argv (A6).
- Every control-plane handler stays owned, small, and audited — no opaque framework between the request and the decision.

---

## 6. File change summary

### New files

| Path | Contents |
|---|---|
| `controlplane/internal/registry/store.go` | bbolt persistence: `workers`/`tokens`/`sessions` (+ Phase B `users`) buckets; load/restore/write-through/backup |
| `controlplane/internal/auth/users.go` | `users` bucket access + bcrypt check + dummy-hash constant-time path |
| `internal/worker/cred.go` | worker-side token+expiry persistence (atomic, `0600`) — **shipped (Phase C)** |
| `internal/worker/bootstrap.go` | git clone bootstrap for absent workspaces — **shipped (Phase D)** |
| `internal/worker/recover.go` | boot recovery (port of `recoverInterruptedSessions`) — **shipped (Phase E)** |
| `internal/worker/title.go` | worker-side session title generation — **shipped (Phase E)** |
| `internal/worker/uiserver.go` | ~~embedded `internal/server` on `--serve-addr` (Phase F option a)~~ — **moot, never built**: Phase 2 made the per-dir in-process server the UI host |
| `internal/cli/buildenv.go` | ~~shared `buildAgentEnvironment(dir)` extracted from `run.go` / `env.go` / server wiring~~ — **moot, never built**: per-dir servers own their wiring; no shared builder needed |

### Modified files

| Path | Change |
|---|---|
| `controlplane/internal/registry/registry.go` | write-through to the store in `Add`/`UpdateToken`/`RouteSession`/`UnrouteSession`; restore-time `LastSeen` reset |
| `controlplane/internal/auth/auth.go` | store-backed per-employee login; cookie payload `expiry|userID`; validate-time existence check |
| `controlplane/internal/master/auth.go` + `tunnel.go` | login form carries `username`; gate `Enabled()` semantics |
| `controlplane/cmd/ogcode-control-plane/main.go` | open bbolt at boot, restore registry + users, close + backup on shutdown; `users add|rm|list` subcommand; corrupt-file boot policy |
| `controlplane/internal/config/config.go` | `dbPath` (defaulted); `operatorPassword` removed after the coexistence release |
| `controlplane/go.mod` | `go.etcd.io/bbolt`, `golang.org/x/crypto` |
| `internal/worker/worker.go` | reconnect loop + re-pair fallback + heartbeat fix (**shipped Phase C**); startAgent bootstrap via `bootstrapWorkspace` (**shipped Phase D**); guidance/approve/reject commands + `replyToPermission`/`envForSession` (**shipped Phase E**) |
| `internal/worker/session.go` | registry gains `loopControl`/`has`/`dirOf`; `hostedSession.lc` (**shipped Phase E**) |
| `internal/worker/bootstrap.go` + `bootstrap_test.go` | `bootstrapWorkspace`/`cloneRepo` + fixtures pinning the sync clone behavior — **shipped Phase D** |
| `internal/worker/reconnect_test.go` | h2c fake-master harness pinning the Phase C transitions — **shipped** |
| `internal/worker/env.go` | per-env `permission.Manager` wired into the LoopRunner (**shipped Phase E**) |
| `internal/worker/{session,recover}_test.go` | guidance/approve/reject + boot-recovery fixtures — **shipped Phase E** |
| `internal/cli/worker.go` | ~~`--serve-addr` semantics (built-in UI)~~ — moot: `--serve-addr` was removed with Phase 2's per-dir servers |

### Deliberately NOT built

- No new interface to `tool.ToolDef` / `Registry` / `agent.RunLoop` — transport-agnostic today and stays that way.
- No changes to `bus.Bus` on either side — the relay rides `PublishRaw` (which exists precisely for already-marshaled payloads) and re-stamps fresh seqs; control frames (`ResourceFrame`) never touch the bus on either leg (`event_routes.go:48-63` locally, the no-op hook on the master).
- No master-side SSE endpoint or REST proxy (Phase F option b deferred — the tunnel serves the operator UI today).
- No roles/ACL tables (Phase B is identity-only; see §9).

---

## 7. Testing strategy

Existing suites already cover the shipped surface (`registry_test.go`, `panel_test.go`, `server_test.go`, `workspaces_test.go`, `workerid_test.go`, pairing/auth/bus tests). New tests, per repo convention `-race` where concurrent:

- **Registry persistence (A)**: seed `Add`+`UpdateToken`+`RouteSession`, close, reopen the same bbolt file, assert workers/tokens/routes restored **and** that `status` comes back `offline` with `LastSeen` reset to boot (pin the ReapStale-on-restore hazard: a restored registry must not reap+fail its own routes on the first tick). A re-`Add` (re-pair) after restore rotates `tokenIndex` and **preserves** session routes (pin the `Add`-leaves-`sessions`-alone contract). Tests use tempfile DBs; nothing touches `~/.ogcode-control-plane/`.
- **Employee accounts (B)**: `users add` stores a bcrypt hash (assert the raw password never appears in the DB file); login with the right username+password issues a cookie whose payload decodes to that user id; **wrong password and unknown user are rejected in comparable time** (dummy-hash path — assert the unknown-user path performs a bcrypt compare); **`users rm` breaks that user's live sessions immediately** (the A1 test — only honest because validate consults the store); account survives store close→reopen; rm-last-account refuses without `--force`; **corrupt-file boot refuses the operator-gated surfaces** while the registry path starts empty (both A2 rules, tested against a byte-truncated DB); first-boot-empty warns and opens.
- **Worker reconnect (C) — shipped**: `internal/worker/reconnect_test.go` drives the worker against an h2c fake master: kill the stream → worker reconnects with the persisted token **without** re-presenting the secret (register-count assertion); expired token → falls back to full re-pair; heartbeat auth failure sets the reauthenticate flag and tears the stream down rather than infinite warn-and-continue (the fix for the old `heartbeatLoop` hole); token file is `0600` and round-trips (`cred_test.go`). Run under `-race`.
- **Git bootstrap (D)**: `StartAgent` with an absent workspace and a `ref` → clone into place (fixture repo), then host; absent + no ref → `ok:false` with the clone hint.
- **Interactive hosting (E) — shipped**: `session_test.go` pins guidance for a hosted session (empty text is a no-op, populated text is pushed + cancels the in-flight stream/tool via the LoopControl), approve unblocks the waiting `requestPermission`, `replyToPermission` errors cleanly on unknown ids/unhosted sessions, and the registry's `loopControl`/`has`/`dirOf`. `recover_test.go` pins boot recovery: a crashed turn (dangling tool call, no finish reason) is closed + claimed resumable; a naturally-finished session is untouched; a non-resumable type (task) is skipped.
- **Relay contracts (already shipped — keep pinned)**: worker publishes N bus events → master re-publishes with **fresh master seqs** (`bus.go:71`); resource frames arrive as control frames and never bump a seq.
- **Eager tunnels (F) — shipped**: `internal/worker/eager_tunnel_test.go` drives the worker through `Run` against the fake master (extended with a `Tunnel` handler that records the auth+route first frame): a discovered workspace gets its per-dir server + exactly one authenticated tunnel at connect time, **before any StartAgent**; the F(a) acceptance drives a **real HTTP GET through the tunnel** from the master side (yamux client over the bidi stream) and asserts the worktree's native `/api/config` answers with that worktree's directory; a subsequent StartAgent on the same dir adds no second handshake. Run under `-race`.
- **Concurrency (G) — shipped**: `TestWorkerStreamRoundTrip_ConcurrentWorkers` (`server_test.go`, run under `-race`) registers two workers, streams both, and issues ListWorkspaces + StartRemoteAgent for both concurrently — each result comes from the right worker and each session routes to the right worker.
- **Security hardening (G) — shipped**: worker-subdomain operator gate (`TestUIProxy_WorkerSubdomain*` in `tunnel_internal_test.go`), tunnel first-frame rejection (`TestTunnel_FirstFrameRejects` in `server_test.go`), control-plane DB `0600` (`TestOpen_CreatesFilePrivate` in `registry/store_test.go`).
- **CLI (B)**: `users add` with the password from env/file — assert `ps`-visible argv never contains it.

---

## 8. Risks & mitigations

| Risk | Mitigation |
|---|---|
| Worker↔master channel is the security surface | Shipped: TLS + constant-time pairing + rotating short-lived tokens + read-only worker w.r.t. master; keep the control plane small and audited (Phase G) |
| A dead worker strands its sessions | Shipped: `ReapLoop` → `ReapStale` → `failSession` with an explicit "worker offline" finish reason; apex console shows worker status |
| Restored registry reaps live workers after a master restart | Phase A restores `LastSeen = boot time`, `status = offline`; the persist→reload test pins it — without this, every restart mass-fails routed sessions before workers re-register |
| Lossy `bus.Bus` could hide dropped remote events | Shipped: master re-publishes with fresh master seqs (worker seqs are unimplementable *and* wrong — `bus.Publish`/`PublishRaw` re-stamp; one numbering must cover the mixed stream); the web client's gap-resync then handles drops |
| Resource frames re-published onto the master bus would trigger perpetual client resyncs | Shipped: control frames on both legs; the relay consumes them without touching the bus (`server.go:246-248`) |
| Provider keys on the worker | Shipped: the worker resolves its own providers from its own env/DB/free-pool (`buildProviderRegistry`) — the master never sees worker credentials |
| ~~Two env-builders could drift~~ | **Moot (F shipped)**: there is no second env-builder — each per-dir `server.Server` owns its wiring (`servers.go`), and the headless/CLI paths were already separate, never shared |
| Registry is now on disk | bbolt transactions (atomic, single-writer) + best-effort `BackupTo` on graceful shutdown; **corrupt-file policy is bucket-aware (A2)**: registry lost → start empty and self-heal (workers re-register); `users` lost → refuse operator-gated surfaces until a human re-seeds. Never "recover" authentication into the open |
| Credential store (employee passwords on disk) | bcrypt hashes only, `users` bucket in a `0600` file; leaked DB yields hashes. v1 is identity-only (§9). Signing key stays per-process (A6): accounts survive restarts, **cookies don't** — that logout-on-restart is deliberate until the deleted-user window has a real fix, not a bug to patch by persisting the key |
| Password on the CLI (`users add`) | Never argv — env var or `--password-file`, mirroring the worker's pairing-secret pattern (A6) |
| Worker exits on every master blip | **Shipped (Phase C)**: `Run` now reconnects with capped backoff (1 s → 30 s) on stream failure instead of exiting — a supervisor is no longer required for resilience; on `Unauthenticated` it re-pairs with the pairing secret |
| Token TTL vs. master downtime | `tokenTtlSeconds` default 900 s bounds the "recognized without re-pairing" benefit; a master down longer than the TTL forces re-pairing by design — the shipped Phase C re-pair fallback is mandatory, not optional |
| Master restart storm | After a restart, every worker re-registers near-simultaneously; registration is a cheap constant-time check + registry insert, and with Phase A/C most present their still-valid tokens instead |
| ConnectRPC proto drift | Shipped: one proto package under `controlplane/proto`, `buf` codegen into the tree so diff-review is explicit; ogcode imports only `gen/` + `tunnel` via the module replace |

---

## 9. Out of scope (explicitly deferred)

- **Session durability/replay across master restarts** (the Temporal trigger). Registry *routing state* survives a restart (Phase A); an **in-flight session** still dies with its worker — the loop is not replayable and is not persisted. On a worker or master restart, the session's worktree and DB survive on the worker; the operator starts a fresh session or manually resumes a reconciled one (Phase E boot recovery marks, never resumes).
- **Managed provider fleet** (Fly Machines / hosted runners) — a legitimate separate track; evaluate in parallel, do not fold in.
- **Multi-tenant isolation between workers** — v1 assumes the panel operator also owns the workers.
- **Per-employee authorization / RBAC** (the answer to §11 Q3). Phase B gives employees *identity* — accounts gate who can log in and open a worker — and deliberately no roles table: every logged-in employee has the same start/stop/approve authority on every worker. Restricting *which* employees may touch *which* workers, or gating `users add|rm` behind an admin role, is future work.
- **NATS / durable message bus** — rejected for the shape of the problem (§1).
- **Master-side aggregated panel (Phase F option b)** — REST proxy + SSE relay + `web/src` Workers section. The tunnel + apex console serve v1; promote (b) only if operators must never reach worker UIs directly.
- **Worker-side HTTP exposure beyond loopback** — the worker's UI binds `127.0.0.1` only; the master's tunnel is the only remote path.

---

## 10. Sequencing

| Phase | Deliverable | Depends on |
|---|---|---|
| A | bbolt registry persistence (workers/tokens/sessions) + restore semantics + corrupt-file policy | — |
| B | Per-employee accounts (`users` bucket, store-backed gate, constant-time login, CLI, boot policy) | A |
| C | Worker reconnect + token persistence + re-pair fallback | — |
| D | Git bootstrap for absent workspaces | — |
| E | Interactive hosting: permission/guidance/abort commands, boot recovery, worker-side titles | C (recommended), F for the approvals-through-UI path |
| 1 | Standalone-server lifecycle: `Serve(ctx)`/`Stop`/`Port`/`Options{Loopback,NoBrowser}` — **shipped** | — |
| 2 | Worker hosts one server per worktree: spawn + tunnel-per-dir + route keying — **shipped 2026-09-11**; E's control RPCs kept but now proxy into the per-dir server's exported methods | 1 |
| 3 | Subdomain scheme `<worker>-<worktree>.<host>` + tunnel handshake route frame — **shipped 2026-09-11**: the frame (`TunnelChunk.route`) shipped with 2; master-side registry now tracks per-entry `workerID`/`route` and the apex console links each live worktree tunnel at `<id>-<route>.<host>`; routing is an exact whole-label match (no host-label split; hyphenated worker ids safe) | 2 |
| F | Operator interaction model: per-worktree server = the built-in UI (option a, realized by 2) — **shipped 2026-09-11**: eager tunnels (`ensureEagerTunnels` + `tunnelAndRelayOnce`) bring every discovered worktree's server + tunnel up at connect time; acceptance pinned in `eager_tunnel_test.go` (native `/api/config` through the tunnel) | E, 2 |
| G | Security hardening (spread across A–F, verified here) — **shipped 2026-09-11** | A–F |

B before C is a preference, not a dependency; A→B is a hard order (accounts live in the store A creates). Phase 2 is the architectural change of record and is now real: it makes F(a) true by construction (one server per dir, hosted in-process) and repurposes the E command path into a proxy over the per-dir server's exported seams (the stream RPCs stay for master-side control surfaces and headless callers).

---

## 11. Open questions for the developer

1. **Operator interaction model**: **ANSWERED (2026-09-11) — option (a), realized as one standalone server process per worktree (Phase 2)**, not an embedded-server-built-on-`buildEnv`. The master-side panel (b) stays deferred. Both remaining sub-questions are resolved: the subdomain scheme is `<worker>-<worktree>.<host>` (single label, split on first hyphen — `hostLabel` unchanged), and the tunnel first-frame extension is a **versioned token frame**: `TunnelChunk.route` (proto field 2, empty = legacy bare `<workerID>` key) — no new RPC.
2. **Shared provider keys**: shipped answer is "a worker always resolves its own providers" (`buildProviderRegistry`: env → its own config DB → free pool). Confirm this stays the rule even when the master and a worker share an operator.
3. **RBAC (post-accounts)**: per-employee accounts (Phase B) gate *who can log in*; v1 is deliberately identity-only. Should `users add|rm` itself eventually require an admin role, and should worker access ever be per-user restricted? Deferred by design (§9) — flag now if v1 must ship with any of it.
4. **Worker discovery**: today endpoints are manual (`ogcode worker --master=…`). A config-file registry on the master (workers self-describe in `controlplane.db` and the console already lists them) would remove the last manual step — worth doing when a second real deployment exists, not before.
5. **`operatorPassword` coexistence window**: one release with the legacy fallback (Phase B) — confirm that's acceptable, or whether accounts should hard-cut in the same release that adds them.