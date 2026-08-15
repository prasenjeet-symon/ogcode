package provider

import "testing"

func hasCacheControl(block map[string]any) bool {
	_, ok := block["cache_control"]
	return ok
}

func TestAttachMessageCacheBreakpoint(t *testing.T) {
	// []map[string]any content: cache_control lands on the last block only.
	t.Run("block slice", func(t *testing.T) {
		msgs := []anthropicMessage{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: []map[string]any{
				{"type": "text", "text": "a"},
				{"type": "text", "text": "b"},
			}},
		}
		attachMessageCacheBreakpoint(msgs)
		blocks := msgs[1].Content.([]map[string]any)
		if hasCacheControl(blocks[0]) {
			t.Error("first block must not be marked")
		}
		if !hasCacheControl(blocks[1]) {
			t.Error("last block must carry cache_control")
		}
	})

	// string content: normalized into a single cacheable text block.
	t.Run("string normalized", func(t *testing.T) {
		msgs := []anthropicMessage{{Role: "user", Content: "tool results here"}}
		attachMessageCacheBreakpoint(msgs)
		b, ok := msgs[0].Content.([]map[string]any)
		if !ok || len(b) != 1 {
			t.Fatalf("string content should become one text block, got %T", msgs[0].Content)
		}
		if b[0]["type"] != "text" || b[0]["text"] != "tool results here" || !hasCacheControl(b[0]) {
			t.Errorf("normalized block wrong: %v", b[0])
		}
	})

	// []any content: cache_control on the last map element.
	t.Run("any slice", func(t *testing.T) {
		msgs := []anthropicMessage{{Role: "user", Content: []any{
			map[string]any{"type": "tool_result", "content": "x"},
			map[string]any{"type": "tool_result", "content": "y"},
		}}}
		attachMessageCacheBreakpoint(msgs)
		arr := msgs[0].Content.([]any)
		if hasCacheControl(arr[0].(map[string]any)) {
			t.Error("first element must not be marked")
		}
		if !hasCacheControl(arr[1].(map[string]any)) {
			t.Error("last element must carry cache_control")
		}
	})

	// Empty string content is left untouched (no empty text block emitted).
	t.Run("empty string untouched", func(t *testing.T) {
		msgs := []anthropicMessage{{Role: "user", Content: ""}}
		attachMessageCacheBreakpoint(msgs)
		if _, ok := msgs[0].Content.(string); !ok {
			t.Errorf("empty string content should be left as-is, got %T", msgs[0].Content)
		}
	})

	// No messages: must not panic.
	t.Run("empty and nil", func(t *testing.T) {
		attachMessageCacheBreakpoint(nil)
		attachMessageCacheBreakpoint([]anthropicMessage{})
	})
}
