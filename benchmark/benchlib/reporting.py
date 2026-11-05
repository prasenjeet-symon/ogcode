"""Aggregate ``raw.jsonl`` into a human report, a CSV and a machine summary.

Medians are taken across the repetitions of each ``(task, agent)`` pair, so one
slow or lucky run does not move the headline number.  The overall "median cost
per task" is the median of those per-task medians: each task contributes one
number regardless of how many repetitions it has.

The report is deliberately explicit about what was measured.  Costs are the
shared-table values recorded for each run (computed from token counts at run
time); the agents' own reported costs and ogcode's resolved provider route are
shown as cross-checks, not used as the result.
"""

from __future__ import annotations

import csv
import json
import pathlib
import statistics
from typing import Any, Optional

from . import config, util


def load(raw: pathlib.Path) -> list[dict[str, Any]]:
    """Read a JSONL result file, ignoring blank lines."""
    rows: list[dict[str, Any]] = []
    for line in raw.read_text().splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            row = json.loads(line)
        except json.JSONDecodeError as exc:
            raise SystemExit(f"{raw}: invalid JSON line: {exc}") from exc
        if isinstance(row, dict):
            rows.append(row)
    return rows


def load_metadata(raw: pathlib.Path) -> Optional[dict[str, Any]]:
    """Read the sidecar written beside a run, when it exists."""
    path = raw.parent / "metadata.json"
    if not path.is_file():
        return None
    try:
        value = json.loads(path.read_text())
    except (OSError, json.JSONDecodeError):
        return None
    return value if isinstance(value, dict) else None


def median(values: list[float]) -> float:
    return float(statistics.median(values)) if values else 0.0


def group(rows: list[dict[str, Any]]) -> dict[tuple[str, str], list[dict[str, Any]]]:
    out: dict[tuple[str, str], list[dict[str, Any]]] = {}
    for row in rows:
        out.setdefault((row["task"], row["agent"]), []).append(row)
    return out


def money(value: float) -> str:
    return f"${value:.4f}"


def ratio(us: float, them: float) -> float:
    return us / them if them else 0.0


def percent_change(us: float, them: float) -> float:
    """Percent change from ``them`` to ``us``; negative means ours is lower."""
    return (us - them) / them * 100 if them else 0.0


def _cost(row: dict[str, Any]) -> float:
    return float(row.get("cost_shared_usd") or 0.0)


def _tokens(row: dict[str, Any], name: str) -> float:
    tokens = row.get("tokens") or {}
    return float(tokens.get(name, 0) or 0)


def _resource(row: dict[str, Any], name: str) -> Optional[float]:
    value = (row.get("resources") or {}).get(name)
    return float(value) if value is not None else None


def _resource_values(rows: list[dict[str, Any]], name: str) -> list[float]:
    values = [_resource(row, name) for row in rows]
    return [value for value in values if value is not None]


def mib(value: Optional[float]) -> str:
    """Format a byte count as mebibytes, or ``n/a`` when it was not sampled."""
    return f"{value / (1024 * 1024):.1f} MiB" if value is not None else "n/a"


def agent_summary(rows: list[dict[str, Any]]) -> dict[str, Any]:
    """The per-agent statistics shared by the markdown and JSON outputs."""
    if not rows:
        return {}
    grouped = group(rows)
    per_task_cost = [
        median([_cost(row) for row in runs]) for runs in grouped.values()
    ]
    token_totals = {
        name: int(sum(_tokens(row, name) for row in rows))
        for name in ("input", "output", "reasoning", "cache_read", "cache_write")
    }
    peak_rss = _resource_values(rows, "peak_tree_rss_bytes")
    cpu = _resource_values(rows, "cpu_total_s")
    cpu_per_wall = _resource_values(rows, "cpu_per_wall")
    return {
        "runs": len(rows),
        "solved": sum(1 for row in rows if row.get("passed")),
        "total_cost_usd": sum(_cost(row) for row in rows),
        "median_cost_per_task_usd": median(per_task_cost),
        "mean_cost_per_task_usd": (
            sum(per_task_cost) / len(per_task_cost) if per_task_cost else 0.0
        ),
        "total_duration_s": sum(float(row.get("duration_s") or 0) for row in rows),
        "median_duration_s": median([float(row.get("duration_s") or 0) for row in rows]),
        "median_turns": median([float(row.get("turns") or 0) for row in rows]),
        "median_tool_calls": median(
            [float(row.get("tool_calls") or 0) for row in rows]
        ),
        "token_totals": token_totals,
        "resource_runs": sum(1 for row in rows if row.get("resources")),
        "median_peak_rss_bytes": median(peak_rss) if peak_rss else None,
        "max_peak_rss_bytes": max(peak_rss) if peak_rss else None,
        "median_cpu_s": median(cpu) if cpu else None,
        "total_cpu_s": sum(cpu) if cpu else None,
        "median_cpu_per_wall": median(cpu_per_wall) if cpu_per_wall else None,
    }


