"""Load, validate and normalise benchmark JSON configuration.

A configuration is intentionally explicit rather than a pile of environment
variables.  The same file records which provider, model, price table and task
set produced a result, which is what makes a published number auditable months
later.

Presets live in ``configs/`` and are selected by name::

    --config ollama-cloud
    --config configs/my-endpoint.json

All keys are validated here so an unknown field is a startup error rather than
a silent no-op halfway through a paid run.
"""

from __future__ import annotations

import os
import pathlib
import re
from dataclasses import dataclass, field
from typing import Any, Optional

from . import util

PACKAGE_ROOT = pathlib.Path(__file__).resolve().parent.parent
CONFIGS_DIR = PACKAGE_ROOT / "configs"
TASKS_DIR = PACKAGE_ROOT / "tasks"
DEFAULT_PRICING = PACKAGE_ROOT / "pricing.json"

# These names are also used by the opencode provider map.  Adding a provider is
# a small, deliberate change: add a preset in providers.py and a sample config.
PROVIDER_KINDS = ("ollama-cloud", "openrouter", "anthropic", "openai", "custom")
AGENT_NAMES = ("ogcode", "opencode")
_ENV_NAME = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*$")
_PROVIDER_ID = re.compile(r"^[A-Za-z0-9_.-]+$")


@dataclass
class ProviderConfig:
    """Where a model is served and which environment variable holds its key."""

    kind: str
    base_url: Optional[str] = None
    api_key_env: Optional[str] = None
    name: Optional[str] = None
    opencode_provider: Optional[str] = None


@dataclass
class ModelConfig:
    """The canonical model id plus optional per-model pricing in USD / 1M tokens."""

    id: str
    pricing: Optional[dict[str, float]] = None


@dataclass
class AgentConfig:
    """Per-agent model override and extra CLI arguments (before the prompt)."""

    model: Optional[str] = None
    extra_args: list[str] = field(default_factory=list)


@dataclass
class RunConfig:
    """Sampling and process controls that do not change what is being measured."""

    reps: int = 5
    tasks: list[str] = field(default_factory=list)  # empty = every task
    agents: list[str] = field(default_factory=lambda: list(AGENT_NAMES))
    jobs: int = 1
    timeout_s: int = 600
    verify_timeout_s: int = 300
    index_timeout_s: int = 900
    output_dir: str = "results"


@dataclass
class BenchmarkConfig:
    """A complete, validated benchmark definition."""

    name: str
    provider: ProviderConfig
    model: ModelConfig
    agents: dict[str, AgentConfig]
    run: RunConfig
    source: pathlib.Path
    description: str = ""

    def agent_model(self, agent: str) -> str:
        """Return the model string passed to one agent.

        An explicit ``agents.<name>.model`` always wins, because some providers
        use different ids for the same weights.  Otherwise ogcode receives the
        raw model id and opencode receives ``provider/model``, which is its
        required format.
        """
        from . import providers

        configured = self.agents[agent].model
        if configured:
            return configured
        resolved = providers.resolve_provider(self.provider)
        if agent == "ogcode":
            return self.model.id
        return providers.opencode_model_string(resolved, self.model.id)

    def to_json(self) -> dict[str, Any]:
        """A serialisable form for ``metadata.json`` (never includes secrets)."""
        return {
            "name": self.name,
            "description": self.description,
            "source": str(self.source),
            "provider": {
                "kind": self.provider.kind,
                "base_url": self.provider.base_url,
                "api_key_env": self.provider.api_key_env,
                "name": self.provider.name,
                "opencode_provider": self.provider.opencode_provider,
            },
            "model": {"id": self.model.id, "pricing": self.model.pricing},
            "agents": {
                name: {"model": cfg.model, "extra_args": list(cfg.extra_args)}
                for name, cfg in self.agents.items()
            },
            "run": {
                "reps": self.run.reps,
                "tasks": list(self.run.tasks),
                "agents": list(self.run.agents),
                "jobs": self.run.jobs,
                "timeout_s": self.run.timeout_s,
                "verify_timeout_s": self.run.verify_timeout_s,
                "index_timeout_s": self.run.index_timeout_s,
                "output_dir": self.run.output_dir,
            },
        }


def available_presets() -> list[str]:
    """Names a user can pass to ``--config`` without a path."""
    return sorted(path.stem for path in CONFIGS_DIR.glob("*.json"))


def resolve_config_path(selector: str) -> pathlib.Path:
    """Turn ``ollama-cloud`` or a path into an existing config file."""
    candidate = pathlib.Path(selector).expanduser()
    if candidate.is_file():
        return candidate.resolve()
    # A bare name is a preset.  A path-looking selector that does not exist is
    # an error, not a reason to guess and run a different config.
    if "/" in selector or selector.endswith(".json"):
        raise SystemExit(f"config file not found: {selector}")
    preset = CONFIGS_DIR / f"{selector}.json"
    if preset.is_file():
        return preset
    raise SystemExit(
        f"unknown config {selector!r}; available presets: {', '.join(available_presets())}"
    )


