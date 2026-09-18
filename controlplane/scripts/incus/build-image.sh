#!/bin/sh
# build-image.sh — bake the `ogcode-base` Incus image (Phase A, operator-only).
#
# Launches a throwaway images:ubuntu/24.04 container, installs the
# prerequisites, the ogcode binary, and the two
# systemd units, then publishes it as the reusable `ogcode-base` image and
# removes the builder.
# Everything the guest needs is pushed from this host — the guest never
# downloads ogcode from the internet.
#
# What the image bakes (the "golden image" contract of INCUS_WORKERS_PLAN.md):
#   /usr/local/bin/ogcode              the worker-capable server binary
#   /etc/systemd/system/ogcode-clone.service   oneshot: clones the assigned repo BEFORE the worker starts
#   /etc/systemd/system/ogcode-worker.service  Restart=always; runs after ogcode-clone
#   cloud-init, git, curl, ca-certificates  cloud-init applies assign.sh's
#                             per-container user-data at launch; the agent shells
#                             out to git; the rest are runtime deps
#
# Per-container configuration (master URL, pairing secret, repo URL + slug,
# worker-id, optional CA) is NOT in the image: assign.sh passes it at
# `incus launch` time as cloud-init user-data, which writes /etc/ogcode/* and
# /root/.ogcode/worker-id and enables the two services.
#
# Usage:
#   ./build-image.sh [--version vX.Y.Z | --binary /path/to/ogcode]
#                    [--arch x86_64|arm64]
#                    [--image ogcode-base] [--keep]
#
# Sources of the binary (first that succeeds):
#   1. --binary PATH           copy an already-built binary (e.g. make build
#                              output ./ogcode, or a release tarball's binary)
#   2. --version vX.Y.Z        download ogcode_<ver>_linux_<arch>.tar.gz from
#                              the GitHub release
#   3. $OGCODE_VERSION env     same as --version
#   4. $PWD/../../..           repo checkout: `make build` (needs CGO_ENABLED=1;
#                              the web UI is embedded — a bare `go build` is not
#                              a valid worker binary)
#
# Prerequisites on THIS host: incus, git, curl, tar (a git checkout for source 4).
# The image is linux-only: run this on the Incus VM, not on macOS.
#
# Manual verification checklist (plan §Phase A):
#   [ ] `incus image list ogcode-base` shows the new alias after the run
#   [ ] `incus publish` left no builder container behind (--keep to keep it for
#       debugging; then `incus delete og-image-builder --force`)
#   [ ] baked binary runs: `incus exec <any ogcode-base container> -- ogcode version`
#   [ ] units present: `incus exec <c> -- systemctl list-unit-files 'ogcode-*'`
#       (both disabled in the image — cloud-init enables them per container;
#       verify with `systemctl is-enabled ogcode-clone ogcode-worker` AFTER a
#       seeded launch)
set -eu

PROGNAME=$(basename "$0")
IMAGE=ogcode-base
KEEP=0
BINARY_SRC=""
VERSION="${OGCODE_VERSION:-}"

usage() {
  sed -n '2,50p' "$0" | sed 's/^# \{0,1\}//'
  exit "${1:-0}"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --help|-h) usage 0 ;;
    --image) IMAGE=$2; shift 2 ;;
    --keep) KEEP=1; shift ;;
    --binary) BINARY_SRC=$2; shift 2 ;;
    --version) VERSION=$2; shift 2 ;;
    --arch) BAKE_ARCH=$2; shift 2 ;;
    *) echo "$PROGNAME: unknown option: $1 (see --help)" >&2; exit 2 ;;
  esac
done

for tool in incus git curl tar; do
  command -v "$tool" >/dev/null 2>&1 || {
    echo "$PROGNAME: required tool not found on this host: $tool" >&2; exit 1;
  }
done

WORKDIR=$(mktemp -d "${TMPDIR:-/tmp}/og-image.XXXXXX")
trap 'rm -rf "$WORKDIR"' EXIT

