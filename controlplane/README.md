# ogcode-control-plane

The **master / control plane** for ogcode remote agent workers. It lets you run
ogcode on other machines ("workers") — laptops, servers, boxes behind NAT — and
reach each one's **real ogcode web UI from your browser at a single domain**,
behind one operator login.

It is a **separate repository from ogcode** on purpose. The only piece that lives
inside ogcode is the worker run-mode (`ogcode worker`); everything here treats
workers as black boxes reached over the wire and imports **none** of ogcode's
`internal/` packages. The master binary builds standalone from this repo alone.

## What it does

- **Registry** — workers dial in, authenticate with a shared pairing secret, and
  are tracked (id, name, workspaces, liveness).
- **Tunnel** — each worker opens a byte pipe over which the master reverse-proxies
  that worker's *own* ogcode web UI (which listens only on the worker's loopback)
  to your browser. This is the drill-down: **one worker id → one subdomain → that
  worker's real UI**, including its Settings (model, keys, MCP servers, skills).
- **Operator gate** — a password login in front of the UI proxy, so a public
  domain doesn't mean a public UI.

You get the genuine ogcode experience per worker, at `https://<workerId>.your-
panel-host/`, with no bespoke UI to maintain. (A native session-event relay also
exists — see [Contracts](#contracts) — but the tunnel is the UI path.)

## Architecture

Workers live behind NAT and cannot accept inbound connections, so the **worker is
the ConnectRPC client**: it dials the master and opens the long-lived streams.
The master (the server) therefore never makes a call *to* a worker — every
master→worker command travels *down* a worker-opened stream.

```
   browser ──https──▶  ┌───────────────────────────────┐
  <id>.panel.example    │            master             │
        ▲               │          (this repo)          │
        │  operator gate │  ┌─────────┐  ┌────────────┐  │
        │  + reverse     │  │registry │  │ UI proxy   │  │
        └───── proxy ────┼─▶│ + router│  │ host-based │  │
                         │  └─────────┘  └────────────┘  │
                         └───▲───────────────▲───────────┘
      Register/Heartbeat ────┘               │
      WorkerStream (bidi): commands down,    │  Tunnel (bidi):
        events/results up                    │  yamux byte pipe
                         ┌───────────────────┴───────────┐
                         │            worker             │
                         │   dials out · no open ports   │
                         │   splices tunnel → 127.0.0.1  │
                         │            ↓ ogcode serve      │
                         └───────────────────────────────┘
```

- **`WorkerStream`** (bidi) — the control channel. First frame is `Hello{token}`;
  then `StartAgent` / `StopAgent` / `ListWorkspaces` / etc. flow down and
  `CommandResult` / `SessionEvent` / `ResourceFrame` flow up, multiplexed by
  session id.
- **`Tunnel`** (bidi) — a raw byte pipe. Both ends run a yamux multiplexer over
  it (master = client/opens streams, worker = server/accepts). The master reverse-
  proxies HTTP over yamux streams; the worker splices each to its local
  `ogcode serve`. yamux **keepalive is on** so an idle tunnel survives a
  proxy/LB idle timeout.

## Reaching a worker (URLs)

One origin serves everything, routed by the request host's first label — an
**exact whole-label match** against each tunnel's registered key (never a split
on hyphens, so worker ids containing hyphens are unambiguous):

| URL | Purpose |
|---|---|
| `https://<workerId>-<worktree>.panel.example.com/` | a worktree UI on that worker (operator-gated); one subdomain per worktree the worker has opened a tunnel for |
| `https://<workerId>.panel.example.com/` | legacy bare form — a tunnel registered without a route label |
| `https://panel.example.com/` | base host — **operator console** (list of workers + links), worker connections, `/healthz` |
| `https://panel.example.com` (worker `--master`) | endpoint workers dial |

A worker registers one tunnel per worktree as it starts hosting sessions there
(tunnels are lazy — a worktree's UI is reachable once the worker has something
running in it; see Status for the pre-hosting gap). The console renders one
"Open `<worktree>` UI" link per live worktree tunnel; a worker with no tunnels
keeps the bare `<workerId>` link.

`<workerId>` is adopted from the worker at registration (stable across
restarts) and is half of the routing key. You
need a wildcard DNS record `*.panel.example.com` (plus the apex) → the master, and
a SAN cert covering **both** `panel.example.com` and `*.panel.example.com`. Dev
uses `*.localhost` / a `<label>.localhost` host and cleartext HTTP/2 (h2c).

## Contracts

- **Global session id, minted by the master**, used everywhere (routing table and
  the worker's own DB) — no worker-local alias.
- **Fresh master seqs on relay.** Relayed worker bus events are re-published on the
  master's own bus with a fresh seq; the worker's numbering is discarded so one
  numbering covers the mixed stream (else a client gap-resync fires every event).
- **Resource frames are control frames** — forwarded out-of-band, never onto the
  bus, so they never consume a seq.

## Security

- **Operator gate** (`internal/auth`) — constant-time password check + a stateless
  HMAC-signed session cookie (`og_operator`, random per-process signing key,
  configurable TTL). It gates **only** the worker-subdomain UI: unauthenticated
  navigation → login page, unauthenticated XHR → 401. Login/logout live under
  `/__operator/…`. The ConnectRPC endpoints, the tunnel, and `/healthz` **bypass**
  the gate — they are authenticated by the pairing secret / worker token instead.
- **Pairing** (`internal/pairing`) — constant-time shared-secret compare
  (`crypto/subtle`) + short-lived, rotating worker tokens. Stdlib only.
- **TLS** (`internal/tlsreload`) — the cert is served via `GetCertificate` and
  **hot-reloaded** on `SIGHUP` and a 15-minute timer, so a renewed wildcard is
  applied with no restart. ALPN offers `h2` (worker streams) and `http/1.1`
  (browsers).
- **Posture.** The gate is the whole perimeter and the master sits in the data
  path (worker secrets transit it), so this assumes a single owner. For public
  exposure, use a strong operator password and prefer a network-layer control
  (VPN/Tailscale, IP allowlist, or an identity-aware proxy). **Not yet built:**
  login rate-limiting/lockout and ACME auto-issue.

- **Per-employee accounts (Phase B).** Instead of the single shared
  `operatorPassword`, the gate can authenticate against bcrypt-hashed accounts
  stored in the bbolt `users` bucket (same DB as the registry — no separate file).
  As soon as **at least one account exists**, `operatorPassword` is ignored and
  only accounts are accepted. Manage accounts with the `users` subcommand
  (passwords come from `$OGCODE_CONTROL_PLANE_USER_PASSWORD` or `--password-file`
  so they never appear in argv):

  ```sh
  ogcode-control-plane users add alice      # create or reset a password (role: user)
  ogcode-control-plane users list           # list accounts with their role
  ogcode-control-plane users rm alice       # delete; requires --force for the last one
  ogcode-control-plane users add bob --workspaces treemain,repo-b
  ogcode-control-plane users add root --admin
  ```

  Account logins use the same stateless HMAC `og_operator` cookie as the legacy
  password. Because the session cookie only embeds the *username* (not a per-login
  secret), deleting an account is what invalidates its live sessions — `users rm`
  breaks that operator's sessions immediately (A1).

  The same accounts can be managed from the browser: the base-host console has a
  **Users** page (`/__operator/users`) to add accounts, delete them (with the
  same last-account force guard), and edit allowlists — operator-gated like the
  console itself, POST-only mutations, and it never renders the bcrypt hash.

- **Start-session form.** The console's **Sessions** page (`/__operator/sessions`)
  is the operator entry point for remote sessions: pick an **online worker**,
  then pick the **workspace** from the directories that worker itself offers —
  the master fetches the worker's live workspace list (falling back to the set
  it advertised at registration) and offers it as a select; there is no
  free-form path input. Paste an optional **repository URL** (`https://…`,
  `ssh://…`, an scp-like `git@host:owner/repo`, or a local path on the worker),
  choose the agent (`build`/`plan`/`task`/`breakdown`), and optionally type the
  first prompt (left blank, the agent reviews the freshly cloned repository).
  Only an advertised workspace path is ever sent to the worker, and the worker
  independently refuses a `StartAgent` for any directory outside its
  registration-time workspace set — cloning lands inside the chosen workspace,
  nowhere else on the worker's disk. The form drives the same
  `StartRemoteAgent` seam a programmatic caller uses: the worker clones
  the repo with its own git credentials when the workspace path is absent
  (pre-existing dirs are used as-is, the URL then ignored), spawns the
  per-directory server and opens the tunnel; the master routes the session. The
  success banner carries the hosted session's URL
  (`https://panel.example.com/sessions/<id>`); a worker-side failure (clone
  error, bad ref) surfaces verbatim as an error banner. Mutations are POST-only
  and operator-gated like every console surface.

- **Live session monitor.** The banner URL is a real page: `/sessions/<id>` on
  the apex host is an operator-gated **live monitor** for one routed session —
  the hosting worker, links into the worktree's tunneled UI (one per open
  tunnel, falling back to the legacy bare worker subdomain), and a live event
  feed streamed over SSE (`/sessions/<id>/events`) from the same relayed bus
  the panel uses. The feed shows event types and payloads with the master seq
  for gap detection, and derives a status line from them (`session.created` →
  running, `permission.requested` → waiting for permission, `loop.done` →
  finished, `session.failed` → failed). It is a monitor, not a transcript:
  relayed payloads carry ids, not message content, so the chat itself stays in
  the workspace UI; and the master tracks routing in memory only, so a
  master restart or a finished session renders a friendly not-found page.
  Unrouted ids, and malformed ones (anything outside `<ses id>` and
  `<ses id>/events`), are 404s on both the page and the stream.

