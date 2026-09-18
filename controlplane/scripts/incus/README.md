# Incus container-per-assignment scripts (Phase A, operator-only)

Operator scripts implementing Phase A of `INCUS_WORKERS_PLAN.md` (repo root).
They create one Incus container per user-repo assignment, bake the golden
image, and tear down on unassign. **No Go code changes**: the container runs
the stock ogcode worker binary, registers as an ordinary worker, and the
existing control-plane panel does the assignment itself (Users page). These
scripts are the Phase A proof of the register-ready contract; Phase B (driver,
placement store) moves this logic into the control-plane master.

## Files

| File | Purpose |
|------|---------|
| `build-image.sh` | bake the `ogcode-base` image (ubuntu 24.04 + ogcode + units) |
| `ogcode-clone.service` | baked unit: clones the assigned repo before the worker starts |
| `ogcode-worker.service` | baked unit: the worker agent, `Restart=always`, after clone |
| `profile.yaml` | `ogcode-worker` profile (sizing + boot keys) |
| `assign.sh` | create + seed (cloud-init user-data) + launch; wait for registration; print panel URL |
| `unassign.sh` | best-effort push of `user/*` branches, then `incus delete --force` |

## Run order

```sh
# on the Incus host (Linux VM); macOS cannot run Incus natively
./build-image.sh --version v0.36.1          # or --binary ./ogcode from make build
incus profile create ogcode-worker          # first time
incus profile edit ogcode-worker < profile.yaml
./assign.sh --master https://panel.example.com \
            --repo https://github.com/org/api.git \
            --user alice \
            --secret-file ./pairing-secret
# ... agent works; operator assigns via the console (Users page) ...
./unassign.sh og-github-com-api-alice
```

`--panel-host` defaults to the host part of `--master`.

## Contract cheat-sheet (why each piece looks the way it does)

- **Worker id = container name.** `loadOrCreateWorkerID` (`internal/worker/
  workerid.go`) prefers an existing `~/.ogcode/worker-id`; assign.sh's
  cloud-init user-data writes the container name there. Zero Go changes.
- **Seeding = cloud-init user-data, not user.* keys.** Per-container values
  travel as a `#cloud-config` assign.sh passes at `incus launch` time (the
  guest API stays at its default ON — cloud-init's Incus datasource fetches
  that user-data through the socket — so user.* keys are operator metadata the
  guest's own config never carries secrets). The `#cloud-config` writes
  `/etc/ogcode/*` (master URL, pairing secret 0600, repo URL + slug, optional
  CA) and `/root/.ogcode/worker-id`, and enables the two services. The
  `user.ogcode.*` instance keys assign.sh also sets are operator metadata for
  `incus list` (and Phase B's reconciliation sweep) only.
- **Name math.** `og-<reposlug>-<user>`; repo slug = `safeRepoName` /
  `repoSlugFromURL` (byte-identical in `internal/worker/repos.go` and
  `controlplane/internal/master/repos.go`) — it is also the clone dir under
  `/root/.ogcode/repos`. User slug = `safeUserName`/`userWorktreeSlug`. The
  container name is capped at 40 bytes (repo segment trimmed first, like
  `routeLabel`) and must satisfy `validWorkerID` (1–63 bytes, `[a-z0-9-]`, no
  edge hyphen) because the worker id IS the container name.
- **Readiness = Register, not an Incus op.** assign.sh waits for the
  `registered with master` line in the guest's `journalctl -u ogcode-worker`
  (internal/worker/worker.go). An `incus launch` completing means nothing.
- **Containment pairing.** The worker unit passes `--repo-root
  /root/.ogcode/repos --workspace /root/.ogcode/repos` — the same root as both
  flags pairs the clone home as the only allowed workspace root.
- **Secrets.** The pairing secret travels host → cloud-init user-data →
  `/etc/ogcode/pairing-secret` (0600) and is read by the worker only from the
  file. Never in argv, never in the image, deliberately never in a `user.*`
  key. Root on the Incus host can read instance user-data — treat the Incus
  host as secret-bearing (that is the plan's threat model).
- **Unpushed work.** unassign.sh pushes `user/*` branches best-effort before
  delete; a failed push warns but does not block (operator's call). Task
  worktrees and unmerged non-user branches die with the container — that is
  the model, not an oversight.

## Phase A caveats

- **Placement is still the existing panel heuristic** (`pickWorker`):
  assignment lands on the clone-holder or the highest-free-bytes online
  worker. To land an assignment on a specific container, assign while it
  satisfies that. Phase B's placement store fixes this.
- **Placement memory is in-RAM** (repoStore, lost on master restart) —
  reassign after a master restart if the first assignment landed elsewhere.
- The base-branch passed to assign.sh is an operator note only (recorded in
  `user.ogcode.base-branch`); the panel's assignment picks the branch.

## Linux-VM verification checklist (run on the VM)

1. `./build-image.sh --version vX.Y.Z` → alias exists (`incus image list`).
2. `incus profile create ogcode-worker && incus profile edit ogcode-worker < profile.yaml` → profile exists.
3. `./assign.sh --master ... --repo ... --user alice --secret-file ...` →
   container registers; checklist items in the script header all pass.
4. Operator assigns via console (Users page) → worktree lands on the
   container; panel URL serves the worktree UI.
5. `./unassign.sh og-<...>` → push warning path exercised (make a local commit
   first), delete, worker offline in console.
6. Re-run assign.sh with the same `--user`/`--repo` → clean re-provision.