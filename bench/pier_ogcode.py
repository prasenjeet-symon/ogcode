"""Pier adapter that runs ogcode as a coding agent on DeepSWE tasks.

Pier resolves custom agents through ``AgentFactory.create_agent_from_import_path``,
so this file does not need to live inside the Pier package or be registered
anywhere. Put it on ``PYTHONPATH`` and point Pier at it:

    pier run -p deep-swe/tasks \
      --agent-import-path pier_ogcode:OgCode \
      --model anthropic/claude-opus-4-8

The agent side of a DeepSWE task runs with ``network_mode = "no-network"``,
so every host this adapter needs -- the release download and the inference
endpoint -- has to be declared in :meth:`network_allowlist` or the run cannot
reach it.
"""

from __future__ import annotations

import json
import shlex
from typing import Any
from urllib.parse import urlparse, urlunparse

from pier.agents.installed.base import BaseInstalledAgent, CliFlag
from pier.agents.network import allowlist_from_urls
from pier.environments.base import BaseEnvironment
from pier.models.agent.context import AgentContext
from pier.models.agent.install import AgentInstallSpec, InstallStep
from pier.models.agent.network import NetworkAllowlist

# Pinned rather than resolved to "latest": a benchmark number is only
# comparable if the agent that produced it is identifiable. Override per run
# with ``--agent-kwarg version=0.31.0``.
DEFAULT_VERSION = "0.30.0"

RELEASE_URL = (
    "https://github.com/prasenjeet-symon/ogcode/releases/download"
    "/v{version}/ogcode_{version}_linux_${{arch}}.tar.gz"
)

# Hosts the install step needs. GitHub serves release metadata from the first
# and redirects the actual tarball to the second.
_INSTALL_DOMAINS = ["github.com", "objects.githubusercontent.com"]

# ogcode has exactly four provider slots. Anything reached through a gateway
# (Gemini, DeepSeek, Groq, ...) goes through the openai slot with
# OPENAI_BASE_URL pointed at it, so there is no slot to guess for an
# unrecognised prefix -- better to say so than to route silently.
_PROVIDER_SLOTS: dict[str, dict[str, Any]] = {
    "anthropic": {
        "keys": ["ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL"],
        "domains": ["api.anthropic.com"],
    },
    "openai": {
        "keys": ["OPENAI_API_KEY", "OPENAI_BASE_URL"],
        "domains": ["api.openai.com"],
    },
    "openrouter": {
        "keys": ["OPENROUTER_API_KEY"],
        "domains": ["openrouter.ai"],
    },
    "ollama": {
        "keys": ["OLLAMA_API_KEY", "OLLAMA_BASE_URL"],
        "domains": [],
    },
}


