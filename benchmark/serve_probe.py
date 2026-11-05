#!/usr/bin/env python3
"""Measure what ``ogcode serve`` and ``opencode serve`` cost while at rest.

The task benchmark (``run_benchmark.py``) samples resources during a model run.
This probe answers the other question: how heavy is the server itself?  It
starts each binary on a free loopback port in a throwaway project, waits for the
port to accept connections, leaves it idle for a few seconds and records:

* cold start (wall time until the port is ready),
* peak resident memory of the whole process tree over startup and idle,
* steady-state resident memory (the median sample once startup is behind it),
* CPU time the process tree consumed from spawn until shutdown.

    python3 serve_probe.py
    python3 serve_probe.py --reps 3 --idle-s 5 --json results/serve-probe.json

Only the standard library and ``benchlib.resources`` are used; the sampler is
the same one the task benchmark uses, so the two reports describe memory the
same way.
"""

from __future__ import annotations

import argparse
import json
import os
import pathlib
import platform
import resource
import signal
import socket
import subprocess
import tempfile
import time
from typing import Any, Optional

from benchlib import resources, util

FALLBACKS = {
    "ogcode": ["~/.local/bin/ogcode"],
    "opencode": ["~/.opencode/bin/opencode"],
}

#: Neither binary spells the version flag the same way.
VERSION_ARGS = {"ogcode": ["version"], "opencode": ["--version"]}


def free_port() -> int:
    """A port nothing is listening on right now."""
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def wait_ready(port: int, process: subprocess.Popen, timeout_s: float) -> Optional[float]:
    """Seconds until the server accepts a TCP connection, or None on timeout.

    A listening socket is the one readiness signal both servers share, and it
    needs no knowledge of either HTTP API.
    """
    started = time.time()
    while time.time() - started < timeout_s:
        if process.poll() is not None:
            return None
        try:
            with socket.create_connection(("127.0.0.1", port), timeout=0.5):
                return time.time() - started
        except OSError:
            time.sleep(0.02)
    return None


def stop(process: subprocess.Popen, grace_s: float = 10.0) -> None:
    """SIGTERM the whole process group, then SIGKILL what is left."""
    if process.poll() is not None:
        return
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except ProcessLookupError:
        return
    try:
        process.wait(timeout=grace_s)
    except subprocess.TimeoutExpired:
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.wait()


def binary_version(binary: str, args: list[str]) -> str:
    try:
        proc = subprocess.run(
            [binary, *args], capture_output=True, text=True, timeout=30
        )
    except (OSError, subprocess.TimeoutExpired):
        return ""
    return (proc.stdout or proc.stderr or "").strip().splitlines()[0]


def child_env(agent: str, scratch: pathlib.Path) -> dict[str, str]:
    """An environment with each agent's own config isolated to the scratch dir."""
    env = dict(os.environ)
    env["PWD"] = str(scratch)
    if agent == "opencode":
        xdg = scratch / "xdg"
        (xdg / "opencode").mkdir(parents=True, exist_ok=True)
        (xdg / "opencode" / "opencode.json").write_text(
            '{\n  "$schema": "https://opencode.ai/config.json",\n  "mcp": {}\n}\n'
        )
        env["XDG_CONFIG_HOME"] = str(xdg)
    else:
        env["OGCODE_LOG_DIR"] = str(scratch / "logs")
    return env


