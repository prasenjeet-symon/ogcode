"""Harbor adapter that runs ogcode as a coding agent (e.g. on Terminal-Bench).

Sibling of `pier_ogcode.py`. Pier is a Harbor fork, and the two interfaces have
diverged in three places, which is why this is a separate file rather than a
shared base:

  * install -- Pier takes a declarative ``install_spec() -> AgentInstallSpec``;
    Harbor takes an imperative ``async def install(environment)``.
  * network -- Pier adds per-agent ``network_allowlist()``; Harbor has no such
    hook. Terminal-Bench tasks set ``allow_internet = true``, so nothing to
    declare.
  * metrics -- Harbor's AgentContext has no ``n_agent_steps``, so the turn
    count goes in ``metadata``.

Harbor resolves custom agents by import path, so this file only needs to be on
PYTHONPATH:

    harbor dataset download terminal-bench/terminal-bench-2-1
    PYTHONPATH=bench harbor run -p terminal-bench-2-1 \
      --agent-import-path harbor_ogcode:OgCode \
      --model ollama/glm-5.3-flash:cloud --force-build

``--force-build`` matters on arm64: every task pins a prebuilt amd64 image in
task.toml, but the Dockerfiles all build from multi-arch bases, so rebuilding
locally yields a native environment instead of an emulated one.
"""

from __future__ import annotations

import json
import shlex
from typing import Any
from urllib.parse import urlparse, urlunparse

from harbor.agents.installed.base import BaseInstalledAgent, CliFlag
from harbor.environments.base import BaseEnvironment
from harbor.models.agent.context import AgentContext

DEFAULT_VERSION = "0.30.0"

RELEASE_URL = (
    "https://github.com/prasenjeet-symon/ogcode/releases/download"
    "/v{version}/ogcode_{version}_linux_${{arch}}.tar.gz"
)

# ogcode has exactly four provider slots. Anything reached through a gateway
# (Gemini, DeepSeek, Groq, ...) goes through the openai slot with
# OPENAI_BASE_URL pointed at it.
_PROVIDER_SLOTS: dict[str, list[str]] = {
    "anthropic": ["ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL"],
    "openai": ["OPENAI_API_KEY", "OPENAI_BASE_URL"],
    "openrouter": ["OPENROUTER_API_KEY"],
    "ollama": ["OLLAMA_API_KEY", "OLLAMA_BASE_URL"],
}

# Docker resolves "localhost" to the container itself, so a base URL naming a
# loopback address reaches nothing once the agent is sandboxed.
_CONTAINER_HOST_GATEWAY = "host.docker.internal"
_LOOPBACK_HOSTS = {"localhost", "127.0.0.1", "0.0.0.0", "::1"}


def _reachable_from_container(url: str) -> str:
    """Point a loopback base URL at the host gateway; leave others alone."""
    parsed = urlparse(url)
    if (parsed.hostname or "").lower() not in _LOOPBACK_HOSTS:
        return url
    netloc = _CONTAINER_HOST_GATEWAY
    if parsed.port:
        netloc = f"{netloc}:{parsed.port}"
    return urlunparse(parsed._replace(netloc=netloc))


