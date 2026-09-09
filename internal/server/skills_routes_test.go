package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/prasenjeet-symon/ogcode/internal/skill"
)

func writeTestSkill(t *testing.T, project, name, desc string) {
	t.Helper()
	dir := filepath.Join(project, ".agents", "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: " + desc + "\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newSkillTestServer(t *testing.T) (*Server, http.Handler, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	project := t.TempDir()
	// A .git marker bounds config.findProjectFile's upward walk to the project,
	// so the test never touches a real ogcode.json above the temp dir.
	if err := os.WriteFile(filepath.Join(project, ".git"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTestSkill(t, project, "docx", "make word docs")
	writeTestSkill(t, project, "pdf", "read pdfs")

	srv := &Server{dir: project, skillLoader: skill.NewLoader(skill.Config{})}
	r := chi.NewRouter()
	r.Get("/skills", srv.handleListSkills)
	r.Post("/skills/{name}", srv.handleSetSkillEnabled)
	return srv, r, project
}

func fetchSkillEnabled(t *testing.T, r http.Handler) map[string]bool {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/skills", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	var got []skillSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	enabled := map[string]bool{}
	for _, s := range got {
		enabled[s.Name] = s.Enabled
	}
	return enabled
}

func toggleSkill(t *testing.T, r http.Handler, name string, on bool) (skillSummary, int) {
	t.Helper()
	body := strings.NewReader(fmt.Sprintf(`{"enabled":%t}`, on))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/skills/"+name, body))
	var got skillSummary
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode set: %v", err)
		}
	}
	return got, rec.Code
}

func readConfig(t *testing.T, project string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(project, "ogcode.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSkills_ToggleRoundTrip(t *testing.T) {
	_, r, project := newSkillTestServer(t)

	if got := fetchSkillEnabled(t, r); !got["docx"] || !got["pdf"] {
		t.Fatalf("expected all enabled, got %v", got)
	}

	// Disable docx.
	if got, code := toggleSkill(t, r, "docx", false); code != http.StatusOK || got.Enabled {
		t.Fatalf("disable docx: code=%d enabled=%v", code, got.Enabled)
	}

	// The listing reflects it; pdf is untouched.
	got := fetchSkillEnabled(t, r)
	if got["docx"] {
		t.Errorf("docx should be disabled in listing")
	}
	if !got["pdf"] {
		t.Errorf("pdf should still be enabled")
	}

	// The project ogcode.json carries the deny rule.
	if cfg := readConfig(t, project); !strings.Contains(cfg, `"docx": "deny"`) {
		t.Errorf("ogcode.json missing deny rule:\n%s", cfg)
	}

	// Re-enable: enabled again, and the rule is gone from the file.
	if got, code := toggleSkill(t, r, "docx", true); code != http.StatusOK || !got.Enabled {
		t.Fatalf("re-enable docx: code=%d enabled=%v", code, got.Enabled)
	}
	if got := fetchSkillEnabled(t, r); !got["docx"] {
		t.Errorf("docx should be enabled again")
	}
	if cfg := readConfig(t, project); strings.Contains(cfg, "docx") {
		t.Errorf("docx rule should be gone from ogcode.json:\n%s", cfg)
	}
}

// Enabling a skill that a broader glob still denies pins an explicit allow for
// its exact name, so the switch wins over the glob.
func TestSkills_EnableOverridesGlobDeny(t *testing.T) {
	srv, r, project := newSkillTestServer(t)
	if err := os.WriteFile(filepath.Join(project, "ogcode.json"),
		[]byte(`{"skills":{"permissions":{"*":"deny"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	srv.skillLoader.SetPermissions(map[string]string{"*": "deny"})

	if got := fetchSkillEnabled(t, r); got["docx"] || got["pdf"] {
		t.Fatalf("expected all disabled under deny-all, got %v", got)
	}

	if got, code := toggleSkill(t, r, "docx", true); code != http.StatusOK || !got.Enabled {
		t.Fatalf("enable docx: code=%d enabled=%v", code, got.Enabled)
	}

	got := fetchSkillEnabled(t, r)
	if !got["docx"] {
		t.Errorf("docx should be enabled after override")
	}
	if got["pdf"] {
		t.Errorf("pdf should remain disabled under the glob")
	}
	if cfg := readConfig(t, project); !strings.Contains(cfg, `"docx": "allow"`) {
		t.Errorf("expected explicit allow for docx:\n%s", cfg)
	}
}

func TestSkills_SetUnknownSkillIs404(t *testing.T) {
	_, r, _ := newSkillTestServer(t)
	if _, code := toggleSkill(t, r, "nope", false); code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", code)
	}
}
