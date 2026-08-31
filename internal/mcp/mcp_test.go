package mcp_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prasenjeet-symon/ogcode/internal/config"
	mcp "github.com/prasenjeet-symon/ogcode/internal/mcp"
	"github.com/prasenjeet-symon/ogcode/internal/tool"
)

// startTestServer wires an in-memory MCP server that exposes one echo tool and
// returns the client session connected to it. Because Manager.New builds
// transports from config.MCPServerConfig (stdio/HTTP only), this test drives the
// SDK directly and exercises the ogcode adapter (mcpTool) against a real
// server session. The transport plumbing itself is covered by the SDK's own
// tests; here we cover the ogcode-specific glue: schema re-marshalling, tool
// call, and result rendering.
func startTestServer(t *testing.T) *sdkmcp.ClientSession {
	t.Helper()
	c1, s1 := sdkmcp.NewInMemoryTransports()
	srv := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "test", Version: "1"}, nil)
	srv.AddTool(&sdkmcp.Tool{
		Name:        "echo",
		Description: "echo back the input text",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{"type": "string"},
			},
			"required": []string{"text"},
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		var args struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return &sdkmcp.CallToolResult{IsError: true, Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "bad args"}}}, nil
		}
		if args.Text != "" {
			return &sdkmcp.CallToolResult{
				Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: args.Text}},
			}, nil
		}
		return &sdkmcp.CallToolResult{IsError: true, Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "missing text"}}}, nil
	})
	ss, err := srv.Connect(context.Background(), s1, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}

	cli := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "1"}, nil)
	cs, err := cli.Connect(context.Background(), c1, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	// Keep the server session alive for the test; closing the client tears it down.
	t.Cleanup(func() {
		_ = cs.Close()
		_ = ss.Close()
	})
	return cs
}

