# Conflict-Free Mutations — Read Bookmarks and a Change Log

> **Scope**: stop one agent from silently clobbering work another agent already read. Today two `build` sessions (or two teammates) can edit the same file in any order; `pathlock` (`internal/tool/pathlock.go:26`) only serialises them, so the second write always wins and the loser never learns why. This plan makes a *stale* mutation refuse itself and hand back every change made since that agent last read the file — who made it, what they said they were doing, and the diff.

> **Core decision**: two kinds of state in a new `internal/filechange` package, keyed by the same `FileKey` that `pathlock` already computes, both mirrored in the project DB (migration 066). (1) A **read bookmark** per (session, file): the file revision that session last read — in memory, and in `file_bookmark`, written through once per observed revision and restored at startup. (2) A **change log** per file: a bounded ring in memory, plus **one permanent row per mutation in `file_change`** — because the log is there to be read later, for review and debugging, so a restart must not erase it. `write` and `edit` record into both and, before writing, refuse if the caller's bookmark is behind the file's revision — unless every change since then was the caller's own. Two new tables, no new service, no lock held across turns.

> **Decisions from the product owner** (asked and answered during design): on a stale mutation, **refuse and hand back the change log** — the edit does *not* land; "changed since you read" counts **`write` and `edit` only** — `bash` and MCP mutations are out of scope for v1; and **neither half may be in-memory only** — the change log is permanent and the read bookmarks are durable, so a restart loses neither the history nor the protection (§1 rows 8-10, §3.2, §3.3).

> **Status**: design, not implemented. No code written yet.

---

## 0. The mechanism in one page

```
read(file)                       → seen := rev(file), captured before reading
                                   bookmark[session, file] = seen, persisted to file_bookmark
                                    (also: file content in context)
write/edit(file, description)   → under the file lock:
                                     if rev(file) > bookmark[session,file]
                                        and some change since bookmark has another session:
                                            REFUSE. Return the changes since the bookmark.
                                            File is untouched. Bookmark unchanged.
                                     else:
                                            apply, rev(file)++, append a Change to log[file]
                                            persist that Change to file_change (project DB)
                                            bookmark[session,file] = rev(file), persisted too
startup                          → restore revs (MAX(rev) from file_change) and every bookmark
                                   from file_bookmark, so the gate resumes where the last process stopped
```

Four consequences worth stating plainly:

- **The loser is never left guessing.** The refusal names the other agent, quotes the description that agent gave for its edit, and shows the diff, so the model can re-read the file and redo its change against the new content instead of retrying blindly.
- **An agent is never blocked by itself.** Its own earlier mutations move the revision but are filtered out of the "someone else changed this" test, so a normal fix-mistake-retry loop keeps working.
- **Nothing is refused that would not have been refused before**, except in the one case this feature exists for. No bookmark (file created by the agent, read through `bash cat`, never read) means allow, exactly as today.
- **The history outlives the process.** Every change is one permanent row in the project DB (§3.2), so a clobber can still be reviewed after a restart or a debugging session next week. The ring only bounds what a *refusal* can show; it never bounds what the table keeps.
- **So does the protection.** Bookmarks are rows too (§3.3) and are restored at startup, so an agent that read a file before the process restarted is still refused afterwards, against the same catch-up list. Nothing about the gate is in-memory only.

---

## 1. Decisions, with the reasoning

