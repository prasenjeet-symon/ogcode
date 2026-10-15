package provider

// Tool-call extra over the OpenAI-compatible protocol: the opaque, per-tool-call
// payload a host attaches to each entry of tool_calls and requires echoed back
// verbatim when the assistant message is replayed from history.
//
// Google's thinking models, served through the OpenAI-compatible endpoint at
// generativelanguage.googleapis.com, attach
// extra_content: {"google": {"thought_signature": "<blob>"}} to every tool call
// of an assistant message. On the following request the turn is resent from
// history, and Gemini requires that exact blob back on the matching call — it
// is the model's own signed record of what it was thinking when it chose the
// tool. Drop it and the request is rejected with 400 INVALID_ARGUMENT, so the
// first tool-using turn succeeds and every one after it fails.
//
// No other host uses this field. OpenAI, OpenRouter and the vendor-compatible
// endpoints either ignore it or reject an unknown field, so it is captured and
// resent only for Gemini.
//
// Prompt caching: an assistant message is resent on every later step of the
// turn, so its extra must come out byte-identical each time. The stored blob is
// the raw JSON Gemini sent, replayed as is.

// EchoesToolExtra reports whether this endpoint attaches a per-tool-call extra
// that must be replayed, so the stream reader captures it only where it will be
// sent back and the request builder attaches it only there too.
func (p *OpenAIProvider) EchoesToolExtra() bool {
	return p.effortHost() == hostGemini
}
