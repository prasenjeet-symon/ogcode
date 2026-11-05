#!/bin/sh
# Red/green gate for the benchmark fixtures.
#
# For every task, prepare a throwaway worktree exactly like a measured run:
#
#   1. the shipped repository must FAIL its verifier (the bug is really there);
#   2. after copying reference/, the same verifier must PASS (the task is
#      solvable, and the grader is not broken).
#
# Usage:
#   sh check_fixtures.sh                 # every task
#   sh check_fixtures.sh py-bugfix ...   # selected tasks
#
# Set BENCHMARK_VERIFY_TIMEOUT to change the per-check timeout in seconds.
set -eu

here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$here"

exec python3 - "$@" <<'PY'
"""Human-facing wrapper around benchlib.fixtures.check_task."""

from __future__ import annotations

import os
import sys

from benchlib import fixtures

names = sys.argv[1:] or fixtures.discover_tasks()
timeout_s = int(os.environ.get("BENCHMARK_VERIFY_TIMEOUT", "300"))
env = dict(os.environ)

known = set(fixtures.discover_tasks())
failed: list[str] = []

for name in names:
    if name not in known:
        print(f"FAIL {name}: no such task (known: {', '.join(sorted(known))})")
        failed.append(name)
        continue

    result = fixtures.check_task(name, timeout_s, env)
    if result["red_as_expected"]:
        print(f"ok   {name}: red as shipped")
    else:
        print(f"FAIL {name}: fixture is GREEN before the fix (not a task)")
        failed.append(name)

    if result["reference_green"]:
        print(f"ok   {name}: reference fix is green")
    else:
        print(f"FAIL {name}: reference fix does NOT pass")
        tail = result["green_output"].strip().splitlines()[-20:]
        if tail:
            print("     " + "\n     ".join(tail))
        failed.append(name)

print("ALL FIXTURES OK" if not failed else "FIXTURE PROBLEMS ABOVE")
raise SystemExit(1 if failed else 0)
PY
