"""Agent-specific commands, output parsing and isolation.

Both agents receive the same task prompt, the same fresh git fixture and the
same timeout.  The differences handled here are only the mechanics each binary
requires: model-string syntax, output format, autonomy flag and the location
from which it reads skills/MCP configuration.
"""

from __future__ import annotations

import csv
import json
import pathlib
import shlex
import shutil
import tempfile
from typing import Any, Optional

from . import providers, util
from .config import AgentConfig, BenchmarkConfig

OGCODE_GLOBAL_CONFIG = pathlib.Path.home() / ".config" / "ogcode" / "config.json"


def ogcode_isolation() -> dict[str, Any]:
    """Project config that hides every skill and MCP server from ogcode.

    The skill loader always picks up the machine's global skill roots, and the
    config loader merges the global ``mcp`` map by name, so a run inside a
    throwaway worktree still inherits both.  Shadowing them from the worktree
    closes it: ``permissions`` denies every skill name, and each globally
    configured server is restated as disabled.  Names must be read from the
    machine's config because ogcode has no "ignore global config" switch (a
    project only ever adds to it).
    """
    try:
        global_cfg = json.loads(OGCODE_GLOBAL_CONFIG.read_text())
    except (OSError, ValueError):
        global_cfg = {}
    return {
        "skills": {"permissions": {"*": "deny"}},
        "mcp": {name: {"disabled": True} for name in (global_cfg.get("mcp") or {})},
    }


def opencode_isolation(
    provider: providers.ResolvedProvider,
    model_id: str,
) -> tuple[dict[str, str], pathlib.Path]:
    """Create a throwaway opencode config directory.

    opencode reads its global config from ``$XDG_CONFIG_HOME/opencode``.  An
    empty config in a throwaway directory removes the user's MCP servers,
    plugins and skills from the run.  A custom endpoint additionally gets an
    explicit OpenAI-compatible provider block; the key is an environment
    interpolation, never a literal.
    """
    root = pathlib.Path(tempfile.mkdtemp(prefix="ogcode-benchmark-xdg-"))
    cfg_dir = root / "opencode"
    cfg_dir.mkdir(parents=True)
    config: dict[str, Any] = {
        "$schema": "https://opencode.ai/config.json",
        "mcp": {},
    }
    if provider.uses_custom_opencode:
        custom = providers.opencode_custom_config(provider, model_id)
        config["provider"] = custom["provider"]
    (cfg_dir / "opencode.json").write_text(json.dumps(config, indent=2) + "\n")
    return {"XDG_CONFIG_HOME": str(root)}, root


def agent_command(
    agent: str,
    config: BenchmarkConfig,
    provider: providers.ResolvedProvider,
    binaries: dict[str, str],
    prompt: str,
) -> list[str]:
    """Build the exact command line for one agent.

    The prompt is one argument, not interpolated into a shell string, so a task
    containing quotes or backticks cannot change the command.  Extra arguments
    from the config are inserted before the prompt so they cannot accidentally
    become part of the task text.
    """
    if agent == "ogcode":
        return [
            binaries["ogcode"],
            "run",
            "--model",
            config.agent_model("ogcode"),
            "--output-format",
            "json",
            *config.agents["ogcode"].extra_args,
            prompt,
        ]
    if agent == "opencode":
        return [
            binaries["opencode"],
            "run",
            "--model",
            config.agent_model("opencode"),
            "--format",
            "json",
            "--auto",
            *config.agents["opencode"].extra_args,
            prompt,
        ]
    raise SystemExit(f"unknown agent {agent!r}")


def command_preview(cmd: list[str]) -> str:
    """Shell-quoted command with the task prompt shortened for display."""
    if not cmd:
        return ""
    shown = list(cmd)
    shown[-1] = "<task prompt>"
    return shlex.join(shown)


def parse_ogcode(stdout: str) -> Optional[dict[str, Any]]:
    """Find the JSON result document in ogcode's stdout.

    ``ogcode run --output-format json`` prints one document, but a warning or a
    tool's own output can share stdout.  Try the whole stream, then each line,
    then every ``{`` position; the first object carrying ``tokens`` wins.
    """
    text = stdout.strip()
    candidates = [text]
    if text:
        candidates.extend(reversed(text.splitlines()))
    for candidate in candidates:
        candidate = candidate.strip()
        if not candidate.startswith("{"):
            continue
        try:
            obj = json.loads(candidate)
        except json.JSONDecodeError:
            continue
        if isinstance(obj, dict) and "tokens" in obj:
            return obj
    decoder = json.JSONDecoder()
    start = text.find("{")
    while start != -1:
        try:
            obj, _ = decoder.raw_decode(text[start:])
        except json.JSONDecodeError:
            start = text.find("{", start + 1)
            continue
        if isinstance(obj, dict) and "tokens" in obj:
            return obj
        start = text.find("{", start + 1)
    return None


