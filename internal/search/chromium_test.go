//go:build darwin || windows

package search

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNewBrowserBackend_FavorsChromiumFirst(t *testing.T) {
	b := NewBrowserBackend()
	if b == nil {
		t.Fatal("NewBrowserBackend() returned nil on darwin/windows")
	}
	if b.Name() != ProviderChromium {
		t.Errorf("NewBrowserBackend().Name() = %q, want %q (Chromium must lead)", b.Name(), ProviderChromium)
	}
	if runtime.GOOS == "darwin" {
		fb, ok := b.(*fallbackBackend)
		if !ok {
			t.Fatalf("on darwin, NewBrowserBackend() = %T, want *fallbackBackend (Chromium -> Safari)", b)
		}
		if fb.primary.Name() != ProviderChromium {
			t.Errorf("darwin browser primary = %q, want %q", fb.primary.Name(), ProviderChromium)
		}
		if fb.secondary.Name() != ProviderSafari {
			t.Errorf("darwin browser secondary = %q, want %q", fb.secondary.Name(), ProviderSafari)
		}
	}
}

func TestChromiumCandidatesFor_DarwinAndWindows(t *testing.T) {
	mac := chromiumCandidatesFor("darwin", "/Users/alice", func(string) string { return "" })
	if len(mac) == 0 {
		t.Fatal("expected darwin candidates")
	}
	if want := filepath.Join("/Applications", "Google Chrome.app", "Contents", "MacOS", "Google Chrome"); mac[0] != want {
		t.Errorf("mac[0] = %q, want %q", mac[0], want)
	}

	win := chromiumCandidatesFor("windows", "", func(k string) string {
		if k == "ProgramFiles" {
			return `C:\Program Files`
		}
		return ""
	})
	if len(win) == 0 {
		t.Fatal("expected windows candidates")
	}
	if !strings.Contains(win[0], "chrome.exe") {
		t.Errorf("win[0] = %q, want Chrome first", win[0])
	}
}

func TestChromiumArgs_DisablesAutomationControlled(t *testing.T) {
	args := chromiumArgs("https://example.com", "/tmp/prof")
	found := false
	for _, a := range args {
		if a == "--disable-blink-features=AutomationControlled" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("chromiumArgs missing --disable-blink-features=AutomationControlled: %v", args)
	}
}