| # | Question | Decision | Why |
|---|---|---|---|
| 1 | Stale mutation: refuse, warn, or merge? | **Refuse; hand back the change log.** | A warning is easy to ignore and the file is already lost. A merge is guesswork. Refusal + the log converts a silent clobber into a re-read. |
| 2 | What counts as a change? | **`write` and `edit`.** | They are the only tools whose mutations ogcode can see with certainty. `bash` (`sed -i`, `>`, `mv`) and `mcp_*` are accepted blind spots for v1 — §8. |
| 3 | Key on timestamp or revision? | **Revision counter.** Timestamps are *recorded and shown*, never compared. | Two mutations can land in the same millisecond; a wall clock can step backwards (NTP, sleep/wake). An integer that only ever increases cannot lie. The user's words were "at what exact timestamp" — the timestamp is still there, in the log and in the refusal message, but it is not the thing decisions are made on. |
| 4 | Bookmark from the *first* read or the *last*? | **The last read.** | The bookmark must describe what the agent's context currently holds. |
| 5 | Bookmark scoped to the range read, or the whole file? | **Whole file revision.** | A `read` with `ranges: [{start_line: 1, end_line: 40}]` (`internal/tool/read.go:51-72`) still goes stale when line 900 changes. Range-granular bookmarks would need per-range revs for no benefit. |
| 6 | What does the caller see on refusal? | A normal `Result` with a `nil` error. | Same shape as the existing claim refusal (`internal/tool/write.go:41-46`, `internal/tool/edit.go:124-133`): the model reads the reason as tool output and acts on it. A Go error would abort the batch and be reported as a fault. |
| 7 | Where does the per-call description live? | A required top-level `description` on `write` and `edit`. | Per call, not per hunk: the reason for a change is a property of the change, and `edit` already carries several hunks (`internal/tool/edit.go:20-53`). Adding it per hunk multiplies the tokens the model must emit for no extra signal. |
| 8 | Is the change log durable? | **Yes — a `file_change` row per mutation in the project DB, kept forever.** | The owner's requirement: the log is for later review and debugging, so a process restart must not erase it, and a teammate's edit should be inspectable after the session is gone. In memory remains the gate's working set; the DB holds the history (§3.2). |
| 9 | Are the read bookmarks durable too? | **Yes — one `file_bookmark` row per (session, file), restored at startup.** | The owner's requirement: the protection must not lapse when the process restarts. The "a write on every read" objection is answered rather than accepted: the write-through fires only when the observed revision differs from the one already stored, so an unchanged re-read writes nothing and a read after somebody's change writes one UPSERT (§3.3). |
| 10 | What happens to a bookmark whose session no longer exists? | **Swept at startup, unlike `file_change`.** | A change row is history and is never pruned; a bookmark for a deleted session can never protect anyone. The sweep keeps the table bounded by *live* sessions rather than every session the project ever had, with no per-delete seam in the server. |

---

## 2. Inventory: what can change a file

Audited against `codebase_map internal/tool` and `RegisterCoreTools` (`internal/tool/core.go:32-33`).

| Tool | Mutates files? | In scope |
|---|---|---|
| `write` | Yes — whole file (`internal/tool/write.go:108-110`) | **Instrumented** |
| `edit` | Yes — hunks (`internal/tool/edit.go:178-180`) | **Instrumented** |
| `bash` | Yes, invisibly — `sed -i`, `>`, `tee`, `mv`, `rm`, formatters, generators | Out of scope v1 (§8) |
| `latex_to_pdf` | Yes — writes a `.tex` and a `.pdf` beside the target | Out of scope v1 |
| `mcp_<server>/<name>` | Arbitrary, by the server's own definition | Out of scope v1 |
| `read`, `file_map`, `check_syntax`, `grep`, `glob`, `view_image`, `codebase_map`, `project_index`, `read_pdf_page`, `pdf_index`, `read_docx_page`, `docx_index` | No | Readers only (`read` is instrumented as a *reader*) |
| `update_intent`, `compact_context`, board/teammate/task/search tools | No file writes in the workspace | — |

There is no `multiedit`, `apply_patch` or `delete` tool. The canonical list of per-file tools and whether they mutate already exists as `fileTools` in `internal/agent/tool_order.go:14-20` — the new instrumentation should agree with it rather than invent a second list.

---

## 3. State — the gate in memory, both tables on disk

New package `internal/filechange`, holding `Change`, `Bookmark`, `Log` (the gate) and `Store` (both tables). It does not sit in `internal/tool` as first drafted: the store needs `*db.DB`, and `internal/tool` does not open sqlite. `internal/tool` imports `filechange` for the `Context` field; `filechange` imports `session` and `db`; no cycle. The split mirrors `internal/board` (types + store) against `internal/server` (wiring).

### 3.1 The gate: revisions and bookmarks in memory, mirrored on disk

