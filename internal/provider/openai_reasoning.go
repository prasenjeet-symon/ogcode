package provider

// Reasoning echo over the OpenAI-compatible protocol: handing each assistant
// message's own reasoning back to the host on later requests.
//
// A thinking model's reasoning streams in beside its answer, and several
// vendors want it back on the assistant message, because their models are
// trained to carry it across the steps of a tool-using turn (each vendor's docs,
// checked 2026-10-07):
//
//   - DeepSeek requires it. With tools in the request, every earlier assistant
//     message must carry its reasoning_content or the API answers 400 — and
//     thinking is on by default for its current models.
//   - Kimi K3 (Moonshot) requires the complete assistant message back,
//     reasoning_content included.
//   - Z.ai wants GLM's reasoning_content returned with the tool results.
//   - Qwen Cloud reads it wherever preserve_thinking is on — by default on some
//     of its models — and documents it on the assistant message.
//   - Ollama reads it as `reasoning` and hands it to the model's template as
//     the message's thinking — gpt-oss's harmony format expects its chain of
//     thought back between tool calls. A daemon too old to know the field
//     ignores it, and OGX's gateway relays the body to Ollama's cloud as is.
//
// Every other host is sent nothing. OpenAI, Groq, Fireworks and Mistral answer
// a message field they do not know with a 400, and an endpoint this file does
// not recognise may too. OpenRouter wants its own reasoning_details echoed
// instead, which this client does not keep.
//
// Prompt caching: an assistant message is resent on every later step of the
// turn, so its reasoning must come out byte-identical each time, or each step
// re-reads the history behind it at full price. The text is the stored
// reasoning part, final once its step ends, and the field is chosen per host,
// never per request.

// reasoningEcho is where a host reads an assistant message's reasoning back.
type reasoningEcho int

const (
	echoNone reasoningEcho = iota
	// echoReasoningContent is the vendor APIs' reasoning_content, sent on every
	// assistant message — empty where the message has none, because DeepSeek
	// rejects a tool request whose assistant message lacks the field, and
	// accepts it empty.
	echoReasoningContent
	// echoReasoning is Ollama's reasoning, sent only where there is text.
	echoReasoning
)

// reasoningEcho names the field this endpoint reads reasoning back from.
func (p *OpenAIProvider) reasoningEcho() reasoningEcho {
	switch p.effortHost() {
	case hostDeepSeek, hostZAI, hostMoonshot, hostQwen:
		return echoReasoningContent
	case hostOllama:
		return echoReasoning
	}
	return echoNone
}

// EchoesReasoning reports whether this endpoint takes reasoning back, so the
// agent loop attaches it only where it will be sent.
func (p *OpenAIProvider) EchoesReasoning() bool {
	return p.reasoningEcho() != echoNone
}

// write puts an assistant message's reasoning where the host reads it.
func (e reasoningEcho) write(msg *oaiMessage, text string) {
	switch e {
	case echoReasoningContent:
		msg.ReasoningContent = &text
	case echoReasoning:
		msg.Reasoning = text
	}
}