def cross_checks(rows: list[dict[str, Any]]) -> dict[str, Any]:
    """Facts a reader can use to decide how much to trust the numbers."""
    reported = [
        row
        for row in rows
        if row.get("cost_reported_usd") is not None
        and row.get("cost_shared_usd") is not None
    ]
    drift = [
        abs(float(row["cost_reported_usd"]) - float(row["cost_shared_usd"]))
        for row in reported
    ]
    indexed = [row for row in rows if row.get("index")]
    built = [
        row for row in indexed if (row["index"].get("origin") or "built") == "built"
    ]
    restored = [row for row in indexed if row["index"].get("origin") == "restored"]
    routed = [row for row in rows if row.get("provider_route_ok") is not None]
    return {
        "reported_cost_runs": len(reported),
        "max_reported_cost_drift_usd": max(drift) if drift else None,
        "failed_verification": sum(1 for row in rows if not row.get("passed")),
        "errors_or_timeouts": sum(1 for row in rows if row.get("error")),
        "indexed_ogcode_runs": len(indexed),
        "index_pages_min": min((int(row["index"].get("pages", 0)) for row in indexed), default=None),
        "index_pages_max": max((int(row["index"].get("pages", 0)) for row in indexed), default=None),
        "index_median_build_s": median(
            [float(row["index"].get("duration_s", 0)) for row in built]
        )
        if built
        else None,
        "index_built_runs": len(built),
        "index_restored_runs": len(restored),
        "audited_tool_runs": sum(1 for row in rows if row.get("tool_audit")),
        "provider_routes_checked": len(routed),
        "provider_route_failures": sum(
            1 for row in routed if row.get("provider_route_ok") is False
        ),
    }


def build_summary(
    rows: list[dict[str, Any]],
    model: str,
    metadata: Optional[dict[str, Any]] = None,
) -> dict[str, Any]:
    agents = sorted({row["agent"] for row in rows})
    per_agent = {
        agent: agent_summary([row for row in rows if row["agent"] == agent])
        for agent in agents
    }
    summary: dict[str, Any] = {
        "schema": 1,
        "generated_at": util.iso_now(),
        "model": model,
        "provider": (metadata or {}).get("provider", {}).get("kind"),
        "config": (metadata or {}).get("benchmark", {}).get("config"),
        "pricing_source": next(
            (row.get("pricing_source") for row in rows if row.get("pricing_source")),
            (metadata or {}).get("pricing", {}).get("source"),
        ),
        "runs": len(rows),
        "tasks": sorted({row["task"] for row in rows}),
        "agents": per_agent,
        "cross_checks": cross_checks(rows),
    }
    if "ogcode" in per_agent and "opencode" in per_agent:
        us, them = per_agent["ogcode"], per_agent["opencode"]
        summary["comparison"] = {
            "ogcode_vs_opencode": {
                "total_cost_reduction_pct": -percent_change(
                    us["total_cost_usd"], them["total_cost_usd"]
                ),
                "median_cost_per_task_reduction_pct": -percent_change(
                    us["median_cost_per_task_usd"], them["median_cost_per_task_usd"]
                ),
                "median_duration_reduction_pct": -percent_change(
                    us["median_duration_s"], them["median_duration_s"]
                ),
                "total_duration_reduction_pct": -percent_change(
                    us["total_duration_s"], them["total_duration_s"]
                ),
                "median_tool_calls_ratio": ratio(
                    us["median_tool_calls"], them["median_tool_calls"]
                ),
            }
        }
        if us["resource_runs"] and them["resource_runs"]:
            resources = {
                "median_peak_rss_reduction_pct": -percent_change(
                    us["median_peak_rss_bytes"], them["median_peak_rss_bytes"]
                ),
                "median_peak_rss_ratio": ratio(
                    us["median_peak_rss_bytes"], them["median_peak_rss_bytes"]
                ),
                "median_cpu_reduction_pct": -percent_change(
                    us["median_cpu_s"], them["median_cpu_s"]
                ),
                "median_cpu_ratio": ratio(us["median_cpu_s"], them["median_cpu_s"]),
            }
            summary["comparison"]["ogcode_vs_opencode"]["resources"] = resources
    return summary


