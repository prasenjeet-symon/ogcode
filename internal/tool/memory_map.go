package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/prasenjeet-symon/ogcode/internal/memfile"
	"github.com/prasenjeet-symon/ogcode/internal/project"
)

// memoryMapBudget caps the rendered map so a project with a long history can't
// flood the recall agent's context. Past the cap, the render degrades the same
// way codebase_map does: labels dropped, names only, plus the drilldown hint.
const memoryMapBudget = 40 * 1024

// sessionLabelCap is the maximum number of topic labels shown on a collapsed
// conversation's summary line. One more than the codebase_map folder cap: a
// conversation's turns are more topically varied than one folder's files, so
// its "what is common here" line needs slightly more spread.
const sessionLabelCap = 7

// MemoryMapTool is the index over a project's per-turn markdown memory — the
// memory analogue of codebase_map. At project scope every conversation is
// collapsed to ONE line carrying its most common topic labels and its turn
// count; calling again with subdir set to a conversation tag descends into it
// and lists that conversation's summaries as file lines — file name and topic
// labels, the way codebase_map lists a folder's files. Scope (project vs a
// single session) comes from the recall context, never from the model.
type MemoryMapTool struct {
	Store *memfile.Store
}

// NewMemoryMapTool constructs a MemoryMapTool over the project-local index.
func NewMemoryMapTool(store *memfile.Store) MemoryMapTool {
	return MemoryMapTool{Store: store}
}

func (t MemoryMapTool) ID() string { return "memory_map" }

func (t MemoryMapTool) Description() string {
	return "Return one level of a labeled map of this project's persistent memory — markdown summaries of past turns. Every conversation (session) is shown as a SINGLE line ending in \"/\": its most common topic labels and the number of turns inside it. To look inside a conversation, call again with subdir set to its tag (e.g. \"ses01M26\") — the call then lists that conversation's summaries as one line each: file name and topic labels. Use this to find which conversation holds the answer to a recall question before reading anything; file_map gives a summary's heading outline with line ranges, and read(path, start_line, end_line) pulls just that section."
}

