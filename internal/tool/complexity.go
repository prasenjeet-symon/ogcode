package tool

import (
	"strings"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
)

// complexityNotice is what a spawn answers when the class was not one of the
// three the router understands.
const complexityNotice = "complexity must be easy, medium or hard — omit it to spawn without a classification"

// parseComplexity reads the complexity argument of a spawning call. Omission is
// fine — the child then carries no complexity header — but a class the router
// would not understand is bounced: a misspelled one would silently route the
// child as if it had never been classified, which is worse than no prediction.
func parseComplexity(raw string) (provider.Complexity, bool) {
	c := provider.Complexity(strings.TrimSpace(raw))
	if c == "" {
		return "", true
	}
	return c, c.Valid()
}
