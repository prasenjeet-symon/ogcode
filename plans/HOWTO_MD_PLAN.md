# HOWTO.md — Project Procedure Record

**Status:** design only. No code in this plan has been written.

## 1. Summary

Add a third project document beside `AGENT.md` and `MEMORY.md`:

| File | Holds | When the agent needs it | Delivery |
|---|---|---|---|
| `AGENT.md` | Rules that govern behaviour | Always | Loaded into entry [0] |
| `MEMORY.md` | Invariants, canonical values, gotchas | Before acting | Loaded into entry [0] |
| **`HOWTO.md`** | **Repeatable procedures this repo has verified** | **Only when doing X** | **A fixed pointer; nothing from the file is inlined** |

The load-bearing decision is in the last column: **no content from `HOWTO.md` enters the system prompt** — not the body, and not a heading index either. The prompt carries one short,
constant paragraph: what the file is, that it sits at the project root, and that it is worth reading when the task in front of the agent looks like one of its procedures. The agent opens it with the same file tools it uses on every other project file.

There is no table of contents in the prompt and no existence check. Discovery is already solved — `codebase_map` lists the root, `read` returns the file, `grep`/`glob` find it — and a second discovery mechanism inside the prompt would be a copy that can disagree with the file.

Procedures are the content that is large, long-lived, and needed rarely. Eagerly loading them, or even indexing them, spends per-step prompt bytes on information nobody has asked for yet. One paragraph saying "this file exists, here is when to open it" is the whole of what the prompt should carry.

## 2. The problem

`MEMORY.md` in this repo is **188,057 bytes**. It is pasted verbatim into entry [0] of the system prompt (`internal/agent/loop.go:3926-3928`), and `mdSizeLimit` returns `0` — no budget — unless `OGCODE_MEMORY_MD_MAX_BYTES` is set (`internal/agent/mdblock.go:41-59`). So it is re-sent on every step of every turn, at full price on any provider without prefix caching, and it shortens the context window before compaction for every provider.

Most of that file is not the same *kind* of content, and the kinds have opposite delivery requirements:

- **Invariants and canonical values** — `CGO_ENABLED=1` is mandatory; goose keys migrations by version number; the preview URL needs the server port. These must be present *before* the agent acts. Absence fails silently. Eagerly loaded is correct.
- **History** — "the flat edit schema was tried and reverted", "this was the root cause". Short, verifiable, and recency-sorted retrieval is the right store. This is what `project_memory_recall` is for, and `MEMORY.md` has been double-writing it by hand.
- **Procedures** — "redeploy the control plane", "screenshot the UI through headless Chrome", "release vX.Y.Z". Large step lists, needed only when the task is that thing, and they never belong in a per-step prompt.

The third kind is the one with no home. It currently lives in `MEMORY.md` under headings like `### Local dev run`, `### Release`, `### Screenshotting the UI (headless Chrome recipes)`. In this repo it has already gone wrong in the way the feature is meant to prevent: the headless-Chrome recipe appears **twice** (`MEMORY.md:595` and `:1016`), because nothing makes the agent check whether a procedure already exists before writing a new one.

## 3. What HOWTO.md is

A project-level markdown file, **committed to the repo**, holding the procedures that have been run in this project and worked.

Committed, not runtime state, and that distinction carries the design:

- It is reviewable in a PR — a wrong procedure can be argued with before it lands.
- It travels with the repo — a fresh clone has it on the first turn.
- It has an **edit path**. A failing step is fixed by the next agent in one commit. `.ogcode/notes/` and the turn-summary corpus have no such path, which was the decisive argument against putting procedures in the scribe's store.

### 3.1 The three-way split

The split is by *content type*, and the test for which file a piece of knowledge belongs in is a question about the reader:

1. **Does the agent have to obey it regardless of what it is doing?** → `AGENT.md`. Hard rules, repo conventions that apply to every change.
2. **Does the agent have to know it before it acts, and is it small?** → `MEMORY.md`. Invariants, canonical paths/env vars/defaults, gotchas with their fix, decisions and why.
3. **Does the agent need it only while performing one named task?** → `HOWTO.md`. Ordered steps, exact commands, the check that says it worked.

If a sentence would be true and useful in a turn that has nothing to do with the procedure, it is not a procedure — it belongs in `MEMORY.md`.

