package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// TaskDefinition represents a single task parsed from the breakdown agent's JSON output.
type TaskDefinition struct {
	Title        string `json:"title"`
	Description  string `json:"description"`
	Dependencies []int  `json:"dependencies"`
	Effort       string `json:"effort"`
	Complexity   string `json:"complexity"`
	OrderIndex   int    `json:"orderIndex"`
}

// BreakdownPrompt constructs the user message for the breakdown agent from the plan conversation.
// archivePaths contains filesystem paths to previously completed plan markdown files for this
// project. Only the paths are mentioned — the agent reads them via its file tools if needed.
func BreakdownPrompt(messages []*session.MessageWithParts, archivePaths []string) string {
	var b strings.Builder

	if len(archivePaths) > 0 {
		b.WriteString("Previously completed plans for this project are archived at:\n\n")
		for _, path := range archivePaths {
			b.WriteString(fmt.Sprintf("- %s\n", path))
		}
		b.WriteString("\nRead them if you need context about past decisions or what has already been implemented.\n\n")
	}

	// Extract only the final agreed plan — the last assistant message written at lock time.
	finalSummary, _ := extractFinalSummary(messages)

	if finalSummary != "" {
		b.WriteString("=== FINAL AGREED PLAN ===\n")
		b.WriteString("This is the canonical plan the user approved. Use it as the sole source for the task breakdown.\n\n")
		b.WriteString(finalSummary)
		b.WriteString("\n=== END FINAL PLAN ===\n\n")
	}

	b.WriteString("## Rules for the task breakdown\n\n")

	b.WriteString("GRANULARITY: Aim for tasks a developer can complete in one sitting (a few hours). " +
		"Do not over-split — 3 to 10 tasks is typical. Merge trivially small steps into their natural parent task.\n\n")

	b.WriteString("DESCRIPTION QUALITY: Each task description must be implementation-ready. Include:\n" +
		"- Exact file paths to create or modify\n" +
		"- Function/type/interface names to add or change\n" +
		"- Patterns and conventions to follow (reference existing code where relevant)\n" +
		"- Edge cases and error handling to consider\n" +
		"- A verification step at the end: run the project's tests if any exist, otherwise build/compile the project, to confirm there are no compile-time or syntax errors\n" +
		"A developer should be able to implement the task from the description alone without re-reading the plan.\n\n")

	b.WriteString("DEPENDENCY RULE: Each task may depend on AT MOST ONE other task. " +
		"Design strictly linear chains (A→B→C). " +
		"Fan-in graphs (A,B→C) are not allowed. " +
		"If a task logically requires work from multiple predecessors, consolidate those predecessors into a single task first.\n\n")

	b.WriteString("FILE OWNERSHIP RULE: Parallel tasks (tasks with no dependency between them) " +
		"MUST NOT edit the same files. Each file should be owned by exactly one parallel workstream. " +
		"If two tasks need to touch the same file, add a dependency between them so they run sequentially. " +
		"For each task, mentally list the files it will modify — if any file appears in two parallel tasks, " +
		"make one depend on the other. This prevents merge conflicts when the branches are combined.\n\n")

	b.WriteString("Now call the submit_task_breakdown tool with the complete task breakdown array. Do NOT output raw JSON — use the tool.")

	return b.String()
}

// extractFinalSummary separates the last assistant text message (the canonical
// final plan written at lock time) from the rest of the conversation messages.
func extractFinalSummary(messages []*session.MessageWithParts) (summary string, rest []*session.MessageWithParts) {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Info.Role != session.RoleAssistant {
			continue
		}
		for _, part := range msg.Parts {
			if part.Type != session.PartText {
				continue
			}
			var data session.TextPartData
			if err := json.Unmarshal(part.Data, &data); err == nil && strings.TrimSpace(data.Text) != "" {
				return data.Text, messages[:i]
			}
		}
	}
	return "", messages
}

// extractJSONArray finds the first '[' in s and returns the substring up to and
// including its balanced closing ']', respecting JSON string escaping. This
// avoids the over-match problem of a greedy `\[.*\]` regex, which would extend
// to the last ']' in the entire string and produce invalid JSON when the LLM
// appends trailing text containing brackets.
func extractJSONArray(s string) string {
	start := strings.IndexByte(s, '[')
	if start < 0 {
		return ""
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' && inString {
			escaped = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch c {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

// ParseTasks extracts and parses the task definitions from the breakdown agent's response text.
// It handles cases where the LLM wraps the JSON in markdown code fences or adds preamble.
func ParseTasks(text string) ([]TaskDefinition, error) {
	cleaned := strings.TrimSpace(text)

	// Remove ```json ... ``` wrapping
	if strings.HasPrefix(cleaned, "```") {
		idx := strings.Index(cleaned[3:], "\n")
		if idx >= 0 {
			cleaned = cleaned[3+idx+1:]
		}
		closingIdx := strings.LastIndex(cleaned, "```")
		if closingIdx >= 0 {
			cleaned = cleaned[:closingIdx]
		}
		cleaned = strings.TrimSpace(cleaned)
	}

	// Find the JSON array in the text
	match := extractJSONArray(cleaned)
	if match == "" {
		return nil, fmt.Errorf("no JSON array found in breakdown response")
	}

	// LLMs sometimes emit invalid JSON escape sequences (e.g. \` inside strings).
	// Strip backslashes before characters that aren't valid JSON escapes.
	match = sanitizeInvalidEscapes(match)

	var tasks []TaskDefinition
	if err := json.Unmarshal([]byte(match), &tasks); err != nil {
		return nil, fmt.Errorf("parse task definitions: %w", err)
	}

	// Validate and set defaults
	for i := range tasks {
		if tasks[i].Effort == "" {
			tasks[i].Effort = "M"
		}
		if tasks[i].Complexity == "" {
			tasks[i].Complexity = "medium"
		}
		if tasks[i].OrderIndex == 0 && i > 0 {
			tasks[i].OrderIndex = i
		}
		if tasks[i].Dependencies == nil {
			tasks[i].Dependencies = []int{}
		}
	}

	return tasks, nil
}

// sanitizeInvalidEscapes removes backslash escapes before characters that are
// not valid JSON escape sequences. LLMs sometimes emit strings like \` or \'
// inside JSON values, which Go's json.Unmarshal rejects.
func sanitizeInvalidEscapes(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			next := s[i+1]
			switch next {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't', 'u':
				b.WriteByte('\\')
				b.WriteByte(next)
			default:
				// Drop the backslash — e.g. \` becomes `
				b.WriteByte(next)
			}
			i++
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