- **Per-user workspace allowlist.** An account may carry a comma-separated list
  of workspace identifiers — a per-worktree **route label**, a workspace **name**,
  or a workspace **path suffix** (segment-aligned, so `repo-b` matches
  `/srv/repo-b` but not `/srv/other-repo-b`). An empty list means the account may
  open **any** workspace (the default, preserving pre-allowlist accounts). A
  restricted account that opens a worktree subdomain it is not allowed on gets a
  403 page; API fetches get a bare 403; store errors fail closed. Enforcement
  sits in the worker-UI proxy after the operator gate.

  The allowlist is **pinned into the session cookie at login** (a short hash of
  the list, not the list itself) and re-checked against the store's *current*
  allowlist on every request — so rewriting an account's allowlist (`users add
  --workspaces …` on an existing account, or the console's set-workspaces form)
  signs its live sessions out immediately, the same mechanism A1 gives `users
  rm`. Cookies issued before the allowlist shipped keep validating
  (unrestricted); re-login picks up the new scope.

- **Admin vs user accounts.** New accounts carry a role. An **admin** sees every
  worker and session on the console (the default for accounts that existed
  before the flag — the pre-accounts operator). A plain **user** is scoped: the
  apex console lists only workers its allowlist names, and `/sessions/<id>`
  monitors open only sessions started under that account (a foreign session
  renders 404 so its id and existence are not revealed). Create both from the
  Users page role select or `users add --admin`.

- **Repo assignment & user sessions (multi-user repos).** A plain user can be
  assigned to a repository, which provisions its worktree **eagerly**: on the
  Sessions page, "Assign a user to a repository" picks the worker already
  holding that repo's clone (recorded from the previous assignment) or — first
  use — the online worker reporting the most free disk (`Stats`; a worker that
  does not answer is skipped) and sends it
  `AddUserWorktree{repo_url, user_name, base_branch}`. The **base branch**
  field chooses the branch the user's worktree is cut from — left blank, the
  clone's default branch; otherwise it must name a branch the clone can see
  (as written or as `origin/<name>`), and the worker refuses unknown names.
  The worker clones once per repo and creates branch `user/<name>` plus the
  worktree `<clone>/.ogcode/worktrees/user/<name>`; assignment also pins the
  account's allowlist to that worktree's route label (`<name>`) when it has no
  explicit list, and records the base branch on the account. Admins are
  refused — assignment scopes plain users only.
  "Start a user session" then routes a session by **logical targeting**: the
  master resolves the (repo, user) pair to the recorded placement and sends
  `StartAgent{repo_url, user_name}` — no workspace path involved; the worker
  resolves the pair to the user's worktree itself (refusing anything outside
  its registered roots, as always). The clone-placement map is in-memory by
  design (mirroring the worker's own re-seeding): a master restart re-records
  a repo lazily at its next assignment.

## Configuration

A JSON file (default `control-plane.json`; see `control-plane.example.json`):

```jsonc
{
  "master": {
    "listen": ":443",                       // bind address (required)
    "pairingSecret": "…",                   // shared worker secret (required)
    "operatorPassword": "…",                // UI login; empty ⇒ UI runs OPEN (warned)
    "dbPath": "…/controlplane.db",          // registry + accounts (Phase A/B store)
    "sessionTtlSeconds": 43200,             // operator session lifetime (default 12h)
    "cookieDomain": ".panel.example.com",   // one login across all worker subdomains
    "tokenTtlSeconds": 900,                 // worker token lifetime (default 15m)
    "workerTimeoutSeconds": 45,             // missed-heartbeat death (default 45s)
    "tls": { "cert": "…/cert.pem", "key": "…/key.pem" } // omit ⇒ h2c (dev only)
  }
}
```

With no `tls` block the server serves **h2c** (cleartext HTTP/2) — fine for local
dev, never production. With `tls` set, it serves HTTPS/2 with the hot-reloading
cert.

## Running it

```sh
make run    # build + serve against ./control-plane.json
```

For a full internet deployment (wildcard DNS, DNS-01 wildcard cert, systemd,
renewal → SIGHUP), see [docs/deploy.md](docs/deploy.md).

## The worker side (lives in ogcode)

Workers run from the ogcode repo:

```sh
OGCODE_PAIRING_SECRET=… ogcode worker \
  --master https://panel.example.com \
  --workspace ~/code/app             # repeatable; one worktree = one worktree server + tunnel
