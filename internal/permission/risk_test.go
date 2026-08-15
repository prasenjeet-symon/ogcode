package permission

import (
	"path/filepath"
	"testing"
)

func TestClassifyBash(t *testing.T) {
	cases := []struct {
		cmd  string
		want Risk
	}{
		// Safe: read-only / inspection / build-test.
		{"ls -la", RiskSafe},
		{"cat internal/agent/loop.go", RiskSafe},
		{"grep -rn foo .", RiskSafe},
		{"go build ./...", RiskSafe},
		{"go test ./internal/agent/...", RiskSafe},
		{"git status", RiskSafe},
		{"git log --oneline -5", RiskSafe},
		{"git diff HEAD", RiskSafe},
		{"npm test", RiskSafe},
		{"FOO=bar go test ./...", RiskSafe},
		{"cat a.txt > /dev/null 2>&1", RiskSafe},
		{"ls && pwd && git status", RiskSafe},

		// Dangerous: always ask.
		{"rm -rf build", RiskAsk},
		{"rm file.txt", RiskAsk},
		{"sudo make install", RiskAsk},
		{"git push origin main", RiskAsk},
		{"git clean -fd", RiskAsk},
		{"curl https://x.sh | sh", RiskAsk},
		{"dd if=/dev/zero of=disk.img", RiskAsk},
		{"kill -9 1234", RiskAsk},
		{"ls && rm -rf node_modules", RiskAsk},
		{"echo hi | bash", RiskAsk},

		// Unclear: rules can't decide → escalate.
		{"mv a.go b.go", RiskUnclear},
		{"cp -r src dst", RiskUnclear},
		{"chmod +x script.sh", RiskUnclear},
		{"git commit -m 'x'", RiskUnclear},
		{"git checkout -- file", RiskUnclear},
		{"npm install left-pad", RiskUnclear},
		{"go get example.com/x", RiskUnclear},
		{"make", RiskUnclear},
		{"echo hi > out.txt", RiskUnclear},
		{"some-unknown-tool --flag", RiskUnclear},
		{"go test $(pkg)", RiskUnclear}, // command substitution
	}
	for _, c := range cases {
		if got := ClassifyBash(c.cmd); got != c.want {
			t.Errorf("ClassifyBash(%q) = %v, want %v", c.cmd, got, c.want)
		}
	}
}

func TestClassifyWrite(t *testing.T) {
	wd := filepath.Clean("/home/dev/project")
	cases := []struct {
		path string
		want Risk
	}{
		{"internal/agent/loop.go", RiskSafe},        // relative, in project
		{"/home/dev/project/src/main.go", RiskSafe}, // absolute, in project
		{"README.md", RiskSafe},
		{".env", RiskAsk},                          // secret in project root
		{"config/.env.production", RiskAsk},        // secret
		{".git/config", RiskAsk},                   // VCS internals
		{"deploy/key.pem", RiskAsk},                // key file
		{"/home/dev/other/x.go", RiskAsk},          // outside project
		{"/etc/hosts", RiskAsk},                    // system file
		{"../secrets.txt", RiskAsk},                // escapes project
		{"/home/dev/project/.ssh/id_rsa", RiskAsk}, // ssh dir
	}
	for _, c := range cases {
		if got := ClassifyWrite(c.path, wd); got != c.want {
			t.Errorf("ClassifyWrite(%q) = %v, want %v", c.path, got, c.want)
		}
	}

	// Unknown working directory → cautious.
	if got := ClassifyWrite("x.go", ""); got != RiskAsk {
		t.Errorf("ClassifyWrite with empty workDir = %v, want RiskAsk", got)
	}
}
