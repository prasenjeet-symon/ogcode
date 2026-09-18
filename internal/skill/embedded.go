package skill

import (
	"fmt"
	"sync"
)

// embeddedSources are skills ogcode ships in the binary. They are registered
// before anything found on disk, so a user who writes a skill of the same name
// in their own project replaces the built-in rather than colliding with it.
//
// They are stored as raw SKILL.md text and parsed through the same Parse used
// for files, so a built-in cannot drift into a shape a disk skill would be
// rejected for.
var embeddedSources = []string{customizeOgcodeSkill, updateOgcodeSkill}

var (
	embeddedOnce sync.Once
	embedded     []Skill
	embeddedErrs []error
)

// Embedded returns the built-in skills, parsed once per process.
func Embedded() ([]Skill, []error) {
	embeddedOnce.Do(func() {
		for i, src := range embeddedSources {
			// parseContent rather than Parse: a built-in has no directory on
			// disk for the name to match, but it is held to every other rule a
			// disk skill is. Dir stays empty, which is also the signal that
			// nothing ships beside it.
			s, err := parseContent([]byte(src))
			if err != nil {
				embeddedErrs = append(embeddedErrs, fmt.Errorf("built-in skill %d: %w", i, err))
				continue
			}
			s.Source = SourceEmbedded
			embedded = append(embedded, s)
		}
	})
	return embedded, embeddedErrs
}

