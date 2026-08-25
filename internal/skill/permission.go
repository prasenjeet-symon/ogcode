package skill

import (
	"path"
	"sort"
	"strings"
)

// Action is what a skill's configured permission rule allows.
type Action string

const (
	// Allow: listed in the prompt, loads without interruption. The default for
	// any skill no rule matches.
	Allow Action = "allow"
	// Deny: hidden from the prompt entirely, and refused if called anyway.
	Deny Action = "deny"
	// Ask: listed in the prompt; the user approves the load at call time.
	Ask Action = "ask"
)

// Rules maps a name pattern to an action, as written in ogcode.json:
//
//	"skills": { "permissions": { "*": "allow", "internal-*": "deny" } }
//
// Patterns use shell globbing, so "internal-*" covers internal-docs and
// internal-deploy alike.
type Rules map[string]string

// Evaluate returns the action configured for a skill name.
//
// The most specific matching pattern wins, not the first: these rules arrive as
// a JSON object, Go map iteration is randomized, and "first match" over an
// unordered map would give the same config different meanings on different
// runs. Specificity is the ordering the data can actually support — an exact
// name beats any glob, and among globs the one with more literal characters
// beats the one with fewer, so "*" is always the last resort.
//
// A name no pattern matches is allowed: skills are opt-in files the user put in
// their own project, and the permission layer exists to carve exceptions out of
// that, not to gate every skill behind a rule.
//
// An unrecognized action string is treated as Ask rather than ignored. A typo
// in a rule the user wrote to restrict something must not silently widen access
// back to allow.
func (r Rules) Evaluate(name string) Action {
	best := ""
	bestScore := -1
	for pattern := range r {
		if !matches(pattern, name) {
			continue
		}
		score := specificity(pattern)
		// Lexical tie-break keeps two equally specific patterns resolving to the
		// same rule on every run.
		if score > bestScore || (score == bestScore && pattern < best) {
			best, bestScore = pattern, score
		}
	}
	if bestScore < 0 {
		return Allow
	}
	switch Action(strings.ToLower(strings.TrimSpace(r[best]))) {
	case Allow:
		return Allow
	case Deny:
		return Deny
	default:
		return Ask
	}
}

// Invalid returns the rules ogcode cannot honor as written, so the caller can
// report them. A rule the user cannot see failing is a rule they will assume is
// working.
//
// Two things make a rule unusable: an action string that is not allow, deny or
// ask — those are evaluated as Ask — and a pattern that is not a valid glob,
// which matches nothing at all, so a deny written that way protects nothing.
func (r Rules) Invalid() []string {
	var bad []string
	for pattern, action := range r {
		if _, err := path.Match(pattern, ""); err != nil {
			bad = append(bad, pattern)
			continue
		}
		switch Action(strings.ToLower(strings.TrimSpace(action))) {
		case Allow, Deny, Ask:
		default:
			bad = append(bad, pattern)
		}
	}
	sort.Strings(bad)
	return bad
}

// matches reports whether a glob pattern covers a skill name. Skill names never
// contain a separator, so path.Match's refusal to let * cross "/" costs nothing
// here.
func matches(pattern, name string) bool {
	if pattern == name {
		return true
	}
	ok, err := path.Match(pattern, name)
	return err == nil && ok
}

// specificity scores a pattern by how much of it is literal. An exact name gets
// a bonus large enough that no glob, however long, can outrank it.
func specificity(pattern string) int {
	literal := 0
	for _, c := range pattern {
		if c != '*' && c != '?' && c != '[' && c != ']' {
			literal++
		}
	}
	if !strings.ContainsAny(pattern, "*?[") {
		return 1000 + literal
	}
	return literal
}