class OgCode(BaseInstalledAgent):
    """Runs `ogcode run` once per task and reads back its JSON summary."""

    SUPPORTS_ATIF: bool = False

    _OUTPUT_FILENAME = "ogcode.json"
    _LOG_DIR = "/logs/agent"
    _WORKDIR = "/app"

    CLI_FLAGS = [
        CliFlag("max_turns", cli="--max-turns", type="int", default=250),
        CliFlag("agent", cli="--agent", type="enum",
                choices=["build", "plan"], default="build"),
    ]

    def __init__(self, *args, version: str | None = None,
                 binary_url: str | None = None, **kwargs):
        super().__init__(*args, version=version or DEFAULT_VERSION, **kwargs)
        # Points the installer at a tarball other than the published release --
        # a branch build, or a rebuild of a release whose binary does not run on
        # the task image.
        self._binary_url = binary_url

    @staticmethod
    def name() -> str:
        return "ogcode"

    def get_version_command(self) -> str | None:
        return "ogcode version"

    def parse_version(self, stdout: str) -> str:
        # First line is "ogcode <version> (linux/arm64)".
        first = stdout.strip().splitlines()[0] if stdout.strip() else ""
        parts = first.split()
        return parts[1] if len(parts) >= 2 else stdout.strip()

    # ---------------------------------------------------------------- install

    async def install(self, environment: BaseEnvironment) -> None:
        await self.ensure_system_dependencies(environment, ("curl",))
        url = _reachable_from_container(
            self._binary_url or RELEASE_URL.format(version=self._version)
        )
        # Root because the binary lands in /usr/local/bin, on PATH for every
        # user the task might run commands as. Arch is detected so the same
        # adapter works on amd64 images and on natively-built arm64 ones.
        await self.exec_as_root(
            environment,
            command=(
                "set -eu; "
                'arch="$(uname -m)"; '
                'case "$arch" in '
                "x86_64|amd64) arch=x86_64 ;; "
                "aarch64|arm64) arch=arm64 ;; "
                '*) echo "ogcode: unsupported arch $arch" >&2; exit 1 ;; '
                "esac; "
                'tmp="$(mktemp -d)"; '
                f'curl -fsSL "{url}" -o "$tmp/ogcode.tar.gz"; '
                'tar -xzf "$tmp/ogcode.tar.gz" -C "$tmp"; '
                'install -m 0755 "$tmp/ogcode" /usr/local/bin/ogcode; '
                'rm -rf "$tmp"; '
                "ogcode version"
            ),
        )

    # -------------------------------------------------------------------- run

    def _provider_slot(self) -> list[str]:
        if not self.model_name or "/" not in self.model_name:
            raise ValueError(
                "Model name must be 'provider/model', e.g. anthropic/claude-opus-4-8"
            )
        provider, _ = self.model_name.split("/", 1)
        keys = _PROVIDER_SLOTS.get(provider)
        if keys is None:
            raise ValueError(
                f"ogcode has no '{provider}' provider slot. Its slots are "
                f"{', '.join(sorted(_PROVIDER_SLOTS))}. Reach anything else "
                "through the openai slot by setting OPENAI_BASE_URL to the "
                "gateway and naming the model as openai/<model>."
            )
        return keys

    async def run(self, instruction: str, environment: BaseEnvironment,
                  context: AgentContext) -> None:
        keys = self._provider_slot()
        _, bare_model = self.model_name.split("/", 1)

        env: dict[str, str] = {}
        for key in keys:
            if value := self._get_env(key):
                env[key] = (_reachable_from_container(value)
                            if key.endswith("_BASE_URL") else value)

        # ogcode merges an ogcode.json found in the working directory or any
        # parent. Writing an explicit empty one keeps the run hermetic:
        # `ogcode run` connects MCP servers synchronously before its first step,
        # and a sealed sandbox would make that a stall rather than an error.
        empty_config = shlex.quote(json.dumps({"mcp": {}, "skills": {}}))
        await self.exec_as_agent(
            environment,
            command=(f"mkdir -p {self._LOG_DIR} && "
                     f"echo {empty_config} > {self._WORKDIR}/ogcode.json"),
            env=env,
        )

        out = f"{self._LOG_DIR}/{self._OUTPUT_FILENAME}"
        await self.exec_as_agent(
            environment,
            # stdin is /dev/null on purpose: `ogcode run` appends piped stdin to
            # the prompt, and an open-but-empty pipe would block it forever.
            # stderr stays off stdout so the JSON document parses.
            command=(
                f"cd {self._WORKDIR} && "
                f"ogcode run --output-format json --model {shlex.quote(bare_model)} "
                f"{self.build_cli_flags()} -- {shlex.quote(self.render_instruction(instruction))} "
                f"</dev/null >{out} 2>{self._LOG_DIR}/ogcode.stderr.log"
            ),
            env=env,
        )

    # ------------------------------------------------------------ post-mortem

    def populate_context_post_run(self, context: AgentContext) -> None:
        path = self.logs_dir / self._OUTPUT_FILENAME
        if not path.exists():
            return
        try:
            raw = path.read_text()
        except OSError:
            self.logger.exception("Failed to read ogcode run summary at %s", path)
            return
        # ogcode releases before the stderr-logging fix print one slog line to
        # stdout ahead of the JSON, so skip to the opening brace rather than
        # lose every metric on an older binary.
        start = raw.find("{")
        if start == -1:
            self.logger.warning("No JSON document in ogcode summary at %s", path)
            return
        try:
            summary: dict[str, Any] = json.loads(raw[start:])
        except json.JSONDecodeError:
            self.logger.exception("Malformed ogcode run summary at %s", path)
            return

        tokens = summary.get("tokens") or {}
        cache_read = tokens.get("cache_read", 0) or 0
        cache_write = tokens.get("cache_write", 0) or 0

        # n_input_tokens is documented as including cache, so fold both counters
        # in; n_cache_tokens reports the read side, the half that displaces
        # fresh input.
        context.n_input_tokens = (tokens.get("input", 0) or 0) + cache_read + cache_write
        context.n_cache_tokens = cache_read
        context.n_output_tokens = tokens.get("output", 0) or 0
        # None rather than 0.0 when ogcode could not price the model -- a fake
        # zero would drag any aggregate cost figure down.
        context.cost_usd = summary.get("cost_usd")
        # Harbor's AgentContext has no n_agent_steps field, unlike Pier's.
        context.metadata = {
            "num_turns": summary.get("num_turns"),
            "finish": summary.get("finish"),
            "session_id": summary.get("session_id"),
            "model": summary.get("model"),
            "reasoning_tokens": tokens.get("reasoning", 0) or 0,
        }