```go
type Change struct {
    Rev         int64
    Path        string    // FileKey; what the gate and the table are keyed on
    Session     session.SessionID
    Agent       string    // tool.Context.Agent ("build", "task", …)
    Tool        string    // "write" | "edit"
    Note        string    // the caller's `description`
    At          time.Time // recorded and shown, never compared
    Diff        string
    DiffOmitted bool
}

type Bookmark struct {
    Path    string    // FileKey
    Session session.SessionID
    Rev     int64     // the revision that session last read
    At      time.Time
}

type Log struct {
    mu    sync.Mutex
    files map[string]*fileEntry                         // key: FileKey(dir, path)
    saved map[string]map[session.SessionID]int64        // what the store holds, per (file, session)
    Name  func(session.SessionID) string                // optional; renders a board handle
    Sink  func(ctx context.Context, ch Change) error    // optional; the durable history
    Persist func(ctx context.Context, b Bookmark) error // optional; the durable bookmark
}

func (l *Log) Restore(revs map[string]int64, bookmarks map[string]map[session.SessionID]int64)
func (l *Log) Read(ctx context.Context, key string, s session.SessionID, rev int64) // bookmark + Persist when it changed
func (l *Log) Seen(key string, s session.SessionID) int64            // bookmark, 0 when none
func (l *Log) Rev(key string) int64                                  // current revision
func (l *Log) Stale(key string, s session.SessionID, seen int64) []Change
func (l *Log) Record(ctx context.Context, key string, ch Change) int64 // ring + Sink, returns new rev
```

- **Key** is `FileKey(dir, path)` (`internal/tool/pathlock.go:41-46`) — the same normalisation (abs, clean, `resolveWriteTarget`, symlinks on the parent, lowercase on macOS/Windows) that already makes two spellings of one path collide. Reuse it; do not add a second path normaliser. The tools compute it; `filechange` treats it as an opaque string.
- **Revision** lives on `fileEntry`, starts at 0, and is bumped only inside `Record`, which is only called while the file lock is held. Reads therefore never observe a torn `(content, rev)` pair from a mutator.
- **Bookmark** is a per-file `map[session.SessionID]int64`, filled from `file_bookmark` at startup (§3.3). One entry per session that has read the file — bounded by the number of sessions that have worked in the project, not by how often they read.
- **`saved` is the durable shadow** of that map: the revision the store is known to hold for each (file, session). `Read` always updates memory, and calls `Persist` only when the incoming revision differs from `saved`; a successful write updates `saved`, a failed one leaves it alone so the next read retries. An unchanged re-read is therefore no write at all.
- **`Record` bumps the author's bookmark by calling `Read`** with the new revision, so an agent's own catch-up is one code path for memory and for disk.
- **Ring bound**: keep the last 50 changes per file (a `[]Change` truncated from the head). A refusal that spans more than 50 changes says so ("…and 12 earlier changes no longer listed; re-read the file") rather than pretending the list is complete. The ring bounds what a *refusal* shows, not what the table keeps.
- **`Name`** is how a refusal says "designer" instead of a raw session id. The server can supply one backed by the board roster (`board.BySession`); when nil, fall back to the session id, then to the agent name.
- **Process-wide is sound**: ogcode holds an advisory lock on `<dir>/.ogcode/server.lock` so there is exactly one server per project directory (`internal/server/projectlock.go`), and `pathlock` already assumes process scope.

### 3.2 The history: one row per mutation, kept forever

```sql
CREATE TABLE file_change (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  directory    TEXT NOT NULL,
  path         TEXT NOT NULL,             -- FileKey: absolute, cleaned, symlink-resolved
  rev          INTEGER NOT NULL,
  session_id   TEXT NOT NULL DEFAULT '',  -- no FK: the row outlives the session
  agent        TEXT NOT NULL DEFAULT '',
  tool         TEXT NOT NULL,
  note         TEXT NOT NULL DEFAULT '',  -- the caller's description, verbatim
  diff         TEXT NOT NULL DEFAULT '',
  diff_omitted INTEGER NOT NULL DEFAULT 0,
  created_at   INTEGER NOT NULL           -- unix seconds, like board_post
);
CREATE INDEX file_change_path ON file_change(directory, path, rev);
CREATE INDEX file_change_time ON file_change(directory, created_at);
```

