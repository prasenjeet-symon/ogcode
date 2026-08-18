# Agent Behavioral Instructions

## Mandatory: Use Project Index Before Exploration

**Rule:** Before exploring any file, folder, or project structure, you **MUST** use the `codebase_map` tool first.

This applies to all of the following scenarios:

- **Starting a new task** — Call `codebase_map` (optionally scoped with `subdir`) before reading any source files.
- **Looking for a file** — Use `codebase_map` with an appropriate `subdir` to locate files, instead of guessing paths with `glob` or `grep`.
- **Understanding project structure** — Use `codebase_map` to get the labeled tree of topics and files before diving into code.
- **Exploring a new package/directory** — Call `codebase_map` with `subdir` set to that package/directory.

### Why?

The project index provides topic labels and a structured overview of every indexed file. Using it first ensures:

1. **Faster navigation** — You immediately know which files are relevant without blind `glob`/`grep` searches.
2. **Better context** — Topic labels help you understand what each file contains before reading it.
3. **Fewer mistakes** — You won't miss important files or read irrelevant ones.

### Workflow

```
Task received
  → codebase_map(subdir=...)          ← which files matter
  → file_map(path)                    ← where things are inside one file
  → read(path, start_line, end_line)  ← only the region you need
  → Then make changes
```

### When `codebase_map` is not enough

If `codebase_map` doesn't cover what you need (e.g., unindexed files, binary patterns), you may fall back to `glob` and `grep`. But `codebase_map` must always be the **first** exploration step.

### Scoping tip

For large projects, always use the `subdir` parameter to scope `codebase_map` to the relevant directory (e.g., `"internal/tool"`, `"web/src"`). This keeps the response focused and fast.

## Mandatory: Map a File Before Reading It

**Rule:** Before reading a source file you do not already know, call `file_map`
on it. Read the whole file only when the map shows you genuinely need all of it.

`file_map` parses the file and returns every top-level declaration with its
1-based line range and doc comment. Those ranges go straight into `read`:

```
file_map("internal/tool/read.go")
  → 35-142  func (ReadTool) Execute(ctx context.Context, ...) (Result, error)

read("internal/tool/read.go", start_line=35, end_line=142)
```

`start_line` and `end_line` are inclusive and use the same numbering `file_map`
prints, so a range is copied across as-is — never convert it to `offset`.

### Why?

Reading a 600-line file to look at one 40-line function puts the other 560 lines
in context for the rest of the turn, and they are re-sent on every step that
follows. The map costs a few dozen lines and tells you which range to ask for.

Declaration ranges include the doc comment above them, so one read gives you
both the code and its explanation.

### After editing a file, map it again

`file_map` reads the file fresh on every call — it consults no index, so its
ranges are never stale. **The ranges already in your context are.** Editing a
file shifts every line below the edit, which silently invalidates ranges you
were given earlier. Call `file_map` again before your next read of that file.

### Limits

- Files above 2 MB are not mapped — page through them with `read(path, offset, limit)`.
- Go, TypeScript, TSX and JavaScript (`.go`, `.ts`, `.tsx`, `.js`, `.jsx`,
  `.mts`, `.cts`, `.mjs`, `.cjs`) are fully parsed. Everything else falls back to
  a heuristic scan: the declarations it finds are real, but each range ends where
  the next declaration starts rather than at the true end, so allow some slack.
- Indented entries are nested inside the entry above them — a class's methods, or
  the handlers inside a component. A component that is one large arrow function
  is mapped from the inside, so you can jump to one handler rather than reading
  the whole component.
- The map lists declarations, not struct fields, class properties, or non-function
  local variables.