### 3.2 The corollary: the environment is not a procedure

A procedure is only worth recording when the answer is **not derivable** from what the agent can already see. If the `Makefile` has the recipe, `--help` prints the flags, or the file tree shows which module to build in, then a HOWTO entry is a **cache, not a procedure** — and a cache that is re-read costs more than the lookup it saves.

The concrete form of the test, at write time:

> Could a competent agent that has read `AGENT.md`, `MEMORY.md`, the `Makefile` and the file tree get this right on the first attempt?

If yes, do not write it. What earns an entry is the part that *is* surprising: the ordering constraint, the step that looks skippable and is not, the flag without which the thing silently 401s, the cleanup that avoids a stale-code-signature kill.

## 4. No content in the prompt

### 4.1 What the current load path does

`LoadAgentMD` and `LoadMemoryMD` (`internal/agent/agentmd.go:41`, `internal/agent/memorymd.go:30`) walk from the working directory up to the filesystem root, collect matches root-to-leaf, dedupe byte-identical files, and return the concatenation. `RunLoop` calls both once per turn (`internal/agent/loop.go:300-306`) and hands the result to `staticSystemPrompt`, which pastes them raw into entry [0] (`:3922-3928`).

Copying that for `HOWTO.md` would mean a second file of unbounded size in the block that is re-sent on every step of every turn. It would make the prompt larger than it is today on the very turn the file gains content — the opposite of the goal.

A heading index — the previous shape of this design — avoids the body but keeps a second problem: it is derived state. Every edit to `HOWTO.md` leaves the prompt's copy stale until the next turn rebuilds it, and the prompt is then describing headings the agent is one read away from anyway.

### 4.2 The fixed pointer

The prompt section is a constant description, not a payload. It has two branches, mirroring `memoryMDPrompt`, and the prose below is the deliverable — written out here rather than summarised, because it is the only place the authoring rules reach the agent.

**Every project-scoped agent:**

```markdown
## HOWTO.md — Project Procedures

`HOWTO.md` at the project root records procedures this project has verified — ordered
steps that were actually run, plus the check that says they worked. Open it when the
task in front of you looks like one it may already cover; when no heading matches, it
has nothing for this task and you are done with it.

Each entry is a `## ` heading naming a task, then `Verified:`, `Touches:`, the steps,
and `Done when:`. Follow the steps for the task a heading names, subject to the rules
above.
```

**A writable agent** appends this — it is the part that makes the file fill up:

```markdown
### Adding an entry

When a turn ran a procedure end to end and it worked, record it before you finish —
unless the `Makefile`, `--help` or the file tree already answers it. Read `HOWTO.md`
first, creating it at the project root if it does not exist; if an entry already
covers the task, update it in place, never a second copy.

Name the heading for the task, not the tool, and record the path that worked rather
than the one you planned. Keep it to about 30 lines:

Verified: <date> @ <version from `ogcode version`>
Touches: <paths or services the steps write to>

1. <exact commands, ordered; one clause wherever the order matters>
2. ...

