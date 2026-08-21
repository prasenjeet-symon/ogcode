# Agent Behavioral Instructions

Rules specific to working in this repository. The general exploration workflow —
`codebase_map`, `file_map`, ranged reads — is built into the system prompt every
agent already receives, so it is deliberately not repeated here.

## Building and testing

- **`CGO_ENABLED=1` is required.** The Swift tree-sitter binding in
  `internal/codemap/grammars/swift` is cgo. Without it the build fails with
  `build constraints exclude all Go files in .../grammars/swift`, which says
  nothing about cgo and reads like a missing package.
- Verify with `CGO_ENABLED=1 go build ./...` and `CGO_ENABLED=1 go test ./...`.
  That is what CI runs.
- **No linter is configured**, so formatting is on you: run `gofmt -w` on the
  files you touched and nothing else. Several files in the tree are already
  drifted, and a blanket `gofmt ./...` would bury your change in unrelated
  reformatting.
- Web UI: `cd web && npm install --legacy-peer-deps && npm run build`. The flag
  is required, not a convenience.

## Changing the system prompt

Agent prompts live in `internal/agent/agent.go` (one per agent) and
`internal/agent/prompt_builder.go` (shared sections). They are assembled by
`staticSystemPrompt` and `buildSystemPromptEntries` in `internal/agent/loop.go`.

- **Entry [0] must stay byte-identical for the whole session.** Providers attach
  the prompt-cache breakpoint to the first system block. Anything that varies
  per turn — the date, the viewport — belongs in a later entry, never in the
  static block.
- **A section that names a tool by id must only reach agents that hold it.**
  `Registry.ForAgent` (`internal/tool/tool.go`) offers the model only the tools
  in the agent's `Tools` list, so naming one it lacks is an instruction it can
  never follow — and nothing fails loudly. Add the tool id to
  `mandatoryPromptTools` in `internal/agent/tool_reachability_test.go` so the
  invariant is pinned.
- **Describe a tool the way it actually behaves.** Read its `Description()` and
  its `Execute` before writing prose about it. The two drifting apart is how an
  agent ends up calling something with arguments it had no way to derive.
- **Prompt text is not free.** It is re-sent on every step of every turn. State
  the rule and the mechanics the agent cannot infer; cut the paragraph arguing
  for the rule.

## Scope

- `.ogcode/` is runtime state, not source — do not edit it. Notes under
  `.ogcode/notes/` are written by the NoteAgent; read them, never modify them.
- Prefer a pinned invariant over a comment. When you fix a class of bug, add the
  test that makes it unrepresentable — that is the convention the agent and tool
  packages already follow.
