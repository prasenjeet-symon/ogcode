# Scheduled Session Wakes — Design Plan for ogcode

> **Scope**: let an agent (or a user) arm a **wake** — a future moment at which ogcode returns to an *existing* session, injects a prompt as a user turn, and runs the agent loop on it unattended. A woken run can arm further wakes, so a chain of follow-ups is a natural consequence of the primitive rather than a second feature.

> **Core architectural decision**: this is a **trigger, not a runtime**. ogcode already has exactly one door into "run a turn on an existing session without a human typing" — `Server.startSessionLoop` (`internal/server/resume_routes.go:148`), shared today by the prompt handler, the resume handler and `HostSession`. A wake writes the user message and knocks on that same door. Nothing about prompt assembly, streaming, tools, compaction, permissions, keep-awake or crash recovery is reimplemented, forked or special-cased. If a wake needs behaviour the interactive path does not have, that behaviour is added to the shared path — never beside it.

> **Anti-framework by default**: no cron library, no job queue, no distributed scheduler, no durable-workflow engine. The state is one SQLite table in the project DB; the clock is one ticker owned by the server; the executor is the loop that already exists. The interesting engineering here is not "how do we run a job later" — it is the three collision problems in §2, which no scheduler library would solve for us.

---

## 0. What this builds on (existing seams — read before designing anything new)

| Seam | Where | Why it matters here |
|---|---|---|
| `startSessionLoop(sessionID, agentName, vw, vh)` | `internal/server/resume_routes.go:148` | The single execution door. Registers cancel func + `LoopControl`, sets permission gating, tracks the loop in `s.running`. **Also preempts**: it cancels any loop already running on that session (`:160`). See §2.1. |
| `Server.HostSession` | `internal/server/host_session.go` | Precedent for a *non-HTTP, in-process* caller creating session/message/part rows and starting the loop. A wake is the same shape with an existing session instead of a new one — `WakeSession` belongs in this file. |
| `LoopRunner.RunLoop` | `internal/agent/loop.go:93` | Unchanged. Reads the session row for directory/model, publishes `loop.done` on every exit path, recovers panics, reconciles orphaned tool calls. |
| Resume | `internal/agent/resume.go`, `internal/server/resume_routes.go` | Proves a loop can be started on a session with no new user input, and that crash recovery (`recoverInterruptedSessions`) is already a startup sweep. A wake interrupted by a process death is reconciled by machinery that exists. |
| Permission gating | `WithPermissionGating` (`loop_control.go:272`), `requestPermission` (`loop.go:2255`), `Manager.EnsureRules` (`permission/permission.go:77`) | Gated loops **block indefinitely** on `pr.ReplyCh` (`loop.go:2302`). Unattended runs must answer this question before they can exist. See §2.2. |
| `session.Permission == "auto"` | `loop.go:2267` | The risk classifier already auto-approves calls it judges safe and escalates the unclear middle. This is the pre-built answer to "what may an unattended run do". |
| Goose migrations | `internal/db/*.sql`, embedded | Next file is `043_`. |
| `internal/task` / `internal/plan` package shape | `store.go` + `<noun>.go` + `util.go` | The layout `internal/wake` copies verbatim. |
| Tool seam via function field | `tool.TaskFunc` (`internal/tool/task.go:12`) | How a tool reaches into the agent/server layer without an import cycle. The `schedule` tool uses the same trick. |
| `message.data` is a JSON blob | `session.Store.CreateMessage` | Adding a provenance field to `MessageInfo` needs **no migration** — the same way `Interrupted` and `Delivery` were added. |
| Bus → SSE | `bus.Publish` → `/api/event` (`event_routes.go`) | Wake lifecycle rides the existing stream; no new transport. |

---

## 1. Naming (decided first, because the obvious name is already taken)

The repo already owns **`task`**: a plan-derived unit of work with a branch, a worktree and a PR (`internal/task/task.go`). Calling this feature "scheduled tasks" would overload that noun in the same codebase, and every future reader would have to disambiguate by context.

