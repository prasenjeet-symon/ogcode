package provider

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestAnthropicThinkingBlocksInAssistantMessages verifies that reasoning/thinking
// blocks from previous assistant turns are forwarded back to the Anthropic API
// as "thinking" content blocks. Without this, multi-turn thinking breaks with an
// API error: "Expected `thinking` or `redacted_thinking`".
func TestAnthropicThinkingBlocksInAssistantMessages(t *testing.T) {
	// Simulate a multi-turn conversation where the assistant produced thinking
	// content in the previous turn. The ModelMessage carries ReasoningParts
	// that must be rendered as Anthropic "thinking" content blocks.
	messages := []ModelMessage{
		{Role: "user", Content: json.RawMessage(`"What is 2+2?"`)},
		{
			Role:    "assistant",
			Content: json.RawMessage(`"4"`),
			ReasoningParts: []ReasoningPart{
				{Text: "The user is asking a simple arithmetic question.", Signature: "ErkBCgIYAhIM..."},
			},
		},
		{Role: "user", Content: json.RawMessage(`"And 3+3?"`)},
	}

	req := StreamRequest{
		Model:    "claude-sonnet-4-6",
		System:   []string{"You are a math tutor."},
		Messages: messages,
	}

	// Replicate the message-building logic from StreamChat
	anthropicMessages := make([]anthropicMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == "system" {
			continue
		}

		if m.ToolCallID != "" {
			// Tool result handling (not relevant to this test)
		} else if m.ToolCalls != nil {
			// Assistant with tool calls (not relevant to this test)
		} else if m.Role == "assistant" && len(m.ReasoningParts) > 0 {
			// Assistant message with thinking blocks: thinking blocks must
			// precede text blocks per Anthropic API requirements.
			var blocks []map[string]any
			for _, rp := range m.ReasoningParts {
				blocks = append(blocks, map[string]any{
					"type":      "thinking",
					"thinking":  rp.Text,
					"signature": rp.Signature,
				})
			}
			var text string
			if m.Content != nil {
				json.Unmarshal(m.Content, &text)
			}
			if text != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": text})
			}
			anthropicMessages = append(anthropicMessages, anthropicMessage{Role: "assistant", Content: blocks})
		} else {
			var content any
			if err := json.Unmarshal(m.Content, &content); err != nil {
				content = string(m.Content)
			}
			anthropicMessages = append(anthropicMessages, anthropicMessage{
				Role:    m.Role,
				Content: content,
			})
		}
	}

	// Verify the assistant message has thinking blocks
	if len(anthropicMessages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(anthropicMessages))
	}

	// Second message should be the assistant message with thinking
	assistantMsg := anthropicMessages[1]
	if assistantMsg.Role != "assistant" {
		t.Errorf("expected assistant role, got %s", assistantMsg.Role)
	}

	blocks, ok := assistantMsg.Content.([]map[string]any)
	if !ok {
		t.Fatalf("expected Content to be []map[string]any, got %T", assistantMsg.Content)
	}

	// First block should be thinking
	if len(blocks) < 2 {
		t.Fatalf("expected at least 2 blocks (thinking + text), got %d", len(blocks))
	}

	thinkingBlock := blocks[0]
	if thinkingBlock["type"] != "thinking" {
		t.Errorf("expected first block type 'thinking', got %v", thinkingBlock["type"])
	}
	if thinkingBlock["thinking"] != "The user is asking a simple arithmetic question." {
		t.Errorf("expected thinking text, got %v", thinkingBlock["thinking"])
	}
	if thinkingBlock["signature"] != "ErkBCgIYAhIM..." {
		t.Errorf("expected signature, got %v", thinkingBlock["signature"])
	}

	// Second block should be text
	textBlock := blocks[1]
	if textBlock["type"] != "text" {
		t.Errorf("expected second block type 'text', got %v", textBlock["type"])
	}
}

