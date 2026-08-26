package permission

import (
	"os"
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

// mutatingFlags lists, per otherwise-safe command, the options that stop it
// being read-only, with the verdict each one earns.
//
// find is the case that matters: walking a tree is harmless, which is why it
// sits in safeCommands, but -delete removes every match and -exec runs an
// arbitrary command per match. Without this, "find . -delete" and
// "find . -exec rm {} ;" both classified RiskSafe and ran unprompted in Auto
// mode. The verdicts follow the rules already applied elsewhere here: -delete is
// unambiguously destructive, so RiskAsk; the flags that run a command the rules
// cannot see, or write to a file, get RiskUnclear — the same answer command
// substitution and a file redirect already get, leaving the LLM check to judge
// whether e.g. "find . -exec grep foo {} ;" is fine.
//
// Other safe commands can hide a write behind a flag the same way (sort -o,
// tee); add them here as they come up.
var mutatingFlags = map[string]map[string]Risk{
	"find": {
		"-delete":  RiskAsk,
		"-exec":    RiskUnclear,
		"-execdir": RiskUnclear,
		"-ok":      RiskUnclear,
		"-okdir":   RiskUnclear,
		"-fprint":  RiskUnclear,
		"-fprintf": RiskUnclear,
		"-fls":     RiskUnclear,
	},
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
		if r, ok := mutatingFlag(name, fields[1:]); ok {
			return r // a read-only tool told to delete or exec is not read-only
		}
		if hasFileRedirect(seg) {
			return RiskUnclear // a safe reader writing to a file is no longer purely safe
		}
		return RiskSafe
	}
	return RiskUnclear
}

