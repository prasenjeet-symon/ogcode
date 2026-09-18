package server

import (
	"context"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// scrcpyTarget is where the ws-scrcpy Node app listens. It is a separate
// process on the same machine (started with ~/.android-browser/start.sh, which
// also starts the emulator); ogcode neither bundles nor spawns it. Overridable
// for tests via OGCODE_SCRCPY_TARGET (same pattern as OLLAMA_SHOW_URL).
func scrcpyTarget() string {
	if v := os.Getenv("OGCODE_SCRCPY_TARGET"); v != "" {
		return v
	}
	return "http://127.0.0.1:8000"
}

// scrcpyPrefix is the ogcode-side URL prefix the device UI lives under. A
// request to /scrcpy/foo is forwarded to ws-scrcpy as /foo.
const scrcpyPrefix = "/scrcpy"

// scrcpyProxy lazily builds the reverse proxy that forwards /scrcpy/* to the
// ws-scrcpy server. It is created on the first proxied request: when ws-scrcpy
// is not running the handler still works — every request fails dialing the
// target and the ErrorHandler answers 502, which the panel treats as "device
// UI down". Built at request time (not route-build time) so the
// OGCODE_SCRCPY_TARGET override is honored even though routes() runs early.
var (
	scrcpyProxyOnce sync.Once
	scrcpyProxy     *httputil.ReverseProxy
)

func getScrcpyProxy() *httputil.ReverseProxy {
	scrcpyProxyOnce.Do(func() {
		target, err := url.Parse(scrcpyTarget())
		if err != nil {
			// A configured URL — surfacing the misconfiguration beats silently
			// proxying to a garbage target.
			panic("scrcpy: parse target " + scrcpyTarget() + ": " + err.Error())
		}
		proxy := httputil.NewSingleHostReverseProxy(target)
		// Flush immediately: the stream WebSocket upgrades and video frames must
		// reach the browser live rather than buffering (same contract as the
		// controlplane tunnel proxy).
		proxy.FlushInterval = -1
		proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w,
				"ws-scrcpy is not reachable at "+scrcpyTarget()+" (start it with ~/.android-browser/start.sh): "+err.Error(),
				http.StatusBadGateway)
		}
		scrcpyProxy = proxy
	})
	return scrcpyProxy
}

// serveScrcpy wires the ws-scrcpy web app (http://127.0.0.1:8000, started
// separately from ogcode) onto /scrcpy. The proxy strips the /scrcpy prefix,
// so the SPA, its assets and its stream WebSockets all reach ws-scrcpy exactly
// as it serves them at its own root.
//
// This is dumb plumbing on purpose: ws-scrcpy stays an external, independently
// updatable app and ogcode learns nothing about H.264 or the scrcpy protocol.
// Dropping the feature later means deleting this route.
//
// It is registered before the SPA fallback (next to servePublic) so /scrcpy
// paths never answer with the embedded index.html. The wildcard must be
// r.Handle("/scrcpy/*"), not r.Get("/scrcpy/"): chi treats a Get-registered
// "/scrcpy/" as an exact path and sends /scrcpy/bundle.js to NotFound (the SPA
// fallback) — Handle's /scrcpy/* matches everything under the prefix.
func (s *Server) serveScrcpy(r chiRouter) {
	// getScrcpyProxy() must be called at REQUEST time, not at route-build
	// time: routes() runs inside Serve() before a test (or an operator env
	// override) can point OGCODE_SCRCPY_TARGET elsewhere, and the sync.Once
	// would freeze the default target forever.
	r.Handle(scrcpyPrefix+"/*", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.StripPrefix(scrcpyPrefix, getScrcpyProxy()).ServeHTTP(w, req)
	}))
	r.Handle(scrcpyPrefix, http.RedirectHandler(scrcpyPrefix+"/", http.StatusMovedPermanently))
}

// handleScrcpyStatus answers GET /api/scrcpy/status with whether ws-scrcpy is
// up and where the proxy points. The device panel polls this to decide between
// embedding the stream and showing the "start ws-scrcpy" hint.
func (s *Server) handleScrcpyStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"up":     s.scrcpyReachable(),
		"target": scrcpyTarget(),
	})
}