Done when: <the check that says it worked>
```

Nothing is listed — not headings, not a count, not whether the file exists. The agent answers "is there a procedure for this?" the same way it answers "is there a file for this?" on every other task: by looking.

Three consequences, all deliberate:

- **Nothing to regenerate.** The section is a constant, so editing `HOWTO.md` never rewrites entry [0]'s cached prefix. Entry [0] as a whole is rebuilt once per turn (`buildSystemPromptEntries`, `internal/agent/loop.go:3841-3869`, which re-reads `AGENT.md`/`MEMORY.md` there), but nothing about `HOWTO.md` takes part in that rebuild — there is no machinery to keep in sync and no derived state to go stale.
- **One source of truth.** The headings live in exactly one place. A prompt-side copy can disagree with the file — a heading for an entry that was deleted is worse than no index, because it sends the agent looking for something that is not there.
- **Discovery costs one call, at the moment it matters.** The agent is already reading project files to do the work; opening `HOWTO.md` is the same call, and `codebase_map` at the root names it without one.

What the section must state, because none of it can be inferred:

- what the file is, and where it is;
- when to open it — a task-shaped trigger, plus the fact that no read is needed when nothing matches;
- for a writable agent, when to add an entry and the shape it takes — §6 and §7 compressed to the mechanics the agent cannot infer;
- that the body is the project's own procedure record rather than untrusted material (§9) — it now arrives through a tool result, so the prompt is the only thing that grants it standing.

What it must not do: list or cap headings, cache them, stat the file to choose a wording branch, or paste anything from the file.

### 4.3 Why not built-in skills

A skill is the natural container for a procedure — a name, a description, and a body loaded only when the model selects it — and `SourceEmbedded = "built-in"` already exists in `internal/skill`. It is not usable today: the build agent's skill list is gated on `!intentOffered` (`internal/agent/loop.go:803`, `if guidance := skillGuidancePrompt(visibleSkills); guidance != ""`), and the intent matcher is on for every build turn. A built-in skill therefore cannot reach the agent that would use it.

`HOWTO.md` needs no gate, no registry, no description-matching model call, and no change to the skill subsystem. It is the version that lands now. If the skill gate opens later, a `HOWTO.md` entry can become a built-in skill without changing what the file records — see §10.8, phase 3.

## 5. Where the file lives

- **Project root**, beside `AGENT.md`, at a fixed path. **No walk-up discovery**, because nothing loads the file: the prompt names the path and the agent opens it. `AGENT.md` and `MEMORY.md` need a walk precisely because a loader has to find them in order to inject them; `HOWTO.md` has no loader, so it needs no walk.
- **Tracked by git.** It must not be added to `.gitignore`. Note that `MEMORY.md` *is* gitignored (`.gitignore:76`) — the two files deliberately differ here, and the difference is the feature.
- **Indexed by the project index with no code change.** `textExtensions` already contains `".md"` (`internal/indexer/text.go:12-14`), the indexer re-indexes changed files by mtime (migration 049's `mod_time`), and `codebase_map` lists root-level files. So `HOWTO.md` appears in the document index and is searchable by label as soon as it exists.
- **But the prompt does not depend on the index.** Auto-indexing is consent-gated (`OGCODE_AUTO_INDEX_MAX_FILES`, default 500, and a project that declined consent) and absent on a fresh clone. The named path works with `read` regardless, which is why the file lives at a predictable root path rather than in `docs/`.

## 6. Entry format

One `## ` heading per procedure. The heading is what a reader scanning the file is looking for, so it names the **task**, not the tool: `## Redeploy the control plane`, not `## Makefile`.

```markdown
## Redeploy the control plane

Verified: 2026-10-19 @ v0.48.0
Touches: controlplane/, ~/.ogcode-control-plane/

1. `cd controlplane && make build`
2. Stop the running master before touching the user store — bbolt is single-writer.
3. `./bin/ogcode-control-plane users add operator` (password via `$OGCODE_CONTROL_PLANE_USER_PASSWORD`, never argv)
4. Relaunch with `nohup ./bin/ogcode-control-plane serve --config ~/.ogcode-control-plane/config.json >> /tmp/cp.log 2>&1 &`

Done when: the console answers on the configured port and the worker re-registers within ~1s.
```

Four fields, each with a reason:

- **`Verified:`** — date plus the version the procedure was last run at. It is the staleness signal. An entry whose version is far behind `ogcode version` is not forbidden, but it should be re-verified on first use rather than followed blindly.
- **`Touches:`** — the paths or services the steps write to. A reader can tell in one line whether the procedure is in the blast radius of the task at hand.
- **The steps** — ordered, exact, and including the non-obvious constraint that makes the order matter. A step that looks skippable and is not gets one clause explaining why. Do not write the whole rationale: that is `## Decisions & why` material and belongs in the commit or `MEMORY.md`.
- **`Done when:`** — the completion check. Without it a procedure has no way to be wrong, which is how stale procedures survive.

Keep an entry to roughly 30 lines. When a procedure needs more than that, the entry holds the short version and a pointer to the full walkthrough (`controlplane/docs/deploy.md`), which is the same shape `MEMORY.md` already uses for the deploy guide.

## 7. Write gate

The agent writes to `HOWTO.md` at the **end of the turn**, after the work is green — build passes, tests pass, the thing was actually observed to work. Four conditions, all required:

