# Ogcode Architecture Audit — Agent Loop

> Findings from comparing ogcode's agent-loop architecture against the two OpenCode
> implementations (`opencode-ai/opencode` in Go and `sst/opencode` in TypeScript).
> This document is the tracker for fixing each issue. Update the **Status** and the
> **Progress log** as work lands.

Legend — **Status**: `Open` · `In progress` · `Done` · `Deferred`

| ID | Priority | Title | Status |
|----|----------|-------|--------|
| [P0-1](#p0-1--tool-execution-is-not-permission-gated) | P0 | Tool execution is not permission-gated (+ no bash denylist) | Done |
| [P0-2](#p0-2--concurrent-writeedit-on-the-same-file-can-race) | P0 | Concurrent `write`/`edit` on the same file can race | Done |
| [P1-1](#p1-1--monolithic-2470-line-runloop) | P1 | Monolithic ~2,470-line `RunLoop` | Done (stream core deferred by decision) |
| [P1-2](#p1-2--full-history-reloaded-from-sqlite-every-iteration) | P1 | Full history reloaded from SQLite every iteration | Done |
| [P1-3](#p1-3--token-budgeting-is-bytes4-not-real-tokens) | P1 | Token budgeting is bytes÷4, not real tokens | Done |
| [P1-4](#p1-4--model-cannot-delegate-subtasks) | P1 | Model cannot delegate subtasks (no `task` tool) | Done |
| [P2-1](#p2-1--compaction-is-destructive-and-the-summary-grows-unbounded) | P2 | Compaction is destructive; summary grows unbounded | Done |
| [P2-2](#p2-2--error-classification-by-substring-matching) | P2 | Error classification by substring; ignores `Retry-After` | Done |
| [P2-3](#p2-3--lossy-event-bus) | P2 | Lossy event bus (drops on full buffer) | Done |
| [P2-4](#p2-4--one-system-prompt-regardless-of-model-family) | P2 | One system prompt regardless of model family | Done |

---

## P0-1 — Tool execution is not permission-gated

**Status:** Done (interactive approval prompts) · **Priority:** P0 (safety/security)

**Evidence**
- `internal/agent/loop.go` `executeTool` builds `tool.Context` with **no `Ask` function** set.
- `internal/permission` (Ruleset/Evaluate/DefaultRuleset/Manager) is fully built but **never instantiated** in `internal/server/server.go` and **never imported** by `internal/agent` or `internal/tool`.
- `internal/server/permission_routes.go` `handlePermissionReply` only publishes a `permission.replied` event — it never calls `Manager.Reply`, so a pending request would never resolve.
- `internal/tool/bash.go` runs any command with only a timeout — no denylist, no sandbox.

**Impact**
- Build/Task agents run `bash`/`write`/`edit` fully autonomously. The headless `TaskAgent` executes inside git worktrees with zero gating.
- A model (or a prompt-injection payload in a fetched page / file) can run destructive commands.

**Reference behavior**
- Go opencode: `bash.go` has a `bannedCommands` denylist + permission gate + timeout + output truncation; loop breaks on `permission.ErrorPermissionDenied`.
- TS opencode: every tool's `execute` calls `ctx.ask({permission, patterns, always})`.

**Fix plan (staged, non-breaking)**
1. **Bash denylist** — refuse a small, high-confidence set of catastrophic commands (recursive root/home deletion, disk overwrite, `mkfs`, fork bomb). Always on; must not block normal dev commands. *(this turn)*
2. **Enforcement hook** — thread a policy evaluation into `executeTool`: evaluate a `permission.Ruleset`; `Deny` → refuse; `Allow` → run; `Ask` → interactive prompt.
3. **Interactive `ask` flow** — instantiate `permission.Manager` in the server, implement `tool.Context.Ask` to publish `permission.requested` over SSE and block on the reply channel (honoring ctx cancellation and mid-loop guidance), and make `handlePermissionReply` call `Manager.Reply`. Requires frontend to render the prompt.
4. **Default policy** — default to `Allow` (preserve today's auto-run UX) so nothing hangs when no interactive responder is attached; `Ask`/`Deny` become opt-in via config. In `Ask` state with no responder registered, degrade to `Allow` (never block silently).

---

## P0-2 — Concurrent `write`/`edit` on the same file can race

**Status:** Done · **Priority:** P0 (data corruption)

**Evidence**
- `internal/agent/loop.go` executes **all** ready tool calls in one turn concurrently (goroutines + `WaitGroup`), with a comment asserting built-in tools are "stateless and safe for concurrent use."
- `internal/tool/write.go` and `internal/tool/edit.go` take **no locks**. `edit` is a read-modify-write.

**Impact**
- If the model emits two `edit`/`write` calls targeting the same path in one turn (it does), they interleave: lost updates, or `edit` reading a half-written file. Silent data loss.

**Reference behavior**
- Go opencode avoids this by executing tools strictly sequentially.

**Fix**
- Add a per-path keyed mutex (`internal/tool/pathlock.go`), acquired by `write` and `edit` around their read-modify-write so mutations to the same file serialize while different files and reads stay parallel.

---

## P1-1 — Monolithic ~2,470-line `RunLoop`

**Status:** Done (four extractions landed; stream-processing core intentionally left in place) · **Priority:** P1 (maintainability)

**Evidence**
- `internal/agent/loop.go` `RunLoop` mixes streaming, DB persistence, compaction, mid-loop guidance, retry, parallel tool execution, and agentic memory in one function full of subtle race-window comments.

**Impact**
- Hard to test in isolation (guidance/cancellation only testable end-to-end), hard to reason about, high change-risk.

**Reference behavior**
- TS opencode splits `prompt.ts` / `processor.ts` / `llm.ts` / `overflow.ts` / `compaction.ts` / `tools.ts`.
- Go opencode splits `Run` / `processGeneration` / `streamAndHandleEvents`.

**Fix plan**
- Extract, incrementally and behavior-preservingly: `streamProcessor` (events → message parts), `compactor`, `toolExecutor`, `historyProvider`. Land one extraction at a time with tests green between each.

**Progress (2026-08-15)** — `RunLoop` down from ~1,310 to ~1,024 lines via four behavior-preserving extractions in `internal/agent/loop.go`, `go test -race ./internal/agent/...` green after **each**:
1. **`compactRequest`** — the proactive and reactive compaction sites duplicated ~15 lines each (summarize → swap messages → replace summary → persist → publish); now one shared method called from both.
2. **`resolveRunModel`** — the provider/model resolution + image-support + context-window preamble (~35 lines) → one method returning `(provider, modelID, supportsImages, contextWindow)`.
3. **`executeReadyToolCalls`** — the whole parallel tool-execution block (~155 lines: mark-running, child-context + tool-cancel wiring, concurrent exec, mid-loop-cancel detection, result writeback) → one method returning `loopAborted bool` (caller returns `ctx.Err()` on true).
4. **`writeToolResultMessage`** — the tool-result user-message + parts creation (~45 lines) → one method returning the new message ID for the working-set fold.

**Decision (2026-08-15): stream core left in place.** The largest inline block is the **stream-event processing** (~360 lines: accumulation state, the two flush closures, the `for evt := range streamCh` switch, and finalization). Extracting it means bundling ~10 shared variables and three control-flow exits (loop-abort → `ctx.Err()`, guidance-cancel → break, `EventError` → error) into a `streamProcessor` struct — **pure refactor of the most delicate streaming/cancellation code**, high churn with a real risk of a subtle streaming/guidance regression for a purely cosmetic gain. By explicit decision this was left in place: it's cohesive and thoroughly commented where it sits. The four extractions above already removed the bulk of the mechanical duplication and the highest-value chunks (tool execution, model resolution). If revisited later, do it as its own dedicated change behind the existing guidance/race/streaming tests.

---

## P1-2 — Full history reloaded from SQLite every iteration

**Status:** Done · **Priority:** P1 (efficiency)

**Evidence**
- `internal/agent/loop.go` calls `GetMessages(sessionID, "", 1000)` at the top of **every** step, then re-unmarshals and re-runs `convertMessages` over the whole history.

**Impact**
- O(steps × messages) DB reads + JSON parses per turn. Wasteful on long sessions / many-step turns.

**Reference behavior**
- Go opencode keeps `msgHistory` in memory across the turn and appends assistant + tool-result each iteration.

**Fix plan**
- Maintain an in-memory working slice for the turn (DB stays the durable log via streaming part writes); reload only when mid-loop guidance or an external mutation invalidates the working set.

**Resolution (2026-08-15)**
- `RunLoop` now loads the full history **once**, then folds in only the messages it creates each iteration. Implemented in `internal/agent/loop.go`: a hoisted `messages` working set + `newMessageIDs` (the assistant message, the tool-result message, and the `cancelPartialToolCalls` message — that helper now returns its created ID), drained at the top of the loop via `Store.GetMessage(id)`.
- **Why known-ID folding, not a time cursor:** message IDs are `msg_`+ULID, which are only *millisecond*-timestamped with **non-monotonic** entropy — two messages in the same millisecond can sort either way, so a `WHERE id > lastID` delta fetch could silently skip one and desync the conversation. Folding by the exact IDs the loop creates is order-safe; a deleted (orphaned, guidance-cancelled) assistant message returns `(nil,nil)` from `GetMessage` and is skipped, so the working set matches a full reload. A `GetMessage` read error falls back to a full reload. Working set is capped to the last 1000 (mirrors the old window).
- **Tests:** new `TestRunLoop_WorkingSetAccumulatesAcrossToolRounds` drives 3 tool rounds and asserts the final provider request carries all 3 `tool_use`/`tool_result` pairs in order; full agent suite + the guidance/orphan/cancel tests pass under `go test -race`.

---

## P1-3 — Token budgeting is bytes÷4, not real tokens

**Status:** Done · **Priority:** P1 (accuracy)

**Evidence**
- `internal/agent/loop.go` `estimateRequestSize` counts bytes; the proactive-compaction trigger compares byte estimate against a byte threshold. The real input-token count is only available *after* step 1, and tool-schema tokens are never counted.

**Impact**
- Dense code/JSON under-counts on the first (largest) request — the case most likely to overflow. Over/under-compaction.

**Reference behavior**
- TS opencode budgets against `model.limit.input − reserve` using actual `usage.total`.

**Fix plan**
- Integrate a real tokenizer (e.g. `tiktoken-go`, or Anthropic `count_tokens`); include serialized tool schemas in the estimate; keep the byte estimate only as a fallback for unknown tokenizers.

**Resolution (2026-08-15)**
- **Decision — no heavyweight tokenizer dependency.** Anthropic's tokenizer is closed; OpenAI's needs a large vendored vocab; the only tokenizer already in the tree (`daulet/tokenizers`, pulled in by the local embedder) is the MiniLM vocab (wrong counts) and CGO-heavy. This app is deliberately offline-first and dependency-light, and the provider already reports the **exact** input-token count after step 1. So a real tokenizer would add weight for marginal value.
- **What shipped** (`internal/agent/tokens.go`): the whole budget is now **token-based** (`estimateRequestTokens` / `effectiveRequestTokens` / `compactionThresholdTokens`), replacing the bytes÷4 round-trip in `loop.go`. New `estimateTokens` blends the byte estimate with a segment count (each alphanumeric run + each punctuation/symbol/non-ASCII rune ≈ 1 token) so it tracks real BPE within ~20% for **both** prose and dense code/JSON — no more 2–3× under-count on JSON. Tool-definition schemas are now counted (previously skipped). Images use a flat ~1600-token cost instead of being counted as ~len(base64)/4 text tokens (which over-counted a 1 MB image by ~100× and could trip compaction spuriously). The provider's exact reported count is still preferred once available.
- **Tests:** `internal/agent/tokens_test.go` (prose not over-counted, dense JSON lifted above the byte estimate, tool schemas counted, image flat cost); `compaction_threshold_test.go` rewritten token-based; migrated the two existing `estimateRequestSize` tests. `go build`/`vet`/`test -race` (agent) green.

---

## P1-4 — Model cannot delegate subtasks

**Status:** Done (read-only, depth-1, synchronous) · **Priority:** P1 (capability)

**Evidence**
- The only nested loop is the fixed `deep_search` → `RunSearchSession` in `internal/agent/loop.go`. Build/Plan agents cannot spawn a child agent on demand; the worktree pipeline is UI-driven, not model-driven.

**Reference behavior**
- Both OpenCodes expose a model-callable `task`/`agent` tool (TS's is resumable + backgroundable; Go's is a read-only depth-1 sandbox).

**Fix plan**
- Add a `task`/`spawn_agent` tool that reuses `RunLoop` (optionally the worktree infra), returning the child's final text part. Start with a read-only, depth-1, synchronous variant (like Go opencode) to bound blast radius.

**Resolution (2026-08-15)**
- New model-callable **`task` tool** (`internal/tool/task.go`, `{description, prompt}`) delegates a self-contained investigation to an autonomous sub-agent and returns its final written answer as the tool result. Wired to `LoopRunner.RunTaskSession` (mirrors `RunSearchSession`: ephemeral session → nested `RunLoop` → final text → session deleted) in both `server.go` and the CLI.
- New **`SubagentAgent`** (`id "subagent"`) runs the child loop with two hard invariants: **read-only** (no `write`/`edit`/`bash` — a headless, ungated child can never mutate the repo or run a shell) and **depth-1** (its toolset omits `task`, so it cannot recurse). Tools: `read, glob, grep, codebase_map, deep_search, view_image, memory_recall, {pdf,docx} readers`. Added to `codingAgentTools` (Build/Task) and `PlanAgent` so those agents can delegate; child runs with `Permissions=nil` and `WithoutLoopControl` (headless), a 40-step cap, and a 300s tool-level timeout. Cancellation propagates from the parent tool context.
- **Tests:** `internal/agent/subagent_test.go` — `TestSubagentAgent_ReadOnlyDepth1` locks the read-only/depth-1 invariants and parent delegation; `TestRunTaskSession_ReturnsFinalTextAndCleansUp` drives the full child loop and asserts the answer is returned and the ephemeral session is deleted. `go build`/`vet`/`test -race` green.
- **Follow-ups (not in scope):** the sub-agent session is ephemeral (not shown in the UI); background/async and resumable sub-agents (like TS opencode's) are future work; `bash` is intentionally excluded — revisit if delegated investigations need shell.

---

## P2-1 — Compaction is destructive and the summary grows unbounded

**Status:** Done · **Priority:** P2

**Evidence**
- `internal/agent/loop.go` `llmCompact` **appends** each new summary to the persisted `compactionSummary` and re-injects it as a system block; `keepRecent = 12` is a fixed count regardless of context window.
- Oversized tool results are head-truncated globally (`capToolOutput`) rather than previewed-and-spilled.

**Reference behavior**
- TS opencode: `preserveRecentBudget = clamp(2k…15k, ~25% of usable)`, prunes old tool outputs, and spills full tool output to storage keeping only a bounded preview in history.

**Fix plan**
- Cap / re-summarize the running summary so it can't grow without bound; scale "keep recent" to the context window; keep a bounded preview in history and spill full large tool outputs to disk.

**Correction:** on closer reading the summary does **not** grow unbounded via append — the loop *replaces* `compactionSummary` each time (`compactionSummary = summaryAddendum`). The real defect was that re-compaction was **destructive**: `llmCompact` regenerated the summary from the current messages alone and never folded in the prior summary, so once a summary was superseded, the far-past context it captured was dropped — worst on long turns where the working-set cap (P1-2) also trims the oldest messages. Plus two quality issues: `keepRecent` fixed at 12, and history truncation kept the *oldest* 30k chars.

**Resolution (2026-08-15)** — all in `internal/agent/loop.go`:
- **Non-destructive folding (B):** `llmCompact` now takes the prior summary and, when present, instructs the summarizer to **merge** it with the new history into one updated summary ("keep every still-relevant fact from the prior summary"). The new summary supersedes the old, so it's **replaced** in the system prompt via `replaceOrAppendSummary` (not appended) to avoid duplicating the folded content. Result: earlier context survives re-compaction, and the summary stays bounded (one merged summary, capped by the summarizer's output budget).
- **`keepRecent` scales with the window (A):** `compactionKeepRecent(contextWindow)` → 8…40 (default 12 when unknown), so a 200k model keeps ~20 recent messages verbatim and an 8k model keeps 8.
- **History keeps the recent tail (C):** the 30k-char cap now keeps the *most recent* old exchanges (older ones are covered by the folded prior summary) instead of the oldest.
- **Tests:** `internal/agent/compaction_test.go` — `compactionKeepRecent` scaling, `replaceOrAppendSummary` (replace-in-place vs append), and two `llmCompact` tests (prior summary folded into the summarizer prompt; plain framing + 40-message keep on a 400k window) using a capturing mock summarizer. `go build`/`vet`/`test -race` green.
- **Deferred follow-up (E):** tool-output preview-and-spill (bounded preview in history, full text to disk) — a larger change needing a spill store; noted, not done. The global `capToolOutput` head-truncation stays for now.

---

## P2-2 — Error classification by substring matching

**Status:** Done · **Priority:** P2

**Evidence**
- `internal/agent/loop.go` `isTransientError` / `isContextLengthError` sniff lowercased error strings; retry backoff is `attempt²` seconds and **ignores `Retry-After`**.

**Reference behavior**
- Go opencode honors `Retry-After` with 8 retries + jitter; both references use typed errors.

**Fix plan**
- Have providers return structured errors (status code + category). Classify on those; honor `Retry-After` when present.

**Resolution (2026-08-15)**
- New `provider.APIError` (`internal/provider/errors.go`) carries `StatusCode`, parsed `RetryAfter`, `Body`, and `Provider`, with `IsTransient()` (429/529/5xx), `IsContextLength()` (400 + body match, or Ollama's empty-body 400), and a shared `IsContextLengthMessage` helper. `Error()` keeps the historical `"<provider> API error <code>: <body>"` string so logs and the string fallbacks are unchanged. The Anthropic and OpenAI-family providers now return it (via `NewAPIError`, which parses the `Retry-After` header — integer seconds or HTTP-date).
- The loop's `isTransientError` / `isContextLengthError` now prefer the typed error via `errors.As` (unwraps through `fmt.Errorf("...: %w")`), falling back to string matching only for stream/network errors with no status. Net correctness win: a **400 whose body merely contains "timeout" is no longer retried** (the old string matcher retried it).
- The retry backoff now **honors `Retry-After`** when the server sends it (capped at `maxRetryBackoff` = 120s so a pathological value can't stall the loop), otherwise keeps the quadratic 1s/4s/9s schedule.
- **Tests:** `internal/provider/errors_test.go` (Error format, IsTransient/IsContextLength tables, `parseRetryAfter` incl. HTTP-date, `NewAPIError`); `internal/agent/retry_classify_test.go` (typed-vs-fallback classification, wrapped errors, the 400-with-"timeout" case, `retryAfterFromError`). Existing `TestIsContextLengthError` still passes. `go build`/`vet`/`test -race` (provider, agent) green.

---

## P2-3 — Lossy event bus

**Status:** Done · **Priority:** P2

**Evidence**
- `internal/bus/bus.go` `Publish` uses `select { case ch <- evt: default: }` — drops on a full buffer (`bufSize` 1024). A slow SSE client during a fast stream (300 ms text flushes + per-part updates) can silently desync the UI.

**Reference behavior**
- TS opencode uses a durable, sequenced `session_message` log replayed by a projector; live UI bus is separate.

**Fix plan**
- For UI deltas the drop is tolerable; if reliability is wanted, add a per-subscriber sequence number + client resync, or a durable step log so a server restart mid-loop can resume instead of orphaning the partial assistant message.

**Resolution (2026-08-15)**
- Kept Publish **non-blocking** (a full buffer must never stall the agent loop) but made drops **detectable**: `bus.Event` now carries a bus-global monotonic `Seq` stamped on every publish (`internal/bus/bus.go`), plus a `Dropped()` counter for observability. Control frames (`server.connected/config/heartbeat`) are sent directly by the SSE handler and carry no seq, so they're excluded from gap detection.
- **Client resync:** the SSE handler already marshals the whole `bus.Event`, so seq rides along for free. The web client (`web/src/context/server.tsx`) tracks the last seq per connection and, on a gap (`seq > lastSeq+1`), ticks a new `resyncTick`; `web/src/context/session.tsx` reacts by re-fetching the active session's messages — healing any UI staleness a dropped `message.updated` would cause. Out-of-order/duplicate seqs (possible under concurrent publishes) at worst cause a harmless extra resync; seq resets on reconnect.
- **Tests:** `internal/bus/bus_test.go` — monotonic seq, a dropped event leaving a detectable seq gap + `Dropped()` count, and all subscribers seeing the same seq for one event. `go build`/`vet`/`test -race` (bus) green; frontend `tsc` clean + `vite build` OK.
- **Deferred (heavier):** a durable, sequenced event log for crash/restart replay (TS opencode's `session_message`) — larger architectural work, noted not done.

---

## P2-4 — One system prompt regardless of model family

**Status:** Done · **Priority:** P2

**Evidence**
- `internal/agent/loop.go` `buildSystemPrompt` emits the same body for every provider/model.

**Reference behavior**
- TS opencode routes to 8+ per-model prompt files; Go opencode branches Anthropic-vs-OpenAI.

**Fix plan**
- Branch the base coding prompt by provider family (at minimum Anthropic vs OpenAI-compatible vs local), so non-Claude models get an appropriately-tuned prompt.

**Resolution (2026-08-15)**
- New `modelFamily(providerID, modelID)` (`internal/agent/prompt_builder.go`) classifies into `anthropic` / `openai` / `gemini` / `local` / `generic`, with **model-name signals winning over the provider id** (so Claude/GPT/Gemini served via OpenRouter or the free pool are classified correctly; `ollama` → `local` regardless of model).
- New `modelFamilyStylePrompt(family)` returns a short **working-style block** appended to the coding prompt for codebase agents. The base prompt is already Claude-tuned, so `anthropic`/`generic` add nothing; `openai` gets "act decisively, minimal preamble, verify with tools", `gemini` gets "follow the format, be concise, use tools directly", and `local` gets "keep it short, ONE tool at a time, never invent paths/output, prefer the simplest solution".
- Chose a **style-block injection over eight separate full prompts** (closer to the Go OpenCode's Anthropic-vs-OpenAI split than TS's per-model files): it delivers the per-family tuning that matters without maintaining divergent copies of the large shared prompt. `buildSystemPrompt` is now a thin wrapper (family = generic) over `buildSystemPromptForFamily`, which the loop calls with the resolved family. The block is part of the **static, cacheable base prefix** (model is fixed per session), so prompt caching is unaffected. Injection is gated to project-scoped agents (Build/Task/Plan/Note), never utility agents.
- **Tests:** `internal/agent/model_family_test.go` — `modelFamily` classification table (incl. aggregator/model-name precedence), style-block presence per family, and injection gated to codebase agents (present for openai Build, absent for anthropic, absent for the wrapper, absent for the non-codebase Index agent). Existing `prompt_builder_test.go` still passes via the wrapper. `go build`/`vet`/`test -race` green.

---

## Progress log

- **2026-08-14 — P0-2 Done.** Added `internal/tool/pathlock.go` (per-path keyed mutex); `write` and `edit` now serialize read-modify-write on the same file while different files and reads stay parallel. `go build ./...` + `go vet` clean, `go test ./internal/tool/...` green.
- **2026-08-14 — P0-1 step 1 Done (denylist).** Added `internal/tool/bash_safety.go` + `bash_safety_test.go`; the `bash` tool now refuses a conservative set of catastrophic commands (recursive root/home deletion incl. `sudo`, `mkfs`, raw disk `dd`/redirect, fork bomb) and returns the refusal as a normal result so the loop continues. Verified it does **not** false-positive on normal dev commands (`rm -rf node_modules`, `rm -rf ./build`, `chmod -R 755 ./public`, `dd if=in of=out`, …).
- **2026-08-15 — P0-1 Done (interactive approval prompts).** Chosen UX: full interactive per-tool prompts. Backend: `permission.Manager` made concurrency-safe + per-session rulesets + `Remove`/`Ruleset`/`AddRule` (`internal/permission/permission.go` + tests); `LoopRunner.Permissions` wired; `executeTool` now evaluates the session ruleset and, on `Ask`, publishes `permission.requested` and **blocks** on the reply channel (respecting ctx/guidance cancellation) — `once`/`always`/`reject` supported, `always` records a session grant; gating is opt-in per run via `WithPermissionGating`, set only on the interactive chat context, so headless loops (task/breakdown/note/search/CLI) never block; `handlePermissionReply` now resolves the pending request via `Manager.Reply`. Frontend (SolidJS): `replyPermission` typed; `pendingPermissions` queue + `respondPermission` in the session context, driven by `permission.requested`/`permission.replied` SSE events; new `PermissionPrompt` component rendered above the composer. `go build`/`vet`/tests green; `tsc` clean on touched files; `vite build` OK.
- **Note / follow-ups for P0-1:** rulesets are in-memory (reset on server restart) — could persist to the existing `session.permission` column later; `matchGlob` is exact-or-`*` only (fine for the current tool/`*` rules); plan-mode interactive bash also prompts now (correct, has UI).
- **2026-08-15 — P1-2 Done.** In-memory working set with known-ID folding replaces the per-step full reload; see the P1-2 Resolution section above. `go build`/`vet`/`test -race` (agent) green.
- **2026-08-15 — P1-3 Done.** Token-based compaction budgeting with a dependency-free BPE-approximating estimator, tool-schema counting, and flat image cost; see the P1-3 Resolution section above. `go build`/`vet`/`test -race` (agent) green.
- **2026-08-15 — P1-4 Done.** Model-callable `task` tool + read-only, depth-1, synchronous `SubagentAgent` via `RunTaskSession`; see the P1-4 Resolution section above. `go build`/`vet`/`test -race` (agent, tool) green.
- **2026-08-15 — P2-2 Done.** Structured `provider.APIError` (status + Retry-After), typed transient/context classification with string fallback, and Retry-After-honoring backoff; see the P2-2 Resolution section above. `go build`/`vet`/`test -race` (provider, agent) green.
- **2026-08-15 — P2-1 Done.** Non-destructive compaction (prior summary folded/merged, replaced not appended), window-scaled `keepRecent`, recent-tail history truncation; see the P2-1 Resolution section above. `go build`/`vet`/`test -race` (agent) green.
- **2026-08-15 — P2-4 Done.** Model-family classifier + per-family working-style block injected into the coding prompt (openai/gemini/local), cache-stable; see the P2-4 Resolution section above. `go build`/`vet`/`test -race` (agent) green.
- **2026-08-15 — P2-3 Done.** Bus event `Seq` + `Dropped()` counter (non-blocking Publish kept) and client seq-gap → resync in the web app; see the P2-3 Resolution section above. `go build`/`vet`/`test -race` (bus) green; frontend `tsc`/`vite build` OK.
- **2026-08-15 — P1-1 Done (stream core deferred by decision).** Four behavior-preserving extractions from `RunLoop` (`compactRequest`, `resolveRunModel`, `executeReadyToolCalls`, `writeToolResultMessage`); ~1,310 → ~1,024 lines; `test -race` green after each. Stream-processing core left in place by explicit decision (see P1-1 section). No behavior change.

---

## Summary — all items resolved

Every P0/P1/P2 item from the audit is landed and tested (P1-1 to the agreed scope). Full `go build ./...` + `go vet ./...` clean; `go test -race` green across `internal/{agent,provider,tool,permission,bus}`; frontend `tsc` clean + `vite build` OK. Work is in the working tree, uncommitted.
