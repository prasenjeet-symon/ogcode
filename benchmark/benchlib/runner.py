"""The measured run loop.

One row is one agent attempt at one task.  The sequence is deliberately rigid:

1. copy the fixture and commit it,
2. prove it fails its verifier,
3. for ogcode, restore the project index built once for this fixture,
4. run the agent under a wall-clock timeout,
5. restore the tests and run the verifier,
6. record tokens, dollars, diff, tool audit and provider route.

Steps 1-3 are setup and are excluded from the duration figure.  Step 4 is the
only thing the benchmark compares.  Steps 5-6 decide whether the answer was
right and leave evidence for a reviewer.
"""

from __future__ import annotations

import concurrent.futures
import json
import os
import pathlib
import platform
import shutil
import subprocess
import sys
import tempfile
import threading
from dataclasses import dataclass
from typing import Any, Optional

from . import agents, config, fixtures, indexcache, pricing, providers, util

BENCHMARK_VERSION = (config.PACKAGE_ROOT / "VERSION").read_text().strip()


@dataclass(frozen=True)
class RunContext:
    """Everything a single run needs, resolved once before the loop."""

    config: config.BenchmarkConfig
    provider: providers.ResolvedProvider
    price: dict[str, float]
    price_source: str
    binaries: dict[str, str]
    out_dir: pathlib.Path
    provider_env: dict[str, str]
    verify_env: dict[str, str]
    keep: bool
    pristine_root: pathlib.Path
    index_cache: pathlib.Path
    index_rebuild: bool
    ogcode_version: str


def binary_version(binary: str, agent: str) -> str:
    """Best-effort version string for metadata; never fatal."""
    cmd = [binary, "version"] if agent == "ogcode" else [binary, "--version"]
    try:
        proc = subprocess.run(
            cmd,
            capture_output=True,
            text=True,
            timeout=20,
            env=providers.scrub_provider_env(),
        )
    except (OSError, subprocess.TimeoutExpired):
        return "unknown"
    text = (proc.stdout or proc.stderr).strip()
    return text.splitlines()[0] if text else "unknown"


def _index_specs(context: RunContext, tasks: list[str]) -> list[indexcache.Spec]:
    return [
        indexcache.Spec.of(
            task,
            config=context.config,
            provider=context.provider,
            ogcode_version=context.ogcode_version,
            cache_dir=context.index_cache,
        )
        for task in tasks
    ]


def _prepare_indexes(context: RunContext, tasks: list[str]) -> list[dict[str, Any]]:
    """Build every missing index once, before any measured attempt."""
    results = indexcache.prebuild(
        _index_specs(context, tasks), context, force=context.index_rebuild
    )
    print(f"index cache={context.index_cache}", flush=True)
    for result in results:
        print(
            f"index {result['task']:12s} {result['origin']:7s} "
            f"{result['pages']:6d} pages  {result['duration_s']:6.1f}s",
            flush=True,
        )
        if not result["ok"]:
            raise SystemExit(
                f"could not index {result['task']} ({result['pages']} pages): "
                f"{result['error']}\n"
                "an unindexed run is not the measurement"
            )
    return results


def build_indexes(context: RunContext) -> int:
    """Preprocess: fill the cache for every selected task and exit."""
    tasks = config.task_names(context.config)
    missing = sorted(set(tasks) - set(fixtures.discover_tasks()))
    if missing:
        raise SystemExit(f"unknown task(s): {', '.join(missing)}")
    _prepare_indexes(context, tasks)
    print(f"\n{len(tasks)} index(es) ready; run the benchmark without rebuilding.")
    return 0


