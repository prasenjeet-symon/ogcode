package version

import (
	"strings"
	"testing"
)

func TestDetectInstallCommandFor(t *testing.T) {
	noop := func(string) string { return "" }
	tests := []struct {
		name     string
		goos     string
		execPath string
		getenv   func(string) string
		want     string
	}{
		{
			name:     "homebrew cellar symlink",
			goos:     "darwin",
			execPath: "/usr/local/Cellar/ogcode/v0.36.1/bin/ogcode",
			want:     "brew upgrade ogcode",
		},
		{
			name:     "homebrew apple silicon prefix",
			goos:     "darwin",
			execPath: "/opt/homebrew/bin/ogcode",
			want:     "brew upgrade ogcode",
		},
		{
			name:     "linuxbrew",
			goos:     "linux",
			execPath: "/home/linuxbrew/.linuxbrew/Cellar/ogcode/v0.36.1/bin/ogcode",
			want:     "brew upgrade ogcode",
		},
		{
			name:     "curl script install is not homebrew",
			goos:     "darwin",
			execPath: "/usr/local/bin/ogcode",
			want:     "curl -fsSL https://ogcode.xyz/install.sh | sh",
		},
		{
			name:     "curl script install windows temp",
			goos:     "windows",
			execPath: `C:\Temp\ogcode.exe`,
			want:     "winget upgrade ogcode",
		},
		{
			name: "scoop shim",
			goos: "windows",
			getenv: func(key string) string {
				if key == "USERPROFILE" {
					return `C:\Users\dev`
				}
				return ""
			},
			execPath: `C:\Users\dev\scoop\shims\ogcode.exe`,
			want:     "scoop update ogcode",
		},
		{
			name: "scoop global install",
			goos: "windows",
			getenv: func(key string) string {
				if key == "SCOOP_GLOBAL" {
					return `C:\ProgramData\scoop`
				}
				return ""
			},
			execPath: `C:\ProgramData\scoop\apps\ogcode\current\ogcode.exe`,
			want:     "scoop update ogcode",
		},
		{
			name: "scoop root env override",
			goos: "windows",
			getenv: func(key string) string {
				if key == "SCOOP" {
					return `D:\scoop`
				}
				return ""
			},
			execPath: `D:\scoop\apps\ogcode\current\ogcode.exe`,
			want:     "scoop update ogcode",
		},
		{
			name: "scoop outside windows ignored",
			goos: "darwin",
			getenv: func(key string) string {
				if key == "USERPROFILE" {
					return `C:\Users\dev`
				}
				return ""
			},
			execPath: `/Users/dev/scoop/shims/ogcode`,
			want:     "curl -fsSL https://ogcode.xyz/install.sh | sh",
		},
		{
			name: "cargo default bin",
			goos: "linux",
			getenv: func(key string) string {
				if key == "HOME" {
					return "/home/dev"
				}
				return ""
			},
			execPath: "/home/dev/.cargo/bin/ogcode",
			want:     "cargo install ogcode --force",
		},
		{
			name: "cargo CARGO_HOME override",
			goos: "windows",
			getenv: func(key string) string {
				if key == "CARGO_HOME" {
					return `D:\rust\cargo`
				}
				return ""
			},
			execPath: `D:\rust\cargo\bin\ogcode.exe`,
			want:     "cargo install ogcode --force",
		},
		{
			name: "cargo home from USERPROFILE",
			goos: "windows",
			getenv: func(key string) string {
				if key == "USERPROFILE" {
					return `C:\Users\dev`
				}
				return ""
			},
			execPath: `C:\Users\dev\.cargo\bin\ogcode.exe`,
			want:     "cargo install ogcode --force",
		},
		{
			name: "cargo install under sibling home prefix does not match",
			goos: "linux",
			getenv: func(key string) string {
				if key == "HOME" {
					return "/home/dev"
				}
				return ""
			},
			execPath: "/home/developer/.cargo/bin/ogcode",
			want:     "curl -fsSL https://ogcode.xyz/install.sh | sh",
		},
		{
			name: "install.ps1 directory",
			goos: "windows",
			getenv: func(key string) string {
				if key == "LOCALAPPDATA" {
					return `C:\Users\dev\AppData\Local`
				}
				return ""
			},
			execPath: `C:\Users\dev\AppData\Local\ogcode\ogcode.exe`,
			want:     "irm https://ogcode.xyz/install.ps1 | iex",
		},
		{
			name:     "winget fallback on windows",
			goos:     "windows",
			execPath: `C:\Program Files\ogcode\ogcode.exe`,
			want:     "winget upgrade ogcode",
		},
		{
			name: "winget fallback beats nothing on empty env",
			goos: "windows",
			getenv: func(key string) string {
				if key == "LOCALAPPDATA" {
					return `C:\Users\dev\AppData\Local`
				}
				return ""
			},
			execPath: `C:\Users\dev\AppData\Local\ogcode.exe`,
			want:     "winget upgrade ogcode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := tt.getenv
			if getenv == nil {
				getenv = noop
			}
			got := detectInstallCommandFor(tt.goos, tt.execPath, getenv)
			if got != tt.want {
				t.Errorf("detectInstallCommandFor(%q, %q) = %q, want %q", tt.goos, tt.execPath, got, tt.want)
			}
		})
	}
}

