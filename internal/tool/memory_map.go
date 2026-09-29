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

// minTagLen is the shortest tag memory_map shows for a conversation. Tags grow
// past it only as far as they must to stay unique among the conversations
// listed (see conversationTags), so an 8-character tag from before tags were
// made unique still resolves whenever it names one conversation.
const minTagLen = 8

// sessionLabelCap is the maximum number of topic labels shown on a collapsed
// conversation's summary line.
//
// Equal to codebase_map's folderLabelCap, and for the same reason: the labels
// are a sample drawn evenly from every turn (see sampleLabels), so twenty is
// enough to say what a conversation covered. It was forty "most common"
// labels — but a turn's labels are nearly all its own, so the counts tied at
// one and the line was really an alphabetical slice of the conversation. The
// byte budget still bounds the level: on a wide one, renderMemoryMap lowers
// this rung by rung.
const sessionLabelCap = folderLabelCap

// MemoryMapTool is the index over a project's per-turn markdown memory — the
// memory analogue of codebase_map. At project scope every conversation is
// collapsed to ONE line carrying its turn count and a sample of topic labels
// drawn from across its turns; calling again with subdir set to a conversation tag descends into it
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
	return "Return one level of a labeled map of this project's persistent memory — markdown summaries of past turns. Every conversation (session) is shown as a SINGLE line: its tag ending in \"/\", the number of turns inside it, and a sample of topic labels drawn from across all its turns. Labels are separated by semicolons. Each tag names exactly one conversation. To look inside a conversation, call again with subdir set to its tag (e.g. \"ses01M269J\") — the call then lists that conversation's summaries as one line each: file name and topic labels. Use this to find which conversation holds the answer to a recall question before reading anything; file_map gives a summary's heading outline with line ranges, and read(path, start_line, end_line) pulls just that section."
}

func (t MemoryMapTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"subdir": {
				"type": "string",
				"description": "Optional conversation tag (e.g. \"ses01M269J\"), exactly as the map shows it, to descend into. Any longer prefix of the conversation's session id works too. The map then lists that conversation's turns individually instead of collapsing it to one line. Omit to start at the project level."
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
	shownTag := params.Subdir
	if sessionScoped && params.Subdir != "" {
		// Session scope pins the conversation; a subdir naming anything else is
		// out of scope. Any prefix of this conversation's key names it.
		own := memfile.SessionTag(scope.SessionID)
		if !strings.HasPrefix(memfile.SessionKey(scope.SessionID), memfile.SessionKey(params.Subdir)) {
			return Result{Title: "Memory Map", Output: fmt.Sprintf("This recall is scoped to conversation %q; subdir %q is not part of it.", own, params.Subdir)}, nil
		}
		shownTag = own
	}
	if params.Subdir != "" && !sessionScoped {
		// Project scope + subdir: keep only the named conversation's entries.
		// The tag is a prefix of one conversation's key; a prefix that fits
		// several — a short tag from before tags were made unique — is refused
		// with the tags that tell them apart, never answered with a blend.
		tags := conversationTags(entries)
		matched := resolveConversation(params.Subdir, entries)
		switch len(matched) {
		case 0:
			return Result{Title: "Memory Map", Output: fmt.Sprintf("No conversation with tag %q in %s's memory. The tags are the prefixes ending in \"/\" above; call without subdir to list them.", params.Subdir, label)}, nil
		case 1:
		default:
			var b strings.Builder
			fmt.Fprintf(&b, "Tag %q fits %d conversations in %s's memory. Call again with one of their tags:\n", params.Subdir, len(matched), label)
			for _, key := range matched {
				n := 0
				for _, e := range entries {
					if memfile.SessionKey(e.SessionID) == key {
						n++
					}
				}
				fmt.Fprintf(&b, "%s/ (%s)\n", tags[key], turnCount(n))
			}
			return Result{Title: "Memory Map", Output: b.String()}, nil
		}
		scoped := make([]*memfile.Entry, 0, len(entries))
		for _, e := range entries {
			if memfile.SessionKey(e.SessionID) == matched[0] {
				scoped = append(scoped, e)
			}
		}
		entries = scoped
		shownTag = tags[matched[0]]
	}
	flat = flat || params.Subdir != ""

	return Result{
		Title:  "Memory Map",
		Output: renderMemoryMap(entries, label, flat, shownTag),
		// Rendered to memoryMapBudget, which sits above the generic 50 KB cap,
		// so opt out of the loop's backstop: it would otherwise head-truncate
		// the map mid-conversation. The budget is what bounds this result.
		Truncated: true,
	}, nil
}

// renderSummaryLine renders one summary the way codebase_map renders a file:
// name, then its topic labels after "—", semicolon-separated. The filename
// already carries the rest — the UTC timestamp leads it and the session tag
// and title slug follow — and the heading outline is file_map's job, not the
// map's.
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
	if joined := joinLabels(labels); joined != "" {
		return name + " — " + joined + "\n"
	}
	return name + "\n"
}

