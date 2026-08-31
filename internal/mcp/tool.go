package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// newMCPTool adapts a discovered MCP tool (t) from server into ogcode's
// tool.ToolDef under the host-side id. The session is captured so Execute can
// call back into the owning server. Parameters serialises the MCP tool's
// InputSchema (any-typed on the client side) into the json.RawMessage the host
// registry expects; Execute turns each content item in the MCP result into
// text or an image the host can render.
func newMCPTool(id, server string, t *mcp.Tool, session *mcp.ClientSession) tool.ToolDef {
	return mcpTool{id: id, server: server, spec: t, session: session}
}

// NewTool is the exported constructor for testing: it wraps a live session and
// a discovered tool into a ToolDef the same way Manager does internally.
func NewTool(id, server string, t *mcp.Tool, session *mcp.ClientSession) tool.ToolDef {
	return newMCPTool(id, server, t, session)
}

// mcpTool adapts a single MCP tool discovered from a server into ogcode's
// tool.ToolDef. It is immutable after construction; the live session is held by
// reference so Close on the Manager invalidates all tools at once.
type mcpTool struct {
	id      string
	server  string
	spec    *mcp.Tool
	session *mcp.ClientSession
}

func (mt mcpTool) ID() string          { return mt.id }
func (mt mcpTool) Description() string { return mt.spec.Description }

// Parameters returns the MCP tool's input schema as a json.RawMessage. The SDK
// stores InputSchema as any (unmarshalled into map[string]any on the client
// side), so we re-marshal it to get the stable JSON the host's tool registry
// hands to the model. A nil or malformed schema yields an empty object so the
// tool is still callable.
func (mt mcpTool) Parameters() json.RawMessage {
	if mt.spec.InputSchema == nil {
		return json.RawMessage("{}")
	}
	b, err := json.Marshal(mt.spec.InputSchema)
	if err != nil {
		return json.RawMessage("{}")
	}
	return b
}

// Execute calls the MCP tool and renders its result content into the host's
// tool.Result. Text content is concatenated with separators; image content is
// written to a temporary file and the agent is told the path so it can inspect
// the image on demand with the view_image tool — the bytes are never inlined
// into the model context (which would re-send a large base64 blob on every
// subsequent step of the turn). The first image wins; if a tool returns
// several, the rest are reported by count only. Errors from the server
// (IsError) and transport failures both become a Result whose Output carries
// the message and whose Denied flag is false — the host loop treats any
// non-empty Output as a normal completion, and the agent reads the error text.
func (mt mcpTool) Execute(ctx context.Context, input json.RawMessage, _ tool.Context) (tool.Result, error) {
	var args any
	if len(input) > 0 {
		if err := json.Unmarshal(input, &args); err != nil {
			return tool.Result{Output: fmt.Sprintf("invalid arguments: %v", err)}, nil
		}
	}
	out, err := mt.session.CallTool(ctx, &mcp.CallToolParams{Name: mt.spec.Name, Arguments: args})
	if err != nil {
		return tool.Result{Output: fmt.Sprintf("mcp call %q failed: %v", mt.id, err)}, nil
	}
	res := tool.Result{Title: mt.id}
	var parts []string
	if out.IsError {
		parts = append(parts, "[server error]")
	}
	imageCount := 0
	for _, c := range out.Content {
		switch content := c.(type) {
		case *mcp.TextContent:
			parts = append(parts, content.Text)
		case *mcp.ImageContent:
			imageCount++
			if imageCount == 1 {
				if path, perr := writeMCPImageToTemp(content); perr == nil {
					parts = append(parts, fmt.Sprintf(
						"Image exported to %s (%s, %d bytes). Use the view_image tool with this path to inspect it.",
						path, content.MIMEType, len(content.Data)))
				} else {
					parts = append(parts, fmt.Sprintf("Image content received (%s, %d bytes) but could not be written to disk: %v",
						content.MIMEType, len(content.Data), perr))
				}
			} else {
				parts = append(parts, fmt.Sprintf("Additional image %d (%s, %d bytes) not written; only the first image is kept.",
					imageCount, content.MIMEType, len(content.Data)))
			}
		case *mcp.EmbeddedResource:
			if content.Resource != nil && content.Resource.Text != "" {
				parts = append(parts, content.Resource.Text)
			}
		}
	}
	res.Output = strings.Join(parts, "\n")
	res.Output, res.Truncated = tool.TruncateOutput(res.Output, tool.KeepTail)
	return res, nil
}

// writeMCPImageToTemp persists a single MCP image content item to a temporary
// file and returns its path. The file lives under an ogcode-specific temp
// directory so it is easy to find and clean up. The filename extension is
// derived from the MIME type.
func writeMCPImageToTemp(c *mcp.ImageContent) (string, error) {
	dir, err := os.MkdirTemp("", "ogcode-mcp-image-*")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}
	ext := mimeExtension(c.MIMEType)
	path := filepath.Join(dir, "export"+ext)
	if err := os.WriteFile(path, c.Data, 0o644); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("write image: %w", err)
	}
	return path, nil
}

// mimeExtension returns a file extension (with leading dot) for common image
// MIME types, falling back to .bin for the rest.
func mimeExtension(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/bmp":
		return ".bmp"
	case "image/webp":
		return ".webp"
	case "image/svg+xml":
		return ".svg"
	default:
		return ".bin"
	}
}
