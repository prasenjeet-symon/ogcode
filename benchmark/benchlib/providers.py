"""Provider resolution for both agents.

The benchmark supports five kinds of provider:

``ollama-cloud``, ``openrouter``, ``anthropic``, ``openai`` and ``custom``.
The first four are first-party presets; ``custom`` is any OpenAI-compatible
chat-completions endpoint.

ogcode has one provider slot per wire protocol.  To keep provider selection
deterministic we register exactly one slot for a run:

* Anthropic models use ``ANTHROPIC_API_KEY`` (and optionally
  ``ANTHROPIC_BASE_URL``).
* Everything else uses the OpenAI-compatible slot:
  ``OPENAI_API_KEY`` plus ``OPENAI_BASE_URL`` pointing at the selected
  endpoint (Ollama Cloud, OpenRouter, OpenAI, or a custom base URL).

opencode has its own built-in provider ids (``ollama-cloud``, ``openrouter``,
``anthropic``, ``openai``).  For a custom endpoint, a temporary opencode
config declares an OpenAI-compatible provider and interpolates the key from
the environment, so no secret is ever written to disk.
"""

from __future__ import annotations

import os
import re
import urllib.parse
from dataclasses import dataclass
from typing import Any, Optional

from .config import BenchmarkConfig, ProviderConfig


@dataclass(frozen=True)
class ProviderPreset:
    """Static knowledge about one provider family."""

    kind: str
    api_key_env: str
    base_url: Optional[str]
    ogcode_provider: str
    opencode_provider: str
    display_name: str
    opencode_native_env: Optional[str] = None


PRESETS: dict[str, ProviderPreset] = {
    "ollama-cloud": ProviderPreset(
        kind="ollama-cloud",
        api_key_env="OLLAMA_API_KEY",
        base_url="https://ollama.com/v1",
        ogcode_provider="openai",
        opencode_provider="ollama-cloud",
        display_name="Ollama Cloud",
        opencode_native_env="OLLAMA_API_KEY",
    ),
    "openrouter": ProviderPreset(
        kind="openrouter",
        api_key_env="OPENROUTER_API_KEY",
        base_url="https://openrouter.ai/api/v1",
        ogcode_provider="openai",
        opencode_provider="openrouter",
        display_name="OpenRouter",
        opencode_native_env="OPENROUTER_API_KEY",
    ),
    "anthropic": ProviderPreset(
        kind="anthropic",
        api_key_env="ANTHROPIC_API_KEY",
        base_url=None,
        ogcode_provider="anthropic",
        opencode_provider="anthropic",
        display_name="Anthropic",
        opencode_native_env="ANTHROPIC_API_KEY",
    ),
    "openai": ProviderPreset(
        kind="openai",
        api_key_env="OPENAI_API_KEY",
        base_url="https://api.openai.com/v1",
        ogcode_provider="openai",
        opencode_provider="openai",
        display_name="OpenAI",
        opencode_native_env="OPENAI_API_KEY",
    ),
    "custom": ProviderPreset(
        kind="custom",
        api_key_env="BENCHMARK_API_KEY",
        base_url=None,
        ogcode_provider="openai",
        opencode_provider="benchmark",
        display_name="Custom OpenAI-compatible endpoint",
        opencode_native_env=None,
    ),
}


# Every provider variable is removed before the selected one is re-added.  A
# developer machine commonly has several keys exported; without this scrub
# ogcode's provider priority would silently choose a different service from
# the one named in the config.
PROVIDER_ENV_VARS = (
    "ANTHROPIC_API_KEY",
    "ANTHROPIC_AUTH_TOKEN",
    "ANTHROPIC_BASE_URL",
    "OPENAI_API_KEY",
    "OPENAI_BASE_URL",
    "OPENAI_MODEL",
    "OPENAI_ORG_ID",
    "OPENAI_PROJECT_ID",
    "OPENROUTER_API_KEY",
    "OPENROUTER_MODEL",
    "OPENROUTER_REFERER",
    "OPENROUTER_TITLE",
    "OLLAMA_API_KEY",
    "OLLAMA_BASE_URL",
    "OLLAMA_MODEL",
    "OLLAMA_HOST",
    "GEMINI_API_KEY",
    "GOOGLE_API_KEY",
    "GROQ_API_KEY",
    "DEEPSEEK_API_KEY",
    "MISTRAL_API_KEY",
    "XAI_API_KEY",
    "TOGETHER_API_KEY",
    "FIREWORKS_API_KEY",
    "CEREBRAS_API_KEY",
    "OPENCODE_CONFIG",
)