Migration **066** — 065 is the board migration and is still uncommitted; `git log --all -- internal/db/066*` is empty, so 066 is free. It carries both tables in this plan: `file_change` here and `file_bookmark` in §3.3. Column spelling and the `directory` scoping copy `board_post` (`internal/db/065_agent_board.sql:28-43`).

- **No foreign key on `session_id`**, on purpose: `usage_event` records spend for sessions that no longer exist, and a change outlives its author the same way. A FK would turn "delete this session" into a mid-turn insert failure (the 787 class in `MEMORY.md`) for a row that should have survived regardless.
- **Scoped by directory**, like the board: `filechange.Open(database, dir)`, every query carrying `directory`. Every DB runs every migration, so the table also exists in the global config DB; only the project DB is written.
- **Permanent and unpruned** — that is what "for later review" means. A row is bounded (`note` ≤ 2 KB; `diff` ≤ 8 KB with `diff_omitted=1`), so growth is bounded per mutation, not in total. Trimming is a documented `DELETE` in `HOWTO.md`; no code ever prunes.
- **Revisions are re-seeded at startup**: one `SELECT path, MAX(rev) … GROUP BY path` on construction, written into the `Log`'s per-file entries, so `rev` keeps counting up across restarts instead of repeating numbers in a table meant to be read later. Paired with the restored bookmarks (§3.3) this is what makes the protection survive a restart: a restored bookmark of 7 against a restored revision of 9 is still stale. One query, no writes.
- **The sink is best-effort**: `Record` appends to the ring, then calls `Sink` (when set) with the caller's context under `context.WithoutCancel` and a short deadline, so a cancelled turn still writes the row for a file it already changed. A sink error is logged at Warn with path and rev and does **not** fail the tool call — the bytes are already on disk and the gate already knows the change. This is a review trail, not a write-ahead journal: the file on disk stays the truth.

### 3.3 The bookmarks: one row per (session, file), restored at startup

```sql
CREATE TABLE file_bookmark (
  directory  TEXT NOT NULL,
  path       TEXT NOT NULL,             -- FileKey, the spelling file_change.path uses
  session_id TEXT NOT NULL,
  rev        INTEGER NOT NULL,          -- the revision that session last read
  updated_at INTEGER NOT NULL,          -- unix seconds
  PRIMARY KEY (directory, path, session_id)
);
CREATE INDEX file_bookmark_session ON file_bookmark(directory, session_id);
```

- **Write-through, once per observed revision.** `Log.Read` compares the revision it was handed with `saved` (§3.1) and calls `Persist` only when they differ. The statement is an idempotent UPSERT, so a duplicate costs nothing even if it slips through:

  ```sql
  INSERT INTO file_bookmark (directory, path, session_id, rev, updated_at)
  VALUES (?, ?, ?, ?, ?)
  ON CONFLICT(directory, path, session_id)
  DO UPDATE SET rev = excluded.rev, updated_at = excluded.updated_at
  WHERE file_bookmark.rev <> excluded.rev;
  ```

  The hot path is therefore not "a write per read": it is a write per *revision the session observes* — one after somebody else's change, one for the author's own mutation — and nothing at all for a repeat read of an unchanged file.
- **Restored in one query at startup**: `SELECT path, session_id, rev FROM file_bookmark WHERE directory = ?` fills the per-file maps; `Restore` unions those with the revision re-seed so a path present in only one of the two tables still gets an entry. Cost is the row count, bounded by live sessions × files read — hundreds of KB in a heavily used project, once per process.
- **No foreign key on `session_id`**, same reason as `file_change`: a read that races a session delete must not fail an insert with a 787 mid-turn. Bookmarks have no history value, so the sweep below is what removes them.
- **Best-effort, never a failed read.** `Persist` runs under `context.WithoutCancel` with a short deadline; on error it logs at Warn with path, session and rev, keeps memory authoritative and leaves `saved` untouched so the next read retries. A bookmark that fails to persist costs protection after a restart, never the read itself.
- **Dead bookmarks are swept at startup**: `DELETE FROM file_bookmark WHERE directory = ? AND session_id NOT IN (SELECT id FROM session)`. This is the counterpart to §1 row 10 — unlike the history, a bookmark with no session is garbage, and sweeping keeps the table proportional to live work rather than to every session ever created.
- **A bookmark ahead of the log is fail-open.** If a change row was lost (§3.2) but its author's bookmark persisted, the gate sees no gap and allows the mutation. Consistent with the lost row: no history, no refusal.
- **This closes the restart hole, not the second-process hole.** A running process still holds its revisions and ring in memory, so a change another process makes *while it runs* stays invisible until the next start (§8). Making the gate read `file_change` per mutation is the upgrade, listed in §11.

