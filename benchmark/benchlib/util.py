"""Small, dependency-free helpers shared by the benchmark harness.

The benchmark deliberately uses only the Python standard library.  That keeps
the package runnable with the same ``python3`` a task's test suite already needs,
and means a reviewer can read every line that touches a model.

Nothing in this module knows about providers or tasks; it is the boring layer
underneath those modules.
"""

from __future__ import annotations

import hashlib
import json
import os
import pathlib
import resource
import shutil
import signal
import subprocess
import time
from typing import Any

from . import resources


#: Token counters every agent row has, even when a provider omits a field.
#: ``input`` is uncached input; cache reads and writes are separate counters.
ZERO_TOKENS = {
    "input": 0,
    "output": 0,
    "reasoning": 0,
    "cache_read": 0,
    "cache_write": 0,
    "total": 0,
}


def iso_now() -> str:
    """Return the current UTC time in the format used in result rows."""
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


def utc_stamp() -> str:
    """A compact UTC stamp suitable for a filename or a run directory."""
    return time.strftime("%Y%m%dT%H%M%SZ", time.gmtime())


def find_binary(name: str, fallbacks: list[str] | None = None) -> str:
    """Locate an agent binary on ``PATH``, then at a few conventional paths.

    A benchmark should fail before spending money if an agent is not installed.
    ``shutil.which`` handles the normal case; the fallbacks cover installers
    that put a binary in a user-local directory not present on a non-login
    shell's ``PATH``.
    """
    found = shutil.which(name)
    if found:
        return found
    for candidate in fallbacks or []:
        path = os.path.expanduser(candidate)
        if os.path.isfile(path) and os.access(path, os.X_OK):
            return path
    tried = ", ".join(fallbacks or []) or "none"
    raise SystemExit(f"{name} not found on PATH (also tried: {tried})")


def run_cmd(
    cmd: list[str],
    cwd: pathlib.Path,
    timeout_s: int,
    env: dict[str, str],
    sample_resources: bool = False,
) -> dict[str, Any]:
    """Run one child process and always return a result dictionary.

    The benchmark needs the same timeout semantics for both agents.  A child is
    started in its own process group and killed as a group on timeout, so a
    shell or tool subprocess cannot survive the agent and keep editing files
    after the row has been recorded.

    ``PWD`` is set explicitly because a child that trusts ``$PWD`` over
    ``getcwd()`` would otherwise work outside the throwaway worktree.

    ``sample_resources`` adds a ``resources`` entry: peak resident memory of the
    child's whole process tree, sampled while it runs, and the exact CPU time
    the tree consumed.  CPU comes from ``RUSAGE_CHILDREN`` -- reaping is the one
    moment the kernel can account for a process that already exited -- while
    peak RSS has to be polled, since no finished process reports it.  Both are
    read around the same window so they describe the same run.
    """
    started = time.time()
    child_env = dict(env)
    child_env["PWD"] = str(cwd)
    before = (
        resource.getrusage(resource.RUSAGE_CHILDREN) if sample_resources else None
    )
    proc = subprocess.Popen(
        cmd,
        cwd=str(cwd),
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        env=child_env,
        start_new_session=True,
    )
    sampler = None
    if sample_resources:
        sampler = resources.Sampler(proc.pid)
        sampler.start()
    timed_out = False
    try:
        out, err = proc.communicate(timeout=timeout_s)
    except subprocess.TimeoutExpired:
        timed_out = True
        try:
            os.killpg(os.getpgid(proc.pid), signal.SIGKILL)
        except ProcessLookupError:
            pass
        out, err = proc.communicate()
    duration_s = round(time.time() - started, 2)
    result = {
        "stdout": out or "",
        "stderr": err or "",
        "code": proc.returncode,
        "duration_s": duration_s,
        "timed_out": timed_out,
    }
    if sampler is not None and before is not None:
        after = resource.getrusage(resource.RUSAGE_CHILDREN)
        sampled = sampler.stop()
        user_s = max(after.ru_utime - before.ru_utime, 0.0)
        sys_s = max(after.ru_stime - before.ru_stime, 0.0)
        sampled.update(
            {
                "cpu_user_s": round(user_s, 3),
                "cpu_sys_s": round(sys_s, 3),
                "cpu_total_s": round(user_s + sys_s, 3),
                "wall_s": duration_s,
                "cpu_per_wall": round((user_s + sys_s) / duration_s, 3)
                if duration_s
                else None,
            }
        )
        result["resources"] = sampled
    return result


def git(dest: pathlib.Path, *args: str) -> str:
    """Run git in ``dest`` and return stdout, raising on a non-zero exit."""
    proc = subprocess.run(
        ["git", *args],
        cwd=str(dest),
        capture_output=True,
        text=True,
        check=True,
    )
    return proc.stdout


def read_json(path: pathlib.Path) -> Any:
    """Read JSON with a message that names the file, not just a line number."""
    try:
        return json.loads(path.read_text())
    except OSError as exc:
        raise SystemExit(f"cannot read {path}: {exc}") from exc
    except json.JSONDecodeError as exc:
        raise SystemExit(f"invalid JSON in {path}: {exc}") from exc


def write_json(path: pathlib.Path, value: Any) -> None:
    """Write stable, human-readable JSON with a trailing newline."""
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, indent=2, sort_keys=False) + "\n")


def sha256_file(path: pathlib.Path) -> str:
    """Content hash used to detect fixture drift and record package inputs."""
    digest = hashlib.sha256()
    with path.open("rb") as fh:
        for chunk in iter(lambda: fh.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def relative_path(path: pathlib.Path, root: pathlib.Path) -> str:
    """Return a POSIX-style path relative to ``root`` when possible."""
    try:
        return path.resolve().relative_to(root.resolve()).as_posix()
    except ValueError:
        return str(path.resolve())