1. **It was run this turn.** Not recalled, not inferred from reading code. The agent performing the procedure has the tool output; the memory scribe does not (its digest carries tool names and inputs, not results), which is why the scribe is not the writer.
2. **The agent has read `HOWTO.md` this turn and it does not already cover it.** Near-miss → update that entry in place. Do not append a second version. With no prompt-side index, this read *is* the dedupe check, so the gate has to name it explicitly.
3. **It is not answerable from the environment** (§3.2). If the `Makefile` says it, skip it.
4. **The steps survived a real run.** If the turn succeeded by a different path than the one attempted, record the path that worked, not the path that was planned.

## 8. Read and repair contract

- **Reading is on demand, through the ordinary file tools.** The prompt carries no content and no index; if nothing in the task calls for it, `HOWTO.md` never enters the turn.
- **Finding it is the normal problem.** The prompt names the root path; `codebase_map` at the root lists it; `read` opens it. No dedicated tool, because the file is committed and the file tools already reach it.
- **A failing entry is fixed in the same turn.** This is the compensating control for the strongest objection to recording procedures anywhere: a wrong procedure still executes. Because the file is committed and editable, the agent that discovers a broken step rewrites it before the turn ends, and the fix is a normal commit. That is the capability the append-only memory corpus lacks.
- **A stale `Verified:` is a prompt to check, not a reason to refuse.** Re-verify the first step, then follow.

## 9. Authority boundary

`AGENT.md` is the only document with instruction authority. `MEMORY.md` deliberately has none — it cannot forge an `agent-md` block (`internal/agent/mdblock.go:17-25`, pinned by `TestLoadMemoryMD_CannotForgeAnAgentMDBlock`). `HOWTO.md` sits between them, and the boundary now matters more than it did when a heading index was on the table:

> `HOWTO.md` is the project's own procedure record. Follow the steps for the task it names, subject to the rules above.

The body reaches the agent as a **tool result**, which the untrusted-content rule would otherwise treat as data, so this sentence is the only thing that gives a reviewed, committed procedure its standing. It grants standing and nothing else: a procedure cannot override a permission rule, a `Deny` grant, or a hard rule, and it cannot claim the authority of `AGENT.md`.

Since nothing is ever inlined, there is no `<howto-md>` tag, nothing to add to `mdWrapperTag`, and no forgery surface to pin — the boundary is a sentence of prose, not a parser.

## 10. Implementation plan

### 10.1 Files to read first

| File | Why |
|---|---|
| `internal/agent/prompt_builder.go:238-289` | `memoryMDPrompt` — the sibling to sit beside, and the sentence to amend |
| `internal/agent/loop.go:3906-3948` | `staticSystemPrompt` — the one call site |
| `internal/agent/mdblock.go:17-59` | The authority comment and `mdWrapperTag` — what *not* to reuse |
| `internal/agent/tool_reachability_test.go:21-38,377` | `mandatoryPromptTools`, and the dangling-cross-reference guard |

### 10.2 Proposed structure

```
internal/agent/
  prompt_builder.go   + howToMDPrompt(canWriteFiles bool), amended memoryMDPrompt
  loop.go             one added call in staticSystemPrompt
  prompt_builder_test.go / tool_reachability_test.go   extended
```

No new file, no loader, no constants, no migration, no tool, no route, no web change. The entire feature is one constant prompt function and one call site.

### 10.3 Step 1 — `howToMDPrompt` in `prompt_builder.go`

```go
// howToMDPrompt returns the HOWTO.md pointer. It carries no content from the
// file: discovery happens through the ordinary file tools, so the section is a
// constant that never takes part in the per-turn rebuild of entry [0].
func howToMDPrompt(canWriteFiles bool) string
```

Two branches, mirroring `memoryMDPrompt`'s split:

- **Read-only agent** — §4.2's first block verbatim: what the file is, where it is, when to read it, and that the steps are followed subject to the rules above.
- **Writable agent** — that block, then §4.2's `### Adding an entry` block verbatim: the write gate from §7 in prompt form — at the end of a green turn, only what was run, read the file first and update in place, skip what the environment already answers, and the `Verified:`/`Touches:`/`Done when:` shape.

The read-only branch is ~530 B and the writable one ~1.2 KB, both constants. The section is in entry [0] and re-sent every step, so the argument for each rule is cut: state the rule and the mechanics the agent cannot infer. **Do not shorten the writable branch back to a bare pointer** — the authoring rules exist nowhere else in the prompt, and without them the file stays empty.