# Docker resolves "localhost" to the container itself, so a base URL naming a
# loopback address reaches nothing once the agent is sandboxed. Rewriting it to
# the host gateway is what makes an endpoint running on the developer's own
# machine -- an Ollama router, a local proxy -- usable from inside a task.
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
    """Runs ``ogcode run`` once per task and reads back its JSON summary.

    ogcode is not an ATIF producer: it reports totals for the whole run rather
    than a per-step trajectory, so ``SUPPORTS_ATIF`` stays False and only the
    aggregate metrics on :class:`AgentContext` get filled.
    """

    SUPPORTS_ATIF: bool = False

    _OUTPUT_FILENAME = "ogcode.json"
    _LOG_DIR = "/logs/agent"
    _WORKDIR = "/app"

    CLI_FLAGS = [
        CliFlag("max_turns", cli="--max-turns", type="int", default=250),
        CliFlag(
            "agent",
            cli="--agent",
            type="enum",
            choices=["build", "plan"],
            default="build",
        ),
    ]

    def __init__(
        self,
        *args,
        version: str | None = None,
        binary_url: str | None = None,
        **kwargs,
    ):
        super().__init__(*args, version=version or DEFAULT_VERSION, **kwargs)
        # Points the installer at a tarball other than the published release --
        # a build from a branch, or a rebuild of a release whose binary does not
        # run on the task image. The tarball must contain an ``ogcode``
        # executable at its root, as the release archives do.
        self._binary_url = binary_url

    @staticmethod
    def name() -> str:
        return "ogcode"

    # ---------------------------------------------------------------- install

    def install_spec(self) -> AgentInstallSpec:
        url = self._binary_url or RELEASE_URL.format(version=self._version)
        url = _reachable_from_container(url)
        return AgentInstallSpec(
            agent_name=self.name(),
            version=self._version,
            steps=[
                InstallStep(
                    user="root",
                    env={"DEBIAN_FRONTEND": "noninteractive"},
                    run=(
                        "if command -v apt-get >/dev/null 2>&1; then "
                        "apt-get update && apt-get install -y curl ca-certificates; "
                        "fi"
                    ),
                ),
                InstallStep(
                    # Root because the binary lands in /usr/local/bin, which is
                    # on PATH for every user the task might run commands as.
                    user="root",
                    run=(
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
                ),
            ],
            verification_command=self.get_version_command(),
        )

    def get_version_command(self) -> str | None:
        return "ogcode version"

    def parse_version(self, stdout: str) -> str:
        # First line is "ogcode <version> (linux/amd64)".
        first = stdout.strip().splitlines()[0] if stdout.strip() else ""
        parts = first.split()
        return parts[1] if len(parts) >= 2 else stdout.strip()

    # ---------------------------------------------------------------- network

    def _provider_slot(self) -> tuple[str, dict[str, Any]]:
        if not self.model_name or "/" not in self.model_name:
            raise ValueError(
                "Model name must be 'provider/model', e.g. anthropic/claude-opus-4-8"
            )
        provider, _ = self.model_name.split("/", 1)
        slot = _PROVIDER_SLOTS.get(provider)
        if slot is None:
            raise ValueError(
                f"ogcode has no '{provider}' provider slot. Its slots are "
                f"{', '.join(sorted(_PROVIDER_SLOTS))}. Reach anything else "
                "through the openai slot by setting OPENAI_BASE_URL to the "
                "gateway and naming the model as openai/<model>."
            )
        return provider, slot

    def network_allowlist(self) -> NetworkAllowlist:
        _, slot = self._provider_slot()
        # A configured base URL replaces the provider's public endpoint, so the
        # allowlist has to follow it or the run is sealed off from its gateway.
        base_urls = [
            _reachable_from_container(value)
            for key in slot["keys"]
            if key.endswith("_BASE_URL") and (value := self._get_env(key))
        ]
        install_urls = [_reachable_from_container(self._binary_url)] if self._binary_url else []
        return allowlist_from_urls(
            [*base_urls, *install_urls],
            default_domains=[*_INSTALL_DOMAINS, *slot["domains"]],
        )

    # -------------------------------------------------------------------- run

    async def run(
        self,
        instruction: str,
        environment: BaseEnvironment,
        context: AgentContext,
    ) -> None:
        _, slot = self._provider_slot()
        _, bare_model = self.model_name.split("/", 1)

        env = self.build_process_env()
        for key in slot["keys"]:
            if value := self._get_env(key):
                env[key] = (
                    _reachable_from_container(value)
                    if key.endswith("_BASE_URL")
                    else value
                )

        # ogcode merges an ogcode.json found in the working directory (or any
        # parent up to the repo root) into its config. Writing an explicit
        # empty one keeps the run hermetic: `ogcode run` connects MCP servers
        # synchronously before the first step, and a sealed sandbox would make
        # that a stall rather than an error.
        config_path = f"{self._WORKDIR}/ogcode.json"
        empty_config = shlex.quote(json.dumps({"mcp": {}, "skills": {}}))
        await self.exec_as_agent(
            environment,
            command=f"mkdir -p {self._LOG_DIR} && echo {empty_config} > {shlex.quote(config_path)}",
            env=env,
        )

        cli_flags = self.build_cli_flags()
        out = f"{self._LOG_DIR}/{self._OUTPUT_FILENAME}"

        await self.exec_as_agent(
            environment,
            # stdin is /dev/null on purpose: `ogcode run` appends piped stdin to
            # the prompt, and an open-but-empty pipe would block it forever.
            # stderr is kept off stdout so the JSON document parses.
            command=(
                f"cd {self._WORKDIR} && "
                f"ogcode run --output-format json --model {shlex.quote(bare_model)} "
                f"{cli_flags} -- {shlex.quote(instruction)} "
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
        # stdout ahead of the JSON document, so skip to the opening brace
        # rather than lose every metric on an older binary.
        start = raw.find("{")
        if start == -1:
            self.logger.warning("No JSON document in ogcode summary at %s", path)
            return
        try:
            summary = json.loads(raw[start:])
        except json.JSONDecodeError:
            self.logger.exception("Malformed ogcode run summary at %s", path)
            return

        tokens = summary.get("tokens") or {}
        cache_read = tokens.get("cache_read", 0) or 0
        cache_write = tokens.get("cache_write", 0) or 0

        # n_input_tokens is documented as including cache, so fold both cache
        # counters in; n_cache_tokens reports the read side, which is the half
        # that displaces fresh input.
        context.n_input_tokens = (tokens.get("input", 0) or 0) + cache_read + cache_write
        context.n_cache_tokens = cache_read
        context.n_output_tokens = tokens.get("output", 0) or 0
        context.n_agent_steps = summary.get("num_turns")
        # None rather than 0.0 when ogcode could not price the model -- Pier
        # aggregates these into a median cost, where a fake zero would drag it.
        context.cost_usd = summary.get("cost_usd")
        context.metadata = {
            "finish": summary.get("finish"),
            "session_id": summary.get("session_id"),
            "model": summary.get("model"),
            "reasoning_tokens": tokens.get("reasoning", 0) or 0,
        }
