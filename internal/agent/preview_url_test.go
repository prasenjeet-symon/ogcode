package agent

import "testing"

func TestPreviewDomainDefault(t *testing.T) {
	t.Setenv(previewDomainEnv, "")
	if got := PreviewDomain(); got != defaultPreviewDomain {
		t.Errorf("empty env should yield the default domain, got %q", got)
	}

	t.Setenv(previewDomainEnv, "x.y")
	if got := PreviewDomain(); got != "x.y" {
		t.Errorf("expected the env override, got %q", got)
	}
}

func TestPreviewURL(t *testing.T) {
	t.Setenv(previewDomainEnv, "")

	tests := []struct {
		name   string
		origin string
		port   int
		want   string
	}{
		{
			name:   "loopback origin carries its port",
			origin: "http://127.0.0.1:9595",
			port:   3000,
			want:   "http://3000.preview.localhost:9595/",
		},
		{
			name:   "portless origin carries no port suffix",
			origin: "https://panel.example",
			port:   8080,
			want:   "https://8080.preview.localhost/",
		},
		{
			name:   "empty origin falls back to http",
			origin: "",
			port:   4321,
			want:   "http://4321.preview.localhost/",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PreviewURL(tt.origin, tt.port); got != tt.want {
				t.Errorf("PreviewURL(%q, %d) = %q, want %q", tt.origin, tt.port, got, tt.want)
			}
		})
	}
}

// TestAnnouncedPreviewPorts pins the narrow announce shape: only the
// <port>.<domain> hostname the agent is told to hand the user counts. A bare
// loopback URL is not an announcement, so postgres on 127.0.0.1:5432 is not read
// as a service.
func TestAnnouncedPreviewPorts(t *testing.T) {
	t.Setenv(previewDomainEnv, "")
	domain := PreviewDomain()
	tests := []struct {
		name string
		in   string
		want []int
	}{
		{"hostname form", "serving at http://3000." + domain + ":7800/", []int{3000}},
		{"hostname form without port", "see http://4321." + domain + "/", []int{4321}},
		{"bare loopback is not an announcement", "postgres on 127.0.0.1:5432", nil},
		{"localhost name is not an announcement", "open localhost:8080", nil},
		{"duplicates collapse", "http://5173." + domain + "/ and again 5173." + domain + "/", []int{5173}},
		{"sorted", "http://9000." + domain + "/ and http://1000." + domain + "/", []int{1000, 9000}},
		{"unrelated hostname not matched", "https://example.com:443/ is not a preview", nil},
		{"empty text", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AnnouncedPreviewPorts(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}

// TestAnnouncedPreviewPortsHonoursDomainOverride pins that a configured preview
// domain is the one matched, so an announcement made under a real wildcard
// domain is still recorded.
func TestAnnouncedPreviewPortsHonoursDomainOverride(t *testing.T) {
	t.Setenv(previewDomainEnv, "preview.example.com")
	got := AnnouncedPreviewPorts("live at http://3000.preview.example.com/")
	if len(got) != 1 || got[0] != 3000 {
		t.Fatalf("got %v, want [3000]", got)
	}
	// The default domain is no longer the announced one.
	if got := AnnouncedPreviewPorts("http://3000.preview.localhost/"); got != nil {
		t.Fatalf("got %v, want nil under the override", got)
	}
}

// TestPreviewDomainNormalization pins how OGCODE_PREVIEW_DOMAIN is read: case,
// a leading "*." or "." and a trailing root dot are folded away, and a value
// that is not a bare DNS name — a scheme, a port, a path, an IP — falls back to
// the default instead of silently matching no request.
func TestPreviewDomainNormalization(t *testing.T) {
	for in, want := range map[string]string{
		"Preview.Example.COM":         "preview.example.com",
		"*.preview.example.com":       "preview.example.com",
		".preview.example.com.":       "preview.example.com",
		"  preview.example.com ":      "preview.example.com",
		"localhost":                   "localhost",
		"https://preview.example.com": defaultPreviewDomain,
		"preview.example.com:8443":    defaultPreviewDomain,
		"preview.example.com/x":       defaultPreviewDomain,
		"127.0.0.1":                   defaultPreviewDomain,
		"bad_label.example.com":       defaultPreviewDomain,
		"-bad.example.com":            defaultPreviewDomain,
		"a..b":                        defaultPreviewDomain,
	} {
		t.Setenv(previewDomainEnv, in)
		if got := PreviewDomain(); got != want {
			t.Errorf("OGCODE_PREVIEW_DOMAIN=%q → %q, want %q", in, got, want)
		}
	}
}

// TestParsePreviewPort pins the one spelling a port has: digits only, no sign,
// no leading zero, in range. strconv.Atoi would accept "+3000" and "03000",
// which would give one service several preview origins.
func TestParsePreviewPort(t *testing.T) {
	for in, want := range map[string]int{
		"1": 1, "3000": 3000, "65535": 65535,
		"0": 0, "65536": 0, "99999": 0, "123456": 0, "03000": 0, "+3000": 0, "-1": 0, "3e3": 0, " 80": 0, "": 0,
	} {
		got, ok := ParsePreviewPort(in)
		if got != want || ok != (want != 0) {
			t.Errorf("ParsePreviewPort(%q) = (%d, %v), want %d", in, got, ok, want)
		}
	}
}

// TestAnnouncedPreviewPortsBoundaries pins that only a hostname the dispatcher
// routes counts as an announcement: the port label must start the host and the
// domain must end it, matching is case-insensitive like the Host header, and a
// sentence-ending dot after the host is fine.
func TestAnnouncedPreviewPortsBoundaries(t *testing.T) {
	t.Setenv(previewDomainEnv, "")
	d := PreviewDomain()
	for _, tt := range []struct {
		in   string
		want []int
	}{
		{"open 3000." + d + ".", []int{3000}},
		{"(see http://3000." + d + ":9595/)", []int{3000}},
		{"HTTP://4321.PREVIEW.LOCALHOST:9595/", []int{4321}},
		{"`5173." + d + "`", []int{5173}},
		{"a.3000." + d, nil},
		{"x-3000." + d, nil},
		{"13000x." + d, nil},
		{"123456." + d, nil},
		{"3000." + d + ".evil.example", nil},
		{"3000." + d + "x", nil},
		{"03000." + d, nil},
	} {
		got := AnnouncedPreviewPorts(tt.in)
		if len(got) != len(tt.want) || (len(got) == 1 && got[0] != tt.want[0]) {
			t.Errorf("AnnouncedPreviewPorts(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

// TestOriginIsLoopback pins which origins a *.localhost preview URL lands back
// on this server from: only loopback names, and an unknown origin is not
// treated as remote.
func TestOriginIsLoopback(t *testing.T) {
	for origin, want := range map[string]bool{
		"http://localhost:9595":        true,
		"http://127.0.0.1:9595":        true,
		"http://[::1]:9595":            true,
		"http://app.localhost:9595":    true,
		"":                             true,
		"http://192.168.1.10:9595":     false,
		"https://ogcode.example.com":   false,
		"http://myserver.tailnet:9595": false,
	} {
		if got := originIsLoopback(origin); got != want {
			t.Errorf("originIsLoopback(%q) = %v, want %v", origin, got, want)
		}
	}
}