If the prose names a tool id, keep it to `codebase_map` — every agent that receives this section holds it by construction, because `projectScoped()` is defined as `HasTool("codebase_map")`. Write "read it" as prose rather than backticking `read`, which would be naming a tool the gate does not guarantee; if a tool id other than `codebase_map` is ever named, add it to `mandatoryPromptTools` (`internal/agent/tool_reachability_test.go:21`).

### 10.4 Step 2 — one call site

In `staticSystemPrompt`, immediately after the `memoryMDPrompt` line (`internal/agent/loop.go:3938`):

```go
prompt += "\n\n" + howToMDPrompt(canWriteFiles)
```

It sits inside the existing `if a.projectScoped()` block, so utility agents never see it, and it takes no `hasFile` argument: there is no filesystem check, so the section is emitted unconditionally for every project-scoped agent.

`projectScoped()` is `a.HasTool("codebase_map")` (`internal/agent/agent.go:497-504`), so the section reaches agents that can actually read project files and never reaches the ones that cannot. Nothing is loaded in `RunLoop` — there is no `HOWTO.md` equivalent of the `agentMDContent` / `memoryMDContent` reads at `loop.go:300-306`.

### 10.5 Step 3 — carve procedures out of `memoryMDPrompt`

The sentence at `internal/agent/prompt_builder.go:263` currently claims `MEMORY.md` holds "conventions, architecture, gotchas, and facts like config values, versions, and build/test commands". Amend it to name the split: procedures belong in `HOWTO.md`; `MEMORY.md` keeps invariants, canonical values and gotchas. This is the paragraph that decides what the agent writes where, so leaving it alone means the new file stays empty while `MEMORY.md` keeps growing.

`TestBuildSystemPrompt_NoDanglingMemoryMDReference` (`internal/agent/tool_reachability_test.go:377`) is the existing guard against a stale cross-reference here; extend it to cover the new `HOWTO.md` sentence.

### 10.6 Step 4 — tests

- **The invariant:** `TestStaticSystemPromptNeverInlinesHowToMD` — write a `HOWTO.md` with a sentinel body into a temp working directory, build the static prompt for the build agent, assert the sentinel (and every `## ` heading from it) is absent. This is the test the whole design turns on, so it should fail loudly if anyone reintroduces a heading index or a body paste.
- Present for the build agent and Plan; absent for the keyword indexer and web-research agents (utility, not project-scoped).
- Writable vs read-only wording differs, and the read-only branch carries no maintenance instructions.
- The writable branch names all three fields (`Verified:`, `Touches:`, `Done when:`) and the read-before-writing dedupe; §4.2 is the only place the entry format is described to the agent, so a prompt edit that drops a field fails here.
- The section names no tool the receiving agent lacks (covered by the `mandatoryPromptTools` invariant if a tool id is added).
- The amended `memoryMDPrompt` sentence names `HOWTO.md` and still does not dangle when no `MEMORY.md` exists.

### 10.7 Step 5 — docs

- `AGENT.md` `## Memory` gains a short `## Procedures` paragraph: what goes in `HOWTO.md`, the write gate, and that the prompt carries a pointer rather than the content.
- `docs-site/src/content/docs/memory-and-context.md:12-13` lists `AGENTS.md`/`AGENT.md` and `MEMORY.md`; add `HOWTO.md` to that list.

### 10.8 Phasing

| Phase | Work | Why this order |
|---|---|---|
| 1 | `howToMDPrompt`, the call site, the `memoryMDPrompt` amendment, tests, docs | The pointer must exist before anything moves into it. **DONE** |
| 2 | Migrate the procedure sections out of `MEMORY.md` (§11) | **DONE 2026-10-19** — `HOWTO.md` created with ten entries, ≈9.9 KB moved out of `MEMORY.md` |
| 3 | Optional: expose entries as built-in skills if the build-agent skill gate opens | **SUPERSEDED** — the per-turn `update_intent` matcher (`internal/agent/intent_tools.go`) is now the build agent's skill-discovery path, so a HOWTO-entry skill list would duplicate it |

## 11. Migrating `MEMORY.md` (phase 2 — DONE 2026-10-19)

