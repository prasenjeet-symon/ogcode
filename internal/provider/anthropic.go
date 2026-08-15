package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

// AnthropicProvider implements Provider for the Anthropic Messages API.
type AnthropicProvider struct {
	apiKey  string
	model   string
	baseURL string
}

func NewAnthropicProvider() *AnthropicProvider {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	model := os.Getenv("ANTHROPIC_MODEL")
	if model == "" {
		model = "claude-sonnet-4-6"
	}
	baseURL := os.Getenv("ANTHROPIC_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.anthropic.com/v1"
	}
	return &AnthropicProvider{apiKey: apiKey, model: model, baseURL: baseURL}
}

func (p *AnthropicProvider) ID() string { return "anthropic" }

func (p *AnthropicProvider) Models() []ModelInfo {
	all := make([]ModelInfo, 0, len(AnthropicModels))
	for _, m := range AnthropicModels {
		all = append(all, ModelInfo{
			ID:              m.ID,
			Name:            m.Name,
			ProviderID:      "anthropic",
			ActiveByDefault: m.ActiveByDefault,
			Default:         m.ID == p.model,
			InputPricePerM:  m.InputPricePerM,
			OutputPricePerM: m.OutputPricePerM,
			SupportsImages:  m.SupportsImages,
			ContextWindow:   m.ContextWindow,
		})
	}
	return all
}

