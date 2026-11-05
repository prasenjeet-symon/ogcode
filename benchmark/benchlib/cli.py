"""Command-line interface for the benchmark.

The CLI is intentionally thin: it loads a config, applies explicit overrides,
prints a redacted plan in ``--dry-run`` and hands the validated objects to the
runner.  Keeping argument handling separate from the run loop means a config
file is enough to reproduce a run; the flags are only conveniences.
"""

from __future__ import annotations

import argparse
import os
import pathlib
import shutil
import sys
from typing import Optional

from . import agents, config, indexcache, pricing, providers, runner, util


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="run_benchmark.py",
        description=(
            "Measure cost, time and solve rate for ogcode vs opencode on identical tasks."
        ),
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=(
            "Examples:\n"
            "  python3 run_benchmark.py --config ollama-cloud --reps 5\n"
            "  python3 run_benchmark.py --config openrouter --tasks py-bugfix --agents ogcode\n"
            "  python3 run_benchmark.py --config ollama-cloud --build-index\n"
            "  python3 run_benchmark.py --config configs/custom-openai-compatible.json --dry-run\n"
            "\n"
            "Presets: "
            + ", ".join(config.available_presets())
        ),
    )
    parser.add_argument(
        "--config",
        default="ollama-cloud",
        help="preset name from configs/ or a path to a JSON config (default: ollama-cloud)",
    )
    parser.add_argument("--list-configs", action="store_true", help="list bundled presets and exit")
    parser.add_argument("--version", action="version", version=f"ogcode-benchmark {runner.BENCHMARK_VERSION}")
    parser.add_argument("--tasks", help="comma-separated task names, or 'all'")
    parser.add_argument("--agents", help="comma-separated agent names (ogcode,opencode)")
    parser.add_argument("--reps", type=int, help="repetitions per task and agent")
    parser.add_argument("--jobs", type=int, help="agent runs to execute concurrently")
    parser.add_argument("--model", help="override the canonical model id (clears per-agent model overrides)")
    parser.add_argument("--ogcode-model", help="override only the model string passed to ogcode")
    parser.add_argument("--opencode-model", help="override only the model string passed to opencode")
    parser.add_argument("--timeout", type=int, dest="timeout_s", help="seconds per agent run")
    parser.add_argument("--verify-timeout", type=int, dest="verify_timeout_s", help="seconds per verification")
    parser.add_argument("--index-timeout", type=int, dest="index_timeout_s", help="seconds to build the ogcode index")
    parser.add_argument("--out", help="results directory (default: run.output_dir from config)")
    parser.add_argument(
        "--build-index",
        action="store_true",
        help="build the ogcode index for every selected task and exit (reused by later runs)",
    )
    parser.add_argument(
        "--rebuild-index",
        action="store_true",
        help="ignore cached indexes and build them again",
    )
    parser.add_argument(
        "--index-cache",
        help="directory for reusable indexes (default: ~/.cache/ogcode-benchmark/indexes)",
    )
    parser.add_argument("--pricing", help="price table JSON (default: pricing.json)")
    parser.add_argument("--ogcode", default="ogcode", help="ogcode binary or path")
    parser.add_argument("--opencode", default="opencode", help="opencode binary or path")
    parser.add_argument("--keep", action="store_true", help="keep per-run worktrees for inspection")
    parser.add_argument("--dry-run", action="store_true", help="validate and print the plan without spending money")
    return parser


def _split_list(value: Optional[str]) -> Optional[list[str]]:
    if value is None:
        return None
    items = [item.strip() for item in value.split(",") if item.strip()]
    return items or None


def _apply_overrides(cfg: config.BenchmarkConfig, args: argparse.Namespace) -> None:
    """Apply CLI conveniences to an already validated config in place."""
    if args.model:
        cfg.model.id = args.model.strip()
        for agent in config.AGENT_NAMES:
            cfg.agents[agent].model = None
    if args.ogcode_model:
        cfg.agents["ogcode"].model = args.ogcode_model.strip()
    if args.opencode_model:
        cfg.agents["opencode"].model = args.opencode_model.strip()
    if args.tasks is not None:
        tasks = _split_list(args.tasks)
        cfg.run.tasks = [] if not tasks or tasks == ["all"] else tasks
    if args.agents is not None:
        chosen = _split_list(args.agents)
        if not chosen:
            raise SystemExit("--agents must name at least one agent")
        unknown = sorted(set(chosen) - set(config.AGENT_NAMES))
        if unknown:
            raise SystemExit(f"unknown agent(s): {', '.join(unknown)}")
        cfg.run.agents = chosen
    for name in ("reps", "jobs", "timeout_s", "verify_timeout_s", "index_timeout_s"):
        value = getattr(args, name)
        if value is not None:
            if value <= 0:
                shown = {
                    "timeout_s": "timeout",
                    "verify_timeout_s": "verify-timeout",
                    "index_timeout_s": "index-timeout",
                }.get(name, name.replace("_", "-"))
                raise SystemExit(f"--{shown} must be a positive integer")
            setattr(cfg.run, name, value)


def _print_configs() -> int:
    for name in config.available_presets():
        try:
            cfg = config.load_config(name)
        except SystemExit as exc:
            print(f"{name:28s} INVALID: {exc}")
            continue
        print(config.config_summary(cfg))
    return 0


def _binary_for_dry_run(name: str, fallback: str) -> str:
    return shutil.which(name) or fallback


def _resolve_index_cache(value: Optional[str]) -> pathlib.Path:
    if value:
        return pathlib.Path(value).expanduser().resolve()
    return indexcache.default_cache_dir()