// turnSummary is one collapsed conversation: the grouping tag and its entries.
type turnSummary struct {
	tag     string
	entries []*memfile.Entry
}

// labels samples the conversation's topics for its line: every turn offers its
// own labels, most important first, and sampleLabels shares the line among
// them — evenly, since every turn counts the same, and spread across the
// conversation rather than read off one end of it. The newest turn leads: it
// is where the conversation ended up, and the oldest is often just a greeting.
func (s turnSummary) labels(n int) []string {
	turns := append([]*memfile.Entry(nil), s.entries...)
	sort.SliceStable(turns, func(i, j int) bool {
		if turns[i].CreatedAt != turns[j].CreatedAt {
			return turns[i].CreatedAt > turns[j].CreatedAt
		}
		return turns[i].Path < turns[j].Path
	})
	sources := make([]labelSource, 0, len(turns))
	for i, e := range turns {
		sources = append(sources, labelSource{name: filepath.Base(e.Path), order: i, size: 1, labels: e.Labels})
	}
	return sampleLabels(sources, n)
}

// summarizeSessions groups entries by conversation — by the full session key,
// never by a shortened tag, which conversations begun close together can share
// — and names each group by its tag from conversationTags. Groups come back
// sorted by tag, which for ogcode's time-ordered session ids is oldest first.
func summarizeSessions(entries []*memfile.Entry) []turnSummary {
	tags := conversationTags(entries)
	byKey := make(map[string][]*memfile.Entry)
	for _, e := range entries {
		key := memfile.SessionKey(e.SessionID)
		byKey[key] = append(byKey[key], e)
	}
	summaries := make([]turnSummary, 0, len(byKey))
	for key, group := range byKey {
		summaries = append(summaries, turnSummary{tag: tags[key], entries: group})
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].tag < summaries[j].tag })
	return summaries
}