**Landed**: `HOWTO.md` at the repo root, tracked (NOT gitignored — `MEMORY.md` is), 10 entries, 12.5 KB. ≈9.9 KB of prose left `MEMORY.md` (190,496 → ~183 KB after the record of the move itself was added): six sections deleted, the rest replaced by a pointer, every gotcha and invariant left in place. Only the WebP recipe was re-run; every other entry is marked `migrated from MEMORY.md, not re-run`.

**A move, not a copy.** A section that lands in `HOWTO.md` is deleted from `MEMORY.md` in the same edit; otherwise this becomes a third copy of the same procedure. The table below is the record of what moved.

Candidates, by existing heading:

| `MEMORY.md` | Action |
|---|---|
| `### Local dev run` (controlplane master + worker) | move |
| `### Release` | move |
| `### Deployment & first-boot` | move the steps; keep the invariants (listener-before-preflight, health probe) in `MEMORY.md` |
| `### Deploying the standalone server` | move; it is largely a pointer to `docs/DEPLOY.md` already |
| `### Screenshotting the UI (headless Chrome recipes)` `:595` and `### Screenshotting the UI with headless Chrome` `:1016` | **merge into one entry**, then move |
| `### Image conversion on this machine` | move |
| `### Install channels — none rebuild from source` | move the per-channel commands; keep the asset-name-case gotcha |
| `### The install-id stitch` | move the walkthrough; keep the `VAR=x cmd \| sh` gotcha |
| `### Discord notifications` | move |
| `### WhatsApp skill` | pointer to `~/.claude/skills/whatsapp/` — move or drop |

Roughly 10 KB of the 188 KB, which is a real but not decisive cut. The point of phase 2 is not the byte count: it is that the file stops being the default home for prose that has an ordered path.

## 12. Deliberately not doing

- **Loading the body into the prompt.** The single decision this plan turns on (§4).
- **A heading index / table of contents in the prompt.** It is derived state that can disagree with the file, and it answers a question one `read` answers better (§4.1).
- **An existence check to switch wording.** `os.Stat` on a documentation file buys a slightly better sentence for an absent file and costs a filesystem read plus a second code path; the section says "read it when…" and a missing file is a self-explanatory `read` failure.
- **Walk-up discovery of ancestor `HOWTO.md` files.** With no loader there is nothing to find; the prompt names the root path and a nearer file is discoverable with the file tools.
- **Involving the memory scribe.** `buildTurnDigest` (`internal/agent/loop.go:2317`) has tool names and inputs but no results, so it cannot know whether a procedure worked. The doing agent writes.
- **Built-in skills as the container today.** Blocked by the `!intentOffered` gate at `loop.go:803` (§4.3).
- **A `<howto-md>` prompt tag.** Nothing is inlined, so there is nothing to wrap and nothing to forge.
- **A new tool, route, migration, or web surface.** The file is committed and the ordinary file tools reach it.
- **Auto-migrating `MEMORY.md` now.** Deferred until the pointer exists and has been exercised once.

## 13. Open questions

1. **Does the pointer need a per-project path in a monorepo?** The section names the root `HOWTO.md` only. A nearer file in a subdirectory is reachable with the file tools but not advertised. Revisit if monorepo sessions routinely miss their local procedures.
2. **Is ~1.2 KB of constant prompt (the writable branch) for a file that may not exist yet the right trade?** It costs the same on every turn of every project-scoped session, including projects that never write one. The alternative — the existence check — was rejected above for simplicity; the cost argument is worth watching once the section has a real user.
3. **Should a long procedure live in `docs/how-to/<slug>.md`?** The entry-plus-pointer form (§6) already allows it without a second discovery path. Revisit only if entries routinely exceed the 30-line guidance.
4. **When does the write gate become enforceable?** Today it is prose in the prompt. A `tool`-level check is impossible without a loader, so the only enforcement available is the prompt plus the agent's own read of the file before writing.

## Summary

`HOWTO.md` is a committed project file of verified procedures. The system prompt carries a fixed paragraph — what the file is, where it is, when to open it, and, for an agent that can write, what to record and in what shape — and nothing derived from the file itself, so the prompt's cost is constant instead of growing with the file and an edit to it never rewrites the cached prefix. The agent discovers it exactly as it discovers any other project file, and reads it before adding an entry, which is what stops a second copy of a procedure that already exists. `MEMORY.md` keeps what must be known before acting, and the procedures it has been carrying move out in a later phase.