def _print_dry_run(
    cfg: config.BenchmarkConfig,
    provider: providers.ResolvedProvider,
    price: Optional[dict[str, float]],
    price_source: str,
    binaries: dict[str, str],
    out_dir: pathlib.Path,
    index_cache: pathlib.Path,
    ogcode_version: str,
    index_rebuild: bool,
) -> int:
    tasks = config.task_names(cfg)
    key_status = (
        "<set>" if os.environ.get(provider.api_key_env, "").strip() else "<missing>"
    )
    print(f"ogcode-benchmark {runner.BENCHMARK_VERSION}")
    print(f"config           {cfg.name} ({cfg.source})")
    print(f"provider         {provider.kind} — {provider.display_name}")
    if provider.base_url:
        print(f"base URL         {provider.base_url}")
    print(f"api key          {provider.api_key_env}={key_status}")
    print(f"ogcode slot      {provider.ogcode_provider}")
    print(
        f"opencode         {provider.opencode_provider}"
        + (" (generated temp config)" if provider.uses_custom_opencode else " (built-in)")
    )
    print(f"model            {cfg.model.id}")
    print(f"price            {pricing.describe_price(price)} (source: {price_source})")
    print(f"tasks            {', '.join(tasks)}")
    print(f"agents           {', '.join(cfg.run.agents)}")
    print(f"reps             {cfg.run.reps}")
    print(f"jobs             {cfg.run.jobs}")
    print(f"timeouts         run={cfg.run.timeout_s}s verify={cfg.run.verify_timeout_s}s index={cfg.run.index_timeout_s}s")
    print(f"out              {out_dir}")
    cached = 0
    if "ogcode" in cfg.run.agents:
        cached = sum(
            1
            for task in tasks
            if indexcache.is_cached(
                indexcache.Spec.of(
                    task,
                    config=cfg,
                    provider=provider,
                    ogcode_version=ogcode_version,
                    cache_dir=index_cache,
                )
            )
        )
    print(f"index cache      {index_cache} ({cached}/{len(tasks)} cached)")
    print(
        f"index            {'rebuild' if index_rebuild else 'reuse when present'}"
    )
    print(f"ogcode           {binaries['ogcode']}")
    print(f"opencode         {binaries['opencode']}")
    print("\ncommands (task prompt abbreviated):")
    prompt = "<task prompt>"
    for agent in cfg.run.agents:
        cmd = agents.agent_command(agent, cfg, provider, binaries, prompt)
        print(f"  {agent:9s} {agents.command_preview(cmd)}")

    warnings: list[str] = []
    if key_status == "<missing>":
        warnings.append(f"{provider.api_key_env} is not set; a real run will stop here")
    if price is None:
        warnings.append(
            "no price for this model in the config or pricing.json; a real run will stop here"
        )
    for agent, binary in binaries.items():
        if agent in cfg.run.agents and not shutil.which(binary) and not pathlib.Path(binary).is_file():
            warnings.append(f"{agent} binary not found: {binary}")
    if warnings:
        print("\nwarnings:")
        for warning in warnings:
            print(f"  - {warning}")
    print("\nDry run only: no model calls were made.")
    return 0


def _resolve_binaries(args: argparse.Namespace, require: bool) -> dict[str, str]:
    binaries = {}
    for agent, value, fallbacks in (
        ("ogcode", args.ogcode, ["~/.local/bin/ogcode"]),
        ("opencode", args.opencode, ["~/.opencode/bin/opencode"]),
    ):
        if require:
            binaries[agent] = util.find_binary(value, fallbacks)
        else:
            found = util.find_binary(value, fallbacks) if shutil.which(value) or any(
                pathlib.Path(os.path.expanduser(path)).is_file() for path in fallbacks
            ) else value
            binaries[agent] = found
    return binaries


def main(argv: Optional[list[str]] = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    if args.list_configs:
        return _print_configs()

    cfg = config.load_config(args.config)
    _apply_overrides(cfg, args)

    provider = providers.resolve_provider(cfg.provider)
    price_path = pathlib.Path(args.pricing).expanduser().resolve() if args.pricing else None
    price, price_source = pricing.resolve_price(cfg, price_path)
    out_dir = config.resolve_output_dir(cfg, args.out)
    binaries = _resolve_binaries(args, require=not args.dry_run)

    if args.dry_run:
        index_cache = _resolve_index_cache(args.index_cache)
        return _print_dry_run(
            cfg,
            provider,
            price,
            price_source,
            binaries,
            out_dir,
            index_cache,
            runner.binary_version(binaries["ogcode"], "ogcode"),
            args.rebuild_index,
        )

    api_key = os.environ.get(provider.api_key_env, "").strip()
    if not api_key:
        raise SystemExit(
            f"{provider.api_key_env} is not set; export it or choose a config that names "
            "an environment variable you have"
        )

    provider_environment = providers.provider_env(provider, api_key)
    verify_environment = providers.scrub_provider_env()
    context = runner.RunContext(
        config=cfg,
        provider=provider,
        price=price or {},
        price_source=price_source,
        binaries=binaries,
        out_dir=out_dir,
        provider_env=provider_environment,
        verify_env=verify_environment,
        keep=args.keep,
        pristine_root=out_dir / ".pristine",
        index_cache=_resolve_index_cache(args.index_cache),
        index_rebuild=args.rebuild_index,
        ogcode_version=runner.binary_version(binaries["ogcode"], "ogcode"),
    )
    if args.build_index:
        return runner.build_indexes(context)
    if price is None:
        raise SystemExit(
            f"no price for model {cfg.model.id!r}. Add model.pricing to {cfg.source} "
            "or an entry to pricing.json before spending money."
        )
    runner.run(context)
    return 0
