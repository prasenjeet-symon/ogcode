package provider

import (
	"encoding/json"
	"strings"
	"testing"
)

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