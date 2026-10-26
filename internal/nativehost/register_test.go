package nativehost

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// setManifestDirs redirects the install targets into test trees for the
// duration of the test.
func setManifestDirs(t *testing.T, dirs ...string) {
	t.Helper()
	original := manifestDirsFor
	manifestDirsFor = func() []string { return dirs }
	t.Cleanup(func() { manifestDirsFor = original })
}

// browserTree returns the profile root of a browser whose folders exist and
// of one that has never been used, both under one temp tree.
func browserTree(t *testing.T) (existingRoot, missingRoot string) {
	t.Helper()
	base := t.TempDir()
	existingRoot = filepath.Join(base, "Chrome")
	if err := os.MkdirAll(existingRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	return existingRoot, filepath.Join(base, "Brave")
}

func TestRegisterWritesTheManifestIntoExistingBrowsersOnly(t *testing.T) {
	root, missing := browserTree(t)
	setManifestDirs(t,
		filepath.Join(root, nativeHostDirName),
		filepath.Join(missing, nativeHostDirName),
	)

	if err := Register("/bin/ogcode", []string{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, nativeHostDirName, manifestName))
	if err != nil {
		t.Fatalf("reading the manifest: %v", err)
	}
	var m nativeManifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("manifest is not JSON: %v", err)
	}
	if m.Name != NativeHostName {
		t.Errorf("name = %q, want %q", m.Name, NativeHostName)
	}
	if m.Path != "/bin/ogcode" {
		t.Errorf("path = %q, want the executable path", m.Path)
	}
	if m.Type != "stdio" {
		t.Errorf("type = %q, want stdio", m.Type)
	}
	if !strings.Contains(m.Description, "ogcode") {
		t.Errorf("description = %q, want it to name ogcode", m.Description)
	}
	if want := []string{"chrome-extension://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/"}; !reflect.DeepEqual(m.AllowedOrigins, want) {
		t.Errorf("allowed_origins = %v, want %v (trailing slash)", m.AllowedOrigins, want)
	}

	// The browser that was never run got nothing — and its manifest folder
	// must not have been created behind the parent-exists heuristic.
	if _, err := os.Stat(filepath.Join(missing, nativeHostDirName)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("absent browser's NativeMessagingHosts stat err = %v, want not-exist (nothing written)", err)
	}
}

