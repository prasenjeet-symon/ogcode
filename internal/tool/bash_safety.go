package tool

import (
	"regexp"
	"strings"
)

// isDangerousCommand is a conservative, defense-in-depth denylist for the bash
// tool. It is NOT a sandbox: it targets a small set of high-confidence, almost
// always catastrophic and irreversible commands (recursive deletion of root or
// the home directory, raw disk overwrite, filesystem format, fork bomb) while
// deliberately leaving normal development commands — including `rm -rf` on
// project subdirectories like node_modules or ./build — untouched.
//
// It returns (true, reason) when the command should be refused. Because it errs
// toward NOT blocking, it must be paired with the permission layer (P0-1 step 2+)
// for real gating; on its own it only stops the worst disasters.
func isDangerousCommand(command string) (bool, string) {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return false, ""
	}

	if forkBombRe.MatchString(cmd) {
		return true, "fork bomb"
	}

	// Evaluate each simple segment independently so a dangerous command can't
	// hide after a `;`, `&&`, `||`, `|`, or `&`.
	for _, seg := range splitShellSegments(cmd) {
		fields := strings.Fields(seg)
		// Strip leading privilege wrappers so `sudo rm -rf /` is still inspected.
		for len(fields) > 1 && (fields[0] == "sudo" || fields[0] == "doas") {
			fields = fields[1:]
		}
		if len(fields) == 0 {
			continue
		}
		name := fields[0]
		args := fields[1:]

		switch {
		case name == "rm":
			if hasRecursiveFlag(args) {
				for _, a := range args {
					if isCatastrophicPath(a) {
						return true, "recursive delete of " + a
					}
				}
			}
		case name == "mkfs" || strings.HasPrefix(name, "mkfs."):
			return true, "filesystem format (mkfs)"
		case name == "chmod" || name == "chown":
			if hasFlag(args, "R") {
				for _, a := range args {
					if isCatastrophicPath(a) {
						return true, "recursive " + name + " on " + a
					}
				}
			}
		case name == "dd":
			for _, a := range args {
				if strings.HasPrefix(a, "of=/dev/") && isRawDisk(strings.TrimPrefix(a, "of=")) {
					return true, "raw disk write (dd " + a + ")"
				}
			}
		}

		// Shell redirection straight onto a raw block device, e.g. `> /dev/sda`.
		if diskRedirectRe.MatchString(seg) {
			return true, "raw disk overwrite via redirection"
		}
	}

	return false, ""
}

var (
	// Fork bomb: :(){ :|:& };:  — tolerant of whitespace variations.
	forkBombRe = regexp.MustCompile(`:\s*\(\s*\)\s*\{[^}]*\|[^}]*&[^}]*\}\s*;\s*:`)
	// Redirect (truncating or appending) onto a raw block device.
	diskRedirectRe = regexp.MustCompile(`>>?\s*/dev/(sd[a-z]|nvme\d|hd[a-z]|disk\d|vd[a-z])`)
)

// splitShellSegments breaks a command line into simple segments on the common
// shell separators. It is a heuristic (it does not honor quoting), which is fine
// for a safety check that only needs to find a dangerous command somewhere.
func splitShellSegments(cmd string) []string {
	replacer := strings.NewReplacer(
		"&&", "\x00", "||", "\x00", ";", "\x00", "|", "\x00", "&", "\x00",
	)
	return strings.Split(replacer.Replace(cmd), "\x00")
}

// hasRecursiveFlag reports whether the args contain a recursive flag, either as
// a long option (--recursive) or as an r/R inside a combined short flag (-rf).
func hasRecursiveFlag(args []string) bool {
	for _, a := range args {
		if a == "--recursive" {
			return true
		}
	}
	return hasFlag(args, "r") || hasFlag(args, "R")
}

// hasFlag reports whether letter appears in any short flag group (e.g. "R" in
// "-Rf") or as the corresponding long flag.
func hasFlag(args []string, letter string) bool {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			continue
		}
		if strings.HasPrefix(a, "--") {
			continue
		}
		if strings.Contains(a[1:], letter) {
			return true
		}
	}
	return false
}

// isCatastrophicPath reports whether p is a whole-system or whole-home target
// that must never be recursively deleted or chmod'd. It matches only exact,
// high-confidence tokens — subdirectories like "/tmp/x", "./build", or
// "node_modules" are intentionally NOT flagged.
func isCatastrophicPath(p string) bool {
	switch p {
	case "/", "/*", "~", "~/", "~/*", "$HOME", "$HOME/", "$HOME/*", "/root", "/root/", "/etc", "/usr", "/bin", "/boot", "/var", "/lib", "/System", "/Users":
		return true
	}
	return false
}

// isRawDisk reports whether target names a raw block device (dd of=... guard).
func isRawDisk(target string) bool {
	return diskRedirectRe.MatchString(">" + target)
}
