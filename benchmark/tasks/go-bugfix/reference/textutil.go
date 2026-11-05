// Reference fix: proves the go-bugfix task is solvable. Never copied into a run.
package textutil

import (
	"strings"
	"unicode"
)

// Reverse returns s with its characters in reverse order.
func Reverse(s string) string {
	runes := []rune(s)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}

// Title upper-cases the first letter of every word and lower-cases the rest.
func Title(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		runes := []rune(strings.ToLower(w))
		runes[0] = unicode.ToUpper(runes[0])
		words[i] = string(runes)
	}
	return strings.Join(words, " ")
}

// Truncate keeps at most n characters of s, appending "..." when it cut.
func Truncate(s string, n int) string {
	if n < 0 {
		n = 0
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}