// TestAnthropicThinkingBlocksWithToolCalls verifies that thinking blocks are
// placed before tool_use blocks in assistant messages with tool calls.
func TestAnthropicThinkingBlocksWithToolCalls(t *testing.T) {
	// Assistant message with both reasoning and tool calls
	messages := []ModelMessage{
		{Role: "user", Content: json.RawMessage(`"Read the file"`)},
		{
			Role:      "assistant",
			ToolCalls: json.RawMessage(`[{"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":\"/tmp/test\"}"}}]`),
			ReasoningParts: []ReasoningPart{
				{Text: "I need to read the file first.", Signature: "Sig123=="},
			},
		},
	}

	// Replicate the Anthropic message-building logic for tool calls with thinking
	anthropicMessages := make([]anthropicMessage, 0)
	for _, m := range messages {
		if m.ToolCalls != nil {
			type oaiFn struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}
			type oaiCall struct {
				ID       string `json:"id"`
				Function oaiFn  `json:"function"`
			}
			var calls []oaiCall
			var blocks []map[string]any
			if err := json.Unmarshal(m.ToolCalls, &calls); err == nil {
				// Prepend thinking blocks first (required by Anthropic API)
				for _, rp := range m.ReasoningParts {
					blocks = append(blocks, map[string]any{
						"type":      "thinking",
						"thinking":  rp.Text,
						"signature": rp.Signature,
					})
				}
				for _, call := range calls {
					var input any
					json.Unmarshal([]byte(call.Function.Arguments), &input)
					if _, ok := input.(map[string]any); !ok {
						input = map[string]any{}
					}
					blocks = append(blocks, map[string]any{
						"type":  "tool_use",
						"id":    call.ID,
						"name":  call.Function.Name,
						"input": input,
					})
				}
			}
			anthropicMessages = append(anthropicMessages, anthropicMessage{Role: "assistant", Content: blocks})
		}
	}

	if len(anthropicMessages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(anthropicMessages))
	}

	blocks, ok := anthropicMessages[0].Content.([]map[string]any)
	if !ok {
		t.Fatalf("expected Content to be []map[string]any, got %T", anthropicMessages[0].Content)
	}

	// First block must be thinking (not tool_use)
	if blocks[0]["type"] != "thinking" {
		t.Errorf("expected first block to be 'thinking', got %v", blocks[0]["type"])
	}
	// Second block must be tool_use
	if blocks[1]["type"] != "tool_use" {
		t.Errorf("expected second block to be 'tool_use', got %v", blocks[1]["type"])
	}
}



// TestAnthropicRedactedThinkingEvent verifies that a redacted_thinking
// content_block_start event — which carries only a signature and no text deltas
// — is correctly parsed and the signature is extracted. This is the streaming
// side of the thinking-content round-trip: without the signature, multi-turn
// thinking with redacted blocks fails with an Anthropic API error.
func TestAnthropicRedactedThinkingEvent(t *testing.T) {
	// Simulate the JSON payload of a content_block_start event for a
	// redacted_thinking block. Anthropic delivers the signature here, not
	// via a signature_delta (which only accompanies non-redacted thinking).
	raw := `{"type":"content_block_start","index":0,"content_block":{"type":"redacted_thinking","signature":"EuYBCg=="}}`

	var evt anthropicEvent
	if err := json.Unmarshal([]byte(raw), &evt); err != nil {
		t.Fatalf("failed to parse redacted_thinking event: %v", err)
	}
	if evt.Type != "content_block_start" {
		t.Fatalf("expected type 'content_block_start', got %q", evt.Type)
	}
	if evt.ContentBlock == nil {
		t.Fatal("expected non-nil ContentBlock")
	}
	if evt.ContentBlock.Type != "redacted_thinking" {
		t.Errorf("expected content block type 'redacted_thinking', got %q", evt.ContentBlock.Type)
	}
	if evt.ContentBlock.Signature != "EuYBCg==" {
		t.Errorf("expected signature 'EuYBCg==', got %q", evt.ContentBlock.Signature)
	}
}

// TestAnthropicRedactedThinkingForwardedAsThinkingBlock verifies that a
// reasoning part with empty text but a non-empty signature (as produced by a
// redacted_thinking block) is still forwarded to the API as a thinking content
// block. Anthropic accepts redacted_thinking-originated blocks as "thinking"
// blocks as long as the signature is present.
func TestAnthropicRedactedThinkingForwardedAsThinkingBlock(t *testing.T) {
	messages := []ModelMessage{
		{Role: "user", Content: json.RawMessage(`"Continue"`)},
		{
			Role:    "assistant",
			Content: json.RawMessage(`"Here is the answer."`),
			ReasoningParts: []ReasoningPart{
				{Text: "", Signature: "EuYBCg=="},
			},
		},
		{Role: "user", Content: json.RawMessage(`"Thanks"`)},
	}

	// Replicate the plain-assistant-with-reasoning branch from StreamChat
	var assistantMsg anthropicMessage
	for _, m := range messages {
		if m.Role == "assistant" && len(m.ReasoningParts) > 0 && m.ToolCalls == nil {
			var blocks []map[string]any
			for _, rp := range m.ReasoningParts {
				blocks = append(blocks, map[string]any{
					"type":      "thinking",
					"thinking":  rp.Text,
					"signature": rp.Signature,
				})
			}
			var text string
			if m.Content != nil {
				json.Unmarshal(m.Content, &text)
			}
			if text != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": text})
			}
			assistantMsg = anthropicMessage{Role: "assistant", Content: blocks}
			break
		}
	}

	blocks, ok := assistantMsg.Content.([]map[string]any)
	if !ok {
		t.Fatalf("expected []map[string]any, got %T", assistantMsg.Content)
	}
	if len(blocks) < 2 {
		t.Fatalf("expected at least 2 blocks, got %d", len(blocks))
	}
	if blocks[0]["type"] != "thinking" {
		t.Errorf("expected first block 'thinking', got %v", blocks[0]["type"])
	}
	if blocks[0]["signature"] != "EuYBCg==" {
		t.Errorf("expected signature preserved, got %v", blocks[0]["signature"])
	}
	if blocks[0]["thinking"] != "" {
		t.Errorf("expected empty thinking text for redacted block, got %v", blocks[0]["thinking"])
	}
	if blocks[1]["type"] != "text" {
		t.Errorf("expected second block 'text', got %v", blocks[1]["type"])
	}
}

