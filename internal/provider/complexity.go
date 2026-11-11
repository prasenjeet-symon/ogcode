package provider

import "net/http"

// Complexity is the spawning agent's prediction of how hard a delegated task
// is, stated at the moment it hands the work over. It travels as the
// X-Client-Complexity header beside X-Client-Task rather than in the body: it
// stays out of the prompt cache key, and a proxy can serve an easy child from a
// cheaper model without parsing JSON.
//
// The two headers answer different questions — Task names the kind of work,
// Complexity says how much of it there is — so they are kept apart and a router
// can combine them.
//
// The spellings are an agreement with the router, so they match its vocabulary
// exactly.
type Complexity string

const (
	// ComplexityEasy is work a smaller, faster model handles: a lookup, a
	// one-file rename, a question answered from what is already in context.
	ComplexityEasy Complexity = "easy"
	// ComplexityMedium is ordinary engineering work: a feature across a couple
	// of files, a bug in a known area, a small refactor.
	ComplexityMedium Complexity = "medium"
	// ComplexityHard is work that needs the strongest model: a behaviour that
	// spans packages, a design decision, a subtle failure with no reproduction
	// yet.
	ComplexityHard Complexity = "hard"
)

// headerComplexity carries the spawner's prediction to the router. It sits
// beside the task kind (task.go) and the client assertion headers
// (appassertion.go), which the router also reads.
const headerComplexity = "X-Client-Complexity"

// Valid reports whether c is one of the three classes. The empty string is not
// valid: a caller with no prediction sends no header at all.
func (c Complexity) Valid() bool {
	switch c {
	case ComplexityEasy, ComplexityMedium, ComplexityHard:
		return true
	}
	return false
}

// setComplexityHeader stamps req with the predicted complexity of the task it
// carries. An empty complexity leaves the request untouched, so an unclassified
// child — or any caller that has no prediction — behaves exactly as before.
func setComplexityHeader(req *http.Request, c Complexity) {
	if c == "" {
		return
	}
	req.Header.Set(headerComplexity, string(c))
}
