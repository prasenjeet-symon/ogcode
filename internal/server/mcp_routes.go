package server

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/prasenjeet-symon/ogcode/internal/config"
	"github.com/prasenjeet-symon/ogcode/internal/mcp"
)

// mcpServerSummary is one MCP server as the settings screen needs it: how it is
// configured (name, transport, target, how it authenticates, and where the
// config lives) joined with its live runtime state (connected, tool count, any
// connect error). Never carries headers, tokens, or other secrets.
type mcpServerSummary struct {
	Name      string `json:"name"`
	Transport string `json:"transport"`
	Target    string `json:"target"`
	Scope     string `json:"scope"`
	Auth      string `json:"auth"`
	Enabled   bool   `json:"enabled"`
	Connected bool   `json:"connected"`
	ToolCount int    `json:"toolCount"`
	Error     string `json:"error,omitempty"`
}

// handleListMCP returns every configured MCP server — enabled or disabled —
// joined with its live status. Like the skills screen, disabled servers are
// listed (switched off) so they can be turned back on.
func (s *Server) handleListMCP(w http.ResponseWriter, r *http.Request) {
	if s.mcpManager == nil {
		writeJSON(w, http.StatusOK, []mcpServerSummary{})
		return
	}
	servers := config.Load(s.dir).MCP
	status := s.mcpStatusByName()

	out := make([]mcpServerSummary, 0, len(servers))
	for name, sc := range servers {
		out = append(out, s.mcpSummary(name, sc, status[name]))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, http.StatusOK, out)
}

// handleSetMCPEnabled turns one MCP server on or off for this project. It writes
// the choice into the project ogcode.json and applies it live: disabling closes
// the connection and removes the server's tools from the agent's toolset (so
// they stop reaching the prompt), enabling reconnects and registers them. Auth
// tokens and the server's own config are left untouched either way.
func (s *Server) handleSetMCPEnabled(w http.ResponseWriter, r *http.Request) {
	if s.mcpManager == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "mcp is not configured"})
		return
	}
	name := strings.TrimSpace(chi.URLParam(r, "name"))
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "server name is required"})
		return
	}

	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if _, ok := config.Load(s.dir).MCP[name]; !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no MCP server named " + name})
		return
	}

	// Persist first, then read back the effective config so the manager dials the
	// merged definition (which, when re-enabling a global server, comes from the
	// pinned or global entry).
	if _, err := config.SetMCPDisabled(s.dir, name, !body.Enabled); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	sc := config.Load(s.dir).MCP[name]

	added, removed, applyErr := s.mcpManager.SetServerEnabled(r.Context(), name, sc, body.Enabled)
	for _, t := range added {
		s.toolRegistry.Register(t)
	}
	if len(removed) > 0 {
		s.toolRegistry.Remove(removed...)
	}
	// A failed *enable* is not a request failure: the server is enabled in config
	// but could not connect right now (server down, or first-time OAuth). The
	// summary carries the error, matching how a server that fails at startup is
	// shown — enabled, not connected. Everything else already succeeded.

	summary := s.mcpSummary(name, sc, s.mcpStatusByName()[name])
	if applyErr != nil && summary.Error == "" {
		summary.Error = applyErr.Error()
	}
	writeJSON(w, http.StatusOK, summary)
}

// mcpStatusByName indexes the manager's runtime statuses by server name.
func (s *Server) mcpStatusByName() map[string]mcp.ServerStatus {
	byName := map[string]mcp.ServerStatus{}
	for _, st := range s.mcpManager.Statuses() {
		byName[st.Name] = st
	}
	return byName
}

func (s *Server) mcpSummary(name string, sc config.MCPServerConfig, st mcp.ServerStatus) mcpServerSummary {
	sum := mcpServerSummary{
		Name:      name,
		Transport: mcpTransport(sc),
		Target:    mcpTarget(sc),
		Scope:     config.MCPScope(s.dir, name),
		Auth:      mcpAuth(sc),
		Enabled:   !sc.Disabled,
	}
	// Runtime status only means something for an enabled server; a disabled one
	// is never dialled and has no entry.
	if sum.Enabled {
		sum.Connected = st.Connected
		sum.ToolCount = st.ToolCount
		sum.Error = st.Err
	}
	return sum
}

// mcpTransport reports the transport ogcode will use for sc, resolving the
// inferred case (empty Transport → stdio when a command is set, otherwise http)
// the same way the manager does.
func mcpTransport(sc config.MCPServerConfig) string {
	switch strings.ToLower(sc.Transport) {
	case "stdio":
		return "stdio"
	case "streamable-http", "http", "https":
		return "http"
	case "sse":
		return "sse"
	default:
		if sc.Command != "" {
			return "stdio"
		}
		if sc.URL != "" {
			return "http"
		}
		return "unknown"
	}
}

// mcpTarget is a human-identifiable pointer to what the server is — the command
// line for stdio, the URL for HTTP/SSE. Never a secret (headers/tokens excluded).
func mcpTarget(sc config.MCPServerConfig) string {
	if sc.Command != "" {
		if len(sc.Args) > 0 {
			return sc.Command + " " + strings.Join(sc.Args, " ")
		}
		return sc.Command
	}
	return sc.URL
}

// mcpAuth classifies how the server authenticates, so the UI can reassure the
// user that disabling keeps their credentials: "oauth" (tokens are on disk and
// survive a disable), "headers" (a static token in config), or "none".
func mcpAuth(sc config.MCPServerConfig) string {
	if sc.URL != "" && len(sc.Headers) == 0 && (sc.Auth == nil || !sc.Auth.SkipOAuth) {
		return "oauth"
	}
	if len(sc.Headers) > 0 {
		return "headers"
	}
	return "none"
}