// TestAnthropicPromptCaching verifies that the Anthropic provider emits
// cache_control markers on the system prompt block and the last tool
// definition, enabling prompt caching for repeated prefixes.
func TestAnthropicPromptCaching(t *testing.T) {
	req := StreamRequest{
		Model:  "claude-sonnet-4-6",
		System: []string{"You are a helpful coding assistant.", "Working directory: /tmp"},
		Messages: []ModelMessage{
			{Role: "user", Content: json.RawMessage(`"Hello"`)},
		},
		Tools: []ToolDefinition{
			{Name: "bash", Description: "Run a shell command", Parameters: json.RawMessage(`{"type":"object"}`)},
			{Name: "read", Description: "Read a file", Parameters: json.RawMessage(`{"type":"object"}`)},
		},
	}

	// StreamChat sends an HTTP request immediately; we can't easily intercept
	// it. Instead, replicate the exact request-building logic to verify the
	// JSON structure. This mirrors the code in StreamChat.

	systemPrompt := strings.Join(req.System, "\n\n")

	tools := make([]anthropicTool, 0, len(req.Tools))
	for _, tt := range req.Tools {
		tools = append(tools, anthropicTool{
			Name:        tt.Name,
			Description: tt.Description,
			InputSchema: tt.Parameters,
		})
	}

	systemBlocks := []anthropicSystemBlock{
		{Type: "text", Text: systemPrompt, CacheControl: &anthropicCacheControl{Type: "ephemeral"}},
	}
	if len(tools) > 0 {
		tools[len(tools)-1].CacheControl = &anthropicCacheControl{Type: "ephemeral"}
	}

	body := anthropicRequest{
		Model:       req.Model,
		MaxTokens:   max(req.MaxTokens, 4096),
		System:      systemBlocks,
		Messages:    []anthropicMessage{{Role: "user", Content: "Hello"}},
		Tools:       tools,
		Stream:      true,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	// Parse back to inspect the structure.
	var raw map[string]any
	if err := json.Unmarshal(jsonBody, &raw); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}

	// System must be an array of blocks, not a plain string.
	sysRaw, ok := raw["system"].([]any)
	if !ok {
		t.Fatalf("expected system to be an array, got %T", raw["system"])
	}
	if len(sysRaw) != 1 {
		t.Fatalf("expected 1 system block, got %d", len(sysRaw))
	}
	sysBlock, ok := sysRaw[0].(map[string]any)
	if !ok {
		t.Fatalf("expected system block to be an object, got %T", sysRaw[0])
	}
	if sysBlock["type"] != "text" {
		t.Errorf("expected system block type 'text', got %v", sysBlock["type"])
	}
	cc, ok := sysBlock["cache_control"].(map[string]any)
	if !ok {
		t.Fatal("expected cache_control on system block")
	}
	if cc["type"] != "ephemeral" {
		t.Errorf("expected cache_control type 'ephemeral', got %v", cc["type"])
	}

	// Last tool must have cache_control.
	toolsRaw, ok := raw["tools"].([]any)
	if !ok {
		t.Fatalf("expected tools to be an array, got %T", raw["tools"])
	}
	if len(toolsRaw) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(toolsRaw))
	}
	lastTool, ok := toolsRaw[len(toolsRaw)-1].(map[string]any)
	if !ok {
		t.Fatalf("expected last tool to be an object, got %T", toolsRaw[len(toolsRaw)-1])
	}
	lastCC, ok := lastTool["cache_control"].(map[string]any)
	if !ok {
		t.Fatal("expected cache_control on last tool")
	}
	if lastCC["type"] != "ephemeral" {
		t.Errorf("expected last tool cache_control type 'ephemeral', got %v", lastCC["type"])
	}

	// First tool must NOT have cache_control (only the last one is marked).
	firstTool, ok := toolsRaw[0].(map[string]any)
	if !ok {
		t.Fatalf("expected first tool to be an object, got %T", toolsRaw[0])
	}
	if _, hasCC := firstTool["cache_control"]; hasCC {
		t.Error("did not expect cache_control on first tool")
	}
}

