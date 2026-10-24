package tool

import (
	"context"

	"github.com/prasenjeet-symon/ogcode/internal/filechange"
)

// observeShell records what a shell command changed among the files the gate
// knows, so a mutation made through bash is visible to the read-bookmark gate:
// the command itself cannot be gated — it is a shell, not a file write — but
// the files it leaves behind can be.
//
// Only files the gate has already seen are compared: the ones a session read or
// a tool wrote in this project. That is exactly the set a refusal can be owed
// on — a file nobody read carries no bookmark, so changing it can stale
// nothing — and it keeps a build's artifacts out of the history.
//
// The comparison is the fingerprint stored at the last read or write: size and
// modification time, not content. It can miss a rewrite that keeps both (same
// length, inside one filesystem timestamp tick), which is why the change it
// records carries no diff and the refusal tells the caller to re-read.
//
// The change is recorded unattributed, and no bookmark moves. A shell command
// is not a read: the gate never held the bytes, so it cannot say who wrote
// them — the file may have been rewritten by this command, by another session,
// or by a process the gate cannot see, and a size and an mtime do not tell the
// three apart. Crediting the session that happened to run the command would
// excuse exactly the foreign change it has to be refused by, and would name the
// wrong author to everyone else. So the observer is refused like any other
// session until it re-reads the file, which is what a stale refusal asks for.
func observeShell(ctx context.Context, tctx Context, command string) {
	log := tctx.fileLog()
	if log == nil {
		return
	}
	for _, key := range log.Files() {
		// The whole compare-and-record runs under the same lock write and edit
		// take, and the before-fingerprint is snapshotted inside it: a change
		// the gate can see — a tool write, or the command before this one —
		// then cannot land between the snapshot and the record and leave this
		// command a duplicate row for bytes it did not change. Record stamps
		// the file's new fingerprint, which is the state the next command's
		// snapshot is measured against — a deleted file included, so a later
		// re-creation is caught too — and with no Session it leaves every
		// bookmark alone.
		unlock := lockFileKey(key)
		before, ok := log.Fingerprint(key)
		if !ok {
			unlock()
			continue
		}
		if after := filechange.FingerprintOf(key); after != before {
			log.Record(ctx, key, filechange.Change{
				Agent: tctx.Agent,
				Tool:  "bash",
				// The command is the description: it is what the author was
				// doing, and it is all a refused session is told about how.
				Note: command,
				// There is no diff to show — the gate never held the bytes —
				// and saying so is better than an empty change.
				DiffOmitted: true,
			})
		}
		unlock()
	}
}