def bench_one(
    task: str,
    agent: str,
    rep: int,
    context: RunContext,
    stamp: str,
) -> dict[str, Any]:
    """Run one agent once on one task and return its result row."""
    cfg = context.config
    prompt = fixtures.task_prompt(task)
    started_at = util.iso_now()
    log_dir: Optional[pathlib.Path] = None
    if agent == "ogcode":
        log_dir = agents.ogcode_log_dir(context.out_dir, task, agent, rep, stamp)

    work = pathlib.Path(tempfile.mkdtemp(prefix=f"ogcode-benchmark-{task}-{agent}-{rep}-"))
    row: dict[str, Any] = {
        "schema": 1,
        "started_at": started_at,
        "ts": None,
        "task": task,
        "agent": agent,
        "rep": rep,
        "config_name": cfg.name,
        "provider_kind": context.provider.kind,
        "model": cfg.model.id,
        "agent_model": cfg.agent_model(agent),
        "pricing_source": context.price_source,
        "passed": False,
        "duration_s": 0.0,
        "exit_code": None,
        "timed_out": False,
        "tokens": dict(util.ZERO_TOKENS),
        "turns": 0,
        "cost_shared_usd": None,
        "cost_reported_usd": None,
        "diff": {"files": 0, "added": 0, "removed": 0, "paths": []},
        "final_message": None,
        "tool_calls": 0,
        "verify_output": "",
        "error": None,
        "index": None,
        "resources": None,
        "ogcode_provider": None,
        "ogcode_url": None,
        "provider_route_ok": None,
    }
    isolation_dir: Optional[pathlib.Path] = None
    try:
        rev = fixtures.prepare(task, work)
        already_green, green_output = fixtures.verify(
            task, work, rev, cfg.run.verify_timeout_s, context.verify_env
        )
        if already_green:
            raise SystemExit(
                f"fixture {task} passes before the agent runs, so it cannot measure anything.\n"
                f"{green_output}"
            )

        if agent == "ogcode":
            assert log_dir is not None
            row["index"] = indexcache.acquire(task, work, context, log_dir)
            if not row["index"]["ok"]:
                raise SystemExit(
                    f"could not index {task} ({row['index']['pages']} pages): "
                    f"{row['index']['error']}\n"
                    "an unindexed run is not the measurement"
                )

        isolation: dict[str, str] = {}
        if agent == "opencode":
            isolation, isolation_dir = agents.opencode_isolation(context.provider, cfg.model.id)

        cmd = agents.agent_command(agent, cfg, context.provider, context.binaries, prompt)
        run_env = dict(context.provider_env)
        if agent == "ogcode":
            # Keep ogcode's run log beside this row, not in the developer's
            # global log directory.  The path must be absolute: relative paths
            # are resolved inside the throwaway worktree and deleted with it.
            assert log_dir is not None
            run_env["OGCODE_LOG_DIR"] = str(log_dir)
        run_env.update(isolation)
        proc = util.run_cmd(
            cmd, work, cfg.run.timeout_s, run_env, sample_resources=True
        )

        row["resources"] = proc.get("resources")

        row["duration_s"] = proc["duration_s"]
        row["exit_code"] = proc["code"]
        row["timed_out"] = proc["timed_out"]
        row["diff"] = fixtures.diff_stats(work)
        passed, verify_output = fixtures.verify(
            task, work, rev, cfg.run.verify_timeout_s, context.verify_env
        )
        row["passed"] = passed
        row["verify_output"] = verify_output

        if agent == "ogcode":
            assert log_dir is not None
            audit = agents.read_tool_audit(
                work, context.out_dir, task, agent, rep, stamp
            )
            row["tool_calls"] = audit["count"]
            row["tool_failures"] = audit["failures"]
            row["tool_sequence"] = audit["sequence"]
            row["tool_audit"] = audit["csv"]
            row["tool_log_dir"] = util.relative_path(log_dir, context.out_dir)

            parsed = agents.parse_ogcode(proc["stdout"])
            if parsed:
                row["tokens"] = agents.normalize_tokens(parsed.get("tokens") or {})
                row["turns"] = int(parsed.get("num_turns", 0) or 0)
                row["cost_reported_usd"] = parsed.get("cost_usd")
                row["finish"] = parsed.get("finish")
                row["final_message"] = parsed.get("result")
            else:
                row["error"] = "could not parse ogcode json output"
            row.update(agents.read_provider_route(log_dir, context.provider))
        else:
            parsed = agents.parse_opencode(proc["stdout"])
            row["tokens"] = agents.normalize_tokens(parsed["tokens"])
            row["turns"] = parsed["turns"]
            row["cost_reported_usd"] = round(parsed["cost_reported_usd"], 8)
            row["tool_calls"] = parsed["tool_calls"]
            row["final_message"] = parsed["final_message"]
            if parsed["errors"]:
                row["error"] = "; ".join(parsed["errors"])

        if proc["timed_out"]:
            row["error"] = f"timed out after {cfg.run.timeout_s}s"
        elif proc["code"] != 0 and not row["error"]:
            tail = (proc["stderr"] or proc["stdout"]).strip()[-500:]
            row["error"] = f"exit {proc['code']}: {tail}"

        row["cost_shared_usd"] = round(
            pricing.shared_cost(row["tokens"], context.price), 8
        )
        row["ts"] = util.iso_now()
        if context.keep:
            row["workdir"] = str(work)
        return row
    finally:
        if isolation_dir is not None:
            shutil.rmtree(isolation_dir, ignore_errors=True)
        if not context.keep:
            shutil.rmtree(work, ignore_errors=True)