def _expect(condition: bool, message: str) -> None:
    if not condition:
        raise SystemExit(message)


def _as_mapping(value: Any, where: str) -> dict[str, Any]:
    _expect(isinstance(value, dict), f"{where} must be a JSON object")
    return value


def _positive_int(value: Any, where: str) -> int:
    _expect(isinstance(value, int) and not isinstance(value, bool) and value > 0, f"{where} must be a positive integer")
    return value


def _string_list(value: Any, where: str) -> list[str]:
    _expect(isinstance(value, list) and all(isinstance(item, str) and item.strip() for item in value), f"{where} must be a list of non-empty strings")
    return [item.strip() for item in value]


def _validate_price(price: Any, where: str) -> dict[str, float]:
    price = _as_mapping(price, where)
    allowed = {"input", "output", "cache_read", "cache_write"}
    unknown = sorted(set(price) - allowed)
    _expect(not unknown, f"{where} has unknown keys: {', '.join(unknown)}")
    for key in ("input", "output"):
        _expect(key in price, f"{where} must include {key!r}")
    for key, value in price.items():
        _expect(isinstance(value, (int, float)) and not isinstance(value, bool) and value >= 0, f"{where}.{key} must be a non-negative number")
    return {key: float(value) for key, value in price.items()}


def _parse_provider(raw: Any) -> ProviderConfig:
    data = _as_mapping(raw, "provider")
    allowed = {"kind", "base_url", "api_key_env", "name", "opencode_provider"}
    unknown = sorted(set(data) - allowed)
    _expect(not unknown, f"provider has unknown keys: {', '.join(unknown)}")

    kind = data.get("kind")
    _expect(kind in PROVIDER_KINDS, f"provider.kind must be one of: {', '.join(PROVIDER_KINDS)}")

    base_url = data.get("base_url")
    if base_url is not None:
        _expect(isinstance(base_url, str) and base_url.startswith(("http://", "https://")), "provider.base_url must start with http:// or https://")
        base_url = base_url.rstrip("/")

    api_key_env = data.get("api_key_env")
    if api_key_env is not None:
        _expect(isinstance(api_key_env, str) and bool(_ENV_NAME.match(api_key_env)), "provider.api_key_env must be an environment variable name")

    name = data.get("name")
    if name is not None:
        _expect(isinstance(name, str) and name.strip(), "provider.name must be a non-empty string")

    opencode_provider = data.get("opencode_provider")
    if opencode_provider is not None:
        _expect(isinstance(opencode_provider, str) and bool(_PROVIDER_ID.match(opencode_provider)), "provider.opencode_provider must be a provider id")

    if kind == "custom":
        _expect(bool(base_url), "provider.base_url is required for a custom endpoint")
        _expect(bool(api_key_env), "provider.api_key_env is required for a custom endpoint")

    return ProviderConfig(
        kind=kind,
        base_url=base_url,
        api_key_env=api_key_env,
        name=name.strip() if name else None,
        opencode_provider=opencode_provider,
    )


def _parse_model(raw: Any) -> ModelConfig:
    data = _as_mapping(raw, "model")
    unknown = sorted(set(data) - {"id", "pricing"})
    _expect(not unknown, f"model has unknown keys: {', '.join(unknown)}")
    model_id = data.get("id")
    _expect(isinstance(model_id, str) and model_id.strip(), "model.id must be a non-empty string")
    pricing = data.get("pricing")
    return ModelConfig(
        id=model_id.strip(),
        pricing=_validate_price(pricing, "model.pricing") if pricing is not None else None,
    )


def _parse_agents(raw: Any) -> dict[str, AgentConfig]:
    data = _as_mapping(raw, "agents")
    unknown = sorted(set(data) - set(AGENT_NAMES))
    _expect(not unknown, f"agents has unknown names: {', '.join(unknown)}")
    agents: dict[str, AgentConfig] = {}
    for name in AGENT_NAMES:
        entry = data.get(name, {})
        entry = _as_mapping(entry, f"agents.{name}")
        extra = sorted(set(entry) - {"model", "extra_args"})
        _expect(not extra, f"agents.{name} has unknown keys: {', '.join(extra)}")
        model = entry.get("model")
        if model is not None:
            _expect(isinstance(model, str) and model.strip(), f"agents.{name}.model must be a non-empty string")
            model = model.strip()
        extra_args = entry.get("extra_args", [])
        _expect(isinstance(extra_args, list) and all(isinstance(item, str) for item in extra_args), f"agents.{name}.extra_args must be a list of strings")
        agents[name] = AgentConfig(model=model, extra_args=list(extra_args))
    return agents


