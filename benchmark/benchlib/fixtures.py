"""Fixture preparation, verification, diffing and drift repair.

A task is a directory containing:

``repo/``
    The starting source tree.  It must fail ``verify.sh`` before any agent
    edits it.
``task.md``
    The exact prompt handed to both agents.
``verify.sh <workdir> <base-rev>``
    The grader.  It restores its own test files from the fixture commit, then
    runs the suite.  Exit zero means solved.
``reference/``
    The known-good patch used by ``check_fixtures.sh`` to prove the task is
    solvable.

A measured run copies ``repo/`` into a throwaway directory, commits it, and
keeps that commit id so the verifier can restore tests even if the agent changed
them.  The fixture source itself is never edited.
"""

from __future__ import annotations

import hashlib
import json
import pathlib
import shutil
import subprocess
import tempfile
from typing import Any

from . import util
from .config import TASKS_DIR

# Generated state that is not part of an agent's solution.  Kept out of the
# fixture commit, the measured diff and the pristine comparison.  ``ogcode.json``
# is written after the commit so the isolation config never counts as a change.
JUNK = [
    ".ogcode/",
    "ogcode.json",
    "__pycache__/",
    "node_modules/",
    ".pytest_cache/",
    ".opencode/",
    "*.pyc",
]

JUNK_PARTS = {".ogcode", "__pycache__", "node_modules", ".pytest_cache", ".opencode"}

# Never copy generated state into a measured worktree.  The fixture commit
# excludes it either way, but copying stale bytecode or tool state would still
# give the agent a dirty starting directory.
COPY_IGNORE = shutil.ignore_patterns(
    "__pycache__", "*.pyc", ".pytest_cache", ".DS_Store", ".ogcode", ".opencode"
)


def discover_tasks() -> list[str]:
    """Every task directory that has a prompt file, sorted for stable output."""
    return sorted(
        path.name
        for path in TASKS_DIR.iterdir()
        if path.is_dir() and (path / "task.md").is_file()
    )


def task_prompt(task: str) -> str:
    """The task prompt, passed verbatim as one argument to each agent."""
    prompt = (TASKS_DIR / task / "task.md").read_text().strip()
    if not prompt:
        raise SystemExit(f"task {task} has an empty task.md")
    return prompt


def prepare(task: str, dest: pathlib.Path) -> str:
    """Copy a fixture into ``dest``, commit it, and return the fixture revision."""
    shutil.copytree(
        TASKS_DIR / task / "repo", dest, dirs_exist_ok=True, ignore=COPY_IGNORE
    )
    util.git(dest, "init", "-q")
    exclude = dest / ".git" / "info" / "exclude"
    exclude.parent.mkdir(parents=True, exist_ok=True)
    exclude.write_text("\n".join(JUNK) + "\n")
    util.git(dest, "add", "-A")
    util.git(
        dest,
        "-c",
        "user.email=bench@ogcode.in",
        "-c",
        "user.name=bench",
        "-c",
        "commit.gpgsign=false",
        "commit",
        "-qm",
        "fixture",
    )
    # Written after the commit and kept out of the tree by .git/info/exclude, so
    # the isolation config never counts as something the agent changed.
    from .agents import ogcode_isolation

    (dest / "ogcode.json").write_text(
        json_dump(ogcode_isolation()) + "\n"
    )
    return util.git(dest, "rev-parse", "HEAD").strip()


def json_dump(value: Any) -> str:
    """Stable JSON for a file written into a fixture."""
    return json.dumps(value, indent=2)


def verify(
    task: str,
    dest: pathlib.Path,
    rev: str,
    timeout_s: int,
    env: dict[str, str],
) -> tuple[bool, str]:
    """Run a task's verifier and return ``(passed, tail_of_output)``."""
    proc = util.run_cmd(
        ["sh", str(TASKS_DIR / task / "verify.sh"), str(dest), rev],
        dest,
        timeout_s,
        env,
    )
    output = (proc["stdout"] + proc["stderr"]).strip()
    return proc["code"] == 0, output[-2000:]


def diff_stats(dest: pathlib.Path) -> dict[str, Any]:
    """What the agent changed, relative to the fixture commit."""
    try:
        util.git(dest, "add", "-A")
        numstat = util.git(dest, "diff", "--cached", "--numstat")
    except subprocess.CalledProcessError:
        return {"files": 0, "added": 0, "removed": 0, "paths": []}
    files = added = removed = 0
    paths: list[str] = []
    for line in numstat.splitlines():
        parts = line.split("\t")
        if len(parts) < 3:
            continue
        files += 1
        added += int(parts[0]) if parts[0].isdigit() else 0
        removed += int(parts[1]) if parts[1].isdigit() else 0
        paths.append(parts[2])
    return {"files": files, "added": added, "removed": removed, "paths": paths}