| Concept | Name | Rationale |
|---|---|---|
| The stored row | **wake** (`internal/wake`, table `session_wake`) | It is not a task; it is an alarm set on a session. Short, unclaimed, and reads correctly in logs: `wake fired`, `wake deferred`, `wake chain`. |
| The agent-facing tool | **`schedule`** | The tool is the verb the model performs. `schedule` is what a model reaches for unprompted; `arm_wake` is not. |
| The UI surface | **"Scheduled"** | What a user expects to click. |

---

## 2. The three hard problems

Everything above is plumbing. These are the reasons a naive implementation is wrong.

### 2.1 Loop collision — a wake must never preempt a live turn

`startSessionLoop` opens with:

```go
if old, ok := s.running[sessionID]; ok {
    old()
    slog.Info("cancelled previous running loop", "session", sessionID)
}
```

That is correct for a user hitting send twice. It is **destructive** for a timer: a 3 PM wake on a session the developer is actively working in kills their in-flight turn mid-tool-call, and the only trace is one `slog.Info`.

**Rule: a wake never calls `startSessionLoop` blind.** The runner asks first, and on a busy session it *defers rather than preempts*:

- busy → re-arm `fire_at = now + 60s`, `deferred_count++`, publish `wake.updated`
- keep deferring up to `maxDeferWindow` (60 min) — a human turn ends long before that
- past the window → apply the wake's misfire policy (§4.3) and record `last_error = "session busy for 60m"`

Deferral is preferred over a queue-behind-the-turn because the user's turn may itself change what the wake should do; re-checking a minute later is both simpler and more correct than committing to a stale prompt.

The busy check and the wake must be **atomic against each other** but need not be against the user: a user prompt landing microseconds after a wake starts gets the existing preempt semantics, which is what the user intends (they are present and typing). Only the timer yields.

### 2.2 Unattended permissions — the deadlock, and the right default

A gated loop with no human parks forever on `loop.go:2302`, holding a goroutine **and** the keep-awake power assertion (`loop.go:115` acquires for the whole turn). An overnight wake that hits one `bash` prompt would keep the Mac's display on until morning.

Three candidate policies, and the one to ship:

| Policy | Behaviour on an `Ask` verdict | Verdict |
|---|---|---|
| Run ungated (like `task`/`breakdown`) | Everything auto-runs | **Rejected.** Those run in disposable worktrees. A wake runs in the developer's real working directory. |
| Park until a human answers | Turn blocks; keep-awake held | **Opt-in only**, with a hard `parkDeadline` (default 30 min) after which it degrades to deny. |
| **Deny and keep going** | Tool returns `Denied`, agent is told why, turn completes and reports | **Default.** |

Ship **`unattended: "deny"`** as the default, and lean on the machinery already present: a session in `auto` mode already auto-approves calls the risk classifier judges safe (`assessAutoRisk`, `loop.go:2331`) and only escalates the unclear middle. So the recommended combination — **`permission: "auto"` + `unattended: "deny"`** — means an unattended run does the safe work, gets cleanly refused on the risky work, and *says so in its final message*, which the user reads when they return. No deadlock, no silent escalation, no held assertion.

The denial message matters. On an unattended deny, the tool result should read as guidance, not as failure:

> Denied: this is an unattended scheduled run, so nothing that needs approval can execute. Finish what you can without it and state plainly in your final message what you could not do and what approval it needs.

**Trust boundary (load-bearing): the `schedule` tool cannot set `unattended`, cannot set `park`, and cannot seed permission rules.** Those are user-only fields, settable via HTTP/UI. An agent that could widen its own unattended privileges by scheduling itself is a privilege-escalation primitive, and it would be one the user never explicitly granted. The agent picks *when* and *what*; the user picks *how much it may do while they are away*.

### 2.3 Context drift — a session revisited fifty times

A wake chain is precisely the shape the context path was not built for: one session, dozens of turns, weeks apart. `workingSetCap = 1000` messages (`loop.go:339`), a per-session `CompactionSummary`, and in-turn `compact_context` all help, but continuity that depends on the model re-reading a 40-turn history is both expensive (Ollama Cloud is billed with no prefix caching — the reason `compact_context` exists) and unreliable.

**Design answer: continuity is carried explicitly, not inferred.** Each wake row holds a `carry` field — a handoff note written by the run that armed it. The woken turn's injected message is:

