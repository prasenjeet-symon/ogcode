#!/usr/bin/env bash
# Build ogcode and run one SWE-bench Lite instance on a NATIVE amd64 Linux host,
# then grade it. Run from the repo root on the amd64 box:
#
#   OLLAMA_BASE_URL=http://<router-host>:8090/v1 bash bench/run_swebench_amd64.sh
#
# Why this must be amd64-native: the eval images are linux/amd64 only, and amd64
# Go crashes under emulation on an arm64 Mac (GC corruption / crypto-tls SIGSEGV).
# Building here uses the repo Dockerfile, which compiles the web UI (go:embed
# all:dist) and links the musl binary in one shot -- the same recipe the release
# uses natively per-platform (CGO can't be cross-compiled; see .goreleaser.yaml).
#
# The binary is served to the eval container over HTTP via host.docker.internal,
# which swebench_runner already wires with --add-host. No hosting needed.
set -euo pipefail

INSTANCE="${INSTANCE:-pallets__flask-5063}"
MODEL="${MODEL:-ollama/glm-5.3-flash:cloud}"
PORT="${PORT:-8099}"
IMAGE="${IMAGE:-ogcode-bench:local}"
WORK="${WORK:-bench_swe_out}"
: "${OLLAMA_BASE_URL:?set OLLAMA_BASE_URL to the router URL reachable from this amd64 host, e.g. http://MAC-LAN-IP:8090/v1}"
export OLLAMA_API_KEY="${OLLAMA_API_KEY:-ollama}"

cd "$(cd "$(dirname "$0")/.." && pwd)"

if [ "$(uname -m)" != "x86_64" ]; then
  echo "error: $(uname -m) host -- this script is for a NATIVE amd64 box (see the emulation note above)." >&2
  exit 1
fi

DOCKERFILE="${DOCKERFILE:-Dockerfile}"
echo "==> [1/4] building ogcode (web UI + musl binary) via $DOCKERFILE"
docker build -f "$DOCKERFILE" -t "$IMAGE" .

echo "==> [2/4] extracting the binary and staging a tarball for the eval container"
serve_dir="$WORK/_serve"
mkdir -p "$serve_dir"
cid="$(docker create "$IMAGE")"
trap 'docker rm -f "$cid" >/dev/null 2>&1 || true' EXIT
docker cp "$cid:/usr/local/bin/ogcode" "$serve_dir/ogcode"
docker rm -f "$cid" >/dev/null; trap - EXIT
# Sanity: does the binary at least run in its own (musl) image? The runner does
# the authoritative check -- `ogcode version` inside the *eval* image, exiting 4
# if the binary won't run there (e.g. musl-dynamic on a glibc image, in which
# case rebuild with a fully-static link).
docker run --rm "$IMAGE" ogcode version >/dev/null && echo "    binary runs (musl image)"
tar -czf "$serve_dir/ogcode.tar.gz" -C "$serve_dir" ogcode
echo "    staged $serve_dir/ogcode.tar.gz"

echo "==> [3/4] serving the tarball on :$PORT for the eval container"
python3 -m http.server "$PORT" --directory "$serve_dir" >"$WORK/httpd.log" 2>&1 &
httpd=$!
trap 'kill "$httpd" 2>/dev/null || true' EXIT
sleep 1

echo "==> [4/4] running $INSTANCE with $MODEL (index-first) and grading"
# Loopback host on purpose: the runner rewrites it to whatever address the
# container reaches the host on -- host.docker.internal by default, or the
# internal-network gateway IP under --isolate-network (the default). The HTTP
# server binds 0.0.0.0, so it is reachable either way.
PYTHONPATH=bench python3 bench/swebench_runner.py \
  --instance-ids "$INSTANCE" \
  --model "$MODEL" \
  --binary-url "http://localhost:$PORT/ogcode.tar.gz" \
  --work-dir "$WORK" \
  "$@"