# ---------------------------------------------------------------- host arch --
# GoReleaser asset mapping (".goreleaser.yaml"): amd64 -> x86_64, 386 -> i386,
# everything else (arm64) is kept verbatim. Incus container arch == host arch.
HOST_ARCH=$(uname -m)
case "${BAKE_ARCH:-$HOST_ARCH}" in
  x86_64|amd64) DL_ARCH=x86_64 ;;
  aarch64|arm64) DL_ARCH=arm64 ;;
  *) echo "$PROGNAME: unsupported arch: ${BAKE_ARCH:-$HOST_ARCH}" >&2; exit 1 ;;
esac

# ------------------------------------------------------------------ binary ---
BIN="$WORKDIR/ogcode"
fetch_binary() {
  if [ -n "$BINARY_SRC" ]; then
    cp "$BINARY_SRC" "$BIN"
  elif [ -n "$VERSION" ]; then
    VER=${VERSION#v}   # release assets carry the version WITHOUT the v prefix
    URL="https://github.com/prasenjeet-symon/ogcode/releases/download/v${VER}/ogcode_${VER}_linux_${DL_ARCH}.tar.gz"
    echo "==> downloading $URL"
    # BIN already lives in WORKDIR; extracting the tarball's root ogcode onto
    # it directly makes the mv below a same-file no-op (fatal under set -eu).
    mkdir "$WORKDIR/bin"
    curl -fsSL "$URL" | tar -xz -C "$WORKDIR/bin" ogcode
    mv "$WORKDIR/bin/ogcode" "$BIN"
    rmdir "$WORKDIR/bin"
  elif [ -f "$(cd "$(dirname "$0")/../../.." && pwd)/main.go" ]; then
    REPO_ROOT=$(cd "$(dirname "$0")/../../.." && pwd)
    echo "==> building ogcode from $REPO_ROOT (make build; CGO is required —"
    echo "    a bare go build is NOT a valid worker binary, the web UI embed +"
    echo "    cgo PDF/Swift grammars live behind CGO_ENABLED=1)"
    ( cd "$REPO_ROOT" && make build >/dev/null )
    cp "$REPO_ROOT/ogcode" "$BIN"
  else
    echo "$PROGNAME: no ogcode source. Pass --binary PATH, --version vX.Y.Z, run" >&2
    echo "  from a checkout, or set \$OGCODE_VERSION." >&2
    exit 1
  fi
  [ -x "$BIN" ] || { echo "$PROGNAME: fetched binary is not executable" >&2; exit 1; }
}
fetch_binary

# ---------------------------------------------------------------- container --
BUILDER=og-image-builder
incus delete "$BUILDER" --force >/dev/null 2>&1 || true
echo "==> launching builder $BUILDER (images:ubuntu/24.04)"
# images: (simplestreams) is the remote every stock Incus install carries; the
# bare ubuntu: remote is LXD's default set, not Incus's.
incus launch images:ubuntu/24.04 "$BUILDER"

# incus exec needs the guest's init to be up; wait for systemd to answer.
wait_guest() {
  i=0
  while [ $i -lt 120 ]; do
    if incus exec "$BUILDER" -- true 2>/dev/null; then return 0; fi
    i=$((i + 1)); sleep 1
  done
  return 1
}
wait_guest || { echo "$PROGNAME: builder container never became reachable" >&2; exit 1; }

echo "==> installing guest packages (cloud-init git curl ca-certificates)"
# cloud-init must be IN the image: assign.sh seeds per-container config as
# cloud-init user-data at launch time, and the images: simplestreams ubuntu
# image does not preinstall it (LXD's ubuntu: images do; we launch images:).
incus exec "$BUILDER" -- env DEBIAN_FRONTEND=noninteractive apt-get update -qq
incus exec "$BUILDER" -- env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq \
  cloud-init git curl ca-certificates

# The guest API socket is /dev/incus/sock on Incus, but cloud-init's
# ds-identify (ubuntu noble) hardcodes /dev/lxd/sock as the LXD-datasource
# probe — without it cloud-init disables itself and no per-container config
# is ever applied. Bake a compat symlink via tmpfiles.d (devtmpfs is
# regenerated at every boot, so a plain baked /dev symlink would not
# survive). Newer Incus releases create the /dev/lxd symlink themselves;
# this makes the image self-sufficient on any Incus.
incus exec "$BUILDER" -- sh -c 'cat > /etc/tmpfiles.d/lxd-sock-compat.conf <<EOF
#Type Path        Mode User Group Age Argument
d     /dev/lxd    0755 -    -     -   -
L+    /dev/lxd/sock -  -    -     -   /dev/incus/sock
EOF
systemd-tmpfiles --create /etc/tmpfiles.d/lxd-sock-compat.conf'

# Pin the datasource list so ds-identify's generator enables cloud-init
# without probing: with a single-entry list it skips the socket check
# entirely (the probe runs at generator time, before tmpfiles-setup has
# created the /dev/lxd/sock compat symlink above, so probing always lost).
# cloud-init proper then reads the socket through the same symlink — this
# entry alone is not enough, and the symlink alone is not enough either.
incus exec "$BUILDER" -- mkdir -p /etc/cloud/cloud.cfg.d
# ds-identify's check_config greps every candidate file at once and keeps the
# LAST matching line in glob order — noble's 90_dpkg.cfg pins a long search
# list and beats any earlier drop-in (and '90-' < '90_' in C locale). The
# robust override is to rewrite 90_dpkg.cfg itself.
# Heredocs cannot be nested inside the single-quoted sh -c payload (the
# outer shell eats the delimiter), so the body is piped in on stdin instead.
printf '%s\n' \
  '# to update this file, run dpkg-reconfigure cloud-init' \
  '# ogcode image pin: single-entry list makes ds-identify skip the socket' \
  '# probe (the /dev/lxd/sock compat symlink does not exist at generator time)' \
  '# and enable cloud-init unconditionally. LXD ds reads the socket through' \
  "# /etc/tmpfiles.d/lxd-sock-compat.conf's symlink to /dev/incus/sock." \
  'datasource_list: [ LXD ]' \
  | incus exec "$BUILDER" -- sh -c 'cat > /etc/cloud/cloud.cfg.d/90_dpkg.cfg'
incus exec "$BUILDER" -- cat /etc/cloud/cloud.cfg.d/90_dpkg.cfg
incus exec "$BUILDER" -- cat /etc/cloud/cloud.cfg | tail -3

echo "==> pushing ogcode binary ($DL_ARCH)"
incus file push "$BIN" "$BUILDER/usr/local/bin/ogcode" --gid 0 --uid 0
incus exec "$BUILDER" -- chmod 0755 /usr/local/bin/ogcode
incus exec "$BUILDER" -- ogcode version

echo "==> installing systemd units"
for unit in ogcode-clone.service ogcode-worker.service; do
  incus file push "$(dirname "$0")/$unit" "$BUILDER/etc/systemd/system/$unit" --gid 0 --uid 0
done
incus exec "$BUILDER" -- systemctl daemon-reload

# ----------------------------------------------------------------- publish ---
echo "==> stopping builder and publishing image $IMAGE"
incus stop "$BUILDER"
if incus image list --format csv | grep -q "^$IMAGE,"; then
  echo "==> removing stale alias $IMAGE (will be replaced)"
  incus image delete "$IMAGE"
fi
incus publish "$BUILDER" --alias "$IMAGE" \
  description="ogcode worker base: ubuntu 24.04 + ogcode + systemd units"
if [ "$KEEP" = 1 ]; then
  echo "==> --keep: leaving builder $BUILDER in place (stopped)."
  echo "    Delete it when done debugging: incus delete $BUILDER --force"
else
  incus delete "$BUILDER" --force
fi

echo "==> done. Next: edit profile.yaml, then 'incus profile create ogcode-worker'"
echo "    (or 'incus profile edit ogcode-worker' < profile.yaml), then ./assign.sh"