```
[scheduled run — armed <relative time> ago]

<prompt>

What you left for this run:
<carry>
```

The conversation history remains available and useful; it is no longer the *contract*. A chain stays coherent even after compaction has folded away the turn that started it. This also composes with the turn-memory work (`internal/memfile`, `OGCODE_TURN_MEMORY`): where that route sends only the current turn, `carry` is exactly the cross-turn state it would otherwise have to recall.

The `schedule` tool's description must push this hard — *"write `carry` as if the run that receives it has never seen this conversation"* — because a model's default is to assume it will remember.

---

## 3. Data model

`internal/db/043_session_wake.sql`:

```sql
-- +goose Up
CREATE TABLE session_wake (
  id             TEXT PRIMARY KEY,
  session_id     TEXT NOT NULL REFERENCES session(id) ON DELETE CASCADE,
  directory      TEXT NOT NULL,          -- denormalized: the startup sweep reads it before loading sessions
  title          TEXT NOT NULL,          -- short label for the UI and logs
  prompt         TEXT NOT NULL,          -- injected as the user turn
  carry          TEXT NOT NULL DEFAULT '',   -- handoff note (§2.3)
  agent          TEXT NOT NULL DEFAULT 'build',
  fire_at        INTEGER NOT NULL,       -- unix ms
  repeat_every   INTEGER NOT NULL DEFAULT 0,  -- ms; 0 = one-shot
  misfire        TEXT NOT NULL DEFAULT 'run_now',  -- run_now | skip | skip_to_next
  unattended     TEXT NOT NULL DEFAULT 'deny',     -- deny | park   (user-set only)
  park_deadline  INTEGER NOT NULL DEFAULT 0,       -- ms; 0 = default 30m
  status         TEXT NOT NULL DEFAULT 'pending',  -- pending|running|done|failed|cancelled|skipped
  chain_id       TEXT NOT NULL,          -- groups a self-chained series
  chain_run      INTEGER NOT NULL DEFAULT 0,   -- nth run in the chain; enforces the cap
  deferred_count INTEGER NOT NULL DEFAULT 0,
  created_by     TEXT NOT NULL,          -- 'agent' | 'user'
  last_error     TEXT NOT NULL DEFAULT '',
  last_run_at    INTEGER NOT NULL DEFAULT 0,
  time_created   INTEGER NOT NULL,
  time_updated   INTEGER NOT NULL
);
CREATE INDEX idx_session_wake_due     ON session_wake(status, fire_at);
CREATE INDEX idx_session_wake_session ON session_wake(session_id);

-- +goose Down
DROP TABLE session_wake;
```

`ON DELETE CASCADE` is real here — the DB is opened with `_pragma=foreign_keys(1)` (`internal/db/db.go:22`), so deleting a session reaps its wakes with no extra code.

**Provenance on the injected message** — add to `session.MessageInfo` (no migration; `data` is a JSON blob):

```go
// Wake, when set, records that this user turn was injected by a scheduled wake
// rather than typed by a person. The UI renders it as a scheduled run; resume
// and reconcile treat it like any other user turn.
Wake *WakeOrigin `json:"wake,omitempty"`

type WakeOrigin struct {
    WakeID   string `json:"wakeId"`
    ChainID  string `json:"chainId"`
    ChainRun int    `json:"chainRun"`
    ArmedAt  int64  `json:"armedAt"`
}
```

Without this the message log lies: a user sees turns they never typed, indistinguishable from their own.

---

## 4. The runner

New package `internal/wake`: `wake.go` (types + status constants), `store.go` (CRUD, `ListDue`, `Claim`), `runner.go` (the ticker), `util.go` (`Now()`), mirroring `internal/task` exactly.

### 4.1 The seam

The runner must not import `internal/server`. Server implements a two-method interface, alongside `HostSession` in `host_session.go`:

```go
// internal/wake
type Waker interface {
    // Busy reports whether an agent loop is already running on the session.
    Busy(id session.SessionID) bool
    // Wake writes the prompt as a user turn (marked with origin) and starts the
    // agent loop on the existing session. It returns an error without starting
    // anything if the session is gone or a loop is already running.
    Wake(id session.SessionID, agentName, prompt string, origin session.WakeOrigin, unattended string) error
}
```