func (p *AnthropicProvider) StreamChat(ctx context.Context, req StreamRequest) (<-chan StreamEvent, error) {
	model := req.Model
	if model == "" {
		model = p.model
	}

	messages := make([]anthropicMessage, 0, len(req.Messages))
	for i := 0; i < len(req.Messages); i++ {
		m := req.Messages[i]
		if m.Role == "system" {
			continue
		}

		if m.ToolCallID != "" {
			// Tool result: collect consecutive tool results into one user message
			// (Anthropic requires alternating roles, so all results for one turn go together)
			var blocks []map[string]any
			for i < len(req.Messages) && req.Messages[i].ToolCallID != "" {
				tr := req.Messages[i]
				var output string
				if err := json.Unmarshal(tr.Content, &output); err != nil {
					output = string(tr.Content)
				}
				// When the tool result carries images, the content becomes an
				// array of blocks: the text output followed by image blocks.
				// Anthropic supports image blocks directly inside tool_result.
				if len(tr.Images) > 0 {
					content := []map[string]any{{"type": "text", "text": output}}
					for _, img := range tr.Images {
						content = append(content, map[string]any{
							"type": "image",
							"source": map[string]any{
								"type":       "base64",
								"media_type": img.MediaType,
								"data":       img.Data,
							},
						})
					}
					blocks = append(blocks, map[string]any{
						"type":        "tool_result",
						"tool_use_id": tr.ToolCallID,
						"content":     content,
					})
				} else {
					blocks = append(blocks, map[string]any{
						"type":        "tool_result",
						"tool_use_id": tr.ToolCallID,
						"content":     output,
					})
				}
				i++
			}
			i-- // outer loop will increment
			messages = append(messages, anthropicMessage{Role: "user", Content: blocks})
		} else if m.ToolCalls != nil {
			// Assistant message with tool calls: convert from OpenAI format to Anthropic tool_use blocks.
			// When the assistant produced thinking/reasoning content, Anthropic requires the
			// thinking blocks to precede all other content blocks (text, tool_use) — otherwise
			// the API returns a 400 error.
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
			// Prepend thinking blocks first (required by Anthropic API)
			for _, rp := range m.ReasoningParts {
				thinkingBlock := map[string]any{
					"type":      "thinking",
					"thinking":  rp.Text,
					"signature": rp.Signature,
				}
				blocks = append(blocks, thinkingBlock)
			}
			if err := json.Unmarshal(m.ToolCalls, &calls); err == nil {
				if m.Content != nil {
					var text string
					if err := json.Unmarshal(m.Content, &text); err == nil && text != "" {
						blocks = append(blocks, map[string]any{"type": "text", "text": text})
					}
				}
				for _, call := range calls {
					var input any
					if err := json.Unmarshal([]byte(call.Function.Arguments), &input); err != nil {
						input = map[string]any{}
					}
					// Anthropic requires tool_use.input to be a JSON object. A no-arg
					// tool call round-trips through storage as "null" (a nil
					// json.RawMessage marshals to null), which unmarshals here to a
					// nil interface rather than a map; scalars/arrays are likewise not
					// objects. Coerce any non-object to {} or the request fails with
					// 400 "tool_use.input: Input should be an object".
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
			messages = append(messages, anthropicMessage{Role: "assistant", Content: blocks})
		} else if len(m.Images) > 0 {
			// Plain message with image attachments (e.g. a capability probe or a
			// user-supplied image): build a content array of text + image blocks.
			var text string
			json.Unmarshal(m.Content, &text)
			blocks := []map[string]any{}
			if text != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": text})
			}
			for _, img := range m.Images {
				blocks = append(blocks, map[string]any{
					"type": "image",
					"source": map[string]any{
						"type":       "base64",
						"media_type": img.MediaType,
						"data":       img.Data,
					},
				})
			}
			messages = append(messages, anthropicMessage{Role: m.Role, Content: blocks})
		} else {
			// For assistant messages with thinking/reasoning blocks, Anthropic requires
			// thinking blocks to precede text blocks. Build a content array.
			if m.Role == "assistant" && len(m.ReasoningParts) > 0 {
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
				messages = append(messages, anthropicMessage{Role: "assistant", Content: blocks})
			} else {
				var content any
				if err := json.Unmarshal(m.Content, &content); err != nil {
					content = string(m.Content)
				}
				messages = append(messages, anthropicMessage{
					Role:    m.Role,
					Content: content,
				})
			}
		}
	}

	systemPrompt := strings.Join(req.System, "\n\n")

	tools := make([]anthropicTool, 0, len(req.Tools))
	for _, t := range req.Tools {
		tools = append(tools, anthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.Parameters,
		})
	}

	// Prompt caching: Anthropic caches the prefix of the request (tools →
	// system → messages) when cache_control breakpoints are present. The
	// tool definitions and the base system prompt are largely static across
	// turns within a session, so we mark them as cacheable to get ~90% cost
	// reduction on repeated prefixes.
	//
	// The system field is sent as an array of content blocks (required to
	// attach cache_control). We split the system prompt entries into:
	//   - A cacheable block containing all entries joined together, with a
	//     cache_control breakpoint. This is the prefix that stays byte-for-byte
	//     identical across turns (tools definitions + base system prompt).
	//   - Optionally, a trailing non-cached block for per-turn dynamic content
	//     (e.g. the current date in a <system-reminder> tag). This content
	//     changes every turn, so it must NOT be in the cached prefix.
	//
	// The last tool definition also gets a cache_control breakpoint, caching
	// the tool block independently — useful when the system prompt is small
	// or below the minimum cacheable threshold.
	//
	// Prefix order is tools → system → messages, so a breakpoint on the
	// cacheable system block caches everything through the system prompt (which
	// includes the tool definitions that precede it).
	var systemBlocks []anthropicSystemBlock
	if len(req.System) > 1 {
		// Multiple entries: the first entry is the static base system prompt
		// (cacheable); remaining entries are dynamic (date, compaction summary).
		// Cache only the static first block.
		systemBlocks = append(systemBlocks, anthropicSystemBlock{
			Type:         "text",
			Text:         req.System[0],
			CacheControl: &anthropicCacheControl{Type: "ephemeral"},
		})
		for _, s := range req.System[1:] {
			systemBlocks = append(systemBlocks, anthropicSystemBlock{
				Type: "text",
				Text: s,
			})
		}
	} else {
		// Single entry or empty: join and cache as one block.
		systemBlocks = []anthropicSystemBlock{
			{Type: "text", Text: systemPrompt, CacheControl: &anthropicCacheControl{Type: "ephemeral"}},
		}
	}
	if len(tools) > 0 {
		tools[len(tools)-1].CacheControl = &anthropicCacheControl{Type: "ephemeral"}
	}

	// Cache the conversation prefix too: mark the end of the message history so
	// the (often large) prior messages read from cache across the many LLM calls
	// within a single agentic tool-use turn, instead of being reprocessed at each
	// step. This is the 3rd of Anthropic's 4 allowed breakpoints (tools and the
	// base system prompt are the other two).
	attachMessageCacheBreakpoint(messages)

	body := anthropicRequest{
		Model:       model,
		MaxTokens:   max(req.MaxTokens, 4096),
		System:      systemBlocks,
		Messages:    messages,
		Tools:       tools,
		Stream:      true,
		Temperature: req.Temperature,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Derive a cancellable request context so the idle watchdog in streamEvents
	// can abort a silently-stalled stream. On any early return (before the reader
	// goroutine takes ownership) the deferred guard cancels it so it never leaks.
	reqCtx, reqCancel := context.WithCancel(ctx)
	streamStarted := false
	defer func() {
		if !streamStarted {
			reqCancel()
		}
	}()

	httpReq, err := http.NewRequestWithContext(reqCtx, "POST", strings.TrimRight(p.baseURL, "/")+"/messages", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: 600 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	slog.Info("anthropic stream connected", "model", model, "status", resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, NewAPIError("anthropic", resp, string(body))
	}

	ch := make(chan StreamEvent, 256)
	streamStarted = true
	go p.streamEvents(resp.Body, ch, reqCancel)
	return ch, nil
}

func (p *AnthropicProvider) streamEvents(body io.ReadCloser, ch chan<- StreamEvent, cancel context.CancelFunc) {
	defer body.Close()
	defer close(ch)
	defer cancel()

	// Idle watchdog: if the stream goes silent for streamIdleTimeout, cancel the
	// request context so the blocked read unblocks and the stream ends, instead
	// of hanging until the 600s HTTP client timeout. Reset on every line received.
	idle := time.AfterFunc(streamIdleTimeout, cancel)
	defer idle.Stop()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var currentToolID string
	var currentToolName string
	usage := TokenUsage{}
	usageDirty := false

	for scanner.Scan() {
		idle.Reset(streamIdleTimeout)
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var evt anthropicEvent
		if err := json.Unmarshal([]byte(data), &evt); err != nil {
			continue
		}

		switch evt.Type {
		case "message_start":
			if evt.Message != nil && evt.Message.Usage != nil {
				usage.InputTokens = evt.Message.Usage.InputTokens
				usage.OutputTokens = evt.Message.Usage.OutputTokens
				usage.CacheReadTokens = evt.Message.Usage.CacheReadInputTokens
				usage.CacheWriteTokens = evt.Message.Usage.CacheCreationInputTokens
				usageDirty = true
			}
		case "content_block_start":
			if evt.ContentBlock != nil {
				switch evt.ContentBlock.Type {
				case "tool_use":
					currentToolID = evt.ContentBlock.ID
					currentToolName = evt.ContentBlock.Name
					// Don't send the placeholder `{}` from content_block_start as ToolInput.
					// Anthropic always sends `"input":{}` here; the real input arrives
					// exclusively via input_json_delta events. Sending `{}` would prepend
					// it to the accumulated delta bytes, producing invalid JSON.
					ch <- StreamEvent{
						Type:       EventToolCallStart,
						ToolCallID: currentToolID,
						ToolName:   currentToolName,
					}
				case "redacted_thinking":
					// A redacted thinking block has no text deltas — only a signature
					// delivered on the content_block_start event. Emit an empty reasoning
					// event (so the loop creates a reasoning part) followed by the
					// signature, so the block is round-tripped to the API on later turns.
					ch <- StreamEvent{Type: EventReasoning, Text: ""}
					if evt.ContentBlock.Signature != "" {
						ch <- StreamEvent{Type: EventReasoningSignature, Signature: evt.ContentBlock.Signature}
					}
				}
			}
		case "content_block_delta":
			if evt.Delta != nil {
				switch evt.Delta.Type {
				case "text_delta":
					ch <- StreamEvent{Type: EventTextDelta, Text: evt.Delta.Text}
				case "input_json_delta":
					if currentToolID != "" {
						ch <- StreamEvent{
							Type:       EventToolCallDelta,
							ToolCallID: currentToolID,
							ToolName:   currentToolName,
							ToolInput:  []byte(evt.Delta.PartialJson),
						}
					}
				case "thinking_delta":
					ch <- StreamEvent{Type: EventReasoning, Text: evt.Delta.Thinking}
				case "signature_delta":
					ch <- StreamEvent{Type: EventReasoningSignature, Signature: evt.Delta.Signature}
				}
			}
		case "content_block_stop":
			if currentToolID != "" {
				ch <- StreamEvent{Type: EventToolCallEnd, ToolCallID: currentToolID, ToolName: currentToolName}
				currentToolID = ""
				currentToolName = ""
			}
		case "message_stop":
			// Emit usage here; finish was already emitted by message_delta.
			if usageDirty {
				u := usage
				ch <- StreamEvent{Type: EventUsage, Usage: &u}
				usageDirty = false
			}
		case "message_delta":
			if evt.Usage != nil {
				// message_delta carries the final OutputTokens count.
				if evt.Usage.OutputTokens > 0 {
					usage.OutputTokens = evt.Usage.OutputTokens
					usageDirty = true
				}
			}
			if evt.Delta != nil && evt.Delta.StopReason != "" {
				// Normalise "max_tokens" → "length" for cross-provider consistency.
				reason := evt.Delta.StopReason
				if reason == "max_tokens" {
					reason = "length"
				}
				ch <- StreamEvent{Type: EventFinish, FinishReason: &reason}
			}
		case "error":
			ch <- StreamEvent{Type: EventError, Error: evt.Error}
		}
	}
}

// Anthropic API types

type anthropicRequest struct {
	Model       string                 `json:"model"`
	MaxTokens   int                    `json:"max_tokens"`
	System      []anthropicSystemBlock `json:"system,omitempty"`
	Messages    []anthropicMessage     `json:"messages"`
	Tools       []anthropicTool        `json:"tools,omitempty"`
	Stream      bool                   `json:"stream"`
	Temperature float64                `json:"temperature,omitempty"`
}

// anthropicSystemBlock is a content block within the system field. Anthropic
// requires the system field to be an array of blocks (not a plain string) to
// attach cache_control markers for prompt caching.
type anthropicSystemBlock struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

// anthropicCacheControl marks a content block or tool as cacheable. When set
// to {"type": "ephemeral"}, Anthropic stores the KV cache for the prefix up to
// and including that block, enabling ~90% cost reduction on repeated prefixes.
type anthropicCacheControl struct {
	Type string `json:"type"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// attachMessageCacheBreakpoint adds a prompt-cache breakpoint at the end of the
// message history by attaching cache_control to the final content block of the
// last message. Anthropic caches the whole prefix up to that point, so within an
// agentic tool-use turn — where each successive LLM call only appends new tool
// results to the prior messages — the earlier (often large) messages read from
// cache instead of being reprocessed every step.
//
// Content may be a []map[string]any (blocks built here), a []any (blocks decoded
// from stored JSON), or a plain string (which is normalized into one text block
// so the marker has somewhere to live). A non-empty content is required; anything
// else is left untouched.
func attachMessageCacheBreakpoint(messages []anthropicMessage) {
	if len(messages) == 0 {
		return
	}
	cc := map[string]any{"type": "ephemeral"}
	last := &messages[len(messages)-1]
	switch c := last.Content.(type) {
	case []map[string]any:
		if len(c) > 0 {
			c[len(c)-1]["cache_control"] = cc
		}
	case []any:
		if len(c) > 0 {
			if block, ok := c[len(c)-1].(map[string]any); ok {
				block["cache_control"] = cc
			}
		}
	case string:
		if c != "" {
			last.Content = []map[string]any{{"type": "text", "text": c, "cache_control": cc}}
		}
	}
}

type anthropicTool struct {
	Name         string                 `json:"name"`
	Description  string                 `json:"description"`
	InputSchema  json.RawMessage        `json:"input_schema"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicEvent struct {
	Type         string                 `json:"type"`
	Index        int                    `json:"index,omitempty"`
	Message      *anthropicMessageInfo  `json:"message,omitempty"`
	ContentBlock *anthropicContentBlock `json:"content_block,omitempty"`
	Delta        *anthropicDelta        `json:"delta,omitempty"`
	Usage        *anthropicUsage        `json:"usage,omitempty"`
	Error        string                 `json:"error,omitempty"`
}

type anthropicMessageInfo struct {
	Usage *anthropicUsage `json:"usage,omitempty"`
}

type anthropicUsage struct {
	InputTokens              int `json:"input_tokens,omitempty"`
	OutputTokens             int `json:"output_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

type anthropicContentBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	Signature string          `json:"signature,omitempty"`
}

type anthropicDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	PartialJson string `json:"partial_json,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	Signature   string `json:"signature,omitempty"`
	StopReason  string `json:"stop_reason,omitempty"`
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