### 3.4 Reading the history

- **Route**: `GET /api/file-changes?path=&before=&limit=`, registered beside `r.Get("/board", …)` in `internal/server/routes.go`, newest first, `path` reported project-relative, `limit` capped the way `handleBoardRead` caps it (`internal/server/board_routes.go:19-45`), 503 before the store exists — the board's own contract.
- **Without the route**, the DB is the review surface: `sqlite3 .ogcode/ogcode.db "select id, rev, agent, tool, note, created_at from file_change where path like '%board.go' order by id desc limit 20"`. That is the whole debugging story for v1; the route exists so a UI panel later reads it instead of shelling out.

---

## 4. Flow, with insertion points

### 4.1 `read` records a bookmark

`internal/tool/read.go` — after the file exists and before the content is rendered:

1. Capture `seen := fileLog.Rev(key)` **before** `os.ReadFile` at `read.go:170`.
2. On success, and **only for a regular file** (skip the directory-listing early return at `read.go:151-168`, and skip a binary file detected at `read.go:180`), call `fileLog.Read(tctx.Ctx, key, tctx.SessionID, seen)`.
3. `Read` write-throughs that bookmark to `file_bookmark` when the revision is not the one already stored (§3.3), so the protection survives a restart and a session resumed in `serve` keeps what it learned in `run`.

Capturing the revision *before* the read is deliberate and conservative: if a mutation slips in between, the bookmark is older than the content, so the next edit may refuse and show a change the reader already has. The opposite order could certify a bookmark newer than the bytes actually read — a missed conflict, which is the one failure this feature must not have.

`file_map` is *not* instrumented: it returns signatures, not content, and an agent that has only seen a map has not seen the lines it is about to rewrite. Its output can still be read as a hint to call `read` first.

### 4.2 `write` and `edit` check, then record

Both tools already have the exact seam: after `lockPath` (`write.go:56-57`, `edit.go:143-144`) and before `writeFileAtomic` (`write.go:108`, `edit.go:178`).

```
key := FileKey(workDir, path)              // computed once; reuse for lock + log
unlock := lockPath(path); defer unlock()

// edit only: the content we are about to change is already in hand (edit.go:146-149)
if changes := fileLog.Stale(key, sessionID, fileLog.Seen(key, sessionID)); len(changes) > 0 {
    return staleRefusal(path, changes), nil   // file untouched
}

... existing work ...
if err := writeFileAtomic(path, data); err != nil { ... }

fileLog.Record(tctx.Ctx, key, Change{ Path: key, Session: sessionID, Agent: tctx.Agent,
                            Tool: "write"|"edit", Note: input.Description, At: time.Now(),
                            Diff: diffLines(oldContent, newContent) })
// ring + one INSERT into file_change; a failed INSERT warns and does not fail the call (§3.2)
fileLog.Read(key, sessionID)                  // the author's own bookmark catches up
```

`Stale` returns non-empty only when the file revision is ahead of `seen` **and** at least one change in that window has a different `Session`. `seen == 0` (no bookmark) means allow — and still record, so other agents' bookmarks are invalidated by a file this agent created.

The diff comes from a new small line-based helper (`diffLines`, capped: stop after ~64 changed lines or ~8 KB, set `DiffOmitted`). One cap, shared: the refusal, the ring and the stored row all show the same truncated diff, so the history reproduces exactly what the refused agent was told. The repo has no reusable diff: `internal/server/git_routes.go:43` shells out to git for the UI, and `internal/tool/edit.go:476` is a whitespace-mismatch explainer, not a diff.

---

## 5. What the refused agent sees

`Result{Title: ..., Output: ...}`, `nil` error, nothing written — modelled on the claim refusal at `internal/tool/write.go:41-46`:

