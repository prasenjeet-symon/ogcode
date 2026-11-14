package logging

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"
)

// Redacted replaces a secret in a log line.
const Redacted = "[REDACTED]"

// secretKeySuffixes name attributes whose string value is a secret whatever it
// looks like. Keys are compared lowercased with '_', '-' and '.' removed, and
// match on suffix, so "api_key", "OPENAI_API_KEY" and "refreshToken" all hit.
// Only string-like values are redacted: "tokens" and "inputTokens" are counts.
var secretKeySuffixes = []string{
	"apikey", "token", "secret", "password", "passwd", "passphrase",
	"authorization", "cookie", "credential", "credentials", "privatekey",
}

func isSecretKey(key string) bool {
	k := strings.ToLower(key)
	k = strings.NewReplacer("_", "", "-", "", ".", "").Replace(k)
	if k == "auth" {
		return true
	}
	// "authorization" is not only a suffix ("x-authorization-header"); any
	// key it appears in names the Authorization header, whose value is a secret.
	if strings.Contains(k, "authorization") {
		return true
	}
	for _, s := range secretKeySuffixes {
		if strings.HasSuffix(k, s) {
			return true
		}
	}
	return false
}

// secretPatterns find secrets inside free text: provider error bodies, request
// URLs, wrapped errors. Each keeps enough context to tell what was removed.
var secretPatterns = []struct {
	re   *regexp.Regexp
	repl string
}{
	// Credentials in a URL: scheme://user:pass@host.
	{regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)[^/\s:@]+:[^/\s@]+@`), "${1}" + Redacted + "@"},
	// Authorization header values.
	{regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`), "${1} " + Redacted},
	// name=value for a name that is always a secret, in a query string or prose.
	{regexp.MustCompile(`(?i)\b((?:x[_-]?)?api[_-]?key|(?:access|refresh|id|auth)[_-]?token|client[_-]?secret|token|secret|password|passwd|pass|pwd|sessionid|session[_-]?id|session|sid|csrf|xsrf|jwt)=[^\s&"',;]+`), "${1}=" + Redacted},
	// Short query parameters that are secrets only inside a URL (?key= is how
	// Gemini takes its API key; code= is an OAuth authorization code).
	{regexp.MustCompile(`(?i)([?&](?:key|code|sig|signature)=)[^\s&"'#]+`), "${1}" + Redacted},
	// "name": "value" in a JSON body.
	{regexp.MustCompile(`(?i)("(?:(?:x[_-]?)?api[_-]?key|(?:access|refresh|id|auth)[_-]?token|client[_-]?secret|token|secret|password|authorization)"\s*:\s*")[^"]*"`), "${1}" + Redacted + `"`},
	// Well-known credential shapes, wherever they appear.
	{regexp.MustCompile(`\b(?:sk-ant-[A-Za-z0-9_-]{8,}|sk-(?:proj-|or-v1-|svcacct-)?[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|xox[abposr]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{16}|AIza[0-9A-Za-z_-]{35}|tvly-[A-Za-z0-9_-]{16,}|eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,})`), Redacted},
	// A Cookie or Set-Cookie header anywhere: the whole header line is a
	// secret — session ids, CSRF tokens, tracking values — up to the newline.
	{regexp.MustCompile(`(?i)\b((?:set-)?cookie)\s*:[^\n]*`), "${1}: " + Redacted},
	// A secret-named key and colon inside a formatted value: %+v stringifies
	// KindAny maps and structs as map[token:abc user:x] or {APIKey:abc}, and
	// the key-based redaction never sees a name in there. Blank the value —
	// one non-space token, so map[...] and {...} keep their closing brackets —
	// and never a count: a plural key such as "tokens: 5" or "inputTokens:
	// 120" holds a number and stays. The value may not contain brackets: that
	// keeps a "[REDACTED]" marker an earlier pattern produced from being
	// matched again and its closing "]" doubled. The separator is captured so
	// "key:value" and "key: value" keep their original spacing. Authorization
	// is deliberately absent: an "Authorization: Bearer …" header is redacted
	// by the pattern above, and blanking the scheme's word here would corrupt
	// it.
	{regexp.MustCompile(`(?i)\b(api[_-]?key|(?:access|refresh|id|auth)[_-]?token|client[_-]?secret|token|secret|password|passwd|pass|pwd|credential|cookie|auth|jwt|session[_-]?id)(\s*:\s*)[^\s\]\}[]+`), "${1}${2}" + Redacted},
	// Content-bearing query parameters: these carry user content — the Cmd-K
	// search text, a directory path, a prompt — so their value never leaves
	// the process. They are not credential-shaped, which is why this pattern
	// runs after every secret one above and those keep their own redaction.
	{regexp.MustCompile(`(?i)([?&](?:q|query|prompt|text|content|message|body|title|note|search|directory|dir|path|file|filename|url|uri)=)[^\s&"'#]+`), "${1}" + Redacted},
}

// Scrub removes secrets from free text.
func Scrub(s string) string {
	for _, p := range secretPatterns {
		s = p.re.ReplaceAllString(s, p.repl)
	}
	return s
}

// redactAttr is a slog ReplaceAttr that blanks secret-named attributes and
// scrubs secrets out of every string, error and formatted value — the message
// included, since the built-in handlers pass it through ReplaceAttr as "msg".
func redactAttr(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 {
		switch a.Key {
		case slog.TimeKey, slog.LevelKey, slog.SourceKey:
			return a
		}
	}
	secret := isSecretKey(a.Key)
	switch a.Value.Kind() {
	case slog.KindString:
		s := a.Value.String()
		if secret && s != "" {
			return slog.String(a.Key, Redacted)
		}
		if c := Scrub(s); c != s {
			return slog.String(a.Key, c)
		}
	case slog.KindGroup:
		// A group reaches here when a caller hands one over directly; the
		// built-in handlers expand one into its members and call ReplaceAttr
		// per member before this sees them, but a custom handler may not,
		// so walk the group: keep the key and the group-ness, redact every
		// member with the group key added to the prefix.
		attrs := a.Value.Group()
		inner := make([]slog.Attr, 0, len(attrs))
		for _, ga := range attrs {
			inner = append(inner, redactAttr(append(append([]string{}, groups...), a.Key), ga))
		}
		if a.Key == "" { // A keyless group inlines into its parent.
			return slog.Attr{Value: slog.GroupValue(inner...)}
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(inner...)}
	case slog.KindAny:
		var s string
		switch v := a.Value.Any().(type) {
		case nil:
			return a
		case error:
			s = v.Error()
		case []byte:
			s = string(v)
		default:
			s = fmt.Sprintf("%+v", v)
		}
		if secret {
			return slog.String(a.Key, Redacted)
		}
		if c := Scrub(s); c != s {
			return slog.String(a.Key, c)
		}
	}
	return a
}