`Server.Wake` is `HostSession` minus session creation: create message + text part, publish `message.updated`, sweep orphaned assistant messages (the same block `handlePrompt` runs at `session_routes.go:421`), then `startSessionLoop`. Factor that shared preamble out of `handlePrompt` rather than copying it — three copies of the orphan sweep is how they drift.

`unattended` reaches the loop as a context value next to `WithPermissionGating` (`WithUnattended(ctx, policy)`), read in `requestPermission` before the blocking select.

### 4.2 The clock

One goroutine started from `serve()` beside the resource sampler, cancelled on shutdown:

- `time.NewTicker(15 * time.Second)` — precision is explicitly **±15 s**, stated in the tool description and the UI. Nothing here needs second accuracy, and a ticker matches the sampler's existing shape.
- Each tick: `SELECT ... WHERE status='pending' AND fire_at <= ? ORDER BY fire_at LIMIT 10`.
- **Claim atomically** before acting: `UPDATE session_wake SET status='running' WHERE id=? AND status='pending'`, proceed only on `RowsAffected == 1`. Two ogcode servers opened on the same project directory is an ordinary accident, and WAL makes this check sufficient.
- Then: busy-check → defer (§2.1), or `Wake(...)`.
- Terminal bookkeeping on `loop.done` for that session: `status='done'`, `last_run_at`, and for `repeat_every > 0` insert the next occurrence. **Re-arm on failure too** — a daily job that silently stops forever after one bad night is the classic scheduler bug.

### 4.3 Missed fires

The server is per-project and runs only while ogcode is open; a laptop asleep at 3 AM fires nothing. This is the honest limitation and it is surfaced, not hidden.

On startup, after `recoverInterruptedSessions`, sweep `status='pending' AND fire_at <= now` and apply per-wake `misfire` (standard Quartz vocabulary, deliberately):

| Policy | Behaviour |
|---|---|
| `run_now` (default) | Fire once, immediately. |
| `skip` | Mark `skipped`, publish, move on. |
| `skip_to_next` | Repeats only: discard the backlog, arm the next future occurrence. Stops forty backlogged dailies from stampeding. |

`keepawake` (`internal/keepawake`) holds an assertion **during** a turn; it cannot wake a sleeping Mac. Real wall-clock wake-up needs `pmset schedule` / a launchd `StartCalendarInterval` agent — noted in §8 as deferred, not smuggled into v1.

---

## 5. The agent-facing tool

One tool, not three. Tool schemas are paid for in every prompt of every turn (`ToProviderTools`), and list/cancel are rare enough that a discriminator is cheaper than two more permanent schema slots.

```
schedule(action, title, prompt, carry, delay | at, repeat_every, wake_id)
```

- `action`: `create` | `list` | `cancel`
- `delay`: relative duration — `"2h"`, `"45m"`, `"3d"`. **Preferred.**
- `at`: RFC3339 absolute. Accepted but secondary — the model knows the current date only from the per-turn `<system-reminder>` (`prompt_builder.go:683`) and is materially worse at absolute arithmetic than at "in 6 hours".
- The `create` result **echoes the resolved absolute fire time in the host's local zone**, so a model that miscomputed can see it and correct on the next call.
- `repeat_every`: optional duration.
- Not settable by the agent: `unattended`, `park_deadline`, permission rules (§2.2).

Registered in `codingAgentTools` (`internal/agent/agent.go:27`) so `build` and `task` agents get it; the read-only sub-agent and note/search agents do not.

Description must carry the three things a model gets wrong unprompted: (1) the woken run happens in this same session but may have lost conversational context — put everything it needs in `carry`; (2) it will run **unattended**, so anything needing approval will be refused; (3) use it for work that genuinely must happen later, never to defer work that could be done now.

---

## 6. HTTP + UI

```
GET    /api/wakes                    list (?sessionId=, ?status=)
POST   /api/wakes                    create (user-initiated; may set unattended/park)
GET    /api/wakes/{wakeID}
PATCH  /api/wakes/{wakeID}           reschedule, retitle, change policy
DELETE /api/wakes/{wakeID}           cancel
POST   /api/wakes/{wakeID}/run       fire now
GET    /api/session/{sessionID}/wakes
```