// conversationTags gives every conversation among entries the shortest tag
// that no other conversation's key starts with — a prefix of its own session
// key, never under minTagLen, the way git shortens a commit hash. Keyed by
// session key.
//
// A fixed-length tag cannot do this: ogcode's session ids lead with their
// creation time, so a short fixed prefix is the same for every conversation
// begun in the same couple of hours, and memory_map used to fold them all into
// one line and one drilldown. A unique prefix is as short as the listing
// allows and grows only where two conversations are close.
func conversationTags(entries []*memfile.Entry) map[string]string {
	seen := make(map[string]struct{}, len(entries))
	keys := make([]string, 0, len(entries))
	for _, e := range entries {
		key := memfile.SessionKey(e.SessionID)
		if _, dup := seen[key]; !dup {
			seen[key] = struct{}{}
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	commonPrefix := func(a, b string) int {
		n := 0
		for n < len(a) && n < len(b) && a[n] == b[n] {
			n++
		}
		return n
	}
	tags := make(map[string]string, len(keys))
	for i, key := range keys {
		// In sorted order a key shares its longest prefix with a neighbour, so
		// one character past both neighbours' shared prefixes tells it from
		// every other key.
		need := minTagLen
		if i > 0 {
			need = max(need, commonPrefix(key, keys[i-1])+1)
		}
		if i+1 < len(keys) {
			need = max(need, commonPrefix(key, keys[i+1])+1)
		}
		tags[key] = key[:min(need, len(key))]
	}
	return tags
}

// resolveConversation returns the session keys among entries that a tag names:
// every key it is a prefix of, or just the one it equals exactly when there is
// one. The tag is reduced the way a key is, so "ses_01M269J" and "ses01M269J"
// name the same conversation.
func resolveConversation(tag string, entries []*memfile.Entry) []string {
	want := memfile.SessionKey(tag)
	seen := make(map[string]struct{})
	var matched []string
	for _, e := range entries {
		key := memfile.SessionKey(e.SessionID)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		if key == want {
			return []string{key}
		}
		if strings.HasPrefix(key, want) {
			matched = append(matched, key)
		}
	}
	sort.Strings(matched)
	return matched
}

// turnCount is a turn tally with the right noun.
func turnCount(n int) string {
	if n == 1 {
		return "1 turn"
	}
	return fmt.Sprintf("%d turns", n)
}

// renderMemoryMap renders one level of the memory map. Flat renders each entry
// as one file line — name plus labels (a conversation's turns, after a subdir
// drilldown or under session scope); otherwise every conversation is one line:
// tag/ (N turns) — sampled topics — the codebase_map folder-line shape.
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
	turnNoun := turnCount
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
			fmt.Fprintf(&b, "Summaries live in %s. Each line below is one summary file, then its topics after \"—\", separated by \";\". Call file_map on a summary for its heading outline with line ranges, then read(path, start_line=N, end_line=M) for just that section.\n\n", memoryDirOf(entries))
			renderFlat(entries, &b, rung.fileCap)
		} else {
			fmt.Fprintf(&b, "%s in %s.\n", turnNoun(len(entries)), label)
			fmt.Fprintf(&b, "Each conversation is ONE line: its tag ending in \"/\"; in parentheses, how many turns it holds; then, after \"—\", a sample of topics drawn from across all its turns, separated by \";\". To see a conversation's turns, call again with subdir set to its tag (e.g. subdir=%q).\n\n", exampleTag(summaries))
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
		b.WriteString("Conversations end in \"/\", with their turn count in parentheses. This level is too wide to show topics, so only the names are listed.\n")
		fmt.Fprintf(&b, "Call memory_map again with subdir set to a conversation tag (e.g. subdir=%q) to get topics for its turns.\n\n", exampleTag(summaries))
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

// renderSessions writes one line per conversation — its tag, its turn count,
// then a sample of its topics — the codebase_map folder line:
//
//	ses01M26/ (64 turns) — OGX Onboarding Screens; Country Whitelist; …
//
// labelCap bounds the topics shown; renderMemoryMap lowers it rung by rung
// when the level will not fit the budget, and 0 degrades to names and counts
// only.
func renderSessions(summaries []turnSummary, b *strings.Builder, labelCap int, turnNoun func(int) string) {
	for _, s := range summaries {
		line := fmt.Sprintf("%s/ (%s)", s.tag, turnNoun(len(s.entries)))
		if labels := joinLabels(s.labels(labelCap)); labels != "" {
			line += " — " + labels
		}
		b.WriteString(line + "\n")
	}
}

// exampleTag is a real conversation tag for the header's next-step example:
// the conversation with the most turns, the likeliest one to open.
func exampleTag(summaries []turnSummary) string {
	best, most := "ses01M269J", -1
	for _, s := range summaries {
		if n := len(s.entries); n > most || (n == most && s.tag < best) {
			best, most = s.tag, n
		}
	}
	return best
}
