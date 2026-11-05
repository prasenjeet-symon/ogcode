"""Shared helpers for the benchmark test suite.

The suite is deliberately stdlib-only, so these helpers are just a small
bootstrap for importing ``benchlib`` when discovery is started either from the
repository root or from inside ``benchmark/``.
"""

from __future__ import annotations

import json
import pathlib
import subprocess
import sys
from typing import Any, Optional

BENCHMARK_ROOT = pathlib.Path(__file__).resolve().parents[1]
REPO_ROOT = BENCHMARK_ROOT.parent
if str(BENCHMARK_ROOT) not in sys.path:
    sys.path.insert(0, str(BENCHMARK_ROOT))

from benchlib import agents, config, fixtures, pricing, providers, reporting, util  # noqa: E402

CONFIGS_DIR = BENCHMARK_ROOT / "configs"
TASKS_DIR = BENCHMARK_ROOT / "tasks"
EXAMPLE_R7 = BENCHMARK_ROOT / "examples" / "ollama-cloud-r7"
RAW_R7 = EXAMPLE_R7 / "raw.jsonl"
METADATA_R7 = EXAMPLE_R7 / "metadata.json"
RUN_SCRIPT = BENCHMARK_ROOT / "run_benchmark.py"
VERSION = (BENCHMARK_ROOT / "VERSION").read_text().strip()

SIX_TASKS = [
    "go-bugfix",
    "js-bugfix",
    "py-bugfix",
    "py-feature",
    "py-ledger",
    "py-shop",
]

__all__ = [
    "BENCHMARK_ROOT",
    "REPO_ROOT",
    "CONFIGS_DIR",
    "TASKS_DIR",
    "EXAMPLE_R7",
    "RAW_R7",
    "METADATA_R7",
    "RUN_SCRIPT",
    "VERSION",
    "SIX_TASKS",
    "agents",
    "config",
    "fixtures",
    "pricing",
    "providers",
    "reporting",
    "util",
    "cli_env",
    "load_config_data",
    "run_cli",
    "write_config",
]


def cli_env(**updates: Optional[str]) -> dict[str, str]:
    """A provider-scrubbed environment for CLI subprocesses.

    No real credential should be visible to a child, and the custom preset's
    ``BENCHMARK_API_KEY`` is removed too because it is not part of
    ``PROVIDER_ENV_VARS`` (the custom key is only known from the config).
    """
    env = providers.scrub_provider_env()
    env["NO_COLOR"] = "1"
    env.pop("BENCHMARK_API_KEY", None)
    for key, value in updates.items():
        if value is None:
            env.pop(key, None)
        else:
            env[key] = value
    return env


def write_config(directory: pathlib.Path, data: dict[str, Any], name: str = "config.json") -> pathlib.Path:
    """Write a temporary JSON config and return its path."""
    path = pathlib.Path(directory) / name
    path.write_text(json.dumps(data, indent=2) + "\n")
    return path


def load_config_data(
    directory: pathlib.Path,
    data: dict[str, Any],
    name: str = "config.json",
) -> config.BenchmarkConfig:
    """Write and load a temporary config in one step."""
    return config.load_config(str(write_config(directory, data, name)))


def run_cli(
    *args: str,
    env: Optional[dict[str, str]] = None,
    cwd: Optional[pathlib.Path] = None,
    timeout: int = 120,
) -> subprocess.CompletedProcess[str]:
    """Run ``run_benchmark.py`` with a provider-scrubbed environment."""
    return subprocess.run(
        [sys.executable, str(RUN_SCRIPT), *args],
        cwd=str(cwd or BENCHMARK_ROOT),
        env=env if env is not None else cli_env(),
        capture_output=True,
        text=True,
        timeout=timeout,
    )