Bus events `wake.created` / `wake.updated` / `wake.deleted`, status in the payload — matching the `session.updated` shape the client already consumes over `/api/event`.

UI, minimum viable and non-negotiable:

1. A **pending-wake indicator on the session** — a session that will act on its own must say so on its own screen.
2. A **Scheduled list** (per project) with next fire time, chain, origin (agent vs. user) and a cancel button.
3. **Scheduled turns visibly marked** in the transcript, via `MessageInfo.Wake`.

An autonomy feature the user cannot see or stop is a worse feature than no autonomy feature.

---

## 7. Safety rails

A self-chaining agent is an unbounded token spender. These are not polish; they are the feature's cost ceiling.

| Rail | Default | Why |
|---|---|---|
| `minDelay` | 60 s | Blocks a 1-second self-reschedule loop from burning a provider balance overnight. |
| `maxPendingPerSession` | 20 | |
| `maxPendingPerProject` | 100 | |
| `maxChainRuns` | 50 | Enforced on `chain_run`; the tool refuses with a message the agent can report, not a silent drop. |
| `maxDeferWindow` | 60 min | §2.1. |
| `parkDeadline` | 30 min | §2.2; bounds the held keep-awake assertion. |
| Global kill switch | `OGCODE_WAKES=0` | Matches the `OGCODE_TURN_MEMORY` / `OGCODE_NO_KEEP_AWAKE` precedent; a user must be able to stop every timer without deleting rows. |

Refusals surface as tool results the model can act on and report, never as silent no-ops — a chain that dies quietly is worse than one that dies loudly.

---

## 8. Phases

| Phase | Content | Done when |
|---|---|---|
| **1 — Storage** | Migration 043, `internal/wake` (`wake.go`, `store.go`, `util.go`), `MessageInfo.Wake`. | Store CRUD + `Claim` atomicity tested. |
| **2 — Runner** | `runner.go` ticker, `Waker` seam, `Server.Wake`/`Server.Busy`, shared orphan-sweep preamble factored out of `handlePrompt`, wired into `serve()`. | A row with `fire_at = now+5s` runs a real turn on an existing session; a busy session defers instead of preempting (**the regression test that matters most**). |
| **3 — Unattended policy** | `WithUnattended`, deny path in `requestPermission`, the denial guidance text, `park` + deadline. | A gated wake on a session with a would-ask `bash` call completes with a clear refusal instead of hanging. |
| **4 — Agent tool** | `schedule` tool, `ScheduleFunc` seam, registration in `codingAgentTools`, rails from §7. | An agent arms a wake, the woken run arms the next, the chain cap holds. |
| **5 — HTTP + UI** | Routes, bus events, Scheduled list, session indicator, transcript marker. | A user can see, reschedule and cancel every pending wake. |
| **6 — Misfire + recovery** | Startup sweep, the three policies, repeat re-arm on failure. | Wakes armed while ogcode was closed resolve per policy, with no stampede. |

Phases 1–3 are the feature; 4 is what the request asked for; 5 is what makes it safe to ship; 6 is what makes it trustworthy.

---

## 9. Deliberately deferred

- **Cron expressions.** `repeat_every` covers the real cases. Cron brings a parser, DST and timezone semantics, and catch-up rules — all cost, no demonstrated need. Revisit only on a concrete "every weekday at 09:00" request.
- **Waking a sleeping machine** (`pmset schedule` / launchd). Real, separate, OS-specific, and it changes the product promise from "while ogcode is open" to "always". Do it as its own change, with its own consent story.
- **Remote workers / control-plane scheduling.** v1 wakes are project-local rows owned by whichever server has that project open (`<project>/.ogcode/ogcode.db`). Master-side scheduling — surviving worker recycling, since the master already holds a durable bbolt registry and already sends `StartAgent` — is a real follow-on and explicitly out of v1 scope. Do not let this leak into the worker path by accident.
- **Cost budgets per chain.** `maxChainRuns` is a proxy. A token/spend ceiling is better and needs the accounting that `MessageInfo.Cost` already collects; worth doing once chains are real.