@dataclass(frozen=True)
class ResolvedProvider:
    """The effective provider after preset defaults and config overrides."""

    kind: str
    api_key_env: str
    base_url: Optional[str]
    display_name: str
    ogcode_provider: str
    opencode_provider: str
    uses_custom_opencode: bool


def resolve_provider(config: ProviderConfig) -> ResolvedProvider:
    """Apply a config's overrides to its provider preset."""
    preset = PRESETS[config.kind]
    base_url = config.base_url or preset.base_url
    api_key_env = config.api_key_env or preset.api_key_env
    opencode_provider = config.opencode_provider or preset.opencode_provider

    # A custom endpoint, or a non-default base URL on an otherwise built-in
    # provider, cannot rely on opencode's built-in endpoint selection.  Declare
    # it explicitly in the temporary config instead.
    base_overridden = bool(
        config.base_url
        and preset.base_url
        and config.base_url.rstrip("/") != preset.base_url.rstrip("/")
    )
    if config.kind == "anthropic":
        if opencode_provider != "anthropic":
            raise SystemExit("provider.opencode_provider is not supported for Anthropic")
        uses_custom_opencode = False
    else:
        uses_custom_opencode = (
            config.kind == "custom"
            or config.opencode_provider is not None
            or base_overridden
        )

    if config.kind == "custom" and not base_url:
        raise SystemExit("a custom provider needs provider.base_url")
    if config.kind == "custom" and not api_key_env:
        raise SystemExit("a custom provider needs provider.api_key_env")

    return ResolvedProvider(
        kind=config.kind,
        api_key_env=api_key_env,
        base_url=base_url.rstrip("/") if base_url else None,
        display_name=config.name or preset.display_name,
        ogcode_provider="anthropic" if config.kind == "anthropic" else "openai",
        opencode_provider=opencode_provider,
        uses_custom_opencode=uses_custom_opencode,
    )


def opencode_model_string(provider: ResolvedProvider, model_id: str) -> str:
    """opencode requires ``provider/model``; the first slash is the split."""
    return f"{provider.opencode_provider}/{model_id}"


def scrub_provider_env(env: Optional[dict[str, str]] = None) -> dict[str, str]:
    """Start a child environment with every known provider credential removed."""
    child = dict(os.environ if env is None else env)
    for name in PROVIDER_ENV_VARS:
        child.pop(name, None)
    return child


def provider_env(
    provider: ResolvedProvider,
    api_key: str,
    base_env: Optional[dict[str, str]] = None,
) -> dict[str, str]:
    """Build the child environment for one selected provider.

    The key is supplied to the process, never written into a config file or a
    command line.  The extra opencode variable is needed for its built-in
    providers: opencode reads ``OLLAMA_API_KEY`` for Ollama Cloud and
    ``OPENROUTER_API_KEY`` for OpenRouter even when ogcode is routed through
    the OpenAI-compatible slot.
    """
    env = scrub_provider_env(base_env)
    env["NO_COLOR"] = "1"

    if provider.ogcode_provider == "anthropic":
        env["ANTHROPIC_API_KEY"] = api_key
        if provider.base_url:
            env["ANTHROPIC_BASE_URL"] = provider.base_url
    else:
        env["OPENAI_API_KEY"] = api_key
        if provider.base_url:
            env["OPENAI_BASE_URL"] = provider.base_url

    if not provider.uses_custom_opencode:
        # The built-in opencode provider may use a different env variable name
        # from the OpenAI-compatible slot ogcode reads.
        preset = PRESETS[provider.kind]
        if preset.opencode_native_env and preset.opencode_native_env != provider.api_key_env:
            env[preset.opencode_native_env] = api_key
        elif provider.kind in ("ollama-cloud", "openrouter"):
            env[preset.opencode_native_env or provider.api_key_env] = api_key
    else:
        # The generated opencode config interpolates {env:<name>}; make sure the
        # variable exists under that name.
        env[provider.api_key_env] = api_key

    return env


