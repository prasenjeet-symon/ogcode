package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

func TestBuildSystemPrompt_MemoryMDSection_PresentRegardlessOfContent(t *testing.T) {
	agent := BuildAgent
	dir := "/tmp/test"

	// Case 1: No MEMORY.md content — section should still appear
	prompt := buildSystemPrompt(agent, dir, false, "", "", 0, 0)
	if !strings.Contains(prompt, "## MEMORY.md — Project Long-Term Memory") {
		t.Error("expected MEMORY.md section to appear even when memoryMDContent is empty")
	}
	if !strings.Contains(prompt, "No MEMORY.md file was found") {
		t.Error("expected 'No MEMORY.md file was found' message when memoryMDContent is empty")
	}

	// Case 2: With MEMORY.md content — section should appear with file content indicator
	memContent := "\n\n<memory-md path=\"MEMORY.md\">\n# Project Notes\nSome facts.\n</memory-md>"
	prompt = buildSystemPrompt(agent, dir, false, "", memContent, 0, 0)
	if !strings.Contains(prompt, "## MEMORY.md — Project Long-Term Memory") {
		t.Error("expected MEMORY.md section to appear when memoryMDContent is present")
	}
	if !strings.Contains(prompt, "The content above in the <memory-md> tag") {
		t.Error("expected content indicator when memoryMDContent is present")
	}
	if !strings.Contains(prompt, memContent) {
		t.Error("expected memoryMDContent to be included in prompt")
	}
	if strings.Contains(prompt, "No MEMORY.md file was found") {
		t.Error("did not expect 'No MEMORY.md file was found' when memoryMDContent is present")
	}
}

func TestBuildSystemPrompt_MemoryMDSection_ContainsPurposeSection(t *testing.T) {
	agent := BuildAgent
	dir := "/tmp/test"

	prompt := buildSystemPrompt(agent, dir, false, "", "", 0, 0)

	// Verify key sections are always present
	for _, sub := range []string{
		"### Purpose",
		"### What belongs in MEMORY.md",
		"### What does NOT belong in MEMORY.md",
		"### How it differs from AGENT.md and agentic memory",
		"### How to maintain MEMORY.md",
	} {
		if !strings.Contains(prompt, sub) {
			t.Errorf("expected section %q in prompt when no MEMORY.md exists", sub)
		}
	}
}

func TestBuildSystemPrompt_MemoryMDSection_RoleAware(t *testing.T) {
	dir := "/tmp/test"

	// BuildAgent has write and edit tools — should get read/write instructions
	buildPrompt := buildSystemPrompt(BuildAgent, dir, false, "", "", 0, 0)
	if !strings.Contains(buildPrompt, "### How to maintain MEMORY.md") {
		t.Error("expected 'How to maintain' heading for BuildAgent (has write tools)")
	}
	if !strings.Contains(buildPrompt, "Use the edit tool for targeted updates") {
		t.Error("expected 'Use the edit tool' instruction for BuildAgent")
	}
	if !strings.Contains(buildPrompt, "create one in the project root directory") {
		t.Error("expected creation prompt when memoryMDContent is empty and agent can write")
	}

	// PlanAgent has no write/edit tools — should get read-only instructions
	planPrompt := buildSystemPrompt(PlanAgent, dir, false, "", "", 0, 0)
	if !strings.Contains(planPrompt, "### How to use MEMORY.md") {
		t.Error("expected 'How to use' heading for PlanAgent (read-only)")
	}
	if strings.Contains(planPrompt, "Use the edit tool") {
		t.Error("did not expect 'Use the edit tool' for read-only PlanAgent")
	}
	if strings.Contains(planPrompt, "create one in the project root directory") {
		t.Error("did not expect creation prompt for read-only PlanAgent")
	}

	// NoteAgent has no write/edit tools — should get read-only instructions
	notePrompt := buildSystemPrompt(NoteAgent, dir, false, "", "", 0, 0)
	if !strings.Contains(notePrompt, "### How to use MEMORY.md") {
		t.Error("expected 'How to use' heading for NoteAgent (read-only)")
	}
	if strings.Contains(notePrompt, "Use the write tool") {
		t.Error("did not expect 'Use the write tool' for read-only NoteAgent")
	}
}