// customizeOgcodeSkill documents the files a user edits to configure ogcode. It
// is the one piece of project knowledge no project's own files can carry: a
// fresh checkout has no AGENT.md explaining what AGENT.md is for.
const customizeOgcodeSkill = `---
name: customize-ogcode
description: How to configure ogcode for a project — ogcode.json provider, skill, and MCP server settings, AGENT.md behavioural rules, MEMORY.md project knowledge, and authoring new skills. Load this when the user asks to change ogcode's own configuration or behaviour.
---

# Customizing ogcode

Four files control how ogcode behaves in a project. They serve different
purposes and are not interchangeable.

## ogcode.json — connection and skill settings

Project config lives in ` + "`ogcode.json`" + ` at the repo root; global config lives in
` + "`~/.config/ogcode/config.json`" + `. Both have the same shape, and project values
win field by field.

` + "```json" + `
{
  "providers": {
    "anthropic": { "baseUrl": "", "apiKey": "" },
    "openai": { "baseUrl": "", "apiKey": "" },
    "openrouter": { "apiKey": "" },
    "ollama": { "baseUrl": "", "apiKey": "" }
  },
  "skills": {
    "paths": ["./team-skills"],
    "urls": ["https://example.com/skills/index.json"],
    "permissions": { "internal-*": "deny", "deploy-*": "ask" },
    "env": { "DEPLOY_TOKEN": "…" }
  },
  "mcp": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
    },
    "github": {
      "url": "https://mcp.example.com/github",
      "headers": { "Authorization": "Bearer ghp_token" }
    }
  }
}
` + "```" + `

A real environment variable always beats the config file, so a key in the
environment is not overridden by one in ogcode.json.

## MCP — external tools over the Model Context Protocol

The ` + "`mcp`" + ` block declares external MCP servers whose tools are exposed to
the agent alongside ogcode's built-in tools. It goes in the same ` + "`ogcode.json`" + `
(project) or ` + "`~/.config/ogcode/config.json`" + ` (global) as ` + "`providers`" + ` and
` + "`skills`" + `. Each key is a server name you choose; its value is one of two shapes:

- **Local subprocess (stdio)**: set ` + "`command`" + ` (the executable) and ` + "`args`" + `
  (its arguments). ` + "`env`" + ` augments — never replaces — the parent process
  environment.
- **Remote HTTP**: set ` + "`url`" + ` and, optionally, ` + "`headers`" + `. The endpoint is
  a streamable-http server. Set ` + "`transport`" + ` to ` + "`\"sse\"`" + ` explicitly for an
  older SSE endpoint.

` + "`transport`" + ` is optional and auto-detected: stdio when ` + "`command`" + ` is set,
otherwise streamable-http. At startup ogcode connects to every declared server
in parallel. Each server's tools are registered under the id
` + "`mcp_<server>_<tool>`" + ` and surfaced to the coding agent through the ` + "`mcp_*`" + `
glob, so no code change is needed to use them. The ` + "`mcp_`" + ` prefix is what
makes the id match the glob; without it the tool connects but never reaches the
agent. Server and tool names are sanitised into the character set providers
accept for a function name (a-z, A-Z, 0-9, ` + "`_`" + `, ` + "`-`" + `), so a
server or tool whose own name contains anything else still yields a usable id.
A server that fails to connect is skipped with a warning; the rest still load.

Merging is **per-name override**, not a union: if a project re-states a server
the global config already named, the project definition replaces the global one
wholesale (re-stating a server implies intent to own its full definition). A
project name the global config did not have is simply added.

### Pitfalls

- **Servers connect at startup, not mid-session.** Editing ` + "`ogcode.json`" + ` or
  ` + "`~/.config/ogcode/config.json`" + ` has no effect on the running session — fully
  restart ogcode for a new or changed server to connect.
- **Don't bridge a remote server through ` + "`npx mcp-remote`" + `.** A Claude
  Desktop config often wraps a remote HTTP server as
  ` + "`\"command\": \"npx\", \"args\": [\"-y\", \"mcp-remote\", \"<url>\"]`" + `. Copied
  verbatim into ogcode this can spawn the ` + "`mcp-remote`" + ` process yet never
  complete the MCP handshake, so no ` + "`mcp_*`" + ` tools register — silently. Use
  ogcode's native ` + "`url`" + ` + ` + "`transport`" + ` shape instead, which cuts out the
  middleman and talks to the endpoint directly.
- **A live subprocess is not proof of a working connection.** ` + "`mcp-remote`" + `
  running only shows ogcode launched the bridge, not that tools loaded. The real
  test is whether ` + "`mcp_<server>_*`" + ` tools appear in the session. If a remote
  server still won't load through the native shape, try omitting ` + "`transport`" + `
  (let ogcode auto-detect) or pointing ` + "`url`" + ` at the ` + "`/mcp`" + ` base path
  rather than ` + "`/mcp/stream`" + `.

## AGENT.md — how the agent should work

AGENT.md holds behavioural rules: build and test commands, formatting policy,
conventions to follow, things not to touch. ogcode reads every AGENT.md from the
filesystem root down to the working directory, so a rule in a subdirectory
layers on top of the repo-wide one.

Write rules the agent cannot infer from the code. Cut the paragraph arguing for
a rule — the prompt is re-sent on every step of every turn.

## MEMORY.md — what is known about the project

MEMORY.md holds facts, not instructions: decisions and their rationale,
gotchas, non-obvious behaviour, values that would otherwise be rediscovered.
If it tells the agent how to act, it belongs in AGENT.md instead.

## Skills — instructions loaded on demand

A skill is a directory holding a SKILL.md. ogcode looks in, from lowest
precedence to highest:

- ` + "`~/.config/ogcode/skills/`" + `, ` + "`~/.ogcode/skills/`" + `, ` + "`~/.agents/skills/`" + `, ` + "`~/.claude/skills/`" + `
- any directory listed in ` + "`skills.paths`" + `
- ` + "`.agents/skills/`" + ` and ` + "`.claude/skills/`" + `, in the project and each parent up to the repo root

` + "```markdown" + `
---
name: git-release
description: Draft release notes, bump the version, and tag the release.
---

## Steps
1. ...
` + "```" + `

The frontmatter name must be lowercase alphanumeric with single hyphens, and
must match the directory name. The description is what the agent sees in its
prompt — it decides from that alone whether to load the skill, so describe when
to use it, not just what it is.

A skill whose instructions need a credential declares it with a ` + "`requires`" + ` list
of environment variable names, either as a comma list or a YAML sequence:

` + "```markdown" + `
---
name: deploy
description: Ship to staging.
requires: DEPLOY_TOKEN
---
` + "```" + `

Loading a skill with a missing requirement is refused, and the refusal names the
variable — the body is withheld because its steps would fail on the same missing
value. Supply the value under ` + "`skills.env`" + ` in ` + "`ogcode.json`" + `, or export it
in the environment ogcode was started from (a real environment variable always
wins). Tell the user which of the two to use; do not retry the load until the
variable is set.

Files shipped beside SKILL.md (scripts, references) are listed to the agent when
the skill loads, and relative paths in the body resolve against the skill's own
directory.

## .ogcode/ — runtime state, not configuration

` + "`.ogcode/`" + ` holds the session database, notes, plan archives, and git
worktrees. It is written by ogcode. Never hand-edit it, and never put skills or
configuration there.
`

