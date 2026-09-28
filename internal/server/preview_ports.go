package server

import "github.com/prasenjeet-symon/ogcode/internal/session"

// previewPortsForProject returns the ports published for this project: the
// services its agent handed the user a live-preview URL for, plus any the user
// added on the Preview page. The announcement is durable: it is recorded as the
// agent writes it (see agent.LoopRunner.recordAnnouncedPorts, which reads the
// agent's own prose as each step ends and writes the port down), so this is a
// plain read of that record. Nothing is mined from the transcript here: a port
// that merely appears in a tool call's arguments or result, or in the reasoning
// trace, is not an announcement and is never listed.
//
// The record is keyed by the project's directory, so a different project's
// previews never leak into this grid. A nil store or an empty directory yields
// nothing.
func previewPortsForProject(store *session.Store, dir string) []int {
	if store == nil || dir == "" {
		return nil
	}
	ports, err := store.AnnouncedPorts(dir)
	if err != nil {
		return nil
	}
	return ports
}