func TestBuildSystemPrompt_MemoryMDSection_WithContent(t *testing.T) {
	dir := "/tmp/test"
	memContent := "\n\n<memory-md path=\"MEMORY.md\">\n# Project Notes\nSome facts.\n</memory-md>"

	// BuildAgent with MEMORY.md content — should show content but NOT creation prompt
	buildPrompt := buildSystemPrompt(BuildAgent, dir, false, "", memContent, 0, 0)
	if !strings.Contains(buildPrompt, "The content above in the <memory-md> tag") {
		t.Error("expected content indicator when memoryMDContent is present for BuildAgent")
	}
	if strings.Contains(buildPrompt, "No MEMORY.md file was found") {
		t.Error("did not expect 'No MEMORY.md file was found' when memoryMDContent is present for BuildAgent")
	}
	if strings.Contains(buildPrompt, "create one in the project root directory") {
		t.Error("did not expect creation prompt when memoryMDContent is present for BuildAgent")
	}

	// PlanAgent with MEMORY.md content — should show read-only version, no creation prompt
	planPrompt := buildSystemPrompt(PlanAgent, dir, false, "", memContent, 0, 0)
	if !strings.Contains(planPrompt, "The content above in the <memory-md> tag") {
		t.Error("expected content indicator when memoryMDContent is present for PlanAgent")
	}
	if strings.Contains(planPrompt, "create one in the project root directory") {
		t.Error("did not expect creation prompt for read-only PlanAgent even with content")
	}
}

func TestBuildSystemPrompt_ViewportPrompt(t *testing.T) {
	dir := "/tmp/test"

	// Without viewport dimensions — should NOT contain viewport section
	prompt := buildSystemPrompt(BuildAgent, dir, false, "", "", 0, 0)
	if strings.Contains(prompt, "Rendering viewport") {
		t.Error("did not expect viewport section when dimensions are 0x0")
	}

	// With viewport dimensions — should contain viewport section
	prompt = buildSystemPrompt(BuildAgent, dir, false, "", "", 1920, 1080)
	if !strings.Contains(prompt, "Rendering viewport") {
		t.Error("expected viewport section when dimensions are provided")
	}
	if !strings.Contains(prompt, "1920") {
		t.Error("expected width 1920 in viewport prompt")
	}
	if !strings.Contains(prompt, "1080") {
		t.Error("expected height 1080 in viewport prompt")
	}
	if !strings.Contains(prompt, "responsive") {
		t.Error("expected responsive design guidance in viewport prompt")
	}
}

// TestBuildSystemPrompt_UtilityAgentsSkipProjectContext verifies that the
// codebase agents get project context (working dir, AGENT.md, MEMORY.md) while
// the utility agents (Index, Search) omit it, and that the agentic-memory block
// only appears for agents that actually have the memory_recall tool.
func TestBuildSystemPrompt_UtilityAgentsSkipProjectContext(t *testing.T) {
	dir := "/tmp/proj"
	const agentMD = "AGENT_MD_SENTINEL rules"

	// Project-scoped agent keeps the context sections and (memory on + has
	// memory_recall) gets the agentic-memory block.
	build := buildSystemPrompt(BuildAgent, dir, true, agentMD, "", 0, 0)
	for _, s := range []string{"Working directory:", "MEMORY.md — Project Long-Term Memory", agentMD, "memory_recall tool"} {
		if !strings.Contains(build, s) {
			t.Errorf("BuildAgent prompt should contain %q", s)
		}
	}

	// Utility agents omit project context — even with memory enabled — because
	// they don't operate on the codebase and lack memory_recall.
	for _, a := range []Agent{IndexAgent, SearchAgent} {
		p := buildSystemPrompt(a, dir, true, agentMD, "some memory content", 0, 0)
		for _, s := range []string{"Working directory:", "MEMORY.md — Project Long-Term Memory", agentMD, "memory_recall tool"} {
			if strings.Contains(p, s) {
				t.Errorf("%s prompt should NOT contain project-context %q", a.ID, s)
			}
		}
		// The agent's own instructions must still be present.
		if !strings.Contains(p, a.System) {
			t.Errorf("%s prompt should still contain the agent's own System instructions", a.ID)
		}
	}
}