```
File changed by another agent since you read it: internal/server/board.go

Nothing was written. Your edits were NOT applied.

Your last read was revision 7. The file is now at revision 9.

[1] rev 8, 14:22:31, designer (edit)
    note: split boardDelivery into two helpers and cap the byte budget
    @@ -118,6 +118,9 @@
    ...

[2] rev 9, 14:23:02, designer (edit)
    note: rename fanOutBoardPost to publishBoardPost, callers updated
    ...

Re-read the file (read with ranges) and re-apply your change against the current content.
```

Rules for this text: name the file, say explicitly that nothing was written, list every change in revision order with handle → agent → tool → note → diff, and end with the next action. If more than 50 changes have accumulated, say the list is truncated. The note is quoted, never interpreted.

---

## 6. The required `description` parameter

Add to both schemas, next to `path`:

```
"description": "A few lines: what this change does and why. Another agent working in
                this project will see this text if it has to re-read the file after your edit."
```

Required in the JSON schema **and enforced in Go**, with a message that says what to send:

- `write.go:17-26` — `required: ["path", "content", "description"]`.
- `edit.go:20-53` — `required: ["path", "edits", "description"]`.
- Decoding is a plain unmarshal (`write.go:29-35`, `edit.go:80-115`), and the code already notes for `edits` that the schema's `required` is advice rather than enforcement (`internal/tool/edit.go:62`). So: after decoding, if `strings.TrimSpace(input.Description) == ""`, return a `Result` titled `Description required`, output explaining the one-sentence format, `nil` error. Empty means not supplied; no silent default.
- The description is **not** parsed, classified or length-checked beyond a generous cap (~1 KB) — it is written into the log verbatim and rendered to whichever agent is refused.

Test churn: 11 call sites construct `WriteTool{}`/`EditTool{}` in `internal/tool/*_test.go` (`write_test.go`, `edit_test.go`, `mutation_syntax_test.go`, `check_syntax_test.go`, …), plus agent-level tests whose fake model emits `write`/`edit` calls (`internal/agent/{tool_order,subagent,descent_gate,intent_gate,read_pressure,prompt_builder,loop_guidance_cancel,tool_reachability,prompt_workflow,retry_classify}_test.go`, `internal/tool/read_test.go`, `internal/tool/update_intent_test.go`, `internal/mcp/tokenstore_test.go`, `internal/permission/permission_test.go`, `internal/provider/stream_client_test.go`). A shared test helper that builds the args map with a description will keep that to one line per site.

Prompt text naming these tools must be checked for a now-incomplete argument list — grep the prompt builders before shipping.

---

## 7. Wiring

One shared `*filechange.Log` per server process, passed down the same path `HeldBy` already travels, with its `Sink` pointing at the project's `filechange.Store` for the history and its `Persist` at the same store for the bookmarks.

| Layer | Change |
|---|---|
| `internal/filechange` | New package: `Change`, `Bookmark`, `Log` (§3.1), `Store` with `Append`/`List`/`SaveBookmark`/`Bookmarks`/`PruneBookmarks` and the revision re-seed (§3.2, §3.3), migration 066. Imports `session` and `db`; knows nothing about `internal/tool`. |
| `internal/tool/tool.go` | New `Context.FileLog *filechange.Log` field beside `HeldBy` (`tool.go:59-64`), plus a `fileLog()` helper that returns a no-op when nil so both tools read cleanly. |
| `internal/agent/loop.go` | New `LoopRunner` field next to `ClaimHolder` (`loop.go:167`), copied into `tctx` where the dispatch builds it (`loop.go:2772-2791`). |
| `internal/server` | Build the store with `filechange.Open(database, s.dir)` beside `s.board` (`server.go:345`), then the gate with `filechange.NewLog(ctx, store)` — restore (§3.3), `Sink` = `store.Append`, `Persist` = `store.SaveBookmark`, plus a `Name` that resolves a session to its board handle — and keep the store for the route. Nil in tests that do not care. |
| `internal/server/routes.go` + `file_change_routes.go` | `r.Get("/file-changes", s.handleFileChanges)` (§3.4). |
| `internal/cli/run.go` | Wire the same store and `filechange.NewLog` at `run.go:230`, where the project DB is already open. Every `run` creates a fresh session (`run.go:238`), which has no bookmark, so it never refuses — but its mutations land in the same history and its reads persist bookmarks that a later `serve` or a resumed session inherits. No `Name`. |
| `internal/tool/core.go` | No change — the tools are constructed by `RegisterCoreTools` and read the log from the call context. |