def tree_bytes(root: pathlib.Path) -> dict[str, bytes]:
    """Every non-junk file under ``root``, keyed by relative path."""
    out: dict[str, bytes] = {}
    for path in sorted(root.rglob("*")):
        if not path.is_file():
            continue
        rel = path.relative_to(root)
        if any(part in JUNK_PARTS for part in rel.parts):
            continue
        out[str(rel)] = path.read_bytes()
    return out


def fixture_digest(task: str) -> str:
    """Content hash of a task's starting repo, recorded in run metadata."""
    digest = hashlib.sha256()
    root = TASKS_DIR / task / "repo"
    for rel, content in sorted(tree_bytes(root).items()):
        digest.update(rel.encode())
        digest.update(b"\0")
        digest.update(content)
    return digest.hexdigest()


def snapshot_fixtures(tasks: list[str], pristine_root: pathlib.Path) -> None:
    """Keep a known-good copy of every fixture source for this run.

    The snapshot is refreshed at the start of every benchmark rather than kept
    forever: a checkout that was accidentally modified after a previous run
    must not become the pristine baseline for the next one.  Restoring drift is
    still a safety net, not a substitute for starting from the fixture on disk.
    """
    for task in tasks:
        dest = pristine_root / task
        if dest.exists():
            shutil.rmtree(dest)
        dest.parent.mkdir(parents=True, exist_ok=True)
        shutil.copytree(TASKS_DIR / task / "repo", dest, ignore=COPY_IGNORE)


def drift_paths(pristine: pathlib.Path, live: pathlib.Path) -> list[str]:
    """Which files differ between the snapshot and the live fixture."""
    before, after = tree_bytes(pristine), tree_bytes(live)
    changed = []
    for name in sorted(set(before) | set(after)):
        if name not in before:
            changed.append(f"+{name}")
        elif name not in after:
            changed.append(f"-{name}")
        elif before[name] != after[name]:
            changed.append(f"~{name}")
    return changed


def restore_fixtures(tasks: list[str], pristine_root: pathlib.Path) -> list[str]:
    """Put fixture sources back after a run, returning tasks that had drifted.

    A run should never touch its fixture, but an agent that resolves a path
    outside its worktree can, and one corrupted fixture silently ruins every
    later run that copies it.  Comparing against the snapshot and repairing
    makes that loud instead of invisible.
    """
    repaired = []
    for task in tasks:
        live = TASKS_DIR / task / "repo"
        pristine = pristine_root / task
        if not pristine.exists():
            continue
        drift = drift_paths(pristine, live)
        if drift:
            shutil.rmtree(live, ignore_errors=True)
            shutil.copytree(pristine, live)
            repaired.append(f"{task}: {', '.join(drift)}")
    return repaired


def check_task(task: str, timeout_s: int, env: dict[str, str]) -> dict[str, Any]:
    """Red/green self-check used by the test suite.

    Returns a small dictionary instead of printing, so one task's failure does
    not hide the result of the others.  ``check_fixtures.sh`` is the
    human-facing wrapper around the same procedure.
    """
    work = pathlib.Path(tempfile.mkdtemp(prefix=f"check-{task}-"))
    try:
        shutil.copytree(TASKS_DIR / task / "repo", work, dirs_exist_ok=True)
        util.git(work, "init", "-q")
        exclude = work / ".git" / "info" / "exclude"
        exclude.parent.mkdir(parents=True, exist_ok=True)
        exclude.write_text("\n".join(JUNK) + "\n")
        util.git(work, "add", "-A")
        util.git(
            work,
            "-c",
            "user.email=bench@ogcode.in",
            "-c",
            "user.name=bench",
            "-c",
            "commit.gpgsign=false",
            "commit",
            "-qm",
            "fixture",
        )
        rev = util.git(work, "rev-parse", "HEAD").strip()
        red, red_output = verify(task, work, rev, timeout_s, env)
        shutil.copytree(TASKS_DIR / task / "reference", work, dirs_exist_ok=True)
        green, green_output = verify(task, work, rev, timeout_s, env)
        return {
            "task": task,
            "red_as_expected": not red,
            "red_output": red_output,
            "reference_green": green,
            "green_output": green_output,
        }
    finally:
        shutil.rmtree(work, ignore_errors=True)
