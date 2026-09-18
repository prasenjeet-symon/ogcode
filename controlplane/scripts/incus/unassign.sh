#!/bin/sh
# unassign.sh — tear down one assignment container (Phase A, operator-only).
#
# Best-effort in-guest `git push origin user/<name>` for every user branch
# found in the container's clones (scripts only — the plan deliberately keeps
# the push OUT of Go code in Phase A), then `incus delete --force`.
#
# THIS DESTRUCTIVE STEP IS THE POINT: the plan's model is that a container is
# one assignment. Unassigning is a plain delete — task worktrees and their
# unmerged branches die with it, which is why the push runs first and why a
# failed push only warns (the operator decides, per the plan's "best-effort"
# stance; the panel also warns on unpushed work).
#
# Usage:
#   ./unassign.sh CONTAINER_NAME [--yes] [--no-push] [--timeout SECS]
#
#   CONTAINER_NAME   the og-* container (assign.sh prints it at creation).
#   --yes            skip the confirmation prompt (for automation).
#   --no-push        skip the best-effort branch push (e.g. the repo is read-
#                    only for this worker, or the operator is discarding work
#                    deliberately).
#   --timeout SECS   per-step guest command budget, default 120.
#
# Prerequisites: incus; the container exists.
#
# Manual verification checklist (plan §Phase A):
#   [ ] dry run: run without --yes and confirm the prompt lists the right
#       container and the branches it will push
#   [ ] pushed branches visible on the origin (git ls-remote --heads origin)
#   [ ] `incus list og-` no longer shows the container
#   [ ] master console shows the worker offline (it stopped registering; the
#       in-memory repoStore entry is lost — that is expected in Phase A)
set -eu

PROGNAME=$(basename "$0")
TIMEOUT="${OGCODE_EXEC_TIMEOUT:-120}"
ASSUME_YES=0
DO_PUSH=1
TARGET=""

usage() {
  sed -n '2,32p' "$0" | sed 's/^# \{0,1\}//'
  exit "${1:-0}"
}

die() { echo "$PROGNAME: $*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --help|-h) usage 0 ;;
    --yes|-y) ASSUME_YES=1; shift ;;
    --no-push) DO_PUSH=0; shift ;;
    --timeout) TIMEOUT=$2; shift 2 ;;
    *) if [ -z "$TARGET" ]; then TARGET=$1; shift; else die "unknown argument: $1"; fi ;;
  esac
done
[ -n "$TARGET" ] || usage 2
case "$TARGET" in
  og-*) : ;;
  *) die "'$TARGET' does not look like an ogcode worker container (og- prefix)" ;;
esac

command -v incus >/dev/null 2>&1 || die "incus not found on this host"
incus list --format csv | cut -d, -f1 | grep -Fxq "$TARGET" ||
  die "container '$TARGET' not found (already unassigned?)"

STATE=$(incus list --format csv "name=$TARGET" | cut -d, -f2)
echo "==> container $TARGET state: $STATE"

# --------------------------------------------------- best-effort push -------
# Push every user/<name> branch that exists in the container's clones. The
# worktree branch name is user/<slug> (safeUserName at work inside the guest's
# worker); clones live at /root/.ogcode/repos/<repo-slug>.
if [ "$DO_PUSH" = 1 ] && [ "$STATE" = "RUNNING" ]; then
  echo "==> pushing user/* branches (best effort)"
  # incus exec has no --timeout flag on Incus 6 (LXD naming); the budget is
  # enforced by coreutils timeout wrapping the in-guest shell instead, so
  # TIMEOUT is interpolated into the payload below.
  incus exec "$TARGET" -- sh -ec "
    timeout $TIMEOUT sh -ec '
    cd /root/.ogcode/repos 2>/dev/null || exit 0
    for d in */; do
      d=\"\${d%/}\"
      [ -d \"\$d/.git\" ] || continue
      branches=\$(git -C \"\$d\" for-each-ref --format=\"%(refname:short)\" \"refs/heads/user/\" 2>/dev/null)
      [ -n \"\$branches\" ] || continue
      for b in \$branches; do
        if git -C \"\$d\" push origin \"\$b\" 2>&1; then
          echo \"pushed \$d \$b\"
        else
          echo \"WARN: push failed for \$d \$b (unpushed work may be lost on delete)\" >&2
        fi
      done
    done
    '" || echo "WARN: in-guest push step failed (continuing to delete)" >&2
elif [ "$DO_PUSH" = 1 ]; then
  echo "==> container not RUNNING; skipping push (cannot exec a stopped container)."
  echo "    If it holds unpushed work, start it first or push from the clone manually."
fi

# ------------------------------------------------------- confirm & delete ---
if [ "$ASSUME_YES" != 1 ]; then
  printf 'Delete container %s and all its in-guest state? [y/N] ' "$TARGET"
  read -r ans
  case "$ans" in
    y|Y|yes|YES) : ;;
    *) echo "aborted (container left intact)"; exit 0 ;;
  esac
fi

echo "==> deleting $TARGET"
incus delete "$TARGET" --force
echo "==> done. Note: the master keeps the assignment's in-memory placement"
echo "    until restart; the worker simply stops being online."