func TestRegisterRewritesTheSameBytesIdempotently(t *testing.T) {
	root, _ := browserTree(t)
	setManifestDirs(t, filepath.Join(root, nativeHostDirName))

	if err := Register("/bin/ogcode", []string{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	first, err := os.ReadFile(filepath.Join(root, nativeHostDirName, manifestName))
	if err != nil {
		t.Fatal(err)
	}
	if err := Register("/bin/ogcode", []string{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}); err != nil {
		t.Fatalf("second Register: %v", err)
	}
	second, err := os.ReadFile(filepath.Join(root, nativeHostDirName, manifestName))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("the second install rewrote different bytes:\n%s\nvs\n%s", first, second)
	}
}

func TestRegisterRefusesZeroIDs(t *testing.T) {
	for _, ids := range [][]string{nil, {}, {"", ""}} {
		if err := Register("/bin/ogcode", ids); err == nil {
			t.Errorf("Register(%v) succeeded, want a refusal without ids", ids)
		}
	}
}

func TestUninstallRemovesOnlyTheManifestAndIsIdempotent(t *testing.T) {
	root, _ := browserTree(t)
	dir := filepath.Join(root, nativeHostDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	setManifestDirs(t, dir, filepath.Join(t.TempDir(), "Ghost", nativeHostDirName))
	manifestPath := filepath.Join(dir, manifestName)
	if err := os.WriteFile(manifestPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	neighbor := filepath.Join(dir, "other-host.json")
	if err := os.WriteFile(neighbor, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Uninstall(); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(manifestPath); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("manifest still present after Uninstall (err %v)", err)
	}
	if _, err := os.Stat(neighbor); err != nil {
		t.Fatalf("Uninstall removed a manifest that is not its own: %v", err)
	}
	if err := Uninstall(); err != nil {
		t.Fatalf("second Uninstall: %v, want idempotent success", err)
	}
}

func TestEnsureRegisteredDoesNothingWithoutCandidates(t *testing.T) {
	root, _ := browserTree(t)
	setManifestDirs(t, filepath.Join(root, nativeHostDirName))
	t.Setenv(ExtensionIDsEnv, "")
	// A project without browser-extension/ and without env ids entitles nothing.

	if err := EnsureRegistered(t.TempDir()); err != nil {
		t.Fatalf("EnsureRegistered: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, nativeHostDirName, manifestName)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a manifest was written without candidates (err %v)", err)
	}
}

func TestEnsureRegisteredWritesTheUnpackedProjectsID(t *testing.T) {
	root, _ := browserTree(t)
	setManifestDirs(t, filepath.Join(root, nativeHostDirName))
	t.Setenv(ExtensionIDsEnv, "")

	project := t.TempDir()
	unpacked := filepath.Join(project, UnpackedExtensionDir)
	if err := os.MkdirAll(unpacked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unpacked, "manifest.json"), []byte(`{"name":"ogweb"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	id, ok := UnpackedIDFor(project)
	if !ok {
		t.Fatal("the shipped companion was not discovered")
	}
	if want := ExtensionIDForPath(unpacked); id != want {
		t.Fatalf("UnpackedIDFor = %q, want %q", id, want)
	}

	if err := EnsureRegistered(project); err != nil {
		t.Fatalf("EnsureRegistered: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, nativeHostDirName, manifestName))
	if err != nil {
		t.Fatalf("reading the manifest: %v", err)
	}
	var m nativeManifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	want := []string{"chrome-extension://" + id + "/"}
	if !reflect.DeepEqual(m.AllowedOrigins, want) {
		t.Fatalf("allowed_origins = %v, want %v", m.AllowedOrigins, want)
	}
	if m.Path == "" || !filepath.IsAbs(m.Path) {
		t.Fatalf("path = %q, want the absolute executable the server runs", m.Path)
	}
}

func TestEnsureRegisteredPicksUpEnvIDs(t *testing.T) {
	root, _ := browserTree(t)
	setManifestDirs(t, filepath.Join(root, nativeHostDirName))
	t.Setenv(ExtensionIDsEnv, " , first_extension_id,second_extension_id,first_extension_id,")
	// No unpacked companion: the env list is the only source.

	if err := EnsureRegistered(t.TempDir()); err != nil {
		t.Fatalf("EnsureRegistered: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, nativeHostDirName, manifestName))
	if err != nil {
		t.Fatal(err)
	}
	var m nativeManifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	// Deduplicated in order, surrounding whitespace and empties dropped.
	want := []string{
		"chrome-extension://first_extension_id/",
		"chrome-extension://second_extension_id/",
	}
	if !reflect.DeepEqual(m.AllowedOrigins, want) {
		t.Fatalf("allowed_origins = %v, want %v", m.AllowedOrigins, want)
	}
}

func TestKillSwitchDisablesEnsureRegisteredButNotRegister(t *testing.T) {
	root, _ := browserTree(t)
	setManifestDirs(t, filepath.Join(root, nativeHostDirName))
	t.Setenv(installEnv, "off")

	project := t.TempDir()
	unpacked := filepath.Join(project, UnpackedExtensionDir)
	if err := os.MkdirAll(unpacked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unpacked, "manifest.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := EnsureRegistered(project); err != nil {
		t.Fatalf("EnsureRegistered: %v, want a silent skip", err)
	}
	if _, err := os.Stat(filepath.Join(root, nativeHostDirName, manifestName)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the kill switch did not stop the manifest write (err %v)", err)
	}

	// The CLI's explicit install is unaffected by the switch, which belongs to
	// the server's boot path only.
	if err := Register("/bin/ogcode", []string{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}); err != nil {
		t.Fatalf("Register under the switch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, nativeHostDirName, manifestName)); err != nil {
		t.Fatalf("Register must still write under the switch: %v", err)
	}
}

func TestDisabledByEnv(t *testing.T) {
	for _, truthy := range []string{"", "1", "true", "YES", "on"} {
		t.Setenv(installEnv, truthy)
		if DisabledByEnv() {
			t.Errorf("OGCODE_NATIVE_HOST_INSTALL=%q read as disabled", truthy)
		}
	}
	for _, off := range []string{"0", "false", "no", "off", " OFF ", "FALSE"} {
		t.Setenv(installEnv, off)
		if !DisabledByEnv() {
			t.Errorf("OGCODE_NATIVE_HOST_INSTALL=%q read as enabled", off)
		}
	}
}

func TestManifestPathsSkipsAbsentBrowsers(t *testing.T) {
	root, missing := browserTree(t)
	setManifestDirs(t, filepath.Join(root, nativeHostDirName), filepath.Join(missing, nativeHostDirName))

	paths := ManifestPaths()
	want := filepath.Join(root, nativeHostDirName, manifestName)
	if len(paths) != 1 || paths[0] != want {
		t.Fatalf("ManifestPaths = %v, want just %q", paths, want)
	}
}