def probe(
    agent: str,
    binary: str,
    rep: int,
    idle_s: float,
    settle_s: float,
    timeout_s: float,
) -> dict[str, Any]:
    port = free_port()
    scratch = pathlib.Path(tempfile.mkdtemp(prefix=f"ogcode-serve-{agent}-"))
    log_path = scratch / "serve.log"
    cmd = [binary, "serve", "--port", str(port)]
    before = resource.getrusage(resource.RUSAGE_CHILDREN)
    started = time.time()
    with log_path.open("w") as log:
        process = subprocess.Popen(
            cmd,
            cwd=str(scratch),
            env=child_env(agent, scratch),
            stdout=log,
            stderr=subprocess.STDOUT,
            text=True,
            start_new_session=True,
        )
        # Start sampling at spawn, so the peak covers startup as well as the
        # idle window that follows it.
        sampler = resources.Sampler(process.pid)
        sampler.start()
        ready_s = wait_ready(port, process, timeout_s)
        if ready_s is not None:
            time.sleep(settle_s + idle_s)
        sampled = sampler.stop()
        stop(process)
    after = resource.getrusage(resource.RUSAGE_CHILDREN)
    wall_s = time.time() - started

    row: dict[str, Any] = {
        "agent": agent,
        "rep": rep,
        "command": " ".join(cmd),
        "port": port,
        "ready_s": ready_s,
        "wall_s": wall_s,
        "cpu_user_s": after.ru_utime - before.ru_utime,
        "cpu_sys_s": after.ru_stime - before.ru_stime,
        "resources": sampled,
    }
    row["cpu_total_s"] = row["cpu_user_s"] + row["cpu_sys_s"]
    row["cpu_per_wall"] = row["cpu_total_s"] / wall_s if wall_s else 0.0
    if ready_s is None:
        tail = ""
        try:
            tail = "\n".join(log_path.read_text(errors="replace").splitlines()[-5:])
        except OSError:
            pass
        row["error"] = f"server not ready within {timeout_s:.0f}s"
        if tail:
            row["log_tail"] = tail
    return row


def _median(values: list[float]) -> float:
    return resources._median(values)


def summarize(rows: list[dict[str, Any]]) -> dict[str, Any]:
    ready = [row["ready_s"] for row in rows if row.get("ready_s") is not None]
    peak = [
        row["resources"]["peak_tree_rss_bytes"]
        for row in rows
        if row.get("resources") and row["resources"].get("peak_tree_rss_bytes")
    ]
    steady = [
        row["resources"]["median_tree_rss_bytes"]
        for row in rows
        if row.get("resources") and row["resources"].get("median_tree_rss_bytes")
    ]
    cpu = [row["cpu_total_s"] for row in rows]
    return {
        "runs": len(rows),
        "ready_median_s": _median(ready) if ready else None,
        "ready_max_s": max(ready) if ready else None,
        "peak_rss_median_bytes": _median([float(value) for value in peak]) if peak else None,
        "peak_rss_max_bytes": max(peak) if peak else None,
        "steady_rss_median_bytes": _median([float(value) for value in steady]) if steady else None,
        "cpu_median_s": _median(cpu) if cpu else None,
        "max_processes": max(
            (row["resources"]["max_processes"] for row in rows if row.get("resources")),
            default=None,
        ),
    }


def mib(value: Optional[float]) -> str:
    return f"{value / (1024 * 1024):.1f} MiB" if value is not None else "n/a"


def markdown(result: dict[str, Any]) -> str:
    agents = list(result["agents"])
    lines = [
        "# Server resources: " + " vs ".join(agents),
        "",
        f"Platform: `{result['platform']}` · {result['reps']} reps · "
        f"{result['idle_s']:g}s idle per rep · "
        f"sampled every {result['interval_s']:g}s (`{result['backend']}`)",
        "",
        "| Agent | Version | Binary | Ready (median) | Peak RSS | Steady RSS "
        "| CPU to serve | Processes |",
        "|---|---|---|---|---|---|---|---|",
    ]
    for agent in agents:
        stats = result["agents"][agent]
        lines.append(
            "| {agent} | {version} | {size} | {ready} | {peak} | {steady} "
            "| {cpu} | {procs} |".format(
                agent=agent,
                version=stats["version"] or "n/a",
                size=mib(stats["binary_bytes"]),
                ready=f"{stats['summary']['ready_median_s']:.2f}s"
                if stats["summary"]["ready_median_s"] is not None
                else "n/a",
                peak=mib(stats["summary"]["peak_rss_median_bytes"]),
                steady=mib(stats["summary"]["steady_rss_median_bytes"]),
                cpu=f"{stats['summary']['cpu_median_s']:.2f}s",
                procs=stats["summary"]["max_processes"],
            )
        )
    lines += [
        "",
        "- Peak and steady RSS cover the server's whole process tree; steady RSS "
        "is the median sample over the idle window, peak also includes startup.",
        "- CPU is the tree's user plus system time from spawn until shutdown, so "
        "it covers startup and the idle window together.",
    ]
    if "ogcode" in result["agents"] and "opencode" in result["agents"]:
        us = result["agents"]["ogcode"]["summary"]
        them = result["agents"]["opencode"]["summary"]
        if us["ready_median_s"] and them["ready_median_s"]:
            lines.append(
                f"- Ratios (ogcode / opencode), medians: ready "
                f"{us['ready_median_s'] / them['ready_median_s']:.2f}×, "
                f"peak RSS {_ratio(us['peak_rss_median_bytes'], them['peak_rss_median_bytes']):.2f}×, "
                f"steady RSS {_ratio(us['steady_rss_median_bytes'], them['steady_rss_median_bytes']):.2f}×, "
                f"CPU {_ratio(us['cpu_median_s'], them['cpu_median_s']):.2f}×."
            )
    failed = [
        f"{row['agent']} rep {row['rep']}: {row['error']}"
        for agent in agents
        for row in result["agents"][agent]["runs"]
        if row.get("error")
    ]
    if failed:
        lines += ["", "## Failures", ""] + [f"- {item}" for item in failed]
    return "\n".join(lines) + "\n"


