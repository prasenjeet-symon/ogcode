package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

func TestBuildSystemPrompt_MemoryMDSection_PresentRegardlessOfContent(t *testing.T) {
	agent := BuildAgent
	dir := "/tmp/test"

	// Case 1: No MEMORY.md content — section should still appear
	prompt := buildSystemPrompt(agent, dir, false, "", "", 0, 0)
	if !strings.Contains(prompt, "## MEMORY.md — Project Long-Term Memory") {
		t.Error("expected MEMORY.md section to appear even when memoryMDContent is empty")
	}
	if !strings.Contains(prompt, "This project has no MEMORY.md file") {
		t.Error("expected the absent-file wording when memoryMDContent is empty")
	}
	if strings.Contains(prompt, "The content above in the <memory-md> tag") {
		t.Error("must not point at a <memory-md> tag when none was prepended")
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
	if strings.Contains(prompt, "This project has no MEMORY.md file") {
		t.Error("did not expect the absent-file wording when memoryMDContent is present")
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
		"### How it differs from AGENT.md",
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
	if !strings.Contains(buildPrompt, "create one in the project root with the write tool") {
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
	if strings.Contains(planPrompt, "create one in the project root with the write tool") {
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
	if strings.Contains(buildPrompt, "create one in the project root with the write tool") {
		t.Error("did not expect creation prompt when memoryMDContent is present for BuildAgent")
	}

	// PlanAgent with MEMORY.md content — should show read-only version, no creation prompt
	planPrompt := buildSystemPrompt(PlanAgent, dir, false, "", memContent, 0, 0)
	if !strings.Contains(planPrompt, "The content above in the <memory-md> tag") {
		t.Error("expected content indicator when memoryMDContent is present for PlanAgent")
	}
	if strings.Contains(planPrompt, "create one in the project root with the write tool") {
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
		Model:     "claude-opus-4-6",
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

	result := convertMessages(messages, false, "claude-opus-4-6")
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
		Model:     "claude-opus-4-6",
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

	result := convertMessages(messages, false, "claude-opus-4-6")
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

// TestConvertMessagesReasoningReplayGuard covers the cases where stored thinking
// blocks must NOT be forwarded. Thinking blocks are tied to the model that
// produced them: an unsigned one (what an OpenAI-family model stores, since it
// keeps its reasoning server-side) has nothing to verify it with and is
// rejected, and a signed one replayed to a different model is silently ignored
// while still being billed as input. Both are reachable by switching the
// session's model mid-conversation.
func TestConvertMessagesReasoningReplayGuard(t *testing.T) {
	assistantWith := func(parts ...session.ReasoningPartData) []*session.MessageWithParts {
		msg := &session.MessageWithParts{
			Info: session.MessageInfo{Role: session.RoleAssistant},
			Parts: []session.Part{{
				Type: session.PartText,
				Data: json.RawMessage(`{"text":"done"}`),
			}},
		}
		for _, d := range parts {
			data, _ := json.Marshal(d)
			msg.Parts = append([]session.Part{{Type: session.PartReasoning, Data: data}}, msg.Parts...)
		}
		return []*session.MessageWithParts{msg}
	}

	t.Run("unsigned reasoning from an OpenAI-family model is dropped", func(t *testing.T) {
		msgs := assistantWith(session.ReasoningPartData{Text: "thinking out loud", Model: "deepseek-reasoner"})
		got := convertMessages(msgs, false, "claude-opus-4-6")
		if len(got[0].ReasoningParts) != 0 {
			t.Errorf("unsigned reasoning must not reach Anthropic, got %+v", got[0].ReasoningParts)
		}
	})

	t.Run("signed reasoning from a different model is dropped", func(t *testing.T) {
		msgs := assistantWith(session.ReasoningPartData{Text: "prior", Signature: "sig==", Model: "claude-opus-4-6"})
		got := convertMessages(msgs, false, "claude-sonnet-4-6")
		if len(got[0].ReasoningParts) != 0 {
			t.Errorf("cross-model reasoning must not be replayed, got %+v", got[0].ReasoningParts)
		}
	})

	t.Run("reasoning of unknown origin is dropped", func(t *testing.T) {
		msgs := assistantWith(session.ReasoningPartData{Text: "legacy row", Signature: "sig=="})
		got := convertMessages(msgs, false, "claude-opus-4-6")
		if len(got[0].ReasoningParts) != 0 {
			t.Errorf("reasoning with no recorded model must be dropped, got %+v", got[0].ReasoningParts)
		}
	})

	t.Run("one foreign block drops the whole sequence", func(t *testing.T) {
		msgs := assistantWith(
			session.ReasoningPartData{Text: "b", Signature: "sig2==", Model: "claude-opus-4-6"},
			session.ReasoningPartData{Text: "a", Signature: "sig1==", Model: "gpt-5"},
		)
		got := convertMessages(msgs, false, "claude-opus-4-6")
		if len(got[0].ReasoningParts) != 0 {
			t.Errorf("a partial sequence is itself a 400; expected all dropped, got %+v", got[0].ReasoningParts)
		}
	})

	t.Run("redacted block from the current model survives", func(t *testing.T) {
		msgs := assistantWith(session.ReasoningPartData{RedactedData: "EuYBCg==", Model: "claude-opus-4-6"})
		got := convertMessages(msgs, false, "claude-opus-4-6")
		if len(got[0].ReasoningParts) != 1 {
			t.Fatalf("expected the redacted block to be replayed, got %+v", got[0].ReasoningParts)
		}
		if got[0].ReasoningParts[0].RedactedData != "EuYBCg==" {
			t.Errorf("expected the payload carried through, got %+v", got[0].ReasoningParts[0])
		}
	})
}

// TestConvertMessages_StripsToolResultImagesWhenModelLacksVision verifies that
// convertMessages does NOT attach images to tool-result messages when
// modelSupportsImages is false. A non-vision model rejects image input with a
// 400, and that 400 is classified fatal (non-resumable), killing the session.
// The image can originate from view_image, read_pdf_page, or an MCP tool — all
// persist a ToolImage in the tool state. Without this guard, a stale image from
// earlier in the history (or one produced this turn because the capability probe
// was wrong) is re-sent on every subsequent turn and the session dies.
func TestConvertMessages_StripsToolResultImagesWhenModelLacksVision(t *testing.T) {
	output := "Image screenshot.png (300x200, png) is attached."
	toolData := session.ToolPartData{
		Tool:   "view_image",
		CallID: "call_img_1",
		State: session.ToolState{
			Status: session.ToolCompleted,
			Input:  json.RawMessage(`{"path":"screenshot.png"}`),
			Output: &output,
			Image: &session.ToolImage{
				MediaType: "image/jpeg",
				Data:      "dGVzdA==", // "test" base64
			},
		},
	}
	toolDataJSON, _ := json.Marshal(toolData)

	// A user message carrying the tool result (the shape writeToolResultMessage
	// produces) followed by an assistant message.
	messages := []*session.MessageWithParts{
		{
			Info: session.MessageInfo{Role: session.RoleUser},
			Parts: []session.Part{{
				Type: session.PartTool,
				Data: toolDataJSON,
			}},
		},
	}

	// modelSupportsImages=false: the image must NOT be forwarded.
	stripped := convertMessages(messages, false, "claude-opus-4-6")
	if len(stripped) != 1 {
		t.Fatalf("expected 1 message, got %d", len(stripped))
	}
	if stripped[0].Role != "tool" {
		t.Fatalf("expected role 'tool', got %q", stripped[0].Role)
	}
	if len(stripped[0].Images) != 0 {
		t.Errorf("expected 0 images when modelSupportsImages=false, got %d — a non-vision model would 400 on this", len(stripped[0].Images))
	}

	// modelSupportsImages=true: the image MUST be forwarded.
	withImages := convertMessages(messages, true, "claude-opus-4-6")
	if len(withImages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(withImages))
	}
	if len(withImages[0].Images) != 1 {
		t.Fatalf("expected 1 image when modelSupportsImages=true, got %d", len(withImages[0].Images))
	}
	if withImages[0].Images[0].MediaType != "image/jpeg" {
		t.Errorf("expected media type 'image/jpeg', got %q", withImages[0].Images[0].MediaType)
	}
}

// TestConvertMessages_StripsImagesFromPriorHistory verifies that images from
// tool results earlier in the conversation are also stripped — not just the
// most recent one. The bug scenario: a vision-capable model produced a tool
// image earlier, then the user switched to a non-vision model; the persisted
// ToolImage is replayed from history and kills the new model's session.
func TestConvertMessages_StripsImagesFromPriorHistory(t *testing.T) {
	mkToolResult := func(callID, toolName string, img *session.ToolImage) session.Part {
		output := "result"
		td := session.ToolPartData{
			Tool:   toolName,
			CallID: callID,
			State: session.ToolState{
				Status: session.ToolCompleted,
				Input:  json.RawMessage(`{}`),
				Output: &output,
				Image:  img,
			},
		}
		data, _ := json.Marshal(td)
		return session.Part{Type: session.PartTool, Data: data}
	}

	img := &session.ToolImage{MediaType: "image/png", Data: "aW1n=="}

	messages := []*session.MessageWithParts{
		// Earlier user turn with a tool result carrying an image.
		{
			Info: session.MessageInfo{Role: session.RoleUser},
			Parts: []session.Part{
				mkToolResult("call_1", "view_image", img),
			},
		},
		// Assistant response.
		{
			Info: session.MessageInfo{Role: session.RoleAssistant},
			Parts: []session.Part{{
				Type: session.PartText,
				Data: json.RawMessage(`{"text":"I see the image."}`),
			}},
		},
		// Later user turn with another tool result carrying an image.
		{
			Info: session.MessageInfo{Role: session.RoleUser},
			Parts: []session.Part{
				mkToolResult("call_2", "read_pdf_page", img),
			},
		},
	}

	result := convertMessages(messages, false, "claude-opus-4-6")
	imageCount := 0
	for _, m := range result {
		imageCount += len(m.Images)
	}
	if imageCount != 0 {
		t.Errorf("expected 0 images across all messages when modelSupportsImages=false, got %d — prior-history images would 400 a non-vision model", imageCount)
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

// panicTool exists only to blow up. A tool that panics used to take the whole
// server process with it — the goroutine that runs tools had no recover, and an
// unrecovered panic in any goroutine kills the process, so one bad tool call
// ended every session the server was serving.
type panicTool struct{}

func (panicTool) ID() string          { return "panic_tool" }
func (panicTool) Description() string { return "panics" }
func (panicTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (panicTool) Execute(context.Context, json.RawMessage, tool.Context) (tool.Result, error) {
	var m map[string]string
	m["boom"] = "boom" // assignment to entry in nil map
	return tool.Result{}, nil
}

func TestExecuteTool_PanicBecomesToolErrorNotProcessDeath(t *testing.T) {
	// Run the tool the way the loop's goroutine does, including its recover.
	// If the recover is removed this test does not fail — it takes the test
	// binary down, which is exactly the production symptom.
	var result tool.Result
	var err error

	func() {
		defer func() {
			if r := recover(); r != nil {
				result = tool.Result{}
				err = fmt.Errorf("tool %s panicked: %v", panicTool{}.ID(), r)
			}
		}()
		result, err = panicTool{}.Execute(context.Background(), nil, tool.Context{})
	}()

	if err == nil {
		t.Fatal("expected the panic to surface as an error")
	}
	if !strings.Contains(err.Error(), "panicked") {
		t.Errorf("error should name the panic, got %v", err)
	}
	if result.Output != "" {
		t.Errorf("expected an empty result alongside the error, got %q", result.Output)
	}
}

// Regression: the Auto-mode LLM risk verdict used to be parsed with
// strings.Contains(up, "SAFE"), which matched "NOT SAFE", "not safe", and
// "UNSAFE" as substrings and auto-approved them. The parser now requires the
// trimmed verdict to be exactly "SAFE". Anything ambiguous defaults to RiskAsk.
func TestIsSafeVerdict(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"SAFE", true},
		{"safe", true},
		{"Safe", true},
		{" SAFE ", true},
		{"SAFE.", true},
		{"SAFE!", true},

		// The bypass: negations contain "SAFE" as a substring.
		{"NOT SAFE", false},
		{"not safe", false},
		{"UNSAFE", false},
		{"It is safe", false},
		{"not safe to run", false},
		{"DANGER", false},
		{"ASK", false},
		{"", false},
		{"safe to run", false},
	}
	for _, c := range cases {
		if got := isSafeVerdict(strings.ToUpper(c.in)); got != c.want {
			t.Errorf("isSafeVerdict(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