def _headline_lines(
    rows: list[dict[str, Any]],
    per_agent: dict[str, dict[str, Any]],
) -> list[str]:
    """A short executive summary; only the comparisons both sides support."""
    if "ogcode" not in per_agent or "opencode" not in per_agent:
        return []
    us, them = per_agent["ogcode"], per_agent["opencode"]
    solved_us = f"{us['solved']}/{us['runs']}"
    solved_them = f"{them['solved']}/{them['runs']}"
    total_cost_change = percent_change(us["total_cost_usd"], them["total_cost_usd"])
    median_cost_change = percent_change(
        us["median_cost_per_task_usd"], them["median_cost_per_task_usd"]
    )
    lines = [
        "## Headline",
        "",
        f"- **Cost:** {money(us['total_cost_usd'])} vs {money(them['total_cost_usd'])} "
        f"total ({total_cost_change:+.1f}%); "
        f"{money(us['median_cost_per_task_usd'])} vs "
        f"{money(them['median_cost_per_task_usd'])} median per task "
        f"({median_cost_change:+.1f}%).",
        f"- **Time:** {us['median_duration_s']:.1f}s vs {them['median_duration_s']:.1f}s "
        f"median run ({percent_change(us['median_duration_s'], them['median_duration_s']):+.1f}%); "
        f"{us['total_duration_s']:.1f}s vs {them['total_duration_s']:.1f}s total agent time "
        f"({percent_change(us['total_duration_s'], them['total_duration_s']):+.1f}%).",
    ]
    if us["resource_runs"] and them["resource_runs"]:
        lines.append(
            f"- **Memory:** {mib(us['median_peak_rss_bytes'])} vs "
            f"{mib(them['median_peak_rss_bytes'])} median peak RSS "
            f"({percent_change(us['median_peak_rss_bytes'], them['median_peak_rss_bytes']):+.1f}%); "
            f"{us['median_cpu_s']:.1f}s vs {them['median_cpu_s']:.1f}s median CPU "
            f"({percent_change(us['median_cpu_s'], them['median_cpu_s']):+.1f}%)."
        )
    lines += [f"- **Solved:** {solved_us} vs {solved_them} runs."]
    if us["solved"] == us["runs"] and them["solved"] == them["runs"]:
        lines.append(f"- Both agents solved every run ({us['runs'] + them['runs']}/{us['runs'] + them['runs']} green).")
    return lines + [""]