def _ratio(us: Optional[float], them: Optional[float]) -> float:
    return float(us) / float(them) if us and them else 0.0


def main(argv: Optional[list[str]] = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--reps", type=int, default=3, help="repetitions per agent")
    parser.add_argument("--idle-s", type=float, default=5.0, help="idle seconds sampled after settling")
    parser.add_argument("--settle-s", type=float, default=2.0, help="seconds to let the server settle before the idle window")
    parser.add_argument("--timeout-s", type=float, default=120.0, help="cold-start timeout")
    parser.add_argument("--agents", default="ogcode,opencode", help="comma-separated agents")
    parser.add_argument("--ogcode", default="ogcode", help="ogcode binary (name or path)")
    parser.add_argument("--opencode", default="opencode", help="opencode binary (name or path)")
    parser.add_argument("--json", type=pathlib.Path, help="write the full result as JSON")
    parser.add_argument("--markdown", type=pathlib.Path, help="write the report as markdown")
    args = parser.parse_args(argv)

    wanted = [agent.strip() for agent in args.agents.split(",") if agent.strip()]
    binaries = {}
    for agent in wanted:
        if agent not in FALLBACKS:
            raise SystemExit(f"unknown agent {agent!r}")
        binaries[agent] = util.find_binary(
            getattr(args, agent), FALLBACKS[agent]
        )

    results: dict[str, Any] = {}
    for agent in wanted:
        binary = binaries[agent]
        version = binary_version(binary, VERSION_ARGS[agent])
        rows = []
        for rep in range(1, args.reps + 1):
            row = probe(
                agent, binary, rep, args.idle_s, args.settle_s, args.timeout_s
            )
            if row["ready_s"] is not None:
                print(f"{agent} rep {rep}: ready {row['ready_s']:.2f}s", flush=True)
            else:
                print(f"{agent} rep {rep}: {row['error']}", flush=True)
            rows.append(row)
        results[agent] = {
            "binary": binary,
            "binary_bytes": os.path.getsize(os.path.realpath(binary)),
            "version": version,
            "runs": rows,
            "summary": summarize(rows),
        }

    backend = next(
        (
            row["resources"]["backend"]
            for agent in results.values()
            for row in agent["runs"]
            if row.get("resources")
        ),
        "unknown",
    )
    result = {
        "schema": 1,
        "generated_at": util.iso_now(),
        "platform": f"{platform.system().lower()}/{platform.machine()}",
        "reps": args.reps,
        "idle_s": args.idle_s,
        "settle_s": args.settle_s,
        "interval_s": resources.DEFAULT_INTERVAL_S,
        "backend": backend,
        "agents": results,
    }

    report = markdown(result)
    print()
    print(report, end="")
    if args.json:
        args.json.parent.mkdir(parents=True, exist_ok=True)
        args.json.write_text(json.dumps(result, indent=2) + "\n")
    if args.markdown:
        args.markdown.parent.mkdir(parents=True, exist_ok=True)
        args.markdown.write_text(report)
    return 0 if all(not row.get("error") for agent in results.values() for row in agent["runs"]) else 1


if __name__ == "__main__":
    raise SystemExit(main())
