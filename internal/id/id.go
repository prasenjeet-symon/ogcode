package id

import (
	"math/rand"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

var (
	// random is the underlying source. It is also the fallback for the rare
	// case the monotonic reader cannot increment (see newULID).
	random = rand.New(rand.NewSource(time.Now().UnixNano()))

	// entropy makes ids minted within the same millisecond strictly increasing.
	//
	// A ULID is a 48-bit millisecond timestamp followed by 80 bits of entropy,
	// and it sorts by time only because the timestamp is the high-order half.
	// Inside one millisecond the timestamps are equal and the entropy decides
	// the order — so drawing fresh randomness each time makes that order a coin
	// flip. Measured on this generator before the fix: of 5000 ids produced in a
	// loop, 4998 consecutive pairs shared a millisecond and 50.5% of them sorted
	// backwards.
	//
	// That matters because ids are the sort key. Messages come back from the
	// store ordered by id, and an assistant turn and the tool-result written
	// straight after it are routinely sub-millisecond apart — no network round
	// trip separates them, just two inserts. Half the time the reply sorted
	// before the thing it replied to, which any code reading order as causality
	// gets wrong.
	//
	// Monotonic increments the previous entropy instead, so equal timestamps
	// break ties in creation order.
	entropy = ulid.Monotonic(random, 0)

	// mu guards entropy. Neither *rand.Rand nor *ulid.MonotonicEntropy is safe
	// for concurrent use, and the monotonic reader is stateful in a way the
	// plain source was not — it carries the previous id forward — so the lock
	// is load-bearing rather than defensive.
	mu sync.Mutex
)

type SessionID string
type MessageID string
type PartID string
type PermissionID string
type PlanID string
type TaskID string

func NewSessionID() SessionID {
	return SessionID("ses_" + newULID())
}

func NewMessageID() MessageID {
	return MessageID("msg_" + newULID())
}

func NewPartID() PartID {
	return PartID("prt_" + newULID())
}

func NewPermissionID() PermissionID {
	return PermissionID("prm_" + newULID())
}

func NewPlanID() PlanID {
	return PlanID("pln_" + newULID())
}

func NewTaskID() TaskID {
	return TaskID("tsk_" + newULID())
}

func NewNoteID() string {
	return "nte_" + newULID()
}

func NewNoteVersionID() string {
	return "ntv_" + newULID()
}

func newULID() string {
	mu.Lock()
	defer mu.Unlock()

	ms := ulid.Timestamp(time.Now())
	if u, err := ulid.New(ms, entropy); err == nil {
		return u.String()
	}

	// The only error a monotonic read returns is overflow: the increments taken
	// within this millisecond have run the entropy past 2^80. Reaching it needs
	// the starting value to have landed near the ceiling and then billions of
	// ids in the same millisecond, so it is not a case that happens — but the
	// panicking constructor was on the path of every message and part created,
	// and a crash there would be a poor way to find out otherwise.
	//
	// Falling back to a plain random read gives up ordering for this one id
	// rather than for the process.
	return ulid.MustNew(ms, random).String()
}
