#!/bin/sh
# assign.sh — provision one Incus container per user-repo assignment (Phase A,
# operator-only). Creates a container from the ogcode-base image + ogcode-worker
# profile, passes a cloud-init user-data that writes /etc/ogcode/* (master URL,
# pairing secret, repo URL + slug, optional CA) and /root/.ogcode/worker-id,
# waits until the in-guest ogcode-worker has registered with the control-plane
# master, and prints the panel URL.
#
# No Go code changes: the container registers as an ORDINARY worker. Assignment
# (user -> repo -> worktree, base branch, branch user/<name>) still happens
# through the EXISTING control-plane panel after this script returns.
#
# Usage:
#   ./assign.sh --master URL --repo REPO_URL --user NAME \
#               [--secret-file PATH | env OGCODE_PAIRING_SECRET] \
#               [--master-ca-file PATH] [--base-branch BRANCH] \
#               [--panel-host HOST] [--image ogcode-base] [--profile ogcode-worker] \
#               [--timeout SECS] [--no-wait]
#
# Required:
#   --master URL          control-plane URL workers dial (e.g.
#                         https://panel.example.com — the apex/panel host, not
#                         a per-worker URL; master config listen/pairingSecret).
#   --repo REPO_URL       repository the container clones at boot (cloned by
#                         the ogcode-clone systemd unit into the same directory
#                         the worker's EnsureRepo would use).
#   --user NAME           the user this container is for. Naming only in Phase
#                         A (the panel's username governs branches/worktrees);
#                         it labels the container.
#   secret: --secret-file PATH or $OGCODE_PAIRING_SECRET. The worker pairing
#     secret (master config `pairingSecret`). Travels via cloud-init
#     user-data into /etc/ogcode/pairing-secret (0600) in-guest — never in
#     the worker's argv, never in the image. (cloud-init user-data is
#     readable by root on the Incus host and, briefly, inside the guest:
#     treat both as secret-bearing, per the plan's threat model.)
#
# Optional:
#   --master-ca-file PATH CA bundle (PEM) for self-signed master TLS; baked
#                         into user.ogcode.master-ca. Omit for a CA-signed
#                         master (worker then uses system roots).
#   --base-branch BRANCH  operator note only (assignment/branch comes from the
#                         panel; recorded in user.ogcode.base-branch).
#   --panel-host HOST     panel host for the printed URL; derived from --master
#                         (scheme + port stripped) when omitted.
#   --image IMAGE         default ogcode-base (from build-image.sh).
#   --profile PROFILE     default ogcode-worker (from profile.yaml).
#   --timeout SECS        registration wait budget, default 600
#                         (registerTimeoutSeconds in the plan).
#   --no-wait             return immediately after launch (readiness is still
#                         signalled only by the worker's Register).
#
# Container naming (the load-bearing name math — mirrors, byte-identical):
#   repo slug   safeRepoName / repoSlugFromURL  (internal/worker/repos.go:69,
#               controlplane/internal/master/repos.go:88) — clone dir name.
#   user slug   safeUserName / userWorktreeSlug (internal/worker/repos.go:226,
#               controlplane/internal/master/repos.go:123) — branch/worktree
#               segment, from the panel at assignment time (NOT here).
#   name        "og-<reposlug>-<user>" (INCUS_WORKERS_PLAN.md §naming), capped
#               at NAME_BUDGET=40 bytes (repo segment trimmed first, user
#               survives — routeLabel's rule) so container name (<=63) + tunnel
#               route (<=63) can't jointly overflow one DNS label.
#               The composite must satisfy validWorkerID (controlplane/internal/
#               master/server.go:457: 1-63 bytes, [a-z0-9-], no edge hyphen) —
#               the worker id IS the container name (workerid.go prefers an
#               existing ~/.ogcode/worker-id; cloud-init writes the container
#               name there). validWorkerID allows no '.'/'_' and only
#               lowercase, so the NAME segments additionally pass through a
#               validWorkerID-safe fold (lowercase + '.'/'_' -> '-', collapse,
#               re-trim). The clone dir keeps the RAW slug — it must stay
#               byte-identical to safeRepoName; only the container name folds.
#
# Phase A caveat (pickWorker, controlplane/internal/master/repos.go:166):
#   placement still prefers the worker already holding the repo slug, else the
#   online worker with the most free bytes. To LAND an assignment on this
#   container, assign through the panel while this container is the repo's
#   clone holder or the highest-free-bytes worker. Phase B's placement store
#   replaces that heuristic.
#
# Prerequisites on this host: incus; the ogcode-base image and ogcode-worker
# profile must exist (build-image.sh / profile.yaml).
#
# Design note: per-container configuration rides in cloud-init user-data, not
# user.* instance keys. user.ogcode.master-url / repo-url / base-branch /
# master-ca are still set as instance keys for the OPERATOR (incus list,
# future Phase B reconciliation sweep) — but the guest reads only what
# cloud-init wrote into /etc/ogcode (the guest API is off; no in-guest
# `incus config get`). The pairing secret is deliberately NOT in a user.* key.
#
# Manual verification checklist (plan §Phase A):
#   [ ] `incus list og-` shows the container RUNNING
#   [ ] `incus exec <name> -- cat /root/.ogcode/worker-id` == container name
#   [ ] `incus exec <name> -- systemctl is-active ogcode-worker` == active
#   [ ] master saw the worker: operator console shows worker <name> online
#       (or `journalctl` on the master for the Register request)
#   [ ] panel URL printed below resolves in a browser (apex console -> Users
#       page -> assign the user to the repo; the worktree lands on this
#       container per the Phase A caveat above)
#   [ ] clone pre-warmed: `incus exec <name> -- ls /root/.ogcode/repos`
#       contains the repo slug BEFORE any assignment
set -eu

