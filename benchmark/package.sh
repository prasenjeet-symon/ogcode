#!/bin/sh
#
# Build the distributable benchmark package archive.
#
# Usage: ./benchmark/package.sh
#
# Stages this directory into a temporary tree and writes
# public/ogcode-benchmark-<VERSION>.zip at the repository root. The
# caller's working directory does not matter.

set -eu

SCRIPT_DIR=$(CDPATH= cd "$(dirname "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd "$SCRIPT_DIR/.." && pwd)
PUBLIC_DIR=$REPO_ROOT/public
VERSION_FILE=$SCRIPT_DIR/VERSION

die() {
    printf 'package.sh: error: %s\n' "$*" >&2
    exit 1
}

info() {
    printf 'package.sh: %s\n' "$*"
}

[ -f "$VERSION_FILE" ] || die "missing VERSION file: $VERSION_FILE"
VERSION=$(tr -d '[:space:]' < "$VERSION_FILE")
[ -n "$VERSION" ] || die "VERSION file is empty: $VERSION_FILE"
case $VERSION in
    *[!A-Za-z0-9._-]*) die "VERSION contains unsafe characters: $VERSION" ;;
esac

command -v python3 >/dev/null 2>&1 || die "python3 is required to build the archive"

mkdir -p "$PUBLIC_DIR"
OUTPUT=$PUBLIC_DIR/ogcode-benchmark-$VERSION.zip

TMP_BASE=${TMPDIR:-/tmp}
TMP_BASE=${TMP_BASE%/}
STAGE_ROOT=$(mktemp -d "$TMP_BASE/ogcode-benchmark.XXXXXX") ||
    die "could not create a temporary staging directory"
STAGE_DIR=$STAGE_ROOT/ogcode-benchmark
STAGE_ZIP=$STAGE_ROOT/ogcode-benchmark-$VERSION.zip
cleanup() {
    rm -rf "$STAGE_ROOT"
}
trap cleanup EXIT HUP INT TERM

info "version: $VERSION"
info "source: $SCRIPT_DIR"
info "staging: $STAGE_DIR"

# Copy the package tree into staging, dropping caches, results and secrets.
python3 - "$SCRIPT_DIR" "$STAGE_DIR" <<'PY'
import os
import shutil
import sys

source, destination = sys.argv[1], sys.argv[2]

EXCLUDED_DIRS = {
    "results",
    ".cache",
    "__pycache__",
    "logs",
    "toolcalls",
    ".pristine",
    ".git",
}
EXCLUDED_FILES = {".DS_Store", ".env"}
EXCLUDED_SUFFIXES = (".pyc", ".zip")

files = 0
size = 0
os.makedirs(destination, exist_ok=True)

for root, dirnames, filenames in os.walk(source):
    dirnames[:] = sorted(name for name in dirnames if name not in EXCLUDED_DIRS)
    rel_root = os.path.relpath(root, source)
    target_root = destination if rel_root == "." else os.path.join(destination, rel_root)
    os.makedirs(target_root, exist_ok=True)

    for name in sorted(filenames):
        path = os.path.join(root, name)
        rel = os.path.relpath(path, source)
        if name in EXCLUDED_FILES or name.endswith(EXCLUDED_SUFFIXES):
            continue
        if os.path.islink(path):
            print(f"package.sh: warning: skipping symlink: {rel}", file=sys.stderr)
            continue
        if not os.path.isfile(path):
            print(f"package.sh: warning: skipping non-regular file: {rel}", file=sys.stderr)
            continue
        shutil.copy2(path, os.path.join(destination, rel))
        files += 1
        size += os.path.getsize(path)

print(f"package.sh: staged {files} files ({size} bytes)")
if files == 0:
    sys.exit("package.sh: error: no files were staged")
PY

for entry in README.md VERSION run_benchmark.py report.py benchlib configs tasks \
    examples tools pricing.json config.schema.json; do
    [ -e "$STAGE_DIR/$entry" ] || die "required entry missing from staging: $entry"
done

for entry in docs tests; do
    if [ ! -e "$STAGE_DIR/$entry" ]; then
        printf 'package.sh: warning: optional directory missing: %s\n' "$entry" >&2
    elif [ -z "$(ls -A "$STAGE_DIR/$entry")" ]; then
        printf 'package.sh: warning: optional directory is empty: %s\n' "$entry" >&2
    fi
done

# Build a deterministic zip: sorted entries, fixed timestamps, preserved modes.
python3 - "$STAGE_DIR" "$STAGE_ZIP" <<'PY'
import hashlib
import os
import shutil
import stat
import sys
import zipfile

stage, archive = sys.argv[1], sys.argv[2]
ROOT = "ogcode-benchmark"
FIXED_TIME = (1980, 1, 1, 0, 0, 0)
DIR_MODE = (stat.S_IFDIR | 0o755) << 16 | 0x10

with zipfile.ZipFile(archive, "w", zipfile.ZIP_DEFLATED, compresslevel=9) as zf:
    for root, dirnames, filenames in os.walk(stage):
        dirnames.sort()
        rel_root = os.path.relpath(root, stage)
        arc_dir = ROOT if rel_root == "." else ROOT + "/" + rel_root.replace(os.sep, "/")

        info = zipfile.ZipInfo(arc_dir + "/", FIXED_TIME)
        info.compress_type = zipfile.ZIP_STORED
        info.external_attr = DIR_MODE
        zf.writestr(info, b"")

        for name in sorted(filenames):
            path = os.path.join(root, name)
            rel = os.path.relpath(path, stage).replace(os.sep, "/")
            info = zipfile.ZipInfo(ROOT + "/" + rel, FIXED_TIME)
            info.compress_type = zipfile.ZIP_DEFLATED
            info.external_attr = (os.stat(path).st_mode & 0xFFFF) << 16
            with open(path, "rb") as src, zf.open(info, "w") as dst:
                shutil.copyfileobj(src, dst)

with zipfile.ZipFile(archive) as zf:
    bad = zf.testzip()
    if bad is not None:
        sys.exit(f"package.sh: error: corrupt archive entry: {bad}")

    totals = {}
    count = 0
    for info in zf.infolist():
        if info.is_dir():
            continue
        parts = info.filename.split("/")
        top = parts[1] if len(parts) > 1 else info.filename
        files, size = totals.get(top, (0, 0))
        totals[top] = (files + 1, size + info.file_size)
        count += 1

    print(f"package.sh: archived {count} files")
    for top in sorted(totals):
        files, size = totals[top]
        print(f"  {top:<24} {files:>5} files {size:>10} bytes")

with open(archive, "rb") as handle:
    digest = hashlib.sha256(handle.read()).hexdigest()
print(f"package.sh: sha256 {digest}")
PY

[ -s "$STAGE_ZIP" ] || die "archive was not created: $STAGE_ZIP"
mv -f "$STAGE_ZIP" "$OUTPUT"

SIZE_BYTES=$(wc -c < "$OUTPUT" | tr -d '[:space:]')
SIZE_HUMAN=$(du -h "$OUTPUT" | awk '{print $1}')
info "archive: $OUTPUT"
info "size: $SIZE_BYTES bytes ($SIZE_HUMAN)"