func TestExtractSearchSources(t *testing.T) {
	inputJSON := func(v any) json.RawMessage {
		b, _ := json.Marshal(v)
		return b
	}

	outputStr := func(s string) *string { return &s }

	tests := []struct {
		name     string
		messages []*session.MessageWithParts
		want     []sourceEntry
	}{
		{
			name:     "empty messages",
			messages: nil,
			want:     nil,
		},
		{
			name: "fetch_page extracts URL from input",
			messages: []*session.MessageWithParts{
				{
					Info: session.MessageInfo{Role: session.RoleAssistant},
					Parts: []session.Part{{
						Type: session.PartTool,
						Data: mustMarshalToolData(session.ToolPartData{
							Tool:   "fetch_page",
							CallID: "call1",
							State: session.ToolState{
								Status: session.ToolCompleted,
								Input:  inputJSON(map[string]string{"url": "https://example.com/page1"}),
								Output: outputStr("# Example\nURL: https://example.com/page1\n\nContent here"),
								Title:  outputStr("Example Page"),
							},
						}),
					}},
				},
			},
			want: []sourceEntry{
				{URL: "https://example.com/page1", Title: "Example Page"},
			},
		},
		{
			name: "web_search extracts URLs from output",
			messages: []*session.MessageWithParts{
				{
					Info: session.MessageInfo{Role: session.RoleAssistant},
					Parts: []session.Part{{
						Type: session.PartTool,
						Data: mustMarshalToolData(session.ToolPartData{
							Tool:   "web_search",
							CallID: "call2",
							State: session.ToolState{
								Status: session.ToolCompleted,
								Input:  inputJSON(map[string]string{"query": "test query"}),
								Output: outputStr("Search results for: test\n\n1. **Result 1**\n   URL: https://example.com/result1\n   Snippet here\n\n2. **Result 2**\n   URL: https://example.com/result2\n   Another snippet"),
								Title:  outputStr("test query"),
							},
						}),
					}},
				},
			},
			want: []sourceEntry{
				{URL: "https://example.com/result1", Title: ""},
				{URL: "https://example.com/result2", Title: ""},
			},
		},
		{
			name: "deduplicates URLs",
			messages: []*session.MessageWithParts{
				{
					Info: session.MessageInfo{Role: session.RoleAssistant},
					Parts: []session.Part{
						{
							Type: session.PartTool,
							Data: mustMarshalToolData(session.ToolPartData{
								Tool:   "fetch_page",
								CallID: "call1",
								State: session.ToolState{
									Status: session.ToolCompleted,
									Input:  inputJSON(map[string]string{"url": "https://example.com/page1"}),
									Output: outputStr("Content"),
									Title:  outputStr("Page 1"),
								},
							}),
						},
						{
							Type: session.PartTool,
							Data: mustMarshalToolData(session.ToolPartData{
								Tool:   "web_search",
								CallID: "call2",
								State: session.ToolState{
									Status: session.ToolCompleted,
									Input:  inputJSON(map[string]string{"query": "test"}),
									Output: outputStr("1. **Page 1**\n   URL: https://example.com/page1\n   Snippet"),
								},
							}),
						},
					},
				},
			},
			want: []sourceEntry{
				{URL: "https://example.com/page1", Title: "Page 1"},
				// URL https://example.com/page1 is already seen, so not repeated from web_search
			},
		},
		{
			name: "ignores other tools",
			messages: []*session.MessageWithParts{
				{
					Info: session.MessageInfo{Role: session.RoleAssistant},
					Parts: []session.Part{{
						Type: session.PartTool,
						Data: mustMarshalToolData(session.ToolPartData{
							Tool:   "bash",
							CallID: "call3",
							State: session.ToolState{
								Status: session.ToolCompleted,
								Input:  inputJSON(map[string]string{"command": "ls"}),
								Output: outputStr("file1.go file2.go"),
							},
						}),
					}},
				},
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractSearchSources(tt.messages)
			if len(got) != len(tt.want) {
				t.Fatalf("expected %d sources, got %d: %+v", len(tt.want), len(got), got)
			}
			for i, want := range tt.want {
				if got[i].URL != want.URL {
					t.Errorf("source %d: expected URL %q, got %q", i, want.URL, got[i].URL)
				}
				if got[i].Title != want.Title {
					t.Errorf("source %d: expected Title %q, got %q", i, want.Title, got[i].Title)
				}
			}
		})
	}
}

