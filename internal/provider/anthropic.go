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
			MaxOutputTokens: m.MaxOutputTokens,
		})
	}
	return all
}

// anthropicThinkingBlock renders one stored reasoning block back into the
// content array. A redacted block is not a thinking block with a missing
// signature: it has its own type and carries its payload in `data`. Sending it
// as a thinking block — or dropping it — breaks the sequence the API checks
// against what the model originally generated, and is rejected with a 400.
func anthropicThinkingBlock(rp ReasoningPart) map[string]any {
	if rp.RedactedData != "" {
		return map[string]any{
			"type": "redacted_thinking",
			"data": rp.RedactedData,
		}
	}
	return map[string]any{
		"type":      "thinking",
		"thinking":  rp.Text,
		"signature": rp.Signature,
	}
}

// anthropicCatalogModel looks up a model's catalogued facts. A model the catalog
// does not know — a future ID, or one reached through a proxy — resolves to
// nothing, and callers fall back to what is safe for any model.
func anthropicCatalogModel(id string) (CatalogModel, bool) {
	for _, m := range AnthropicModels {
		if m.ID == id {
			return m, true
		}
	}
	return CatalogModel{}, false
}

// thinkingConfigFor returns the thinking configuration for a model, or nil when
// none should be sent.
//
// The mode is a per-model fact and lives in the catalog: 4.6 and later take
// `adaptive`, where the model decides how much to think and reasons between
// tool calls without a beta header, while earlier models take only a fixed
// budget that has to be sized against max_tokens. A model the catalog does not
// know — a future ID, or one reached through a proxy — gets nothing, since
// sending a mode a model rejects fails the whole request.
//
// `display: "summarized"` is what makes the reasoning readable. It defaults to
// "omitted" on 4.7 and later, which returns thinking blocks with empty text —
// correct on the wire, but it leaves ogcode's reasoning drawer with nothing in
// it and the user watching a long pause before any output appears.
//
// The configuration is rendered into the prompt, so it must stay stable for the
// life of a cached conversation. Keying it to the model alone keeps it so.
func thinkingConfigFor(model string) *anthropicThinking {
	m, ok := anthropicCatalogModel(model)
	if !ok || m.Thinking != "adaptive" {
		return nil
	}
	return &anthropicThinking{Type: "adaptive", Display: "summarized"}
}

