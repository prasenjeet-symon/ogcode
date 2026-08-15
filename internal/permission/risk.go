package permission

import (
	"path/filepath"
	"strings"
)

// Risk is the rules-based verdict for a tool call in Auto mode.
type Risk int

const (
	// RiskSafe: run automatically without asking.
	RiskSafe Risk = iota
	// RiskAsk: clearly consequential — always ask the user.
	RiskAsk
	// RiskUnclear: the rules can't decide; the caller should escalate (e.g. a
	// quick LLM risk check), defaulting to asking if that's unavailable.
	RiskUnclear
)

// safeCommands are read-only / inspection / build-test commands that, on their
// own, neither destroy data nor change system state.
var safeCommands = map[string]bool{
	"ls": true, "pwd": true, "cd": true, "echo": true, "printf": true, "cat": true,
	"head": true, "tail": true, "less": true, "more": true, "wc": true, "stat": true,
	"file": true, "du": true, "df": true, "env": true, "printenv": true, "whoami": true,
	"id": true, "hostname": true, "uname": true, "date": true, "which": true, "type": true,
	"command": true, "clear": true, "tree": true, "find": true, "grep": true, "egrep": true,
	"fgrep": true, "rg": true, "ag": true, "fd": true, "sort": true, "uniq": true, "cut": true,
	"diff": true, "cmp": true, "basename": true, "dirname": true, "realpath": true,
	"readlink": true, "true": true, "false": true, "test": true, "gofmt": true,
	"golangci-lint": true, "pytest": true, "tsc": true, "eslint": true, "prettier": true,
	"jq": true, "column": true, "nl": true, "tac": true, "xxd": true, "od": true, "strings": true,
}

// safeSubcommands: tools that are only safe for specific read-only subcommands.
var safeSubcommands = map[string]map[string]bool{
	"git": {
		"status": true, "log": true, "diff": true, "show": true, "branch": true,
		"remote": true, "blame": true, "describe": true, "rev-parse": true,
		"ls-files": true, "ls-remote": true, "tag": true, "shortlog": true,
		"reflog": true, "config": true, "fetch": true, "grep": true, "whatchanged": true,
		"cat-file": true, "symbolic-ref": true,
	},
	"go":     {"build": true, "test": true, "vet": true, "run": true, "doc": true, "list": true, "version": true, "env": true, "fmt": true},
	"npm":    {"test": true, "run": true, "ls": true, "list": true, "outdated": true, "view": true, "why": true, "audit": true},
	"pnpm":   {"test": true, "run": true, "ls": true, "list": true, "why": true, "outdated": true},
	"yarn":   {"test": true, "run": true},
	"cargo":  {"build": true, "test": true, "check": true, "clippy": true, "fmt": true, "tree": true},
	"docker": {"ps": true, "images": true, "logs": true, "inspect": true, "version": true, "info": true},
}

// dangerousCommands are clearly destructive, privilege-escalating, or otherwise
// irreversible — always ask, even in Auto mode.
var dangerousCommands = map[string]bool{
	"rm": true, "rmdir": true, "unlink": true, "shred": true, "dd": true, "mkfs": true,
	"fdisk": true, "mkswap": true, "sudo": true, "doas": true, "su": true, "shutdown": true,
	"reboot": true, "halt": true, "poweroff": true, "init": true, "kill": true,
	"killall": true, "pkill": true, "truncate": true, "mount": true, "umount": true,
	"systemctl": true, "launchctl": true, "crontab": true, "iptables": true,
	"passwd": true, "userdel": true, "visudo": true, "chpasswd": true, "diskutil": true,
	// executing a shell almost always means "run whatever came down the pipe"
	// (curl ... | sh), which the rules can't see — treat as dangerous.
	"sh": true, "bash": true, "zsh": true, "ksh": true, "fish": true, "eval": true, "exec": true,
}

// dangerousSubcommands: tools whose specific subcommands are destructive.
var dangerousSubcommands = map[string]map[string]bool{
	"git":    {"push": true, "clean": true, "filter-branch": true},
	"npm":    {"publish": true},
	"docker": {"rmi": true},
}

// ClassifyBash classifies a shell command for Auto mode. It errs toward asking:
// any dangerous segment makes the whole command RiskAsk; a command is RiskSafe
// only if every segment is clearly safe; anything else is RiskUnclear (escalate).
func ClassifyBash(command string) Risk {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return RiskSafe
	}
	unclear := false
	for _, seg := range splitSegments(cmd) {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		fields := strings.Fields(seg)
		if len(fields) == 0 {
			continue
		}
		// Command substitution can hide arbitrary commands — never auto-safe.
		if strings.Contains(seg, "$(") || strings.Contains(seg, "`") {
			unclear = true
			continue
		}
		switch classifySegment(fields, seg) {
		case RiskAsk:
			return RiskAsk
		case RiskUnclear:
			unclear = true
		}
	}
	if unclear {
		return RiskUnclear
	}
	return RiskSafe
}