func (t MemoryMapTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"subdir": {
				"type": "string",
				"description": "Optional conversation tag (e.g. \"ses01M26\") to descend into. The map then lists that conversation's turns individually instead of collapsing it to one line. Omit to start at the project level."
			},
			"topic": {
				"type": "string",
				"description": "Optional topic filter: keeps only summaries whose topics match this substring, case-insensitively (e.g. \"migration\")."
			}
		}
	}`)
}

func (t MemoryMapTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var params struct {
		Subdir string `json:"subdir"`
		Topic  string `json:"topic"`
	}
	if args != nil {
		_ = DecodeArgs(args, &params)
	}

	if t.Store == nil {
		return Result{Title: "Memory Map", Output: "Persistent turn memory is not available in this environment."}, nil
	}

	scope, haveScope := RecallScopeFromContext(ctx)
	projectID := scope.ProjectID
	if projectID == "" {
		projectID = project.Resolve(tctx.SessionDir)
	}

	var (
		entries []*memfile.Entry
		err     error
		label   string
	)
	if haveScope && scope.Scope == "session" && scope.SessionID != "" {
		entries, err = t.Store.ListBySession(scope.SessionID)
		label = "this conversation"
	} else {
		entries, err = t.Store.ListByProject(projectID)
		label = "this project"
	}
	if err != nil {
		return Result{Title: "Memory Map", Output: "Memory index lookup failed: " + err.Error()}, nil
	}

	total := len(entries)
	if params.Topic != "" {
		entries = filterByTopic(entries, params.Topic)
	}
	if len(entries) == 0 {
		if total > 0 {
			return Result{Title: "Memory Map", Output: fmt.Sprintf("No summaries in %s match topic %q (all %d filtered out).", label, params.Topic, total)}, nil
		}
		return Result{Title: "Memory Map", Output: "No past turns are recorded in " + label + "'s memory yet."}, nil
	}

	// Session-scoped recall stays flat: one conversation's turns, each with its
	// outline, is the right table of contents there — there is nothing to
	// collapse. The subdir drilldown is a project-map affordance; honoring it
	// under session scope too costs nothing and reads the same.
	sessionScoped := haveScope && scope.Scope == "session" && scope.SessionID != ""
	flat := sessionScoped // one conversation's turns — there is nothing to collapse
	if sessionScoped {
		if params.Subdir != "" && params.Subdir != memfile.SessionTag(scope.SessionID) {
			// Session scope pins the conversation; a subdir of something else is
			// out of scope.
			return Result{Title: "Memory Map", Output: fmt.Sprintf("This recall is scoped to conversation %q; subdir %q is not part of it.", memfile.SessionTag(scope.SessionID), params.Subdir)}, nil
		}
	}
	if params.Subdir != "" && !sessionScoped {
		// Project scope + subdir: keep only the named conversation's entries.
		scoped := make([]*memfile.Entry, 0, len(entries))
		for _, e := range entries {
			if memfile.SessionTag(e.SessionID) == params.Subdir {
				scoped = append(scoped, e)
			}
		}
		entries = scoped
		if len(entries) == 0 {
			return Result{Title: "Memory Map", Output: fmt.Sprintf("No conversation with tag %q in %s's memory. The tags are the prefixes ending in \"/\" above; call without subdir to list them.", params.Subdir, label)}, nil
		}
	}
	flat = flat || params.Subdir != ""

	return Result{Title: "Memory Map", Output: renderMemoryMap(entries, label, flat, params.Subdir)}, nil
}

// renderSummaryLine renders one summary the way codebase_map renders a file:
// name and topic labels on one line. The filename already carries the rest —
// the UTC timestamp leads it and the session tag and title slug follow — and
// the heading outline is file_map's job, not the map's.
func renderSummaryLine(e *memfile.Entry) string {
	name := filepath.Base(e.Path)
	if len(e.Labels) == 0 {
		return name + "\n"
	}
	return name + "  " + strings.Join(e.Labels, ", ") + "\n"
}

// turnSummary is one collapsed conversation: the grouping tag, its entries, and
// the topic-label frequencies across them.
type turnSummary struct {
	tag     string
	entries []*memfile.Entry
	labels  map[string]int
}

// summarizeSessions groups entries by their session tag — the token Filename
// embeds in every summary of one conversation — and counts label frequencies
// the way codebase_map's dirStatsOf does: once per turn, not once per
// occurrence. Groups come back sorted by tag.
func summarizeSessions(entries []*memfile.Entry) []turnSummary {
	byTag := make(map[string][]*memfile.Entry)
	order := make([]string, 0, len(byTag))
	for _, e := range entries {
		tag := memfile.SessionTag(e.SessionID)
		if _, seen := byTag[tag]; !seen {
			order = append(order, tag)
		}
		byTag[tag] = append(byTag[tag], e)
	}
	sort.Strings(order)

	summaries := make([]turnSummary, 0, len(order))
	for _, tag := range order {
		s := turnSummary{tag: tag, entries: byTag[tag], labels: make(map[string]int)}
		for _, e := range s.entries {
			seen := make(map[string]struct{}, len(e.Labels))
			for _, l := range e.Labels {
				if _, dup := seen[l]; dup {
					continue
				}
				seen[l] = struct{}{}
				s.labels[l]++
			}
		}
		summaries = append(summaries, s)
	}
	return summaries
}

// renderMemoryMap renders one level of the memory map. Flat renders each entry
// as one file line — name plus labels (a conversation's turns, after a subdir
// drilldown or under session scope); otherwise every conversation is one line:
// tag/  top topics  (N turns) — the codebase_map folder-line shape.
func renderMemoryMap(entries []*memfile.Entry, label string, flat bool, subdir string) string {
	var b strings.Builder
	turnNoun := func(n int) string {
		if n == 1 {
			return "1 turn"
		}
		return fmt.Sprintf("%d turns", n)
	}

	if flat {
		fmt.Fprintf(&b, "%s in %s, newest first.\n", turnNoun(len(entries)), scopeName(subdir))
		fmt.Fprintf(&b, "Summaries live in %s. Each line below is one summary file and its topics. Call file_map on a summary for its heading outline with line ranges, then read(path, start_line=N, end_line=M) for just that section.\n\n", memoryDirOf(entries))
		renderFlat(entries, &b)
	} else {
		fmt.Fprintf(&b, "%s in %s.\n", turnNoun(len(entries)), label)
		b.WriteString("Conversations end in \"/\" and are shown as ONE line each: the conversation's most common topics and the number of turns inside it. To see a conversation's turns, call again with subdir set to its tag (e.g. subdir=\"ses01M26\").\n\n")
		renderSessions(summarizeSessions(entries), &b, true, turnNoun)
	}

	if b.Len() <= memoryMapBudget {
		return strings.TrimRight(b.String(), "\n") + "\n"
	}

	// Over budget: drop the labels wholesale and keep the structure — the same
	// degraded render codebase_map falls back to. The drilldown hint goes on the
	// flat path too, where the entries themselves were what overflowed.
	b.Reset()
	if flat {
		fmt.Fprintf(&b, "%s in %s, newest first.\n", turnNoun(len(entries)), scopeName(subdir))
		b.WriteString("This level is too large to show topics, so only file names are listed.\n")
		fmt.Fprintf(&b, "Call memory_map again with topic set to a keyword to narrow it.\n\n")
		renderFlat(entries, &b)
	} else {
		fmt.Fprintf(&b, "%s in %s.\n", turnNoun(len(entries)), label)
		b.WriteString("Conversations end in \"/\". This level is too wide to show topics, so only the names are listed.\n")
		fmt.Fprintf(&b, "Call memory_map again with topic set to a keyword, or subdir set to a conversation tag to get topics for its turns.\n\n")
		renderSessions(summarizeSessions(entries), &b, false, turnNoun)
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// scopeName names the flat level for its header line.
func scopeName(subdir string) string {
	if subdir == "" {
		return "this conversation"
	}
	return "conversation " + subdir
}

// memoryDirOf reports the folder the listed summaries live in — one project's
// .ogcode/memory — so the flat level can show bare file names and the model
// still has everything read() and file_map() need.
func memoryDirOf(entries []*memfile.Entry) string {
	if len(entries) == 0 {
		return ""
	}
	return filepath.Dir(entries[0].Path)
}

// renderFlat writes one line per summary — file name plus topic labels,
// oldest-last (entries arrive newest first).
func renderFlat(entries []*memfile.Entry, b *strings.Builder) {
	for _, e := range entries {
		b.WriteString(renderSummaryLine(e))
	}
}

// renderSessions writes one line per conversation: its tag, its most common
// topics, and its turn count — the codebase_map folder line. withLabels false
// degrades to names and counts only.
func renderSessions(summaries []turnSummary, b *strings.Builder, withLabels bool, turnNoun func(int) string) {
	for _, s := range summaries {
		summary := "(" + turnNoun(len(s.entries)) + ")"
		if withLabels && len(s.labels) > 0 {
			if top := topLabels(s.labels, sessionLabelCap); len(top) > 0 {
				summary = strings.Join(top, ", ") + "  " + summary
			}
		}
		fmt.Fprintf(b, "%s/  %s\n", s.tag, summary)
	}
}

// filterByTopic keeps entries carrying a label that contains topic
// case-insensitively. Go-side, not SQL: the labels live in one JSON column and
// the recall sets are small.
func filterByTopic(entries []*memfile.Entry, topic string) []*memfile.Entry {
	needle := strings.ToLower(topic)
	kept := make([]*memfile.Entry, 0, len(entries))
	for _, e := range entries {
		for _, l := range e.Labels {
			if strings.Contains(strings.ToLower(l), needle) {
				kept = append(kept, e)
				break
			}
		}
	}
	return kept
}