Nil `FileLog` means no bookmarks, no refusals and no rows: every mutation proceeds exactly as today. That keeps the worktree tests and any embedding free of behaviour change. A bare CLI `run` gets the store and a `Log`, but it starts on a new session id, so it has no bookmark to be stale against and never refuses; what it does do is invalidate the bookmarks of every session that read the file.

---

## 8. Known holes

- **`bash` is a blind spot** — accepted, per the decision in §1. `sed -i`, `>` and `mv` change files without touching the log, so an agent can be clobbered by a shell command and never be told. The lock does not cover it either (`pathlock.go:26` comment says as much). Detecting it would need filesystem watching or a pre/post stat fingerprint on every `bash` call; both are a separate piece of work.
- **MCP tools are a blind spot** — same reason. An MCP filesystem server can write anything.
- **`latex_to_pdf` writes files** without going through `write`; a `.tex` it rewrites will be invisible to the log.
- **A restart keeps the history and the protection.** Every change survives as a row, revisions are re-seeded from `file_change`, and bookmarks are restored from `file_bookmark` (§3.3) — so an agent that read a file before a restart is refused after it, with the same catch-up list. What no restart can recover is a change made while the process was down by something outside the log at all (the `bash`, MCP and `latex_to_pdf` blind spots above), because nothing recorded a revision for it.
- **A bookmark that fails to persist fails open.** Warned and retried on the next read; the in-memory gate stays correct throughout, so only a restart loses the protection for that one file (§3.3).
- **A failed DB write leaves a gap in the history.** The mutation lands, the gate knows about it, and only the row is missing — warned at Warn with path and rev (§3.2). Nothing in the file path depends on the database.
- **The history grows without bound.** Bounded per row (8 KB diff, 2 KB note), unbounded in rows, by design. Trimming is a documented `DELETE`, never code.
- **A second ogcode process in the same directory** (a separate `ogcode run`) is only partly visible. Both processes write the same history and the same bookmarks, and a fresh process restores revisions, so it sees everything that landed *before it started*. But a running process keeps its revisions and ring in memory, so a change another process makes *while it runs* is invisible until a restart. Reading `file_change` per mutation would close that and is listed in §11. `serve` is single-instance per directory; `run` is not.
- **The bookmark is per session, not per turn.** An agent that read a file 200 turns ago and never re-read it holds a stale bookmark; on the plus side, that is exactly the case where a catch-up is genuinely useful.
- **A refusal the model ignores.** It can re-issue the identical edit, collect the same list, and loop. The existing claim refusal has the same exposure; both depend on the prompt telling the model that a refusal means "look again", and the output says so in its last line.

**Deliberately not in v1**: a UI panel over `/api/file-changes` — the route lands in v1 precisely so the panel is a front-end change only — and reading the history inside the gate, which is what a live second process needs (§11). Both tables and the restore are in v1; nothing about durability is deferred.

---

## 9. Test plan

Unit, `internal/filechange/log_test.go` (new):
- rev is monotonic; `Read` stores the revision it was given; `Stale` is empty for a fresh bookmark.
- `Stale` ignores changes from the reading session and reports those from another.
- `Stale` reports nothing for a session that never read the file.
- the ring keeps 50 and reports truncation beyond it.
- `Sink` receives the same `Change` the ring holds, once per `Record`; a `Sink` error is returned as a warning-level log and the ring still lists the change.
- `Persist` is called once per new revision and not at all when the revision repeats; a `Persist` error leaves `saved` un-updated, so the next `Read` at the same revision tries again.