func TestExtractURLsFromText(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "extracts URLs from search format",
			text: "1. **Title**\n   URL: https://example.com/page\n   Snippet",
			want: []string{"https://example.com/page"},
		},
		{
			name: "extracts multiple URLs",
			text: "1. **A**\n   URL: https://a.com\n   Snippet\n\n2. **B**\n   URL: https://b.com\n   Snippet",
			want: []string{"https://a.com", "https://b.com"},
		},
		{
			name: "ignores non-URL lines",
			text: "Some text\nMore text without URLs",
			want: nil,
		},
		{
			name: "ignores URL lines without http",
			text: "URL: not-a-url",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractURLsFromText(tt.text)
			if len(got) != len(tt.want) {
				t.Fatalf("expected %d URLs, got %d: %v", len(tt.want), len(got), got)
			}
			for i, want := range tt.want {
				if got[i] != want {
					t.Errorf("URL %d: expected %q, got %q", i, want, got[i])
				}
			}
		})
	}
}

func TestHasSourcesSection(t *testing.T) {
	tests := []struct {
		name   string
		answer string
		want   bool
	}{
		{
			name:   "has ## Sources",
			answer: "Some answer\n\n## Sources\n\n1. URL",
			want:   true,
		},
		{
			name:   "has ### Sources",
			answer: "Some answer\n\n### Sources\n\n1. URL",
			want:   true,
		},
		{
			name:   "has **Sources**",
			answer: "Some answer\n\n**Sources**\n\n1. URL",
			want:   true,
		},
		{
			name:   "no sources section",
			answer: "Some answer without sources",
			want:   false,
		},
		{
			name:   "sources in lowercase",
			answer: "## sources\n\n1. URL",
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasSourcesSection(tt.answer)
			if got != tt.want {
				t.Errorf("hasSourcesSection(%q) = %v, want %v", tt.answer, got, tt.want)
			}
		})
	}
}

func TestFormatSources(t *testing.T) {
	sources := []sourceEntry{
		{URL: "https://example.com/page1", Title: "Page One"},
		{URL: "https://example.com/page2", Title: ""},
	}

	result := formatSources(sources)

	if !strings.Contains(result, "1. [Page One](https://example.com/page1)") {
		t.Errorf("expected titled source link, got: %s", result)
	}
	if !strings.Contains(result, "2. https://example.com/page2") {
		t.Errorf("expected plain URL source, got: %s", result)
	}
}

func mustMarshalToolData(d session.ToolPartData) json.RawMessage {
	b, _ := json.Marshal(d)
	return b
}

