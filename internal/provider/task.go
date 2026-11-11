package provider

import "net/http"

// Task names the kind of work a model request is for, so a router in front of
// the provider can send each call to a model suited to that kind — an indexing
// pass and a user's turn are not the same job. The value travels as the
// X-Client-Task header rather than in the body: it stays out of the prompt
// cache key, and a proxy can act on it without parsing JSON.
//
// The spellings are an agreement with the router, so they match its vocabulary
// exactly.
type Task string

const (
	// TaskTurn is a user-facing agent turn — the build agent, and any agent
	// whose name has no more specific kind.
	TaskTurn Task = "turn"
	// TaskSubagent is a delegated teammate investigation.
	TaskSubagent Task = "subagent"
	// TaskIndex is a keyword-indexing pass over the project.
	TaskIndex Task = "index"
	// TaskNote is the note agent, and the note text transforms the server asks
	// for directly.
	TaskNote Task = "note"
	// TaskSearch is web search ranking and synthesis.
	TaskSearch Task = "search"
	// TaskRecall is the memory-recall agent answering a question about past
	// work.
	TaskRecall Task = "recall"
	// TaskIntent is intent matching — choosing the skills, MCP servers and
	// tools that fit the user's message.
	TaskIntent Task = "intent"
	// TaskMemory is the end-of-turn memory summary written into the project's
	// memory file.
	TaskMemory Task = "memory"
	// TaskCompaction is the summary a conversation is compacted into.
	TaskCompaction Task = "compaction"
	// TaskTitle is a generated conversation title.
	TaskTitle Task = "title"
	// TaskRisk is the auto-mode command risk gate.
	TaskRisk Task = "risk"
	// TaskRewrite is rewriting a short follow-up into a self-contained query.
	TaskRewrite Task = "rewrite"
	// TaskProbe is the one-off image-support probe.
	TaskProbe Task = "probe"
	// TaskValidate is the credentials check behind the settings "test key"
	// flow.
	TaskValidate Task = "validate"
)

// headerTask carries the task kind to the router. It sits beside the client
// assertion headers (appassertion.go), which the router also reads.
const headerTask = "X-Client-Task"

// setTaskHeader stamps req with the kind of work it carries. An empty task
// leaves the request untouched, so a caller that never sets one behaves exactly
// as before.
func setTaskHeader(req *http.Request, task Task) {
	if task == "" {
		return
	}
	req.Header.Set(headerTask, string(task))
}