def build_markdown(
    rows: list[dict[str, Any]],
    model: str,
    metadata: Optional[dict[str, Any]] = None,
) -> str:
    agents = sorted({row["agent"] for row in rows})
    tasks = sorted({row["task"] for row in rows})
    grouped = group(rows)
    per_agent = {
        agent: agent_summary([row for row in rows if row["agent"] == agent])
        for agent in agents
    }
    provider = (metadata or {}).get("provider", {}).get("kind")
    config_name = (metadata or {}).get("benchmark", {}).get("config")

    lines = [
        f"# Cost per task: {' vs '.join(agents)}",
        "",
        f"Model: `{model}`"
        + (f" · Provider: `{provider}`" if provider else "")
        + (f" · Config: `{config_name}`" if config_name else "")
        + f" · {len(rows)} runs",
        "",
    ]
    lines += _headline_lines(rows, per_agent)

    lines += ["## Per task (median of the repetitions)", ""]
    header = (
        "| Task | Agent | Solved | $ / run | Turns | Tool calls | Seconds "
        "| Input tok | Output tok | Cache read tok |"
    )
    lines += [header, "|" + "---|" * 10]
    for task in tasks:
        for agent in agents:
            runs = grouped.get((task, agent), [])
            if not runs:
                continue
            solved = sum(1 for row in runs if row.get("passed"))
            lines.append(
                "| {task} | {agent} | {solved}/{total} | {cost} | {turns:.0f} | "
                "{calls:.0f} | {secs:.0f} | {inp:.0f} | {out:.0f} | {cread:.0f} |".format(
                    task=task,
                    agent=agent,
                    solved=solved,
                    total=len(runs),
                    cost=money(median([_cost(row) for row in runs])),
                    turns=median([float(row.get("turns") or 0) for row in runs]),
                    calls=median([float(row.get("tool_calls") or 0) for row in runs]),
                    secs=median([float(row.get("duration_s") or 0) for row in runs]),
                    inp=median([_tokens(row, "input") for row in runs]),
                    out=median([_tokens(row, "output") for row in runs]),
                    cread=median([_tokens(row, "cache_read") for row in runs]),
                )
            )

    lines += ["", "## Overall", ""]
    header = (
        "| Agent | Solved | Total $ | Median $ / task | Mean $ / task "
        "| Median turns | Median tool calls | Median seconds |"
    )
    lines += [header, "|" + "---|" * 8]
    for agent in agents:
        stats = per_agent[agent]
        lines.append(
            "| {agent} | {solved}/{total} | {total_cost} | {med} | {mean} | "
            "{turns:.0f} | {calls:.0f} | {secs:.0f} |".format(
                agent=agent,
                solved=stats["solved"],
                total=stats["runs"],
                total_cost=money(stats["total_cost_usd"]),
                med=money(stats["median_cost_per_task_usd"]),
                mean=money(stats["mean_cost_per_task_usd"]),
                turns=stats["median_turns"],
                calls=stats["median_tool_calls"],
                secs=stats["median_duration_s"],
            )
        )

    sampled = [agent for agent in agents if per_agent[agent]["resource_runs"]]
    if sampled:
        lines += ["", "## Resources (sampled while each run executed)", ""]
        lines += [
            "| Agent | Runs sampled | Median peak RSS | Max peak RSS "
            "| Median CPU s | Total CPU s | Median CPU / wall |",
            "|---|---|---|---|---|---|---|",
        ]
        for agent in sampled:
            stats = per_agent[agent]
            lines.append(
                "| {agent} | {runs} | {med_rss} | {max_rss} | {cpu:.1f} "
                "| {total_cpu:.1f} | {cpw:.2f} |".format(
                    agent=agent,
                    runs=stats["resource_runs"],
                    med_rss=mib(stats["median_peak_rss_bytes"]),
                    max_rss=mib(stats["max_peak_rss_bytes"]),
                    cpu=stats["median_cpu_s"] or 0.0,
                    total_cpu=stats["total_cpu_s"] or 0.0,
                    cpw=stats["median_cpu_per_wall"] or 0.0,
                )
            )
        first = next((row["resources"] for row in rows if row.get("resources")), {})
        lines += [
            "",
            "- Peak RSS is the resident set of the agent's whole process tree, "
            f"sampled every {first.get('interval_s', 0.1):g}s; CPU time is the "
            "run's own user plus system time from `getrusage`.",
        ]

    if "ogcode" in agents and "opencode" in agents:
        lines += ["", "## Ratios (ogcode divided by opencode, per-task medians)", ""]
        lines += [
            "| Task | $ ratio | Input tok ratio | Output tok ratio | Cache read ratio |",
            "|---|---|---|---|---|",
        ]
        for task in tasks:
            us = grouped.get((task, "ogcode"), [])
            them = grouped.get((task, "opencode"), [])
            if not us or not them:
                continue
            lines.append(
                "| {task} | {cost:.2f} | {inp:.2f} | {out:.2f} | {cread:.2f} |".format(
                    task=task,
                    cost=ratio(
                        median([_cost(row) for row in us]),
                        median([_cost(row) for row in them]),
                    ),
                    inp=ratio(
                        median([_tokens(row, "input") for row in us]),
                        median([_tokens(row, "input") for row in them]),
                    ),
                    out=ratio(
                        median([_tokens(row, "output") for row in us]),
                        median([_tokens(row, "output") for row in them]),
                    ),
                    cread=ratio(
                        median([_tokens(row, "cache_read") for row in us]),
                        median([_tokens(row, "cache_read") for row in them]),
                    ),
                )
            )

    checks = cross_checks(rows)
    lines += ["", "## Cross-checks", ""]
    if checks["max_reported_cost_drift_usd"] is not None:
        lines.append(
            f"- Agent-reported cost vs shared price table: max drift "
            f"${checks['max_reported_cost_drift_usd']:.6f} over "
            f"{checks['reported_cost_runs']} runs."
        )
    lines.append(
        f"- Runs that failed verification: {checks['failed_verification']}."
    )
    lines.append(
        f"- Runs that errored or timed out: {checks['errors_or_timeouts']}."
    )
    if checks["indexed_ogcode_runs"]:
        if checks["index_restored_runs"]:
            detail = f"{checks['index_pages_min']}-{checks['index_pages_max']} pages"
            if checks["index_median_build_s"] is not None:
                detail += f", median {checks['index_median_build_s']:.0f}s to build"
            lines.append(
                f"- Index restored before {checks['index_restored_runs']} of "
                f"{checks['indexed_ogcode_runs']} ogcode runs: {detail}, "
                "excluded from every figure above."
            )
        else:
            lines.append(
                f"- Index built before {checks['indexed_ogcode_runs']} ogcode runs: "
                f"{checks['index_pages_min']}-{checks['index_pages_max']} pages, "
                f"median {checks['index_median_build_s']:.0f}s to build, "
                "excluded from every figure above."
            )
    if checks["audited_tool_runs"]:
        lines.append(
            f"- Tool calls are each agent's own record: ogcode's "
            f"`.ogcode/tool_calls.csv` (copied to `toolcalls/`, "
            f"{checks['audited_tool_runs']} runs) and opencode's streamed "
            "`tool_use` events. Per-run logs, when retained, are under `logs/`."
        )
    if checks["provider_routes_checked"]:
        failures = checks["provider_route_failures"]
        if failures:
            lines.append(
                f"- Provider route checked in {checks['provider_routes_checked']} "
                f"ogcode runs: **{failures} did not resolve through the configured "
                "provider slot** (see `ogcode_provider` in `raw.jsonl`)."
            )
        else:
            lines.append(
                f"- Provider route checked in {checks['provider_routes_checked']} "
                "ogcode runs: all resolved through the configured provider slot."
            )
    return "\n".join(lines) + "\n"


