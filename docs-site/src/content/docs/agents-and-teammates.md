---
title: Agents & teammates
description: "The agent board: teammates the agent spawns, read-only sub-agents, and the rules that keep parallel work safe."
---

Ogcode's build agent usually works alone, but it does not have to. For a change that splits into independent streams (one package, plus its tests, plus a docs pass), the agent can spawn **teammates**: other build agents in the same project directory, working at the same time. You watch all of them on the **Agents page**, and the file change gate keeps their edits from colliding.

## Teammates

A teammate is a fully-fledged build agent, not a copy of a prompt. It has its own session, its own conversation context and its own turn loop, and it works in the same project directory the parent works in. The agent hands it a slug name (a short handle such as `auth-worker`) and a complete brief: the files to touch, the goal, the pattern to follow and what to report back. A teammate never sees the conversation that spawned it, so the brief has to stand on its own.

Several teammates can run at once, and there is no limit on how many. A teammate may spawn teammates of its own, so a large piece of work can fan out into a tree of agents. The agent spawns teammates only in the server environment (where the board exists); from a plain terminal session without one it is told teammates are not available.

The parent steers each teammate through four operations:

| Operation | What it does |
| --- | --- |
| **spawn** | Start a named teammate with a complete, standalone prompt. Spawning asks for your permission, like any consequential action. It returns a handle, the teammate's session URL and the live count of agents on the board. |
| **wait** | Block until a teammate finishes (or until the timeout, a few seconds to two minutes); returns each finished teammate's last message. |
| **stop** | Cancel a teammate; the cancellation cascades to anything it spawned. |
| **status** | Peek at one teammate: its row on the board and its last message. |

The teammate inherits the parent's model, provider, effort setting and permission rules; nothing is reset or relaxed for the child. Its requests for your input mirror into your composer, tagged with its handle, so a question a teammate asks appears on your screen as if the parent had asked it, answered once, and the teammate's turn resumes.

## How a teammate reports

There is no message channel between agents: **a teammate reports by finishing**, and its last message is the result. When the parent is running a turn it receives the report the moment it lands; when the parent is idle, the report is queued and handed over at the start of the parent's next turn. A finished teammate's report is never lost, whether or not the parent was waiting for it.

The agent is expected to keep working while its teammates work: taking the next decision, touching the shared files it kept out of every brief, or waiting only when nothing of its own is left.

## Read-only sub-agents

For investigation rather than building, the agent has a separate **task** tool: a sub-agent that explores the codebase (reading, mapping, searching, plus web research when it is configured) and returns a written answer. It cannot edit files or run shell commands. By default the call is synchronous: the agent's turn blocks until the answer comes back.

With `"background": true` the task instead starts as a read-only child on the board, and the call returns at once. The answer arrives later as a completion report, and the agent can collect it early with a `wait`. A background task cannot spawn further sub-agents; it is a leaf.

## The Agents page

The **agent board** is the tree of every agent in the project directory: each build session and each teammate it spawned, nested under their spawner, running and finished agents both shown as long as they are on the board. You can see it in two places:

- The **Agents page** in the web UI: a full-screen tree, one row per agent, a row opening that agent's session, and a running teammate stoppable behind a confirmation. The sidebar nests teammates below their parent on their own rail as well.
- The roster endpoint, `GET /api/board/roster`, if you want the same tree programmatically: one row per handle, with the session's title and model.

Each agent's spend is visible too. A teammate's token usage is counted on the teammate's own session, and the parent's **token pill** folds its spawned teammates in so you see the whole turn's cost in one figure, with the teammates' share broken out.

## Rules that keep parallel work safe

Parallel agents in one directory could trample each other, so the board leans on the file change gate described below:

- **One file, one owner.** Two agents editing the same file collide: the second agent's write or edit is refused and shows the diff it has not seen, and it must re-read the file and try again. Nobody writes blindly over anybody.
- **Handles are unique.** A handle already taken is refused, never silently renamed, and a small set (such as `user`, `system` and `board`) is reserved. Names stay stable once claimed.
- **Inheritance, not escalation.** A teammate gets exactly the parent's model and permission settings. Spawning a child never widens what can run.