PROGNAME=$(basename "$0")
IMAGE=ogcode-base
PROFILE=ogcode-worker
NAME_BUDGET="${OGCODE_NAME_BUDGET:-40}"   # plan §Tunnels: cap container names ~40 bytes
TIMEOUT="${OGCODE_REGISTER_TIMEOUT:-600}" # plan: registerTimeoutSeconds = 600
WAIT=1
MASTER_URL=""
REPO_URL=""
USER_NAME=""
SECRET=""
CA_PEM=""
PANEL_HOST=""
BASE_BRANCH=""

usage() {
  sed -n '2,99p' "$0" | sed 's/^# \{0,1\}//'
  exit "${1:-0}"
}

die() { echo "$PROGNAME: $*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --help|-h) usage 0 ;;
    --master) MASTER_URL=$2; shift 2 ;;
    --repo) REPO_URL=$2; shift 2 ;;
    --user) USER_NAME=$2; shift 2 ;;
    --secret-file) [ -f "$2" ] || die "secret file not found: $2"; SECRET=$(cat "$2"); shift 2 ;;
    --master-ca-file) [ -f "$2" ] || die "CA file not found: $2"; CA_PEM=$(cat "$2"); shift 2 ;;
    --base-branch) BASE_BRANCH=$2; shift 2 ;;
    --panel-host) PANEL_HOST=$2; shift 2 ;;
    --image) IMAGE=$2; shift 2 ;;
    --profile) PROFILE=$2; shift 2 ;;
    --timeout) TIMEOUT=$2; shift 2 ;;
    --no-wait) WAIT=0; shift ;;
    *) die "unknown argument: $1 (see --help)" ;;
  esac
done