def build_csv(rows: list[dict[str, Any]]) -> list[list[Any]]:
    header = [
        "task",
        "agent",
        "rep",
        "passed",
        "duration_s",
        "turns",
        "tool_calls",
        "input_tokens",
        "output_tokens",
        "cache_read_tokens",
        "reasoning_tokens",
        "cost_shared_usd",
        "cost_reported_usd",
        "error",
        "cache_write_tokens",
        "model",
        "provider_kind",
        "agent_model",
        "config_name",
        "pricing_source",
        "provider_route_ok",
        "index_pages",
        "peak_rss_bytes",
        "peak_footprint_bytes",
        "cpu_user_s",
        "cpu_sys_s",
        "cpu_total_s",
        "cpu_per_wall",
    ]
    body: list[list[Any]] = []
    for row in rows:
        index = row.get("index") or {}
        resources = row.get("resources") or {}
        body.append(
            [
                row["task"],
                row["agent"],
                row["rep"],
                row.get("passed"),
                row.get("duration_s"),
                row.get("turns"),
                row.get("tool_calls") or 0,
                _tokens(row, "input"),
                _tokens(row, "output"),
                _tokens(row, "cache_read"),
                _tokens(row, "reasoning"),
                row.get("cost_shared_usd"),
                row.get("cost_reported_usd"),
                row.get("error") or "",
                _tokens(row, "cache_write"),
                row.get("model") or "",
                row.get("provider_kind") or "",
                row.get("agent_model") or "",
                row.get("config_name") or "",
                row.get("pricing_source") or "",
                row.get("provider_route_ok"),
                index.get("pages"),
                resources.get("peak_tree_rss_bytes"),
                resources.get("peak_tree_footprint_bytes"),
                resources.get("cpu_user_s"),
                resources.get("cpu_sys_s"),
                resources.get("cpu_total_s"),
                resources.get("cpu_per_wall"),
            ]
        )
    return [header] + body


def write_report(
    rows: list[dict[str, Any]],
    out_dir: pathlib.Path,
    model: Optional[str] = None,
    metadata: Optional[dict[str, Any]] = None,
) -> tuple[str, dict[str, Any]]:
    """Write ``report.md``, ``report.csv`` and ``summary.json``."""
    if not rows:
        raise SystemExit("no result rows")
    chosen_model = model or next(
        (str(row["model"]) for row in rows if row.get("model")), "unknown"
    )
    markdown = build_markdown(rows, chosen_model, metadata)
    summary = build_summary(rows, chosen_model, metadata)
    out_dir.mkdir(parents=True, exist_ok=True)
    (out_dir / "report.md").write_text(markdown)
    with (out_dir / "report.csv").open("w", newline="") as fh:
        csv.writer(fh).writerows(build_csv(rows))
    util.write_json(out_dir / "summary.json", summary)
    return markdown, summary