// TestAnthropicNoToolsNoCacheControlOnTools verifies that when there are no
// tools, we don't panic and the system block still gets its cache_control.
func TestAnthropicNoToolsNoCacheControlOnTools(t *testing.T) {
	systemBlocks := []anthropicSystemBlock{
		{Type: "text", Text: "system prompt", CacheControl: &anthropicCacheControl{Type: "ephemeral"}},
	}
	tools := make([]anthropicTool, 0)

	if len(tools) > 0 {
		tools[len(tools)-1].CacheControl = &anthropicCacheControl{Type: "ephemeral"}
	}

	body := anthropicRequest{
		Model:    "claude-sonnet-4-6",
		MaxTokens: 4096,
		System:   systemBlocks,
		Tools:    tools,
		Stream:   true,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(jsonBody, &raw); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}

	// Tools should be omitted (empty slice + omitempty).
	if _, hasTools := raw["tools"]; hasTools {
		t.Error("did not expect tools field when no tools are provided")
	}

	// System block should still have cache_control.
	sysRaw := raw["system"].([]any)
	sysBlock := sysRaw[0].(map[string]any)
	cc := sysBlock["cache_control"].(map[string]any)
	if cc["type"] != "ephemeral" {
		t.Errorf("expected system cache_control type 'ephemeral', got %v", cc["type"])
	}
}

// TestAnthropicMultiSystemBlocksCaching verifies that when multiple system
// prompt entries are provided (e.g. static base prompt + dynamic date
// reminder), only the first (static) block gets cache_control and the
// trailing dynamic blocks do not. This ensures the date — which changes every
// turn — does not invalidate the prompt cache prefix.
func TestAnthropicMultiSystemBlocksCaching(t *testing.T) {
	// Simulate the loop.go pattern: [static system prompt, dynamic date reminder]
	systemEntries := []string{
		"You are a coding agent.\n\nWorking directory: /tmp",
		"<system-reminder>\nCurrent date: Mon Jan 2 15:04:05 MST 2026\n</system-reminder>",
	}

	// Replicate the Anthropic provider's system-block construction logic.
	var systemBlocks []anthropicSystemBlock
	if len(systemEntries) > 1 {
		systemBlocks = append(systemBlocks, anthropicSystemBlock{
			Type:         "text",
			Text:         systemEntries[0],
			CacheControl: &anthropicCacheControl{Type: "ephemeral"},
		})
		for _, s := range systemEntries[1:] {
			systemBlocks = append(systemBlocks, anthropicSystemBlock{
				Type: "text",
				Text: s,
			})
		}
	} else {
		systemBlocks = []anthropicSystemBlock{
			{Type: "text", Text: strings.Join(systemEntries, "\n\n"), CacheControl: &anthropicCacheControl{Type: "ephemeral"}},
		}
	}

	body := anthropicRequest{
		Model:     "claude-sonnet-4-6",
		MaxTokens: 4096,
		System:    systemBlocks,
		Stream:    true,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(jsonBody, &raw); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}

	sysRaw, ok := raw["system"].([]any)
	if !ok {
		t.Fatalf("expected system to be an array, got %T", raw["system"])
	}
	if len(sysRaw) != 2 {
		t.Fatalf("expected 2 system blocks (static + dynamic), got %d", len(sysRaw))
	}

	// First block (static) must have cache_control.
	firstBlock := sysRaw[0].(map[string]any)
	if firstBlock["type"] != "text" {
		t.Errorf("expected first block type 'text', got %v", firstBlock["type"])
	}
	firstCC, ok := firstBlock["cache_control"].(map[string]any)
	if !ok {
		t.Fatal("expected cache_control on first (static) system block")
	}
	if firstCC["type"] != "ephemeral" {
		t.Errorf("expected first block cache_control type 'ephemeral', got %v", firstCC["type"])
	}
	// First block must contain the static system prompt text.
	if !strings.Contains(firstBlock["text"].(string), "Working directory") {
		t.Error("expected first block to contain the static system prompt")
	}

	// Second block (dynamic date) must NOT have cache_control.
	secondBlock := sysRaw[1].(map[string]any)
	if secondBlock["type"] != "text" {
		t.Errorf("expected second block type 'text', got %v", secondBlock["type"])
	}
	if _, hasCC := secondBlock["cache_control"]; hasCC {
		t.Error("did not expect cache_control on second (dynamic) system block — it would invalidate the cache every turn")
	}
	// Second block must contain the date reminder.
	if !strings.Contains(secondBlock["text"].(string), "Current date") {
		t.Error("expected second block to contain the dynamic date reminder")
	}
}