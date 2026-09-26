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

// memoryMapBudget is the byte budget for a rendered memory map.
//
// Independent of MaxToolOutputBytes, the generic 50 KB cap on any tool result:
// the map opts out of the loop's backstop (Execute marks its result Truncated),
// so this budget is the only thing that bounds a map and may sit above that
// generic cap without a large map being head-truncated mid-conversation — a
// level that simply stops, with nothing telling the model what was lost. The
// map degrades on its own terms instead: on a level too wide for every entry's
// full label set, labels are shed rung by rung (see renderMemoryMap) until the
// level fits, and only a level that will not fit at one label per entry falls
// back to the label-less outline with the drilldown hint.
//
// Set well above what a project-sized history costs (a few KB with conversations
// collapsed), so ordinary use never degrades. It exists for the pathological
// level — many conversations, each near the label ceiling — which would
// otherwise run to hundreds of KB.
const memoryMapBudget = 100 * 1024

// sessionLabelCap is the maximum number of topic labels shown on a collapsed
// conversation's summary line.
//
// Equal to codebase_map's folderLabelCap: a conversation line stands for a
// whole branch of turns exactly as a folder line stands for a branch of
// directories, so the two carry the same label budget. Referenced rather than
// restated so the two cannot drift apart. The byte budget still bounds the
// level: on a wide one, renderMemoryMap lowers this rung by rung.
const sessionLabelCap = folderLabelCap

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
			}
		}
	}`)
}

func (t MemoryMapTool) Execute(ctx context.Context, args json.RawMessage, tctx Context) (Result, error) {
	var params struct {
		Subdir string `json:"subdir"`
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

	if len(entries) == 0 {
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

	return Result{
		Title:  "Memory Map",
		Output: renderMemoryMap(entries, label, flat, params.Subdir),
		// Rendered to memoryMapBudget, which sits above the generic 50 KB cap,
		// so opt out of the loop's backstop: it would otherwise head-truncate
		// the map mid-conversation. The budget is what bounds this result.
		Truncated: true,
	}, nil
}

// renderSummaryLine renders one summary the way codebase_map renders a file:
// name and topic labels on one line. The filename already carries the rest —
// the UTC timestamp leads it and the session tag and title slug follow — and
// the heading outline is file_map's job, not the map's.
//
// labelCap bounds the labels shown, exactly as renderProjectLevel does for a
// loose file; renderMemoryMap lowers it rung by rung when a level will not fit
// the budget, and 0 lists the name alone.
func renderSummaryLine(e *memfile.Entry, labelCap int) string {
	name := filepath.Base(e.Path)
	labels := e.Labels
	if len(labels) > labelCap {
		labels = labels[:labelCap]
	}
	if len(labels) == 0 {
		return name + "\n"
	}
	return name + "  " + strings.Join(labels, ", ") + "\n"
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
//
// Like codebase_map's renderProjectMap, it degrades gracefully if the result
// would not fit the budget: collapsing every conversation bounds the output by
// how wide one level is, so a tripped budget means a level with many entries —
// many summaries, or many conversations. The response is to render again at a
// progressively shallower label cap, on both the flat file lines and the
// conversation lines, so a wide level shows fewer labels rather than losing
// them wholesale. Dropping labels entirely is the last resort, for a level
// where even one label per entry will not fit.
func renderMemoryMap(entries []*memfile.Entry, label string, flat bool, subdir string) string {
	turnNoun := func(n int) string {
		if n == 1 {
			return "1 turn"
		}
		return fmt.Sprintf("%d turns", n)
	}
	// Grouped once, and only on the collapsed path: a flat render lists the
	// entries themselves and never collapses a conversation.
	var summaries []turnSummary
	if !flat {
		summaries = summarizeSessions(entries)
	}

	// Full depth first, then progressively shallower — both a flat file line's
	// labels and a conversation line's, since either can be the bulk of a wide
	// level. The floor rung is 1 label each; past it the label-less outline
	// below takes over.
	for _, rung := range []struct{ fileCap, sessionCap int }{
		{textLabelCap, sessionLabelCap},
		{10, 10},
		{3, 3},
		{1, 1},
	} {
		var b strings.Builder
		if flat {
			fmt.Fprintf(&b, "%s in %s, newest first.\n", turnNoun(len(entries)), scopeName(subdir))
			fmt.Fprintf(&b, "Summaries live in %s. Each line below is one summary file and its topics. Call file_map on a summary for its heading outline with line ranges, then read(path, start_line=N, end_line=M) for just that section.\n\n", memoryDirOf(entries))
			renderFlat(entries, &b, rung.fileCap)
		} else {
			fmt.Fprintf(&b, "%s in %s.\n", turnNoun(len(entries)), label)
			b.WriteString("Conversations end in \"/\" and are shown as ONE line each: the conversation's most common topics and the number of turns inside it. To see a conversation's turns, call again with subdir set to its tag (e.g. subdir=\"ses01M26\").\n\n")
			renderSessions(summaries, &b, rung.sessionCap, turnNoun)
		}
		if b.Len() <= memoryMapBudget {
			return strings.TrimRight(b.String(), "\n") + "\n"
		}
	}

	// Past every rung the labels go, not the structure — the last resort
	// codebase_map falls back to. The drilldown hint goes on the flat path too,
	// where the entries themselves were what overflowed.
	var b strings.Builder
	if flat {
		fmt.Fprintf(&b, "%s in %s, newest first.\n", turnNoun(len(entries)), scopeName(subdir))
		b.WriteString("This level is too large to show topics, so only file names are listed.\n")
		b.WriteString("Call file_map on a summary for its heading outline, then read just the section you need.\n\n")
		renderFlat(entries, &b, 0)
	} else {
		fmt.Fprintf(&b, "%s in %s.\n", turnNoun(len(entries)), label)
		b.WriteString("Conversations end in \"/\". This level is too wide to show topics, so only the names are listed.\n")
		fmt.Fprintf(&b, "Call memory_map again with subdir set to a conversation tag to get topics for its turns.\n\n")
		renderSessions(summaries, &b, 0, turnNoun)
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
// oldest-last (entries arrive newest first). labelCap bounds the labels on each
// line; renderMemoryMap lowers it rung by rung when the level will not fit the
// budget.
func renderFlat(entries []*memfile.Entry, b *strings.Builder, labelCap int) {
	for _, e := range entries {
		b.WriteString(renderSummaryLine(e, labelCap))
	}
}

// renderSessions writes one line per conversation: its tag, its most common
// topics, and its turn count — the codebase_map folder line. labelCap bounds
// the topics shown; renderMemoryMap lowers it rung by rung when the level will
// not fit the budget, and 0 degrades to names and counts only.
func renderSessions(summaries []turnSummary, b *strings.Builder, labelCap int, turnNoun func(int) string) {
	for _, s := range summaries {
		summary := "(" + turnNoun(len(s.entries)) + ")"
		if labelCap > 0 && len(s.labels) > 0 {
			if top := topLabels(s.labels, nil, labelCap); len(top) > 0 {
				summary = strings.Join(top, ", ") + "  " + summary
			}
		}
		fmt.Fprintf(b, "%s/  %s\n", s.tag, summary)
	}
}