func classifySegment(fields []string, seg string) Risk {
	name := fields[0]
	// A leading VAR=value assignment (FOO=bar cmd) — skip assignments to find the command.
	for len(fields) > 1 && isEnvAssignment(name) {
		fields = fields[1:]
		name = fields[0]
	}
	if isEnvAssignment(name) {
		return RiskSafe // pure assignment, no command
	}

	if dangerousCommands[name] {
		return RiskAsk
	}
	if subs, ok := dangerousSubcommands[name]; ok && len(fields) > 1 && subs[fields[1]] {
		return RiskAsk
	}
	if subs, ok := safeSubcommands[name]; ok {
		if len(fields) > 1 && subs[fields[1]] {
			if hasFileRedirect(seg) {
				return RiskUnclear
			}
			return RiskSafe
		}
		return RiskUnclear // known tool, unrecognized subcommand
	}
	if safeCommands[name] {
		if hasFileRedirect(seg) {
			return RiskUnclear // a safe reader writing to a file is no longer purely safe
		}
		return RiskSafe
	}
	return RiskUnclear
}

func isEnvAssignment(tok string) bool {
	i := strings.IndexByte(tok, '=')
	if i <= 0 {
		return false
	}
	for _, r := range tok[:i] {
		if !(r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// hasFileRedirect reports whether the segment redirects output to a real file
// (not /dev/null and not a bare fd dup like 2>&1).
func hasFileRedirect(seg string) bool {
	for i := 0; i < len(seg); i++ {
		if seg[i] != '>' {
			continue
		}
		rest := strings.TrimLeft(seg[i+1:], "> ") // skip >> and spaces
		switch {
		case rest == "" || strings.HasPrefix(rest, "&"): // 2>&1 etc.
			continue
		case strings.HasPrefix(rest, "/dev/null"):
			continue
		default:
			return true
		}
	}
	return false
}

// splitSegments breaks a command line on the common shell chaining operators
// (heuristic, quote-agnostic — fine for a risk check that only needs to find any
// dangerous segment). It deliberately does NOT split on a bare "&": that would
// tear apart fd redirections like "2>&1" / ">&2". Background jobs written as
// " & " (space-delimited) are still split.
func splitSegments(cmd string) []string {
	r := strings.NewReplacer("&&", "\x00", "||", "\x00", ";", "\x00", "|", "\x00", " & ", "\x00")
	return strings.Split(r.Replace(cmd), "\x00")
}

// ClassifyWrite classifies a write/edit target. In-project, non-sensitive files
// are safe to auto-write; anything outside the project or matching a sensitive
// pattern (secrets, VCS internals, keys) requires approval.
func ClassifyWrite(path, workDir string) Risk {
	p := path
	if p == "" {
		return RiskAsk
	}
	if !filepath.IsAbs(p) && workDir != "" {
		p = filepath.Join(workDir, p)
	}
	p = filepath.Clean(p)
	if isSensitivePath(p) {
		return RiskAsk
	}
	if workDir == "" {
		return RiskAsk // can't confirm it's in-project — be cautious
	}
	wd := filepath.Clean(workDir)
	if p == wd || strings.HasPrefix(p, wd+string(filepath.Separator)) {
		return RiskSafe
	}
	return RiskAsk // outside the project directory
}

// isSensitivePath flags secrets, credentials, VCS internals, and system files
// that should always require approval to write.
func isSensitivePath(p string) bool {
	lower := strings.ToLower(p)
	base := strings.ToLower(filepath.Base(p))

	// System locations.
	for _, dir := range []string{"/etc/", "/usr/", "/bin/", "/sbin/", "/boot/", "/var/", "/lib/", "/system/", "/library/"} {
		if strings.HasPrefix(lower, dir) {
			return true
		}
	}
	// Secret / credential directories anywhere in the path.
	for _, seg := range []string{"/.git/", "/.ssh/", "/.aws/", "/.gnupg/", "/.config/gcloud/", "/.kube/", "/.docker/"} {
		if strings.Contains(lower, seg) {
			return true
		}
	}
	// Secret / credential file names.
	switch base {
	case ".env", ".env.local", ".env.production", ".npmrc", ".pypirc", ".netrc",
		"credentials", "id_rsa", "id_dsa", "id_ecdsa", "id_ed25519", ".git-credentials":
		return true
	}
	if strings.HasPrefix(base, ".env.") {
		return true
	}
	for _, ext := range []string{".pem", ".key", ".p12", ".pfx", ".keystore"} {
		if strings.HasSuffix(base, ext) {
			return true
		}
	}
	return false
}
