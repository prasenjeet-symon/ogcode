---
name: browser-pair-restart
description: After a change to browser pairing, the native host, or anything else needing both a server restart and Chrome relaunching the native host. Rebuilds, installs, hands over the restart, starts Chrome so autoPairThenConnect re-pairs, and verifies the pair end to end.
---

## Steps

1. **Rebuild and install first.** `make build` (web + server, `CGO_ENABLED=1` is
   handled by the Makefile), then `make -s install`. Verify the new symbol is in
   the installed binary: `strings ~/.local/bin/ogcode | grep -c <new-symbol>` must
   be ≥ 1. A relaunch runs `~/.local/bin/ogcode`, never repo `./ogcode`.

2. **Hand over the server restart — never run it yourself.** Killing the host
   server kills the session you are running in. Give the user the exact commands:
   `kill <pid>; cd <dir> && ogcode` (no `--port`; the port persists in
   `~/.ogcode/ports.json`). This project's port is in `/Users/prasenjeet/Downloads/work/ogcode/.ogcode/`.
   Wait for the old PID to EXIT (`port free` ≠ dead — SIGTERM frees the listener
   before the flock), then check the new PID differs and `GET /api/config`
   returns 200.

3. **Start Chrome — this is the one-liner that used to be manual.** The agent can
   run this itself (background app, harmless to the session):
   `open -a "Google Chrome"`. Chrome is the launcher of the native host: its
   unloaded worker starts with the browser and fires `autoPairThenConnect` ~5 s
   into worker life, which launches the `com.ogcode` host, probes the portmap,
   and pairs against `GET /api/browser/pair` with zero paste.

4. **Verify the pair.** Ask for `browser_status` (or the user's Connect status):
   it must report the extension paired to this server. A quick server-side check
   is that the extension's worker launched the host — the pair token persists at
   `<project>/.ogcode/browser-pair-token`.

## Gotchas

- **A failed `autoPairThenConnect` is memoized for the worker's life.** If Chrome
  came up before the server was listening (or pairing failed once), that boot
  never retries — reload the extension or quit-and-restart Chrome to retry.
  Chrome must come up AFTER the server answers `GET /api/config`.
- **A native-host code fix needs no Chrome or server restart** — the host is
  re-exec'd from the current binary per launch — but a manifest
  `allowed_origins` change needs one full Chrome restart (manifests are read at
  browser startup).
- **Never re-pair on "extension not paired" after a heavy op kills the service
  worker** — the worker died, not the pairing; reopening the side panel
  reconnects.