func TestMCPTool_AdaptsAndCalls(t *testing.T) {
	cs := startTestServer(t)

	ctx := context.Background()
	out, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(out.Tools) != 1 || out.Tools[0].Name != "echo" {
		t.Fatalf("got tools %+v, want one named echo", out.Tools)
	}

	adapter := mcp.NewTool("mcp_test_echo", "test", out.Tools[0], cs)
	if adapter.ID() != "mcp_test_echo" {
		t.Errorf("ID: got %q want mcp_test_echo", adapter.ID())
	}
	if adapter.Description() != "echo back the input text" {
		t.Errorf("Description: got %q", adapter.Description())
	}
	// Parameters must be valid JSON (the InputSchema re-marshalled).
	var schema map[string]any
	if err := json.Unmarshal(adapter.Parameters(), &schema); err != nil {
		t.Fatalf("Parameters not valid JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Errorf("schema type: got %v want object", schema["type"])
	}

	args, _ := json.Marshal(map[string]string{"text": "hello"})
	res, err := adapter.Execute(ctx, args, tool.Context{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Output != "hello" {
		t.Errorf("output: got %q want hello", res.Output)
	}
}

func TestManager_EmptyConfig(t *testing.T) {
	m, err := mcp.New(context.Background(), &config.Config{})
	if err != nil {
		t.Fatalf("New with empty config: %v", err)
	}
	if len(m.Tools()) != 0 {
		t.Errorf("empty config should yield no tools, got %d", len(m.Tools()))
	}
	if err := m.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// TestManager_NewDoesNotConnect pins the lazy-connect contract: New must NOT
// dial any server — it only builds the Manager (and binds the OAuth receiver).
// Tools remain empty until Connect is called. If this regresses, server startup
// blocks again on slow/OAuth MCP servers before the HTTP listener is up.
func TestManager_NewDoesNotConnect(t *testing.T) {
	cfg := &config.Config{
		MCP: map[string]config.MCPServerConfig{
			"fake": {Command: "false"}, // stdio server that would immediately fail
		},
	}
	m, err := mcp.New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if len(m.Tools()) != 0 {
		t.Errorf("New must not connect or discover tools, got %d tools", len(m.Tools()))
	}
	// Connect is what actually dials; here "false" exits non-zero so it fails,
	// but the point is that Connect (not New) is where the failure surfaces.
	if _, err := m.Connect(context.Background()); err == nil {
		t.Error("Connect to a server running `false` should fail")
	}
	if err := m.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// TestManager_ConnectIsIdempotent ensures a second Connect is a no-op: it
// returns no new tools and no error, regardless of how the first call went.
func TestManager_ConnectIsIdempotent(t *testing.T) {
	cfg := &config.Config{
		MCP: map[string]config.MCPServerConfig{
			"fake": {Command: "false"},
		},
	}
	m, _ := mcp.New(context.Background(), cfg)
	_, _ = m.Connect(context.Background())
	tools2, err2 := m.Connect(context.Background())
	if err2 != nil {
		t.Errorf("second Connect should be a no-op with nil error, got err=%v", err2)
	}
	if len(tools2) != 0 {
		t.Errorf("second Connect should return no new tools, got %d", len(tools2))
	}
	_ = m.Close()
}

// startImageTestServer wires an in-memory MCP server whose "snapshot" tool
// returns a small PNG as ImageContent, plus a text label. It exercises the
// adapter's image-to-temp-file path: the bytes must be written to disk and
// the result must point at that path rather than inlining base64.
func startImageTestServer(t *testing.T) *sdkmcp.ClientSession {
	t.Helper()
	c1, s1 := sdkmcp.NewInMemoryTransports()
	srv := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "img-test", Version: "1"}, nil)
	srv.AddTool(&sdkmcp.Tool{
		Name:        "snapshot",
		Description: "return a tiny PNG plus a caption",
		InputSchema: map[string]any{"type": "object"},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		// A minimal 1x1 red PNG.
		png := []byte{
			0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // signature
			0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
			0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
			0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
			0xDE, 0x00, 0x00, 0x00, 0x0C, 0x49, 0x44, 0x41,
			0x54, 0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00,
			0x00, 0x00, 0x03, 0x00, 0x01, 0x5B, 0x4D, 0x8D,
			0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44,
			0xAE, 0x42, 0x60, 0x82,
		}
		return &sdkmcp.CallToolResult{
			Content: []sdkmcp.Content{
				&sdkmcp.TextContent{Text: "caption: red pixel"},
				&sdkmcp.ImageContent{Data: png, MIMEType: "image/png"},
			},
		}, nil
	})
	ss, err := srv.Connect(context.Background(), s1, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	cli := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "img-client", Version: "1"}, nil)
	cs, err := cli.Connect(context.Background(), c1, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() {
		_ = cs.Close()
		_ = ss.Close()
	})
	return cs
}

func TestMCPTool_ImageWrittenToTempNotInlined(t *testing.T) {
	cs := startImageTestServer(t)

	ctx := context.Background()
	out, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}

	adapter := mcp.NewTool("mcp_test_snapshot", "test", out.Tools[0], cs)
	args, _ := json.Marshal(map[string]string{})
	res, err := adapter.Execute(ctx, args, tool.Context{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	// The image must NOT be inlined as a ResultImage — that would re-send a
	// base64 blob into the model context on every subsequent step.
	if res.Image != nil {
		t.Errorf("expected no inlined image, got ResultImage (MediaType=%s, %d base64 chars)",
			res.Image.MediaType, len(res.Image.Data))
	}

	// The output must mention the saved file path and the view_image hint.
	if !strings.Contains(res.Output, "view_image") {
		t.Errorf("output should instruct to use view_image, got: %s", res.Output)
	}
	if !strings.Contains(res.Output, "image/png") {
		t.Errorf("output should mention the MIME type, got: %s", res.Output)
	}

	// Extract the path and verify the file exists on disk with the PNG bytes.
	// The output line looks like: "Image exported to <path> (image/png, N bytes). ..."
	idx := strings.Index(res.Output, "Image exported to ")
	if idx < 0 {
		t.Fatalf("output should contain 'Image exported to', got: %s", res.Output)
	}
	rest := res.Output[idx+len("Image exported to "):]
	end := strings.Index(rest, " ")
	if end < 0 {
		t.Fatalf("could not parse path from output: %s", res.Output)
	}
	path := rest[:end]

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("temp image file should exist at %s: %v", path, err)
	}
	if info.Size() == 0 {
		t.Errorf("temp image file should not be empty: %s", path)
	}
	// The filename should carry a .png extension derived from the MIME type.
	if !strings.HasSuffix(path, ".png") {
		t.Errorf("expected a .png extension, got path: %s", path)
	}
	// The caption text must still flow through.
	if !strings.Contains(res.Output, "caption: red pixel") {
		t.Errorf("text content should be preserved, got: %s", res.Output)
	}
}