func TestDetectInstallCommandFor_PrefixDirIsNotInside(t *testing.T) {
	getenv := func(key string) string {
		if key == "USERPROFILE" {
			return `C:\Users\dev`
		}
		return ""
	}
	// A sibling whose name merely extends the root ("scoop-extra") must not
	// match the scoop root check.
	got := detectInstallCommandFor("windows", `C:\Users\dev\scoop-extra\ogcode.exe`, getenv)
	want := "winget upgrade ogcode"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDetectInstallCommandFor_UnderDirPrefixIsNotInside(t *testing.T) {
	// "/home/developer" must not count as under "/home/dev".
	if underDir("/home/developer/.cargo/bin/ogcode", "/home/dev") {
		t.Error("sibling prefix directory matched as inside")
	}
	if !underDir("/home/dev/.cargo/bin/ogcode", "/home/dev") {
		t.Error("nested path not matched as inside")
	}
	if underDir("/home/dev", "/home/dev") {
		t.Error("dir itself matched as inside")
	}
	if !underDir(`C:\Users\dev\.cargo\bin\ogcode.exe`, `C:/Users/dev`) {
		t.Error("windows backslash path not matched against slash-style dir")
	}
}

func TestIsHomebrewInstall_NeedsCellarOrAppleSiliconPrefix(t *testing.T) {
	// A Linux /usr/local install (the curl script's target) must not read as
	// Homebrew — the old heuristic reported it as brew upgrade.
	if isHomebrewInstall("linux", "/usr/local/bin/ogcode") {
		t.Error("plain /usr/local/bin install matched as Homebrew")
	}
	if isHomebrewInstall("darwin", "/usr/local/bin/ogcode") {
		t.Error("plain /usr/local/bin install matched as Homebrew")
	}
	if !isHomebrewInstall("darwin", "/opt/homebrew/Cellar/ogcode/v0.36.1/bin/ogcode") {
		t.Error("Cellar install not matched as Homebrew")
	}
}

func TestDetectInstallCommandFor_CaseInsensitiveWindowsPaths(t *testing.T) {
	getenv := func(key string) string {
		if key == "LOCALAPPDATA" {
			return `c:\users\dev\appdata\local`
		}
		return ""
	}
	got := detectInstallCommandFor("windows", `C:\Users\DEV\AppData\Local\ogcode\ogcode.exe`, getenv)
	want := "irm https://ogcode.xyz/install.ps1 | iex"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A build must never report a package-manager upgrade command for a
// version it then cannot find — the script commands are the honest
// catch-alls. Pinned because InstallCommand flows straight to the UI.
func TestDetectInstallCommand_KnownChannelsShape(t *testing.T) {
	got := detectInstallCommand()
	if got == "" {
		t.Fatal("detectInstallCommand returned an empty command")
	}
	for _, cmd := range []string{
		"brew upgrade ogcode",
		"cargo install ogcode --force",
		"scoop update ogcode",
		"winget upgrade ogcode",
		"irm https://ogcode.xyz/install.ps1 | iex",
		"curl -fsSL https://ogcode.xyz/install.sh | sh",
	} {
		if got == cmd {
			return
		}
	}
	t.Errorf("detectInstallCommand = %q, not a recognized channel command", got)
}

func TestCurrentExecPath_NonEmptyClean(t *testing.T) {
	got := currentExecPath()
	if got == "" {
		t.Skip("os.Executable unavailable in this environment")
	}
	if strings.HasPrefix(got, "/") == strings.Contains(got, `\`) && !strings.Contains(got, "/") && !strings.Contains(got, `\`) {
		t.Errorf("currentExecPath = %q, not a filesystem path", got)
	}
}