```

Flags: `--master` (required), `--workspace` (repeatable), `--name`,
`--pairing-secret-file`, and `--master-ca` / `--insecure` for a
self-signed/private-CA master (public certs need neither). The worker is
outbound-only — no inbound ports, NAT-friendly. Each worktree gets its own
in-process ogcode server (loopback-only) and its own tunnel; no separate
`serve` process is needed.

**Git is mandatory on the worker host.** Repository cloning and worktree
provisioning (the user-assignment flow) run on the worker with its own git and
credential helper — the master never runs git. A worker without git can still
host directories that already exist on disk, but it cannot clone a repository
or create user worktrees, so assignment does not work.

**Build note:** ogcode requires **cgo** (a tree-sitter grammar) and currently
builds against this repo via a local `replace`, so build it on a machine of the
worker's OS/arch with both repos side by side. (Publishing this module would drop
that requirement.) The worker imports this repo's generated `gen/controlplane/v1`
and the `tunnel` package.

## Layout

```
proto/controlplane/v1/    the ConnectRPC contract (source of truth)
gen/                      generated Go (committed, so diffs are reviewable)
tunnel/                   Connect-stream-as-net.Conn + yamux helpers (imported by BOTH sides)
internal/config/          master config schema + loader
internal/auth/            operator login: password check + signed session cookie
internal/pairing/         pairing-secret check + worker token mint/rotate
internal/tlsreload/       hot-reloading TLS certificate
internal/registry/        worker registry + session router + heartbeat reaper
internal/bus/             seq-stamped lossy event bus
internal/master/          ConnectRPC handlers, tunnel proxy, auth routes, orchestration
cmd/ogcode-control-plane/ the `serve` daemon (h2c dev / TLS prod)
```

## Develop

```sh
make tools      # once: install buf + protoc-gen-go + protoc-gen-connect-go
make generate   # regenerate gen/ from proto/
make test       # go test -race ./...
```

## Status

Done:

- [x] ConnectRPC contract + shared codegen; in-process round-trip tests
- [x] Pairing, registration, heartbeat, token rotation, registry, dead-worker reaper
- [x] Worker-opened control stream (commands down, events/results up) + correlation
- [x] Fresh-seq relay of session events onto the master bus
- [x] **Tunnel**: reverse-proxy each worker's real ogcode UI (yamux, host-routing, keepalive)
- [x] **Operator login gate** on the UI proxy (password → signed cookie)
- [x] **Base-host console**: list connected workers with links (apex landing page)
- [x] **Per-worktree subdomains** (`<workerId>-<worktree>.<host>`): one tunnel per
      worktree, console links each live worktree UI; exact whole-label host routing
- [x] **TLS** with wildcard support + cert hot-reload (SIGHUP + timer)
- [x] **Per-user accounts** in accounts mode (CLI `users` + browser Users page on
      the console) and the **per-user workspace allowlist** enforced by the UI proxy
- [x] **Start-session form** on the console (Sessions page): worker + workspace
      select over the worker's own advertised workspaces (no free-form path; the
      worker independently refuses anything outside its registered set) + repo
      URL + agent + prompt → `StartRemoteAgent` (worker clones the repo when
      the path is absent, hosts, tunnels; master routes)
- [x] **Live session monitor** at `/sessions/<id>` (the banner link): hosting
      worker, worktree UI links, SSE event feed with derived status; unrouted
      ids get a friendly not-found
- [x] **Repo assignment & user sessions**: eager `AddUserWorktree` provisioning
      at assignment (free-disk worker pick via `Stats`), account scoping to the
      user's worktree route label, logical `StartAgent{repo_url, user_name}`
      targeting for user sessions, admin/user roles with console + monitor
      scoping

Next:

- [ ] Merge/deprovision lifecycle: merging `user/<name>` back, removing a user
      worktree, deprovisioning a repo (Phase 4 of `MULTI_USER_REPOS_PLAN.md`)
- [ ] Login rate-limiting/lockout
- [ ] Built-in ACME DNS-01 auto-issue/renew (currently bring-your-own cert)
- [ ] Optional friendly subdomains (worker `--name` instead of id)
- [ ] Eager worktree tunnels: today a worktree UI is reachable only once the
      worker starts hosting a session in it (Phase F)