// updateOgcodeSkill tells the agent how ogcode itself is updated. A user who
// asks "update ogcode" or hits a "new version available" banner needs the
// right command for how their copy was installed, and the agent has no way to
// guess that from the project files — so the knowledge ships in the binary.
const updateOgcodeSkill = `---
name: update-ogcode
description: How to update ogcode itself — detect the install channel (brew, scoop, cargo, winget, or the install scripts), pick the matching upgrade command, verify with ogcode version, and check for updates. Load this when the user asks to update, upgrade, or reinstall ogcode, or asks about the version they are running.
---

# Updating ogcode

There is no self-update: ogcode never replaces its own binary. Updating is a
command run outside the session (or by you through the bash tool), then a
restart of ogcode to pick up the new build.

## 1. Find the install channel

The channel is visible from where the running binary lives. Check it first,
before proposing any command:

` + "```sh" + `
ogcode version
ls -l "$(command -v ogcode)"
GO=$(command -v ogcode); ls -l "$GO" 2>/dev/null; readlink -f "$GO" 2>/dev/null || true
` + "```" + `

` + "readlink -f" + ` resolves a package-manager symlink to the real file, which
is what carries the channel fingerprint. Match the resolved path against this
table, narrowest first:

| Resolved binary location | Channel | Update command |
| --- | --- | --- |
| ` + "`<scoop-root>\\shims`" + ` or ` + "`<scoop-root>\\apps\\...`" + ` | scoop | ` + "`scoop update ogcode`" + ` |
| anything containing ` + "`/Cellar/`" + ` | Homebrew | ` + "`brew upgrade ogcode`" + ` |
| ` + "`/opt/homebrew/bin`" + ` | Homebrew (Apple Silicon) | ` + "`brew upgrade ogcode`" + ` |
| ` + "`$CARGO_HOME/bin`" + `, by default ` + "`~/.cargo/bin`" + ` | cargo | ` + "`cargo install ogcode --force`" + ` |
| ` + "`%LOCALAPPDATA%\\ogcode`" + ` | the install.ps1 script | ` + "`irm https://ogcode.xyz/install.ps1 | iex`" + ` |
| any other Windows location | winget (documented default) | ` + "`winget upgrade ogcode`" + ` |
| any other macOS/Linux location | the install.sh script | ` + "`curl -fsSL https://ogcode.xyz/install.sh | sh`" + ` |

Scoop's root is ` + "`$env:SCOOP`" + `, ` + "`$env:SCOOP_GLOBAL`" + `, or
` + "`$env:USERPROFILE\\scoop`" + `. A ` + "`/usr/local/bin/ogcode`" + ` on macOS or Linux
is a **script install, not Homebrew** — Homebrew-sourced binaries resolve
through a ` + "`Cellar`" + ` path.

## 2. Check whether an update exists

` + "```sh" + `
ogcode check-updates
` + "```" + `

This queries the GitHub releases API and prints Current, Latest, and the
update command for the detected channel. It exits ` + "`1`" + ` when an update is
available, ` + "`0`" + ` when current, so it is scriptable. Run it with the bash
tool and read the result to the user — do not guess whether a release exists.

## 3. Run the update and verify

Run the channel's command from the table with the bash tool, then verify:

` + "```sh" + `
ogcode version
` + "```" + `

The running session keeps the old binary until ogcode is restarted, so tell
the user to restart ogcode to finish the update.

## Notes

- **macOS codesigning:** replacing the binary in place can leave a stale code
  signature and the next launch dies with ` + "`Killed: 9`" + `. If that happens,
  re-sign: ` + "`codesign --force --sign - $(command -v ogcode)`" + `. The
  package-manager and script channels handle this themselves.
- **go install:** ` + "`go install github.com/prasenjeet-symon/ogcode@latest`" + `
  works but does not control the CGO setting the build needs; prefer the
  package-manager or script channels.
- **Dev builds:** if ` + "`ogcode version`" + ` reports a ` + "`dev`" + ` or
  dirty-git-describe version, this is a source build — updating means
  ` + "`git pull`" + ` and a rebuild, not an install command.
`