def _parse_run(raw: Any) -> RunConfig:
    data = _as_mapping(raw, "run")
    allowed = {"reps", "tasks", "agents", "jobs", "timeout_s", "verify_timeout_s", "index_timeout_s", "output_dir"}
    unknown = sorted(set(data) - allowed)
    _expect(not unknown, f"run has unknown keys: {', '.join(unknown)}")

    reps = _positive_int(data.get("reps", 5), "run.reps")
    jobs = _positive_int(data.get("jobs", 1), "run.jobs")
    timeout_s = _positive_int(data.get("timeout_s", 600), "run.timeout_s")
    verify_timeout_s = _positive_int(data.get("verify_timeout_s", 300), "run.verify_timeout_s")
    index_timeout_s = _positive_int(data.get("index_timeout_s", 900), "run.index_timeout_s")

    tasks_raw = data.get("tasks", "all")
    if isinstance(tasks_raw, str):
        _expect(tasks_raw.strip() in ("", "all"), "run.tasks must be 'all' or a list of task names")
        tasks: list[str] = []
    else:
        tasks = _string_list(tasks_raw, "run.tasks")
        _expect(bool(tasks), "run.tasks must contain at least one task")

    agents_raw = data.get("agents", list(AGENT_NAMES))
    agents = _string_list(agents_raw, "run.agents")
    _expect(bool(agents), "run.agents must contain at least one agent")
    unknown_agents = sorted(set(agents) - set(AGENT_NAMES))
    _expect(not unknown_agents, f"run.agents has unknown names: {', '.join(unknown_agents)}")
    # A duplicate would otherwise run the same pair twice and distort the
    # repetition count without any indication in the report.
    agents = list(dict.fromkeys(agents))

    output_dir = data.get("output_dir", "results")
    _expect(isinstance(output_dir, str) and output_dir.strip(), "run.output_dir must be a non-empty string")

    return RunConfig(
        reps=reps,
        tasks=tasks,
        agents=agents,
        jobs=jobs,
        timeout_s=timeout_s,
        verify_timeout_s=verify_timeout_s,
        index_timeout_s=index_timeout_s,
        output_dir=output_dir.strip(),
    )


def load_config(selector: str) -> BenchmarkConfig:
    """Load a preset or a path and validate every field."""
    path = resolve_config_path(selector)
    data = _as_mapping(util.read_json(path), f"config {path}")
    allowed = {"$schema", "name", "description", "provider", "model", "agents", "run"}
    unknown = sorted(set(data) - allowed)
    _expect(not unknown, f"{path} has unknown keys: {', '.join(unknown)}")

    name = data.get("name", path.stem)
    _expect(isinstance(name, str) and name.strip(), "name must be a non-empty string")
    description = data.get("description", "")
    _expect(isinstance(description, str), "description must be a string")
    _expect("provider" in data, f"{path}: provider is required")
    _expect("model" in data, f"{path}: model is required")

    return BenchmarkConfig(
        name=name.strip(),
        description=description,
        provider=_parse_provider(data["provider"]),
        model=_parse_model(data["model"]),
        agents=_parse_agents(data.get("agents", {})),
        run=_parse_run(data.get("run", {})),
        source=path,
    )


def resolve_output_dir(config: BenchmarkConfig, cli_out: Optional[str]) -> pathlib.Path:
    """CLI output wins; otherwise a relative config path is package-relative."""
    if cli_out:
        return pathlib.Path(cli_out).expanduser().resolve()
    configured = pathlib.Path(config.run.output_dir).expanduser()
    if configured.is_absolute():
        return configured
    return (PACKAGE_ROOT / configured).resolve()


def task_names(config: BenchmarkConfig) -> list[str]:
    """Expand ``run.tasks`` against the tasks directory."""
    discovered = sorted(
        path.name for path in TASKS_DIR.iterdir() if path.is_dir() and (path / "task.md").is_file()
    )
    if not config.run.tasks:
        return discovered
    missing = [task for task in config.run.tasks if task not in discovered]
    _expect(not missing, f"unknown task(s): {', '.join(missing)}; available: {', '.join(discovered)}")
    return list(config.run.tasks)


def resolve_pricing(
    config: BenchmarkConfig,
    pricing_path: Optional[pathlib.Path] = None,
) -> tuple[Optional[dict[str, float]], str]:
    """Resolve the shared price table, returning ``(price, source)``.

    A price embedded in the chosen config wins over the package table, because
    a provider-specific config is the more local statement of intent.  A model
    without a price is allowed in ``--dry-run`` but rejected by a real run in
    the CLI, where spending money without a price would be misleading.
    """
    if config.model.pricing is not None:
        return config.model.pricing, "config"
    path = pricing_path or DEFAULT_PRICING
    table = util.read_json(path)
    models = table.get("models") if isinstance(table, dict) else None
    if isinstance(models, dict) and isinstance(models.get(config.model.id), dict):
        return _validate_price(models[config.model.id], f"{path}: models.{config.model.id}"), str(path)
    return None, "missing"


def config_summary(config: BenchmarkConfig) -> str:
    """One-line description used by ``--list-configs``."""
    return f"{config.name:28s} {config.provider.kind:14s} model={config.model.id}"