// mutatingFlag scans args for an option that makes name non-read-only and
// returns the most severe verdict found (RiskAsk outranks RiskUnclear), or
// ok=false when the command carries no such flag.
func mutatingFlag(name string, args []string) (Risk, bool) {
	flags, ok := mutatingFlags[name]
	if !ok {
		return RiskSafe, false
	}
	found := false
	for _, a := range args {
		r, isMutating := flags[a]
		if !isMutating {
			continue
		}
		if r == RiskAsk {
			return RiskAsk, true
		}
		found = true
	}
	if !found {
		return RiskSafe, false
	}
	return RiskUnclear, true
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
// dangerous segment).
//
// Background "&" splits too, but only when it is NOT part of an fd-dup
// redirection like "2>&1" or ">&2" — i.e. only when the "&" is not immediately
// preceded by ">". A bare "&" with no surrounding space ("echo hi&rm -rf /")
// used to slip through: splitSegments only matched " & " (space-delimited on
// both sides), so the whole thing stayed one segment and classifySegment
// judged it by its first word ("echo"), auto-approving "rm -rf /" in the
// background in Auto mode without a prompt.
//
// Newlines separate commands too, and leaving them out used to hand Auto mode a
// straight bypass: "echo hi\nrm -rf /" stayed one segment, so classifySegment
// read "echo" as the command, took "rm -rf /" for its arguments, and returned
// RiskSafe — auto-approving the whole thing without a prompt.
func splitSegments(cmd string) []string {
	// First split on the unambiguous operators that never need context.
	r := strings.NewReplacer(
		"&&", "\x00", "||", "\x00", ";", "\x00", "|", "\x00",
		"\n", "\x00", "\r", "\x00",
	)
	s := r.Replace(cmd)
	// Then split on background "&", but only where it is not preceded by ">"
	// (which would make it an fd-dup like "2>&1"). A trailing bare "&"
	// (e.g. "sleep 1 &") is not a separator and is left in place.
	s = replaceBackgroundAmp(s, "\x00")
	return strings.Split(s, "\x00")
}

// replaceBackgroundAmp replaces every standalone "&" (the shell background
// operator) with sep, leaving fd-dup "&" (the "&" in "2>&1", ">&2", "&>file")
// untouched. It walks the string so it can look at the preceding character
// rather than relying on substring matching, which cannot distinguish "echo &x"
// from "2>&1".
func replaceBackgroundAmp(s, sep string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '&' {
			prev := byte(0)
			if i > 0 {
				prev = s[i-1]
			}
			// ">&" and "&>" are redirections, not background operators.
			if prev == '>' || prev == '<' {
				b.WriteByte(s[i])
				continue
			}
			if i+1 < len(s) && (s[i+1] == '>' || s[i+1] == '<') {
				b.WriteByte(s[i])
				continue
			}
			// Look ahead: a background "&" is followed by end-of-string, whitespace,
			// or another command char. A "&" immediately followed by another "&"
			// is already handled (&& was replaced above); treat a lone "&" with
			// only trailing whitespace as a trailing background marker (no split)
			// and a "&" followed by a non-space char ("&rm") as an inline background
			// operator that must split.
			if i+1 < len(s) {
				next := s[i+1]
				if next == '&' { // shouldn't happen post-replace, but be safe
					b.WriteByte(s[i])
					continue
				}
				if isShellSpace(next) {
					// "echo hi & " or "echo hi &" — split here so the trailing job
					// is a separate (empty) segment that ClassifyBash skips.
					b.WriteString(sep)
					continue
				}
				// "echo hi&rm -rf /" — bare inline background operator. Split.
				b.WriteString(sep)
				continue
			}
			// Trailing "&" at end of string: a trailing background marker, not a
			// separator. Leave it; ClassifyBash trims it away.
			b.WriteByte(s[i])
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// isShellSpace reports whether c is whitespace that separates shell tokens.
func isShellSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\x00'
}

// ClassifyWrite classifies a write/edit target. In-project, non-sensitive files
// are safe to auto-write; anything outside the project or matching a sensitive
// pattern (secrets, VCS internals, keys) requires approval.
//
// Both the target and the project root are resolved through their symlinks
// first. The write tools write with os.WriteFile, which follows symlinks, so a
// lexical containment check was checking the wrong file: a link inside the
// project — "innocent.txt" pointing at ~/.ssh/authorized_keys, or a symlinked
// directory — read as in-project and auto-approved, while the bytes landed
// outside it. Resolving also means isSensitivePath sees the real destination
// rather than whatever the link was named.
func ClassifyWrite(path, workDir string) Risk {
	p := path
	if p == "" {
		return RiskAsk
	}
	if !filepath.IsAbs(p) && workDir != "" {
		p = filepath.Join(workDir, p)
	}
	p = resolveSymlinks(filepath.Clean(p))
	if isSensitivePath(p) {
		return RiskAsk
	}
	if workDir == "" {
		return RiskAsk // can't confirm it's in-project — be cautious
	}
	// The project root gets the same treatment, or every path under a working
	// directory that is itself reached through a link (/tmp on macOS resolves to
	// /private/tmp) would compare against an unresolved root and read as an
	// escape.
	wd := resolveSymlinks(filepath.Clean(workDir))
	if p == wd || strings.HasPrefix(p, wd+string(filepath.Separator)) {
		return RiskSafe
	}
	return RiskAsk // outside the project directory
}

// maxSymlinkHops bounds how many links resolveSymlinks will follow by hand, so
// a link that points at itself cannot spin.
const maxSymlinkHops = 16

// resolveSymlinks returns p with the symlinks along it resolved.
//
// A write target usually does not exist yet — that is the point of the write —
// so EvalSymlinks on the whole path would simply fail. Everything above the
// target is resolved with it, and the target itself is followed by hand when it
// is a dangling link: writing through one creates the file at the far end, so a
// dangling link decides where the write lands exactly as a live one does, and
// leaving it unresolved would have left the original hole open under a
// different name. A path with nothing resolvable on it comes back unchanged and
// the caller's checks run on the lexical form, as they did before.
func resolveSymlinks(p string) string {
	return resolveSymlinksFrom(filepath.Clean(p), 0)
}

func resolveSymlinksFrom(p string, hops int) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	dir, base := filepath.Split(p)
	if dir == "" || base == "" {
		return p // relative to the process cwd, or already at the root
	}
	// The parent is resolved on the same hop budget — walking up a path follows
	// no links, so only the Readlink below spends from it.
	parent := resolveSymlinksFrom(filepath.Clean(dir), hops)
	target := filepath.Join(parent, base)
	if link, err := os.Readlink(target); err == nil && hops < maxSymlinkHops {
		if !filepath.IsAbs(link) {
			link = filepath.Join(parent, link)
		}
		return resolveSymlinksFrom(filepath.Clean(link), hops+1)
	}
	return target
}

// isSensitivePath flags secrets, credentials, VCS internals, and system files
// that should always require approval to write.
func isSensitivePath(p string) bool {
	lower := logicalPath(strings.ToLower(p))
	base := strings.ToLower(filepath.Base(p))

	// System locations. The OS scratch directory is exempt: it is a user work
	// area, not a system one, but on macOS it lives under /var/folders and would
	// match "/var/" — making every file of a project opened from a temp tree
	// need its own approval.
	if !underOSTempDir(lower) {
		for _, dir := range []string{"/etc/", "/usr/", "/bin/", "/sbin/", "/boot/", "/var/", "/lib/", "/system/", "/library/"} {
			if strings.HasPrefix(lower, dir) {
				return true
			}
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

// canonicalPrefixes are prefixes that symlink resolution can prepend on macOS
// without changing what a path means: /etc, /var and /tmp are links into
// /private, and anything reached through a firmlink (/home) lands under the data
// volume root. The sensitive-path list is written in logical terms, so these are
// stripped before it is matched — otherwise resolving would make "/etc/..." stop
// matching "/etc/" while "/home/..." started matching "/system/", flagging an
// ordinary project directory as a system location.
var canonicalPrefixes = []string{"/system/volumes/data", "/private"}

// logicalPath strips those prefixes from an already-lowercased path. They are
// applied in order and cumulatively, since a resolved path can carry both.
func logicalPath(lower string) string {
	for _, prefix := range canonicalPrefixes {
		if lower == prefix {
			return "/"
		}
		lower = strings.TrimPrefix(lower, prefix+"/")
		if !strings.HasPrefix(lower, "/") {
			lower = "/" + lower
		}
	}
	return lower
}

// osTempDir is the OS scratch directory in the same resolved, lowercased,
// logical form isSensitivePath compares against. Computed once: it cannot change
// during a run, and it costs a stat walk.
var osTempDir = func() string {
	t := logicalPath(strings.ToLower(resolveSymlinks(filepath.Clean(os.TempDir()))))
	if t == "/" || t == "." {
		return "" // nothing sensible to exempt
	}
	return strings.TrimSuffix(t, "/")
}()

// underOSTempDir reports whether a logical path is inside the OS scratch tree.
func underOSTempDir(lower string) bool {
	if osTempDir == "" {
		return false
	}
	return lower == osTempDir || strings.HasPrefix(lower, osTempDir+"/")
}