func TestIsContextLengthError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil error", err: nil, want: false},
		{name: "context_length_exceeded", err: fmt.Errorf("context_length_exceeded"), want: true},
		{name: "prompt is too long", err: fmt.Errorf("prompt is too long: 50000 tokens"), want: true},
		{name: "too long", err: fmt.Errorf("this model's maximum context length is too long"), want: true},
		{name: "maximum context", err: fmt.Errorf("maximum context length exceeded"), want: true},
		{name: "context length", err: fmt.Errorf("This model's context length is 4096 tokens"), want: true},
		{name: "ollama empty body 400", err: fmt.Errorf("ollama API error 400: "), want: true},
		{name: "ollama with message 400", err: fmt.Errorf("ollama API error 400: some other error"), want: false},
		{name: "openai 400 with body", err: fmt.Errorf("openai API error 400: invalid request"), want: false},
		{name: "generic 400", err: fmt.Errorf("some error 400"), want: false},
		{name: "rate limit", err: fmt.Errorf("rate limit exceeded"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isContextLengthError(tt.err)
			if got != tt.want {
				t.Errorf("isContextLengthError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestEstimateRequestTokens(t *testing.T) {
	req := provider.StreamRequest{
		System: []string{"You are a helpful assistant."},
		Messages: []provider.ModelMessage{
			{
				Role:    "user",
				Content: json.RawMessage(`"Hello"`),
			},
			{
				Role:    "assistant",
				Content: json.RawMessage(`"Hi there"`),
			},
		},
	}

	tokens := estimateRequestTokens(req)
	if tokens <= 0 {
		t.Errorf("estimateRequestTokens returned %d, expected > 0", tokens)
	}
	// At minimum every message contributes framing overhead plus its content.
	if tokens < len(req.Messages) {
		t.Errorf("estimateRequestTokens = %d, expected at least %d (one per message)", tokens, len(req.Messages))
	}

	// Empty request should have zero tokens.
	emptyTokens := estimateRequestTokens(provider.StreamRequest{})
	if emptyTokens != 0 {
		t.Errorf("estimateRequestTokens(empty) = %d, expected 0", emptyTokens)
	}
}

// TestConvertMessagesReasoningParts verifies that convertMessages correctly
// extracts reasoning parts from stored messages and includes them in the
// ModelMessage so they can be forwarded back to Anthropic as thinking blocks.
func TestConvertMessagesReasoningParts(t *testing.T) {
	reasoningData, _ := json.Marshal(session.ReasoningPartData{
		Text:      "Let me think about this step by step.",
		Signature: "ErkBCgIYAhIM...",
	})

	messages := []*session.MessageWithParts{
		{
			Info: session.MessageInfo{Role: session.RoleAssistant},
			Parts: []session.Part{
				{
					Type: session.PartReasoning,
					Data: reasoningData,
				},
				{
					Type: session.PartText,
					Data: json.RawMessage(`{"text":"The answer is 42."}`),
				},
			},
		},
	}

	result := convertMessages(messages)
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}

	if len(result[0].ReasoningParts) != 1 {
		t.Fatalf("expected 1 reasoning part, got %d", len(result[0].ReasoningParts))
	}

	rp := result[0].ReasoningParts[0]
	if rp.Text != "Let me think about this step by step." {
		t.Errorf("expected reasoning text, got %q", rp.Text)
	}
	if rp.Signature != "ErkBCgIYAhIM..." {
		t.Errorf("expected reasoning signature, got %q", rp.Signature)
	}
}

// TestConvertMessagesReasoningPartsWithToolCalls verifies that assistant messages
// with both reasoning parts and tool calls include reasoning parts.
func TestConvertMessagesReasoningPartsWithToolCalls(t *testing.T) {
	reasoningData, _ := json.Marshal(session.ReasoningPartData{
		Text:      "I need to use a tool.",
		Signature: "ToolSig==",
	})

	messages := []*session.MessageWithParts{
		{
			Info: session.MessageInfo{Role: session.RoleAssistant},
			Parts: []session.Part{
				{
					Type: session.PartReasoning,
					Data: reasoningData,
				},
				{
					Type: session.PartTool,
					Data: json.RawMessage(`{"tool":"bash","callId":"call_1","state":{"status":"completed","input":{"command":"ls"}}}`),
				},
			},
		},
	}

	result := convertMessages(messages)
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}

	if len(result[0].ReasoningParts) != 1 {
		t.Fatalf("expected 1 reasoning part, got %d", len(result[0].ReasoningParts))
	}

	if result[0].ReasoningParts[0].Text != "I need to use a tool." {
		t.Errorf("expected reasoning text, got %q", result[0].ReasoningParts[0].Text)
	}
	if result[0].ReasoningParts[0].Signature != "ToolSig==" {
		t.Errorf("expected reasoning signature, got %q", result[0].ReasoningParts[0].Signature)
	}
}

// TestEstimateRequestTokensReasoningParts verifies that estimateRequestTokens
// accounts for ReasoningParts (thinking blocks) text and signatures. Thinking
// content can be large; if it isn't counted, proactive compaction may trigger
// too late, causing a context-overflow error from the model.
func TestEstimateRequestTokensReasoningParts(t *testing.T) {
	reasoningText := "Let me think about this problem step by step."
	signature := "EuYBCg=="

	withReasoning := provider.StreamRequest{
		System: []string{"You are helpful."},
		Messages: []provider.ModelMessage{
			{
				Role:    "assistant",
				Content: json.RawMessage(`"Answer."`),
				ReasoningParts: []provider.ReasoningPart{
					{Text: reasoningText, Signature: signature},
				},
			},
		},
	}
	withoutReasoning := provider.StreamRequest{
		System: []string{"You are helpful."},
		Messages: []provider.ModelMessage{
			{
				Role:    "assistant",
				Content: json.RawMessage(`"Answer."`),
			},
		},
	}

	tokensWith := estimateRequestTokens(withReasoning)
	tokensWithout := estimateRequestTokens(withoutReasoning)

	if tokensWith <= tokensWithout {
		t.Fatalf("expected tokens with reasoning (%d) to exceed without (%d)", tokensWith, tokensWithout)
	}
	// The difference should reflect the reasoning text (plus signature) tokens.
	if delta, want := tokensWith-tokensWithout, estimateTokens(reasoningText); delta < want {
		t.Errorf("expected token delta >= %d (reasoning text), got %d", want, delta)
	}
}

// TestBuildSystemPrompt_ProjectMemoryScopeGuidance verifies that every agent
// holding project_memory_recall is actually told the two scopes exist. The
// guidance is nested inside the memory_recall block, so an agent granted
// project_memory_recall *without* memory_recall would silently get the tool and
// no instructions on when to prefer it.
func TestBuildSystemPrompt_ProjectMemoryScopeGuidance(t *testing.T) {
	const scopeSentinel = "project_memory_recall searches EVERY past conversation"

	all := []Agent{BuildAgent, TaskAgent, PlanAgent, BreakdownAgent, NoteAgent, IndexAgent, SearchAgent, SubagentAgent}
	for _, a := range all {
		if !a.HasTool("project_memory_recall") {
			continue
		}
		if !a.HasTool("memory_recall") {
			t.Errorf("%s has project_memory_recall but not memory_recall; the scope guidance is gated on the latter and would never render", a.ID)
		}
		p := buildSystemPrompt(a, "/tmp/proj", true, "", "", 0, 0)
		if !strings.Contains(p, scopeSentinel) {
			t.Errorf("%s prompt is missing the project-vs-session scope guidance", a.ID)
		}
		if !strings.Contains(p, `scope: "session"`) {
			t.Errorf("%s prompt does not mention the session scope parameter", a.ID)
		}
	}

	// Memory off: no memory guidance at all, whatever tools the agent holds.
	off := buildSystemPrompt(BuildAgent, "/tmp/proj", false, "", "", 0, 0)
	if strings.Contains(off, scopeSentinel) {
		t.Error("scope guidance leaked into the prompt with agentic memory disabled")
	}

	// Agents without the tool must not be told to use it.
	for _, a := range []Agent{NoteAgent, BreakdownAgent, IndexAgent, SearchAgent} {
		p := buildSystemPrompt(a, "/tmp/proj", true, "", "", 0, 0)
		if strings.Contains(p, scopeSentinel) {
			t.Errorf("%s lacks project_memory_recall but its prompt advertises it", a.ID)
		}
	}
}
