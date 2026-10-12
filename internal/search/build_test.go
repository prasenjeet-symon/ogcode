package search

import "testing"

// The two browser spellings name one slot: "safari" (macOS) and "chrome"
// (Windows) both put the platform browser first, and the slot resolves to
// whatever NewBrowserBackend is on the running host. Where no browser can be
// driven the slot collapses and the chain stays on the HTTP path.
func TestBuildBackendBrowserLeadsTheHTTPPath(t *testing.T) {
	// A stored value must win over nothing; clear the override so the argument
	// under test is the one that decides.
	t.Setenv("OGCODE_SEARCH_BROWSER", "")

	browser := NewBrowserBackend()
	for _, name := range []string{"safari", "chrome"} {
		got := BuildBackend("", "", name)
		if got == nil {
			t.Fatalf("BuildBackend(%q) returned nil", name)
		}
		if browser != nil {
			if got.Name() != browser.Name() {
				t.Errorf("BuildBackend(%q).Name() = %q, want the browser %q to lead", name, got.Name(), browser.Name())
			}
			continue
		}
		if got.Name() != ProviderNative {
			t.Errorf("BuildBackend(%q).Name() = %q, want the HTTP path when no browser is drivable", name, got.Name())
		}
	}
}

// "native" pins the HTTP path and the empty string keeps it in front, so the
// owned engine leads in both — the empty string only leaves the browser behind
// it rather than dropping it.
func TestBuildBackendHTTPPathLeads(t *testing.T) {
	t.Setenv("OGCODE_SEARCH_BROWSER", "")

	for _, browser := range []string{"native", ""} {
		if got := BuildBackend("", "", browser).Name(); got != ProviderNative {
			t.Errorf("BuildBackend(%q).Name() = %q, want %q", browser, got, ProviderNative)
		}
	}
}

// OGCODE_SEARCH_BROWSER overrides the stored choice, so a deployment can pin
// the engine without touching config.
func TestBuildBackendEnvOverridesBrowser(t *testing.T) {
	t.Setenv("OGCODE_SEARCH_BROWSER", "native")

	if got := BuildBackend("", "", "safari").Name(); got != ProviderNative {
		t.Errorf("env 'native' should pin the HTTP path, got %q", got)
	}
}