def _metadata(
    context: RunContext,
    tasks: list[str],
    started_at: str,
    finished_at: Optional[str] = None,
    total_runs: Optional[int] = None,
    index_entries: Optional[list[dict[str, Any]]] = None,
) -> dict[str, Any]:
    """Build the auditable sidecar for a result directory (never a secret)."""
    cfg = context.config
    return {
        "schema": 1,
        "benchmark": {
            "name": "ogcode-benchmark",
            "version": BENCHMARK_VERSION,
            "config": cfg.name,
            "config_path": str(cfg.source),
        },
        "started_at": started_at,
        "finished_at": finished_at,
        "provider": {
            "kind": context.provider.kind,
            "display_name": context.provider.display_name,
            "base_url": context.provider.base_url,
            "api_key_env": context.provider.api_key_env,
            "ogcode_provider": context.provider.ogcode_provider,
            "opencode_provider": context.provider.opencode_provider,
            "opencode_custom_config": context.provider.uses_custom_opencode,
        },
        "pricing": {"source": context.price_source, "usd_per_million_tokens": context.price},
        "agents": {
            name: {
                "binary": context.binaries[name],
                "version": binary_version(context.binaries[name], name),
                "model": cfg.agent_model(name),
                "extra_args": list(cfg.agents[name].extra_args),
            }
            for name in cfg.run.agents
        },
        "tasks": [
            {"name": task, "fixture_sha256": fixtures.fixture_digest(task)}
            for task in tasks
        ],
        "sampling": {
            "reps": cfg.run.reps,
            "jobs": cfg.run.jobs,
            "timeout_s": cfg.run.timeout_s,
            "verify_timeout_s": cfg.run.verify_timeout_s,
            "index_timeout_s": cfg.run.index_timeout_s,
        },
        "host": {
            "platform": platform.platform(),
            "python": sys.version.split()[0],
        },
        "index": {
            "cache_dir": str(context.index_cache),
            "rebuild": context.index_rebuild,
            "entries": [
                {
                    "task": entry.get("task"),
                    "origin": entry.get("origin"),
                    "pages": entry.get("pages"),
                    "duration_s": round(float(entry.get("duration_s") or 0), 3),
                    "key": entry.get("key"),
                }
                for entry in (index_entries or [])
            ],
        },
        "config": cfg.to_json(),
        "total_runs": total_runs,
    }


def run(context: RunContext) -> list[dict[str, Any]]:
    """Execute every (task, agent, repetition) job and append raw rows."""
    cfg = context.config
    tasks = config.task_names(cfg)
    missing = sorted(set(tasks) - set(fixtures.discover_tasks()))
    if missing:
        raise SystemExit(f"unknown task(s): {', '.join(missing)}")

    context.out_dir.mkdir(parents=True, exist_ok=True)
    raw = context.out_dir / "raw.jsonl"
    raw.touch(exist_ok=True)
    metadata_path = context.out_dir / "metadata.json"
    started_at = util.iso_now()
    util.write_json(metadata_path, _metadata(context, tasks, started_at))

    fixtures.snapshot_fixtures(tasks, context.pristine_root)
    index_entries: list[dict[str, Any]] = []
    if "ogcode" in cfg.run.agents:
        index_entries = _prepare_indexes(context, tasks)
    util.write_json(
        metadata_path, _metadata(context, tasks, started_at, index_entries=index_entries)
    )
    print(
        f"benchmark={cfg.name} provider={context.provider.kind} "
        f"model={cfg.model.id} price={pricing.describe_price(context.price)}"
    )
    print(
        f"tasks={tasks} agents={cfg.run.agents} reps={cfg.run.reps} "
        f"jobs={cfg.run.jobs} out={context.out_dir}\n"
    )

    rows: list[dict[str, Any]] = []
    lock = threading.Lock()
    jobs = [
        (task, agent, rep)
        for task in tasks
        for agent in cfg.run.agents
        for rep in range(1, cfg.run.reps + 1)
    ]

    def run_job(job: tuple[str, str, int]) -> dict[str, Any]:
        task, agent, rep = job
        stamp = util.utc_stamp()
        row = bench_one(task, agent, rep, context, stamp)
        with lock:
            drift = fixtures.restore_fixtures([task], context.pristine_root)
            if drift:
                print(
                    f"  !! fixture drifted outside its worktree; restored {drift[0]}",
                    file=sys.stderr,
                    flush=True,
                )
            with raw.open("a") as fh:
                fh.write(json.dumps(row) + "\n")
            print(
                f"{task:12s} {agent:9s} rep{rep}  "
                f"pass={str(row['passed']):5s} ${row['cost_shared_usd']:.5f}  "
                f"{row['duration_s']:6.1f}s  turns={row['turns']:2d}  "
                f"tools={row['tool_calls']:2d}  "
                f"tok in/out={row['tokens']['input']}/{row['tokens']['output']}"
                + (f"  !! {row['error']}" if row["error"] else ""),
                flush=True,
            )
        return row

    try:
        if cfg.run.jobs <= 1:
            for job in jobs:
                rows.append(run_job(job))
        else:
            with concurrent.futures.ThreadPoolExecutor(
                max_workers=cfg.run.jobs
            ) as pool:
                futures = [pool.submit(run_job, job) for job in jobs]
                for future in concurrent.futures.as_completed(futures):
                    try:
                        rows.append(future.result())
                    except SystemExit as exc:
                        for other in futures:
                            other.cancel()
                        print(f"!! {exc}", file=sys.stderr, flush=True)
                        raise
    finally:
        util.write_json(
            metadata_path,
            _metadata(
                context,
                tasks,
                started_at,
                util.iso_now(),
                len(rows),
                index_entries,
            ),
        )

    total = sum(row["cost_shared_usd"] or 0 for row in rows)
    solved = sum(1 for row in rows if row["passed"])
    print(
        f"\n{len(rows)} runs, {solved} solved, ${total:.4f} total. "
        f"Raw rows: {raw}"
    )
    print(f"Report: python3 {config.PACKAGE_ROOT / 'report.py'} --raw {raw}")
    return rows
