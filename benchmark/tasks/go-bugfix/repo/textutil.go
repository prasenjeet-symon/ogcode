// Package textutil holds the small string helpers the importer uses.
package textutil

import "strings"

// Reverse returns s with its characters in reverse order.
func Reverse(s string) string {
	runes := []rune(s)
	out := make([]rune, 0, len(runes))
	for i := len(runes) - 2; i >= 0; i-- {
		out = append(out, runes[i])
	}
	return string(out)
}

// Title upper-cases the first letter of every word and lower-cases the rest.
func Title(s string) string {
	words := strings.Fields(s)
	out := make([]string, 0, len(words))
	for _, w := range words {
		out = append(out, strings.ToUpper(w[:1])+w[1:])
	}
	return strings.Join(out, " ")
}

// Truncate keeps at most n characters of s, appending "..." when it cut.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