[ -n "$MASTER_URL" ] || { usage 2; }
[ -n "$REPO_URL" ] || { usage 2; }
[ -n "$USER_NAME" ] || { usage 2; }
[ -n "$SECRET" ] || SECRET=${OGCODE_PAIRING_SECRET:-}
[ -n "$SECRET" ] || die "no pairing secret: pass --secret-file or set OGCODE_PAIRING_SECRET"
SECRET=$(printf '%s' "$SECRET" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')
[ -n "$SECRET" ] || die "pairing secret is empty/whitespace"

command -v incus >/dev/null 2>&1 || die "incus not found on this host"

# --------------------------------------------------------------------------
# Name math. Mirrors of the Go slug functions — do not "improve" them; the
# worker's clone dir and the master's placement key depend on byte-identity.
# --------------------------------------------------------------------------

# safeRepoName == repoSlugFromURL, verbatim rules (see ogcode-clone.service
# for the numbered list). Steps: scheme, last-@, ':'->'-', .git, ' '->'-',
# keep [a-z0-9-./_] (NO lowercasing), trim -./_, collapse --, '/'->'-', ""->repo.
repo_slug=$(printf '%s' "$REPO_URL" | sed \
  -e 's|^[^:]*://||' \
  -e 's|.*@||' \
  -e 's/:/-/g' \
  -e 's/\.git$//' \
  -e 's/ /-/g' \
  -e 's|[^a-z0-9./_-]||g' \
  -e 's/^[-./_]*//' \
  -e 's/[-./_]*$//')
repo_slug=$(printf '%s' "$repo_slug" | sed -e 's/--*/-/g' -e 's#/#-#g')
[ -n "$repo_slug" ] || repo_slug=repo

# safeUserName == userWorktreeSlug: lowercase, ' '->'-', keep [a-z0-9-._],
# trim -._, collapse --, ""->user.
user_slug=$(printf '%s' "$USER_NAME" | tr ' A-Z' '-a-z' | sed \
  -e 's|[^a-z0-9._-]||g' \
  -e 's/^[._-]*//' \
  -e 's/[._-]*$//')
user_slug=$(printf '%s' "$user_slug" | sed -e 's/--*/-/g')
[ -n "$user_slug" ] || user_slug=user

# validWorkerID-safe fold for the CONTAINER NAME only (lowercase + '.'/'_' ->
# '-', collapse, trim): worker id == container name must satisfy
# [a-z0-9-]{1,63} with no edge hyphen.
fold_for_name() {
  printf '%s' "$1" | tr 'A-Z._' 'a-z--' | sed -e 's/--*/-/g' -e 's/^-*//' -e 's/-$//'
}
name_repo=$(fold_for_name "$repo_slug")
name_user=$(fold_for_name "$user_slug")
[ -n "$name_repo" ] || name_repo=repo
[ -n "$name_user" ] || name_user=user

# Budget: "og-" (3) + repo + "-" (1) + user <= NAME_BUDGET. Repo trimmed first
# (routeLabel's rule), then re-trim trailing hyphens the cut may have exposed.
budget=$((NAME_BUDGET - 3 - 1 - ${#name_user}))
if [ "$budget" -lt 1 ]; then
  budget=$((NAME_BUDGET - 3 - 1))   # degenerate user name; still cut the repo
fi
if [ ${#name_repo} -gt "$budget" ]; then
  name_repo=$(printf '%s' "$name_repo" | cut -c1-"$budget")
  name_repo=$(fold_for_name "$name_repo")
  echo "note: repo segment trimmed to '$name_repo' to fit the ${NAME_BUDGET}-byte name budget" >&2
fi
NAME="og-${name_repo}-${name_user}"

# validWorkerID mirror: 1-63 bytes, [a-z0-9-], no edge hyphen (server.go:457).
[ ${#NAME} -le 63 ] || die "container name '$NAME' exceeds 63 bytes (worker id would be invalid)"
case "$NAME" in
  *[!a-z0-9-]*) die "name '$NAME' has chars outside [a-z0-9-] (validWorkerID)" ;;
  -*) die "name '$NAME' starts with a hyphen (validWorkerID)" ;;
  *-) die "name '$NAME' ends with a hyphen (validWorkerID)" ;;
  *) : ;;
esac
case "$NAME" in
  [a-z0-9-]*) : ;;
  *) die "name '$NAME' is empty" ;;
esac

echo "==> repo slug: $repo_slug (clone dir /root/.ogcode/repos/$repo_slug)"
echo "==> container: $NAME (worker id; user segment from '$USER_NAME')"
if incus list --format csv | cut -d, -f1 | grep -Fxq "$NAME"; then
  die "container '$NAME' already exists (unassign.sh first, or a different --user)"
fi
incus image list --format csv | grep -q "^$IMAGE," ||
  die "image '$IMAGE' not found — run build-image.sh first"

# ------------------------------------------------------------- launch -------
echo "==> launching $NAME (image=$IMAGE profile=$PROFILE)"

# The per-container configuration travels as cloud-init user-data (Incus cloud
# images ship cloud-init; the guest API stays at its default ON — cloud-init's
# Incus datasource fetches this user-data through it — so user.* instance keys
# are operator metadata only and are NOT part of the guest's config surface:
# the guest can read only its OWN instance config through the socket, which
# carries no secrets by design). cloud-init writes:
#   /etc/ogcode/master-url /etc/ogcode/pairing-secret (0600)
#   /etc/ogcode/master-ca.pem (only when provided)
#   /etc/ogcode/repo-url + /etc/ogcode/repo-slug (for ogcode-clone.service)
#   /root/.ogcode/worker-id  (== container name: the worker id)
#   /etc/ogcode/base-branch  (operator note only)
# The units are baked into the image already; cloud-init only drops config
# files and enables the two services (systemctl preset is not needed here —
# enabling explicitly in runcmd covers reboots).
user_data=$(mktemp "${TMPDIR:-/tmp}/og-seed.XXXXXX")
chmod 600 "$user_data"
{
  echo "#cloud-config"
  echo "write_files:"
  echo "  - path: /etc/ogcode/master-url"
  echo "    permissions: '0644'"
  echo "    content: |"
  printf '      %s\n' "$MASTER_URL"
  echo "  - path: /etc/ogcode/pairing-secret"
  echo "    permissions: '0600'"
  echo "    content: |"
  printf '      %s\n' "$SECRET"
  echo "  - path: /etc/ogcode/repo-url"
  echo "    permissions: '0644'"
  echo "    content: |"
  printf '      %s\n' "$REPO_URL"
  echo "  - path: /etc/ogcode/repo-slug"
  echo "    permissions: '0644'"
  echo "    content: |"
  printf '      %s\n' "$repo_slug"
  echo "  - path: /etc/ogcode/base-branch"
  echo "    permissions: '0644'"
  echo "    content: |"
  printf '      %s\n' "$BASE_BRANCH"
  echo "  - path: /root/.ogcode/worker-id"
  echo "    permissions: '0644'"
  echo "    content: |"
  printf '      %s\n' "$NAME"
  echo "  - path: /etc/ogcode/worker.env"
  echo "    permissions: '0644'"
  echo "    content: |"
  printf '      OGCODE_MASTER_URL=%s\n' "$MASTER_URL"
  printf '      OGCODE_WORKER_NAME=%s\n' "$NAME"
  if [ -n "$CA_PEM" ]; then
    printf '      OGCODE_EXTRA_FLAGS=--master-ca /etc/ogcode/master-ca.pem\n'
  else
    printf '      OGCODE_EXTRA_FLAGS=\n'
  fi
  if [ -n "$CA_PEM" ]; then
    echo "  - path: /etc/ogcode/master-ca.pem"
    echo "    permissions: '0644'"
    echo "    content: |"
    printf '%s\n' "$CA_PEM" | sed 's/^/      /'
  fi
  echo "runcmd:"
  echo "  - [systemctl, enable, --now, ogcode-clone.service]"
  echo "  - [systemctl, enable, --now, ogcode-worker.service]"
} > "$user_data"

set -- \
  -c "user.ogcode.worker-id=$NAME" \
  -c "user.ogcode.master-url=$MASTER_URL" \
  -c "user.ogcode.repo-url=$REPO_URL" \
  -c "user.ogcode.base-branch=$BASE_BRANCH"
if [ -n "$CA_PEM" ]; then
  set -- "$@" -c "user.ogcode.master-ca=$CA_PEM"
fi
incus launch "$IMAGE" "$NAME" --profile "$PROFILE" "$@" \
  -c "cloud-init.user-data=$(cat "$user_data")"
rm -f "$user_data"

panel_url="$PANEL_HOST"
if [ -z "$panel_url" ]; then
  # Derive the panel host from the master URL: strip scheme, then any
  # path, then :port (panel DNS is host-based; README's URL table).
  panel_url=$(printf '%s' "$MASTER_URL" | sed -e 's|^[^:]*://||' -e 's|/.*||' -e 's|:.*||')
fi
WORKER_PANEL_URL="https://${NAME}.${panel_url}/"

finish() {
  echo "==> container: $NAME"
  echo "==> worker id: $NAME (== container name; /root/.ogcode/worker-id)"
  echo "==> panel (operator console): https://${panel_url}/"
  echo "==> per-worker panel URL: $WORKER_PANEL_URL"
  echo "==> next: assign '$USER_NAME' to the repo on the Users page of the"
  echo "    console; the worktree lands on this container (see the Phase A"
  echo "    caveat about pickWorker in the header)."
}

if [ "$WAIT" = 0 ]; then
  echo "==> --no-wait: not waiting for registration"
  finish
  exit 0
fi

# ------------------------------------------------- wait for registration ----
# Readiness signal = the worker's Register, visible in the guest as the
# "registered with master" slog line of ogcode-worker (internal/worker/
# worker.go). Provisioning is async; an Incus operation completing means
# nothing about the worker.
echo "==> waiting up to ${TIMEOUT}s for 'registered with master' in journalctl -u ogcode-worker"
i=0
while [ "$i" -lt "$TIMEOUT" ]; do
  if incus exec "$NAME" -- journalctl -u ogcode-worker -n 50 --no-pager 2>/dev/null |
      grep -Fq 'registered with master'; then
    echo "==> worker registered"
    finish
    exit 0
  fi
  i=$((i + 2)); sleep 2
done

echo "$PROGNAME: timed out waiting for registration. Container left running for" >&2
echo "  debugging. Guest logs:" >&2
incus exec "$NAME" -- sh -c '
  echo "--- cloud-init ---"; cloud-init status --long 2>&1 || true
  echo "--- ogcode-clone ---"; journalctl -u ogcode-clone -n 30 --no-pager 2>&1 | tail -n 15
  echo "--- ogcode-worker ---"; journalctl -u ogcode-worker -n 30 --no-pager 2>&1 | tail -n 15
' >&2 || true
exit 1