// scrcpyDevice is one adb-attached device the device picker can stream.
type scrcpyDevice struct {
	// Serial is the adb serial ("emulator-5554", a USB serial, an IP:port).
	Serial string `json:"serial"`
	// State is adb's word for it: "device", "offline", "unauthorized", ….
	State string `json:"state"`
	// Model is the human name adb -l reports ("" when it does not).
	Model string `json:"model"`
}

// adbBinary resolves the adb executable: ANDROID_HOME/platform-tools/adb, then
// ANDROID_SDK_ROOT/platform-tools/adb, then ~/Library/Android/sdk/platform-tools/adb
// (the macOS Android Studio layout), then plain PATH. First hit wins.
func adbBinary() string {
	var candidates []string
	if home := os.Getenv("ANDROID_HOME"); home != "" {
		candidates = append(candidates, filepath.Join(home, "platform-tools", "adb"))
	}
	if sdk := os.Getenv("ANDROID_SDK_ROOT"); sdk != "" {
		candidates = append(candidates, filepath.Join(sdk, "platform-tools", "adb"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, "Library", "Android", "sdk", "platform-tools", "adb"))
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	// No SDK layout found — fall back to PATH; if adb is absent the devices
	// call fails and the endpoint answers with an empty list.
	return "adb"
}

// adbDevices lists the devices `adb devices -l` sees, sorted by serial.
// Offline and unauthorized devices are included — the picker greys them so an
// unaccepted debugging prompt is visible rather than the device silently
// missing. Any failure (adb not installed, timeout) is an empty list: the
// panel says "no devices", which is the honest answer.
func adbDevices() []scrcpyDevice {
	out, err := adbDevicesOutput()
	if err != nil {
		return nil
	}
	devices := parseAdbDevices(out)
	sort.Slice(devices, func(i, j int) bool { return devices[i].Serial < devices[j].Serial })
	return devices
}

// adbDevicesOutput shells out to adb. Kept separate from adbDevices so tests
// can stub the process boundary without faking files on disk.
func adbDevicesOutput() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, adbBinary(), "devices", "-l").Output()
	return string(out), err
}

// parseAdbDevices reads `adb devices -l` output: a "List of devices attached"
// header, then one line per device — "<serial> <state> usb:… product:… model:…".
// The leading-`*` daemon lines and anything without a serial plus a state are
// skipped. A serial is taken only once.
func parseAdbDevices(out string) []scrcpyDevice {
	var devices []scrcpyDevice
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "*") || strings.HasPrefix(line, "List of devices") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || seen[fields[0]] {
			continue
		}
		seen[fields[0]] = true
		d := scrcpyDevice{Serial: fields[0], State: fields[1]}
		for _, f := range fields[2:] {
			if model, ok := strings.CutPrefix(f, "model:"); ok {
				d.Model = strings.ReplaceAll(model, "_", " ")
				break
			}
		}
		devices = append(devices, d)
	}
	return devices
}

// handleScrcpyDevices answers GET /api/scrcpy/devices with the adb-attached
// device list the picker offers: serial, adb state, model. It only reads the
// list — starting emulators and pairing stay outside ogcode, same as
// ws-scrcpy itself.
func (s *Server) handleScrcpyDevices(w http.ResponseWriter, _ *http.Request) {
	devices := adbDevices()
	if devices == nil {
		devices = []scrcpyDevice{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": devices})
}

// scrcpyReachable reports whether ws-scrcpy answers on its port, for UI hints
// and tests. It dials the SPA root: whatever the app serves, an HTTP answer
// means the process is up.
func (s *Server) scrcpyReachable() bool {
	// Bounded: this runs on every /api/scrcpy/status poll (10s cadence from the
	// pill and the device page), and an unresponsive peer must not hold the
	// endpoint open.
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(strings.TrimSuffix(scrcpyTarget(), "/") + "/")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode < 500
}