Unit, `internal/filechange/store_test.go` (new, temp DB via `db.Open`):
- `Append` then `List` round-trips every column, including a note with newlines and a diff.
- `List` is newest-first, honours `limit` and `before`, and returns nothing for another `directory`.
- a long diff is truncated to 8 KB with `diff_omitted = 1`.
- revision re-seed: reopening the store over an existing table makes the next `Rev` continue from `MAX(rev)`, not from 1.
- `SaveBookmark` is an UPSERT: saving twice for one (path, session) leaves one row carrying the later rev.
- `Bookmarks` round-trips a map; another `directory` returns nothing; `PruneBookmarks` drops a row whose session no longer exists and keeps a live one.
- **Restart, end to end**: build a `Log` over the store, read a file at rev 3, record another session's change at rev 4, throw the `Log` away, build a second one over the same store → `Seen` answers 3 and `Stale` still refuses. This is the test for the requirement, and it is the one that fails if `Persist` or the restore regresses.
- a second `Read` at the same revision calls `Persist` zero more times.

Tool-level, `internal/tool/{write,edit}_test.go`:
- a stale `write`/`edit` from a second session returns the refusal, and the file on disk is byte-identical afterwards.
- the refusal names the other agent, contains its `description`, and contains a diff hunk.
- same-session retry after a failed edit still succeeds (no self-deadlock).
- a mutation with no prior read proceeds (guard against over-refusal).
- `description` missing → `Description required`, nothing written; present → recorded verbatim.

Integration, `internal/agent`:
- `tool.Context.FileLog` is non-nil when the runner has one and nil otherwise; a batch of two `write`s on one file from one session does not refuse itself.
- `internal/server`-level: two sessions on one dir, session A reads then session B edits then A edits → A's second edit refuses. Two servers in tests need two dirs (`projectlock`) — this test belongs where a single server hosts two sessions.

Manual, end to end: `make dev`, two browser tabs, both prompt "read internal/server/board.go, then edit the first comment line"; the second edit must refuse with the first agent's note and diff; re-read and retry succeeds. Then restart the server and make a fresh edit from the session that did *not* re-read — it must still refuse, which is the durable bookmarks working (before this change it would have written the clobber silently).

---

## 10. Order of work

1. Migration 066 + `internal/filechange`: `Change`, `Bookmark`, `Store` (`Append`, `List`, `SaveBookmark`, `Bookmarks`, `PruneBookmarks`, revision re-seed) and `store_test.go` including the restart round-trip. No tool touched; nothing behaves differently.
2. `Log` — bookmarks, revisions, ring, `Stale`, `Restore`, the `Sink`/`Persist` hooks and `log_test.go`. Still nothing behaves differently.
3. Hook `read` to record a bookmark, write-through included (§4.1) — still no refusal anywhere.
4. Hook `write`/`edit`: check-before-write, record-after-write, refusal renderer, `diffLines`. Add the "stale refusal leaves the file untouched" tests.
5. Add `description` to both schemas, enforce in Go, fix the test call sites, reword tool descriptions and any prompt text.
6. Wire `Context.FileLog`, `LoopRunner`, the server and CLI stores, and the route; add the two-session integration test, the restart test and the route test.
7. `gofmt`, `CGO_ENABLED=1 go build ./internal/... .`, `make test` (test needs no cgo but build does), then the manual two-tab check.

Steps 1-4 are independently shippable and useful; 5 is the visible contract change; 6 is what makes it do anything across agents. The tables only begin to fill at step 3 (bookmarks) and step 4 (history), which is also when the restart test can first be written end to end.

---

## 11. Questions still open

- **Should the gate read the history on every mutation?** That is what a *live* second process (`ogcode run` alongside `serve`) needs: it would turn `Rev` and `Stale` into a query, adding a read of `file_change` to every `write`/`edit`. Today a process sees only what landed before it started (§8). Revisit if running both on one directory becomes normal.
- **Do we also instrument `file_map` as a read?** Argument for: an agent that mapped the file and then edits it is in the same clobbering position. Argument against: a map is not content, and treating it as a read would suppress the catch-up exactly when the agent most needs the diff. Current design: no.
- **Should a refusal be visible to the *other* agent?** A refused edit is information ("someone is working here"). v1 does not tell them; it would be a board post, which is the other feature's territory.
- **Should `bash` be fingerprinted?** Out of scope, but the cheapest follow-on: stat mtime+size before and after each `bash` call, and if either moved on a tracked file, record a `bash` change with an empty note. Worth a separate decision.