// isToolResult reports whether a message carries a tool result rather than
// conversational content. The internal format marks these two ways — the role
// the OpenAI wire format uses, and the id the result answers — and a message
// that has either must never be emitted as an Anthropic role.
func isToolResult(m ModelMessage) bool {
	return m.Role == "tool" || m.ToolCallID != ""
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

		// Tool results are recognised by role as well as by id. Anthropic has no
		// "tool" role — it carries results as tool_result blocks inside a user
		// message — so a message that reached here as role "tool" and fell
		// through to the generic branch below would be sent verbatim and
		// rejected with `unknown variant \`tool\``. Keying only on ToolCallID
		// left exactly that hole open for any result whose id went missing.
		if isToolResult(m) {
			// Tool result: collect consecutive tool results into one user message
			// (Anthropic requires alternating roles, so all results for one turn go together)
			var blocks []map[string]any
			for i < len(req.Messages) && isToolResult(req.Messages[i]) {
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
				blocks = append(blocks, anthropicThinkingBlock(rp))
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
					blocks = append(blocks, anthropicThinkingBlock(rp))
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
			CacheControl: p.staticPrefixCacheControl(),
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
			{Type: "text", Text: systemPrompt, CacheControl: p.staticPrefixCacheControl()},
		}
	}
	if len(tools) > 0 {
		tools[len(tools)-1].CacheControl = p.staticPrefixCacheControl()
	}

	// Cache the conversation prefix too: mark the end of the message history so
	// the (often large) prior messages read from cache across the many LLM calls
	// within a single agentic tool-use turn, instead of being reprocessed at each
	// step. This is the 3rd of Anthropic's 4 allowed breakpoints (tools and the
	// base system prompt are the other two).
	attachMessageCacheBreakpoint(messages)

	// After the breakpoint, deliberately: the guidance is the one part of the
	// tail that changes when the user steers, so it belongs outside the cached
	// region rather than at its edge.
	messages = appendGuidanceBlock(messages, req.Guidance)

	var thinking *anthropicThinking
	if req.Thinking {
		thinking = thinkingConfigFor(model)
	}
	temperature := req.Temperature
	if thinking != nil {
		// Sampling parameters and thinking do not go together: the models that
		// take a thinking configuration reject temperature outright (4.7 and
		// later) or restrict it while thinking. Thinking is the more useful of
		// the two for an agent loop, so it wins.
		temperature = 0
	}

	maxTokens := max(req.MaxTokens, 4096)
	if thinking != nil && req.MaxTokens == 0 {
		// Thinking is billed and counted as output: it shares max_tokens with the
		// answer. Against the 4096 default a long chain of reasoning eats the room
		// the answer needs and the turn stops mid-sentence, so a thinking request
		// gets the model's own published ceiling instead. max_tokens is a bound,
		// not an allocation — nothing is charged for room left unused.
		if m, ok := anthropicCatalogModel(model); ok && m.MaxOutputTokens > 0 {
			maxTokens = m.MaxOutputTokens
		}
	}

	body := anthropicRequest{
		Model:       model,
		MaxTokens:   maxTokens,
		System:      systemBlocks,
		Messages:    messages,
		Tools:       tools,
		Stream:      true,
		Temperature: temperature,
		Thinking:    thinking,
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
	if p.usesExtendedCacheTTL() {
		httpReq.Header.Set("anthropic-beta", extendedCacheTTLBeta)
	}

	resp, err := streamHTTPClient.Do(httpReq)
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
	// request context so the blocked read unblocks and the stream ends instead of
	// hanging. It wraps the body so it resets on bytes read off the wire, not on
	// lines handed downstream — see idleWatchdog.
	// Anthropic emits tool-call arguments as a run of input_json_delta events, so
	// a working stream is never quiet for long: the tight budget is the default,
	// unless the operator has set one of their own (see resolveIdleTimeout).
	idle := newIdleWatchdog(body, cancel, resolveIdleTimeout(streamIdleTimeout))
	defer idle.Stop()

	scanner := bufio.NewScanner(idle)
	scanner.Buffer(make([]byte, 0, 64*1024), streamMaxLineBytes)

	var currentToolID string
	var currentToolName string
	usage := TokenUsage{}
	usageDirty := false

	for scanner.Scan() {
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
				case "thinking":
					// Opening a thinking block. When the model's thinking text is
					// withheld — `display: "omitted"`, the default on current models
					// — the block produces no thinking_delta at all, and this event
					// plus the closing signature_delta are the only evidence it
					// existed. It still has to be replayed, so the boundary is
					// announced rather than inferred from the first text delta.
					ch <- StreamEvent{Type: EventReasoningStart}
				case "redacted_thinking":
					// A redacted thinking block has no text deltas and no signature —
					// its entire content is the opaque `data` payload delivered on the
					// content_block_start event. It must be round-tripped as a
					// redacted_thinking block, so it gets its own event rather than
					// being flattened into a signature on an empty thinking block.
					if evt.ContentBlock.Data != "" {
						ch <- StreamEvent{Type: EventReasoningRedacted, RedactedData: evt.ContentBlock.Data}
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

	// The scan can stop for reasons other than end-of-stream: a read error, a
	// cancelled request, an over-long line. Report it — returning silently would
	// close the channel with no finish event and no error, leaving the agent loop
	// to guess at what went wrong.
	if err := scanner.Err(); err != nil {
		msg := describeStreamReadError(err, idle.Fired(), idle.Timeout())
		// Count it against the IPv6 path if that is what gave out, so a host
		// whose IPv6 keeps dropping established connections stops using it.
		noteIPv6Failure(err)
		slog.Warn("anthropic stream read failed", "err", err, "idleTimeout", idle.Fired())
		ch <- StreamEvent{Type: EventError, Error: msg, Err: err}
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
	Thinking    *anthropicThinking     `json:"thinking,omitempty"`
}

// anthropicThinking is the request's thinking configuration. Type is the mode;
// Display asks for the reasoning to come back readable rather than as empty
// blocks.
type anthropicThinking struct {
	Type    string `json:"type"`
	Display string `json:"display,omitempty"`
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
	// TTL selects the cache lifetime: "" is the default 5 minutes, "1h" the
	// extended one. Sending it requires the extended-cache-ttl beta header.
	TTL string `json:"ttl,omitempty"`
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
// extendedCacheTTLBeta is the header value that unlocks the 1-hour prompt cache.
const extendedCacheTTLBeta = "extended-cache-ttl-2025-04-11"

// canonicalAnthropicHost is the endpoint the extended TTL is enabled against.
const canonicalAnthropicHost = "api.anthropic.com"

// usesExtendedCacheTTL reports whether this provider may ask for the 1-hour
// cache.
//
// Why 1 hour at all: the default cache lives 5 minutes, and an agent loop
// routinely goes longer than that between requests — a full test run, a build, a
// deep_search (its own timeout is 180s), or simply the developer reading a diff
// before replying. Every expiry re-reads the whole cacheable prefix, which for
// the build agent is roughly 7.8k tokens of tools plus system before any
// history. Writes cost 2x instead of 1.25x, reads stay at 0.1x either way, so
// the trade pays for itself after about one extra read — and this prefix is
// re-read on every step of every turn.
//
// Why only the canonical host: ANTHROPIC_BASE_URL points a number of proxies and
// gateways at this code path. An unknown beta header is ignored harmlessly, but
// an unknown "ttl" field inside cache_control can be a hard 400, which would
// take the user's whole session down to save them tokens. The default TTL still
// applies there, so a proxy loses the improvement, not the request.
func (p *AnthropicProvider) usesExtendedCacheTTL() bool {
	return strings.Contains(strings.ToLower(p.baseURL), canonicalAnthropicHost)
}

// staticPrefixCacheControl is the breakpoint for the parts of the request that
// do not change within a session — the tool definitions and the base system
// block. These are what the extended TTL is for.
//
// The message breakpoint deliberately keeps the 5-minute default: it is rewritten
// on every step, so a 2x write on content that is superseded seconds later is
// the one place the longer TTL costs more than it returns. Anthropic also
// requires 1-hour breakpoints to precede 5-minute ones in the prefix, and the
// prefix order is tools -> system -> messages, so this arrangement is the only
// valid way round.
func (p *AnthropicProvider) staticPrefixCacheControl() *anthropicCacheControl {
	cc := &anthropicCacheControl{Type: "ephemeral"}
	if p.usesExtendedCacheTTL() {
		cc.TTL = "1h"
	}
	return cc
}

// PlacesGuidance reports that this provider positions mid-loop guidance itself.
//
// Anthropic is the clean case for it. Tool results already travel as blocks
// INSIDE a user message here, so the guidance can be one more block appended to
// that same message — no new message, so nothing about message roles changes and
// there is no alternation question to get wrong. The OpenAI-shaped providers
// carry a tool result as its own role:"tool" message, which cannot hold
// unrelated text, so they keep the caller's fallback until that is done
// separately.
func (p *AnthropicProvider) PlacesGuidance() bool { return true }

// appendGuidanceBlock puts the user's mid-loop guidance at the very end of the
// conversation.
//
// The point is where it is NOT. The caller's fallback appends guidance to the
// turn's first user message, and a prompt cache reuses a request only as far as
// the bytes still match, so changing the first message throws away every tool
// result behind it — the whole turn's reading, re-processed at full price on the
// step immediately after the user steers. Appended at the end there is nothing
// behind it to invalidate.
//
// It must run AFTER attachMessageCacheBreakpoint, so the breakpoint lands on the
// content that is stable and the guidance sits outside the cached region. With
// the breakpoint on the guidance instead, changing the guidance would mean no
// exact match at the breakpoint and the hit would depend on the automatic
// lookback finding an earlier one.
//
// A trailing text block after tool_result blocks is the documented shape: the
// API requires tool_result to come first in a user message, not to be alone in
// it. When the conversation happens to end on an assistant message the guidance
// becomes its own user message, which is ordinary alternation.
func appendGuidanceBlock(messages []anthropicMessage, guidance string) []anthropicMessage {
	guidance = strings.TrimSpace(guidance)
	if guidance == "" {
		return messages
	}
	block := map[string]any{"type": "text", "text": guidance}

	if len(messages) == 0 || messages[len(messages)-1].Role != "user" {
		return append(messages, anthropicMessage{
			Role:    "user",
			Content: []map[string]any{block},
		})
	}

	last := &messages[len(messages)-1]
	switch c := last.Content.(type) {
	case []map[string]any:
		last.Content = append(c, block)
	case []any:
		last.Content = append(c, block)
	case string:
		blocks := []map[string]any{}
		if c != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": c})
		}
		last.Content = append(blocks, block)
	default:
		// An unexpected content shape is not worth guessing at: a new user
		// message is always valid, and losing the ideal placement costs a
		// message, not the request.
		return append(messages, anthropicMessage{
			Role:    "user",
			Content: []map[string]any{block},
		})
	}
	return messages
}

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
	// Data is the opaque payload of a redacted_thinking block. Redacted
	// blocks carry no signature and no text — this field is the whole block.
	Data string `json:"data,omitempty"`
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
