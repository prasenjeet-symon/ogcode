package tool

import (
	"strings"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
)

// workloadNotice is what a spawn answers when the workload was not one of the
// two the router understands.
const workloadNotice = "workload must be read-heavy or write-heavy — omit it to spawn without a classification"

// parseWorkload reads the workload argument of a spawning call. Omission is
// fine — the child then carries no workload header — but a workload the router
// would not understand is bounced: a misspelled one would silently route the
// child as if it had never been classified, which is worse than no prediction.
func parseWorkload(raw string) (provider.Workload, bool) {
	w := provider.Workload(strings.TrimSpace(raw))
	if w == "" {
		return "", true
	}
	return w, w.Valid()
}
