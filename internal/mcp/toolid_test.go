package mcp

import (
	"regexp"
	"strings"
	"testing"
)

// providerNamePattern is the constraint Anthropic, OpenAI and OpenAI-compatible
// gateways all place on a function name. NVIDIA (via OpenRouter) stated it
// verbatim when it rejected "mcp_penpot/export_shape": "Only a-z, A-Z, 0-9,
// underscores, and dashes are allowed."
var providerNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func TestToolID_ProviderSafe(t *testing.T) {
	cases := []struct {
		server string
		tool   string
		want   string
	}{
		// The exact pair from the reported 400.
		{"penpot", "export_shape", "mcp_penpot_export_shape"},
		{"Claude_Browser", "browser_batch", "mcp_Claude_Browser_browser_batch"},
		// MCP puts no charset restriction on names, so these are all legal
		// upstream and all used to produce an id the provider refused.
		{"my.server", "get.thing", "mcp_my_server_get_thing"},
		{"my server", "do it", "mcp_my_server_do_it"},
		{"a/b", "c:d", "mcp_a_b_c_d"},
		{"srv", "tool@v2", "mcp_srv_tool_v2"},
		{"srv", "café", "mcp_srv_caf_"},
		// Dashes and underscores are already legal and must survive intact.
		{"git-hub", "create_issue", "mcp_git-hub_create_issue"},
	}
	for _, c := range cases {
		got := toolID(c.server, c.tool)
		if got != c.want {
			t.Errorf("toolID(%q, %q) = %q, want %q", c.server, c.tool, got, c.want)
		}
		if !providerNamePattern.MatchString(got) {
			t.Errorf("toolID(%q, %q) = %q, which no provider will accept", c.server, c.tool, got)
		}
		if !strings.HasPrefix(got, "mcp_") {
			t.Errorf("toolID(%q, %q) = %q, lost the mcp_ prefix the agent glob needs", c.server, c.tool, got)
		}
	}
}

func TestToolID_RespectsLengthCap(t *testing.T) {
	long := strings.Repeat("x", 80)
	a := toolID("server", long+"a")
	b := toolID("server", long+"b")

	for _, id := range []string{a, b} {
		if len(id) > maxToolNameLen {
			t.Errorf("toolID returned %d chars, over the %d cap: %q", len(id), maxToolNameLen, id)
		}
		if !providerNamePattern.MatchString(id) {
			t.Errorf("truncated id %q is not provider-safe", id)
		}
	}
	// Two names differing only past the cut must not collapse into one id —
	// the host registry is a map, so a collision drops a tool silently.
	if a == b {
		t.Errorf("two distinct long tool names truncated to the same id %q", a)
	}
}

func TestUniqueToolID_DisambiguatesCollisions(t *testing.T) {
	used := make(map[string]bool)

	// "a.b" and "a_b" sanitise to the same thing; both must stay reachable.
	first := uniqueToolID(used, "srv", "a.b")
	second := uniqueToolID(used, "srv", "a_b")
	if first != "mcp_srv_a_b" {
		t.Errorf("first id = %q, want mcp_srv_a_b", first)
	}
	if second == first {
		t.Fatalf("collision not disambiguated: both tools got %q", first)
	}
	if second != "mcp_srv_a_b_2" {
		t.Errorf("second id = %q, want mcp_srv_a_b_2", second)
	}

	third := uniqueToolID(used, "srv", "a-b")
	if third == first || third == second {
		t.Errorf("third id %q collides with an earlier one", third)
	}
	for _, id := range []string{first, second, third} {
		if !providerNamePattern.MatchString(id) {
			t.Errorf("id %q is not provider-safe", id)
		}
	}
}
