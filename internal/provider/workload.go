package provider

import "net/http"

// Workload is the spawning agent's prediction of what a delegated task mostly
// does, stated at the moment it hands the work over. It travels as the
// X-Client-Workload header beside X-Client-Task rather than in the body: it
// stays out of the prompt cache key, and a proxy can route a read-heavy child to
// a cheaper model without parsing JSON.
//
// The two headers answer different questions — Task names the kind of work,
// Workload says which side of it the child spends its time on — so they are
// kept apart and a router can combine them with Complexity.
//
// The spellings are an agreement with the router, so they match its vocabulary
// exactly.
type Workload string

const (
	// WorkloadReadHeavy is work whose child mostly reads: searches the tree,
	// opens files, pulls context in, and answers from it.
	WorkloadReadHeavy Workload = "read-heavy"
	// WorkloadWriteHeavy is work whose child mostly edits and creates files.
	WorkloadWriteHeavy Workload = "write-heavy"
)

// headerWorkload carries the spawner's prediction to the router. It sits beside
// the task kind (task.go) and the complexity class (complexity.go), which the
// router also reads.
const headerWorkload = "X-Client-Workload"

// Valid reports whether w is one of the two workloads. The empty string is not
// valid: a caller with no prediction sends no header at all.
func (w Workload) Valid() bool {
	switch w {
	case WorkloadReadHeavy, WorkloadWriteHeavy:
		return true
	}
	return false
}

// setWorkloadHeader stamps req with the predicted workload of the task it
// carries. An empty workload leaves the request untouched, so an unclassified
// child — or any caller that has no prediction — behaves exactly as before.
func setWorkloadHeader(req *http.Request, w Workload) {
	if w == "" {
		return
	}
	req.Header.Set(headerWorkload, string(w))
}