## The file change gate

The same machinery the board uses to detect collisions also protects you from changes nobody told the agent about. Every change to a file the project tracks (made with a write, an edit, or a shell command) is recorded: the path, the revision, which session and agent made it, the tool, the agent's one-line description of the change, and a diff, capped at 8 KiB per change. A session also keeps a bookmark of the revision it last read of each file, and the bookmarks survive a restart.

Before the agent applies a write or an edit, it asks one question: has anyone else changed this file since I last read it? The gate counts changes by *other* sessions (your own edits after your own read are fine). If the answer is yes, the tool **refuses** the change and shows the agent the diff it has not seen, so it re-reads the file and adapts, rather than overwriting an edit made under it.

Shell commands cannot be vetoed mid-flight, so the gate works differently there: after a command runs, any tracked file it changed is recorded against it, marked as an unattributed change with the command itself as the note. If a shell command steps on a file a session has read, that session is refused on the file too until it re-reads. A formatting run can, in other words, invalidate the agent's edit anchors just as visibly as another agent can.

The whole history is available for review at `GET /api/file-changes`, newest first, with pagination and an optional path filter, one row per change with its diff:

```json
{
  "changes": [
    {
      "id": 15,
      "path": "internal/server/server.go",
      "rev": 12,
      "session": "abc123",
      "agent": "build",
      "tool": "edit",
      "note": "tighten the readiness probe",
      "at": "2026-10-26T09:14:07Z",
      "diff": "..."
    }
  ],
  "cursor": 8
}
```

## Custom OpenAI-compatible providers

Any provider that speaks the OpenAI chat-completions API can be added without code, through a `providers` block in `ogcode.json` (found at the project root, or an ancestor of it; the global equivalents live in `~/.config/ogcode/config.json`). Each entry's key is the provider id you will see in the model picker, and the fields are:

| Field | Meaning |
| --- | --- |
| `baseUrl` | Required. The endpoint the provider talks to. On a built-in slot (such as `openai`) it overrides the default endpoint instead. |
| `apiKey` | Sent as a bearer token (custom providers); on a built-in slot it is exported as that provider's environment variable instead. |
| `type` | Optional. Only `openai` (or empty, which means the same) is supported today; any other value is skipped. |
| `model` | Optional default model, used when you have not picked one. |
| `label` | Optional human-readable label, shown as the provider's grouping in the model picker. |
| `disabled` | Set to `true` to switch the provider off without deleting it. Providers are enabled by default. |

```json
{
  "providers": {
    "my-deepseek": {
      "type": "openai",
      "baseUrl": "https://api.deepseek.com/v1",
      "apiKey": "sk-...",
      "model": "deepseek-chat",
      "label": "DeepSeek"
    }
  }
}
```

The built-in ids (`anthropic`, `openai`, `openrouter`, `ollama` and `ogx`) are never shadowed: a custom entry with a built-in id adjusts that slot rather than replacing it, and other custom ids are added alongside.

## Two smaller additions

- **`schedule`**: one-shot reminders the agent sets for itself. `create` needs a `note` and exactly one of `delaySeconds` (up to about a year) or `fireAt` (an absolute RFC3339 timestamp); `list` shows what is pending and `cancel` removes one by id. A reminder is delivered into the session as a notice, and it never starts a turn: if the session is idle, the note is waiting at the start of the next one.
- **`update_intent`**: the plan pill you may see at the top of a session. The agent states what it intends to do as a short list of steps (at most seven), each marked pending, active, done or dropped, and updates the statuses as it works, replacing the whole plan on each call. It is a window on the turn, not a script: the plan is cleared when the next turn starts.

## Next

- [Permissions](/docs/permissions/): the approval rules a teammate inherits.
- [Rich results & preview](/docs/rich-results/): what one agent's session can show on its own.