def opencode_custom_config(provider: ResolvedProvider, model_id: str) -> dict[str, Any]:
    """Temporary opencode config for a custom OpenAI-compatible endpoint.

    opencode's v1 schema uses a singular top-level ``provider`` map.  The API
    key is written as an interpolation token, so the file on disk contains no
    secret and can be safely left in a temp directory until the run ends.
    """
    provider_id = provider.opencode_provider
    return {
        "$schema": "https://opencode.ai/config.json",
        "mcp": {},
        "provider": {
            provider_id: {
                "npm": "@ai-sdk/openai-compatible",
                "name": provider.display_name,
                "options": {
                    "baseURL": provider.base_url,
                    "apiKey": "{env:" + provider.api_key_env + "}",
                },
                "models": {model_id: {"name": model_id}},
            }
        },
    }


def expected_ogcode_route(provider: ResolvedProvider) -> str:
    """The provider id ogcode should report for this run."""
    return provider.ogcode_provider


def provider_route_from_log(text: str, provider: ResolvedProvider) -> dict[str, Any]:
    """Extract and check ogcode's actual provider/URL from its run log.

    This is a post-run audit, not a gate: logging formats can change between
    releases, and a missing line should not invalidate an otherwise measured
    run.  When lines are present they are compared with the selected config, so
    a harness that accidentally routes to a different service is visible in the
    report rather than silently mixed into the sample.
    """
    reported_provider = None
    urls: list[str] = []
    for line in text.splitlines():
        if "resolved provider for model" in line:
            match = re.search(r"provider=([A-Za-z0-9_.-]+)", line)
            if match:
                reported_provider = match.group(1)
        if "streaming chat request" in line:
            match = re.search(r"url=(\S+)", line)
            if match:
                urls.append(match.group(1).strip("\"'"))

    if reported_provider is None and not urls:
        return {
            "ogcode_provider": None,
            "ogcode_url": None,
            "provider_route_ok": None,
        }

    provider_ok = reported_provider == expected_ogcode_route(provider)
    url = urls[-1] if urls else None
    url_ok: Optional[bool] = None
    if url and provider.base_url:
        url_host = urllib.parse.urlparse(url).hostname
        base_host = urllib.parse.urlparse(provider.base_url).hostname
        url_ok = bool(url_host and base_host and url_host == base_host)
    route_ok: Optional[bool] = provider_ok
    if url_ok is False:
        route_ok = False
    elif url_ok is None and not provider_ok:
        route_ok = False

    return {
        "ogcode_provider": reported_provider,
        "ogcode_url": url,
        "provider_route_ok": route_ok,
    }


def redacted_env(provider: ResolvedProvider, base_env: Optional[dict[str, str]] = None) -> str:
    """Human-readable provider environment for ``--dry-run`` (values hidden)."""
    env = os.environ if base_env is None else base_env
    status = "<set>" if env.get(provider.api_key_env) else "<missing>"
    lines = [
        f"api key          {provider.api_key_env}={status}",
        f"ogcode slot      {provider.ogcode_provider}",
        f"opencode provider {provider.opencode_provider}"
        + (" (generated config)" if provider.uses_custom_opencode else " (built-in)"),
    ]
    if provider.base_url:
        lines.insert(1, f"base URL         {provider.base_url}")
    return "\n".join(lines)