def parse_opencode(stdout: str) -> dict[str, Any]:
    """Aggregate opencode's newline-delimited JSON event stream."""
    tokens = dict(util.ZERO_TOKENS)
    cost = 0.0
    steps = 0
    calls = 0
    texts: list[str] = []
    errors: list[str] = []
    for line in stdout.splitlines():
        line = line.strip()
        if not line.startswith("{"):
            continue
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        kind = event.get("type")
        payload = event.get("part") if isinstance(event.get("part"), dict) else event
        if kind == "step_finish":
            steps += 1
            step = payload.get("tokens") or {}
            cache = step.get("cache") or {}
            tokens["input"] += step.get("input", 0) or 0
            tokens["output"] += step.get("output", 0) or 0
            tokens["reasoning"] += step.get("reasoning", 0) or 0
            tokens["cache_read"] += cache.get("read", 0) or 0
            tokens["cache_write"] += cache.get("write", 0) or 0
            tokens["total"] += step.get("total", 0) or 0
            cost += payload.get("cost", 0) or 0
        elif kind == "tool_use":
            calls += 1
        elif kind == "text":
            text = (payload.get("text") or "").strip()
            if text:
                texts.append(text)
        elif kind == "error":
            errors.append(str(payload.get("error") or payload)[:500])
    tokens["total"] = tokens["total"] or tokens["input"] + tokens["output"]
    return {
        "tokens": tokens,
        "cost_reported_usd": cost,
        "turns": steps,
        "tool_calls": calls,
        "final_message": texts[-1] if texts else None,
        "errors": errors,
    }


def normalize_tokens(raw: dict[str, Any]) -> dict[str, int]:
    """Coerce provider-supplied token counts into the benchmark's schema."""
    return {
        "input": int(raw.get("input", 0) or 0),
        "output": int(raw.get("output", 0) or 0),
        "reasoning": int(raw.get("reasoning", 0) or 0),
        "cache_read": int(raw.get("cache_read", 0) or 0),
        "cache_write": int(raw.get("cache_write", 0) or 0),
        "total": int(raw.get("total", 0) or 0),
    }


def ogcode_log_dir(
    out_dir: pathlib.Path,
    task: str,
    agent: str,
    rep: int,
    stamp: str,
) -> pathlib.Path:
    """Log root for one ogcode run, inside the results it belongs to.

    ogcode files every record under a per-project folder keyed by the worktree
    path, so the default root (``~/.ogcode/logs``) keeps one folder per run for
    a worktree that no longer exists.  Pointing the root at the results keeps a
    run's log beside its row, where it can be read later.

    The path is resolved before the agent starts: a relative root would be
    resolved inside the worktree and deleted with it.
    """
    directory = (out_dir.resolve() / "logs" / f"{task}-{agent}-rep{rep}-{stamp}").resolve()
    directory.mkdir(parents=True, exist_ok=True)
    return directory


def read_provider_route(log_dir: Optional[pathlib.Path], provider: providers.ResolvedProvider) -> dict[str, Any]:
    """Read the first available ``run.log`` under a run's log directory."""
    if log_dir is None or not log_dir.exists():
        return {"ogcode_provider": None, "ogcode_url": None, "provider_route_ok": None}
    logs = sorted(log_dir.rglob("run.log"))
    if not logs:
        return {"ogcode_provider": None, "ogcode_url": None, "provider_route_ok": None}
    try:
        text = "\n".join(path.read_text(errors="replace") for path in logs)
    except OSError:
        return {"ogcode_provider": None, "ogcode_url": None, "provider_route_ok": None}
    return providers.provider_route_from_log(text, provider)


def read_tool_audit(
    dest: pathlib.Path,
    out_dir: pathlib.Path,
    task: str,
    agent: str,
    rep: int,
    stamp: str,
) -> dict[str, Any]:
    """Copy out the tool calls ogcode recorded for one run.

    Every call appends a row to ``<workdir>/.ogcode/tool_calls.csv`` as it
    finishes.  The file lives in the worktree, which is deleted after the run
    unless ``--keep``, so the rows are copied into ``<out>/toolcalls/`` first.
    The index build writes into the same file under its own agent name before
    the measured run starts, so only ``agent=build`` rows are counted; the copy
    keeps every row for inspection.
    """
    empty = {"count": 0, "failures": 0, "sequence": [], "csv": None}
    src = dest / ".ogcode" / "tool_calls.csv"
    try:
        with src.open(newline="") as fh:
            rows = list(csv.DictReader(fh))
    except (OSError, csv.Error):
        return empty
    directory = out_dir / "toolcalls"
    directory.mkdir(parents=True, exist_ok=True)
    target = directory / f"{task}-{agent}-rep{rep}-{stamp}.csv"
    shutil.copy2(src, target)
    run_rows = [r for r in rows if (r.get("agent") or "").strip() == "build"]
    return {
        "count": len(run_rows),
        "failures": sum(1 for r in run_rows if (r.get("status") or "").strip() != "completed"),
        "sequence": [r.get("tool") or "" for r in run_rows],
        "csv": util.relative_path(target, out_dir),
    }


def agent_config(config: BenchmarkConfig, agent: str) -> AgentConfig:
    """The parsed per-agent config, with a clear error for an unknown name."""
    if agent not in config.agents:
        raise SystemExit(f"unknown agent {agent!r}")
    return config.agents[agent]
