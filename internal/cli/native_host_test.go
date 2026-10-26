package cli

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/nativehost"
)

// The native-host command family must be reachable the same way as the rest:
// `ogcode native-host` is what Chrome is configured to launch, install and
// uninstall are the things a user runs by hand.
func TestNativeHostCommandsAreRegistered(t *testing.T) {
	host, _, err := rootCmd.Find([]string{"native-host"})
	if err != nil {
		t.Fatalf("find native-host: %v", err)
	}
	if install, _, err := rootCmd.Find([]string{"native-host", "install"}); err != nil || install.Name() != "install" {
		t.Fatalf("find native-host install = (%v, %v)", install, err)
	}
	if uninstall, _, err := rootCmd.Find([]string{"native-host", "uninstall"}); err != nil || uninstall.Name() != "uninstall" {
		t.Fatalf("find native-host uninstall = (%v, %v)", uninstall, err)
	}
	if host.RunE == nil {
		t.Fatal("bare native-host has no run stage")
	}
	if found := nativeHostInstallCmd.Flags().Lookup("extension-id"); found == nil {
		t.Fatal("native-host install has no --extension-id flag")
	}
}

// Chrome binds the host's stdout to the protocol pipe, and a log file beside
// the wire's stdout would be one line away from breaking every reply — like
// version, the command keeps no log file.
func TestNativeHostKeepsNoLogFile(t *testing.T) {
	c, _, err := rootCmd.Find([]string{"native-host"})
	if err != nil {
		t.Fatal(err)
	}
	if dir, name := logTarget(c); name != "" {
		t.Fatalf("native-host logs to %q/%q, want the no-file path like version does", dir, name)
	}
}

func TestNativeHostInstallHintNamesTheUnpackedIDWhenItCan(t *testing.T) {
	project := t.TempDir()
	unpacked := filepath.Join(project, nativehost.UnpackedExtensionDir)
	if err := os.MkdirAll(unpacked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unpacked, "manifest.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	nativeHostInstallHint(project, &buf)
	text := buf.String()
	if !strings.Contains(text, nativehost.ExtensionIDForPath(unpacked)) {
		t.Fatalf("hint does not name the load path's id:\n%s", text)
	}
	if !strings.Contains(text, "Load unpacked") {
		t.Fatalf("hint does not name the load step:\n%s", text)
	}
}

func TestNativeHostInstallHintWithoutAnExtensionNamesTheEnvRoute(t *testing.T) {
	var buf bytes.Buffer
	nativeHostInstallHint(t.TempDir(), &buf)
	text := buf.String()
	if !strings.Contains(text, nativehost.ExtensionIDsEnv) {
		t.Fatalf("hint does not name %s:\n%s", nativehost.ExtensionIDsEnv, text)
	}
	if !strings.Contains(text, "--extension-id") {
		t.Fatalf("hint does not name the flag route:\n%s", text)
	}
}

// Chrome binds the host with the caller's origin as the first argument, not
// the `native-host` spelling; that form must never reach cobra's dispatch,
// which would otherwise exit "unknown command" before the extension gets a
// single framed reply.
func TestIsBrowserLaunched(t *testing.T) {
	const id = "lgecpjpmljakfjgcgboknboioblifleg"
	if !IsBrowserLaunched([]string{"chrome-extension://" + id + "/"}) {
		t.Fatal("bare origin arg is not recognized as Chrome's launch form")
	}
	if !IsBrowserLaunched([]string{"chrome-extension://" + id + "/", "--parent-window=0"}) {
		t.Fatal("the origin is not recognized with --parent-window trailing")
	}
	if IsBrowserLaunched(nil) {
		t.Fatal("empty argv misreads as a browser launch")
	}
	if IsBrowserLaunched([]string{"serve", "--port", "7801"}) {
		t.Fatal("serve misreads as a browser launch")
	}
	if IsBrowserLaunched([]string{"native-host", "install", "--extension-id", id}) {
		t.Fatal("the native-host subcommand must stay cobra's, keeping its logging test meaningful")
	}
}

// The full Chrome launch: argv spelled the way Chrome spells it, a framed
// request on stdin (an unknown type — the pair type would probe the real
// machine's server registry), one framed reply back on stdout, then Chrome's
// goodbye as the stdin close.
func TestExecuteRoutesChromeLaunchToTheHost(t *testing.T) {
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldArgs, oldStdin, oldStdout := os.Args, os.Stdin, os.Stdout
	oldErr, oldLog := os.Stderr, logger
	t.Cleanup(func() {
		os.Args, os.Stdin, os.Stdout = oldArgs, oldStdin, oldStdout
		os.Stderr, logger = oldErr, oldLog
		stdinR.Close()
		stdoutW.Close()
	})
	os.Args = []string{"ogcode", "chrome-extension://lgecpjpmljakfjgcgboknboioblifleg/", "--parent-window=0"}
	os.Stdin, os.Stdout = stdinR, stdoutW

	request := []byte(`{"type":"probe"}`)
	frame := make([]byte, 4+len(request))
	binary.LittleEndian.PutUint32(frame[:4], uint32(len(request)))
	copy(frame[4:], request)
	if _, err := stdinW.Write(frame); err != nil {
		t.Fatal(err)
	}
	if err := stdinW.Close(); err != nil { // Chrome's goodbye ends the loop
		t.Fatal(err)
	}
	if err := Execute(); err != nil {
		t.Fatalf("Execute over Chrome's launch form: %v", err)
	}
	// Execute returning means the host loop ended on the stdin close; the
	// writer must go too so the read back below reaches EOF instead of
	// blocking on a pipe that stays open.
	if err := stdoutW.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(stdoutR)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) < 4 {
		t.Fatalf("no framed reply came back (%d bytes), stdout was hijacked by a usage error", len(out))
	}
	body := out[4:]
	if len(out) != 4+int(binary.LittleEndian.Uint32(out[:4])) {
		t.Fatalf("reply frame is %d bytes, length prefix says %d", len(out)-4, binary.LittleEndian.Uint32(out[:4]))
	}
	if !bytes.Contains(body, []byte(`"ok":false`)) || !bytes.Contains(body, []byte(`"unknown request type \"probe\""`)) {
		t.Fatalf("reply does not answer a probe like the host does: %q", body)
	}
}
