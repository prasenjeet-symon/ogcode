"""Provider preset, environment and route-audit tests."""

from __future__ import annotations

import json
import os
import unittest
from unittest import mock

try:
    from . import support
except ImportError:
    import support

from benchlib import config, providers


def resolved(kind: str, **overrides) -> providers.ResolvedProvider:
    return providers.resolve_provider(config.ProviderConfig(kind=kind, **overrides))


class PresetTest(unittest.TestCase):
    def test_all_five_presets_are_known(self) -> None:
        self.assertEqual(
            set(providers.PRESETS),
            {"ollama-cloud", "openrouter", "anthropic", "openai", "custom"},
        )

    def test_resolved_preset_fields(self) -> None:
        expected = {
            "ollama-cloud": (
                "OLLAMA_API_KEY",
                "https://ollama.com/v1",
                "openai",
                "ollama-cloud",
                "Ollama Cloud",
            ),
            "openrouter": (
                "OPENROUTER_API_KEY",
                "https://openrouter.ai/api/v1",
                "openai",
                "openrouter",
                "OpenRouter",
            ),
            "anthropic": (
                "ANTHROPIC_API_KEY",
                None,
                "anthropic",
                "anthropic",
                "Anthropic",
            ),
            "openai": (
                "OPENAI_API_KEY",
                "https://api.openai.com/v1",
                "openai",
                "openai",
                "OpenAI",
            ),
        }
        for kind, fields in expected.items():
            with self.subTest(kind=kind):
                provider = resolved(kind)
                self.assertEqual(
                    (
                        provider.api_key_env,
                        provider.base_url,
                        provider.ogcode_provider,
                        provider.opencode_provider,
                        provider.display_name,
                    ),
                    fields,
                )
                self.assertFalse(provider.uses_custom_opencode)

    def test_custom_preset(self) -> None:
        provider = resolved(
            "custom",
            base_url="https://example.test/v1/",
            api_key_env="BENCHMARK_API_KEY",
        )
        self.assertEqual(provider.kind, "custom")
        self.assertEqual(provider.base_url, "https://example.test/v1")
        self.assertEqual(provider.api_key_env, "BENCHMARK_API_KEY")
        self.assertEqual(provider.ogcode_provider, "openai")
        self.assertEqual(provider.opencode_provider, "benchmark")
        self.assertEqual(
            provider.display_name, "Custom OpenAI-compatible endpoint"
        )
        self.assertTrue(provider.uses_custom_opencode)

    def test_display_name_override(self) -> None:
        provider = resolved("openai", name="My Gateway")
        self.assertEqual(provider.display_name, "My Gateway")

    def test_opencode_provider_override_uses_generated_config(self) -> None:
        provider = resolved(
            "openrouter", opencode_provider="my-provider.v2"
        )
        self.assertTrue(provider.uses_custom_opencode)
        self.assertEqual(provider.opencode_provider, "my-provider.v2")
        self.assertEqual(
            providers.opencode_model_string(provider, "z-ai/glm-4.6"),
            "my-provider.v2/z-ai/glm-4.6",
        )

    def test_non_default_base_url_uses_generated_config(self) -> None:
        provider = resolved("openai", base_url="https://gateway.test/v1")
        self.assertTrue(provider.uses_custom_opencode)
        self.assertEqual(provider.base_url, "https://gateway.test/v1")

    def test_trailing_slash_is_stripped(self) -> None:
        provider = resolved(
            "custom",
            base_url="https://example.test/v1///",
            api_key_env="BENCHMARK_API_KEY",
        )
        self.assertEqual(provider.base_url, "https://example.test/v1")

    def test_anthropic_rejects_a_foreign_opencode_provider(self) -> None:
        with self.assertRaises(SystemExit) as caught:
            resolved("anthropic", opencode_provider="openai")
        self.assertIn(
            "provider.opencode_provider is not supported for Anthropic",
            str(caught.exception),
        )

    def test_custom_resolution_requires_endpoint_and_defaults_key_env(self) -> None:
        with self.assertRaises(SystemExit) as caught:
            resolved("custom")
        self.assertIn("a custom provider needs provider.base_url", str(caught.exception))

        provider = resolved("custom", base_url="https://example.test/v1")
        self.assertEqual(provider.api_key_env, "BENCHMARK_API_KEY")
        self.assertTrue(provider.uses_custom_opencode)

    def test_expected_ogcode_route(self) -> None:
        self.assertEqual(providers.expected_ogcode_route(resolved("anthropic")), "anthropic")
        for kind in ("ollama-cloud", "openrouter", "openai"):
            with self.subTest(kind=kind):
                self.assertEqual(
                    providers.expected_ogcode_route(resolved(kind)), "openai"
                )


class EnvironmentTest(unittest.TestCase):
    def test_env_mapping_for_every_provider_kind(self) -> None:
        key = "test-key-value"
        cases = {
            "anthropic": {
                "ANTHROPIC_API_KEY": key
            },
            "openai": {
                "OPENAI_API_KEY": key,
                "OPENAI_BASE_URL": "https://api.openai.com/v1",
            },
            "ollama-cloud": {
                "OPENAI_API_KEY": key,
                "OPENAI_BASE_URL": "https://ollama.com/v1",
                "OLLAMA_API_KEY": key,
            },
            "openrouter": {
                "OPENAI_API_KEY": key,
                "OPENAI_BASE_URL": "https://openrouter.ai/api/v1",
                "OPENROUTER_API_KEY": key,
            },
            "custom": {
                "OPENAI_API_KEY": key,
                "OPENAI_BASE_URL": "https://example.test/v1",
                "BENCHMARK_API_KEY": key,
            },
        }
        for kind, expected in cases.items():
            with self.subTest(kind=kind):
                overrides = {}
                if kind == "custom":
                    overrides = {
                        "base_url": "https://example.test/v1",
                        "api_key_env": "BENCHMARK_API_KEY",
                    }
                provider = resolved(kind, **overrides)
                env = providers.provider_env(provider, key, {"HOME": "/tmp"})
                for name, value in expected.items():
                    self.assertEqual(env.get(name), value)
                self.assertEqual(env.get("HOME"), "/tmp")
                self.assertEqual(env.get("NO_COLOR"), "1")

                if kind == "anthropic":
                    self.assertNotIn("OPENAI_API_KEY", env)
                else:
                    self.assertNotIn("ANTHROPIC_API_KEY", env)

    def test_competing_provider_keys_are_scrubbed(self) -> None:
        competing = {
            name: "from-the-developer-machine"
            for name in providers.PROVIDER_ENV_VARS
        }
        competing["HOME"] = "/home/test"
        competing["PATH"] = "/usr/bin"
        provider = resolved("openai")
        env = providers.provider_env(provider, "selected-key", competing)

        expected = {"OPENAI_API_KEY": "selected-key", "OPENAI_BASE_URL": "https://api.openai.com/v1"}
        for name, value in expected.items():
            self.assertEqual(env[name], value)
        for name in providers.PROVIDER_ENV_VARS:
            if name in expected:
                continue
            with self.subTest(name=name):
                self.assertNotIn(name, env)
        self.assertEqual(env["HOME"], "/home/test")
        self.assertEqual(env["PATH"], "/usr/bin")
        # The caller's dictionary is never mutated.
        self.assertEqual(competing["OPENROUTER_API_KEY"], "from-the-developer-machine")

    def test_ollama_native_key_is_added_for_opencode(self) -> None:
        env = providers.provider_env(resolved("ollama-cloud"), "abc")
        self.assertEqual(env["OPENAI_API_KEY"], "abc")
        self.assertEqual(env["OLLAMA_API_KEY"], "abc")

    def test_openrouter_native_key_is_added_for_opencode(self) -> None:
        env = providers.provider_env(resolved("openrouter"), "abc")
        self.assertEqual(env["OPENAI_API_KEY"], "abc")
        self.assertEqual(env["OPENROUTER_API_KEY"], "abc")

    def test_anthropic_base_url_override(self) -> None:
        provider = resolved("anthropic", base_url="https://anthropic-proxy.test/v1/")
        self.assertFalse(provider.uses_custom_opencode)
        env = providers.provider_env(provider, "abc")
        self.assertEqual(env["ANTHROPIC_API_KEY"], "abc")
        self.assertEqual(env["ANTHROPIC_BASE_URL"], "https://anthropic-proxy.test/v1")
        self.assertNotIn("OPENAI_API_KEY", env)

    def test_custom_key_variable_is_present_for_generated_config(self) -> None:
        provider = resolved(
            "custom",
            base_url="https://example.test/v1",
            api_key_env="MY_BENCHMARK_KEY",
        )
        env = providers.provider_env(provider, "abc")
        self.assertEqual(env["MY_BENCHMARK_KEY"], "abc")
        self.assertEqual(env["OPENAI_API_KEY"], "abc")

    def test_scrub_uses_the_process_environment_by_default(self) -> None:
        with mock.patch.dict(
            os.environ,
            {
                "OPENAI_API_KEY": "present",
                "ANTHROPIC_API_KEY": "present",
                "BENCHMARK_KEEP_ME": "kept",
            },
            clear=False,
        ):
            env = providers.scrub_provider_env()
        self.assertNotIn("OPENAI_API_KEY", env)
        self.assertNotIn("ANTHROPIC_API_KEY", env)
        self.assertEqual(env["BENCHMARK_KEEP_ME"], "kept")


class OpencodeConfigTest(unittest.TestCase):
    def test_model_string(self) -> None:
        provider = resolved("openrouter")
        self.assertEqual(
            providers.opencode_model_string(provider, "z-ai/glm-4.6"),
            "openrouter/z-ai/glm-4.6",
        )
        self.assertEqual(
            providers.opencode_model_string(resolved("anthropic"), "claude-sonnet-4-5"),
            "anthropic/claude-sonnet-4-5",
        )

    def test_generated_config_uses_env_interpolation_not_a_key(self) -> None:
        provider = resolved(
            "custom",
            base_url="https://example.test/v1",
            api_key_env="MY_BENCHMARK_KEY",
            name="My Gateway",
        )
        generated = providers.opencode_custom_config(provider, "my-model")
        self.assertEqual(generated["$schema"], "https://opencode.ai/config.json")
        self.assertEqual(generated["mcp"], {})
        self.assertEqual(set(generated["provider"]), {"benchmark"})
        block = generated["provider"]["benchmark"]
        self.assertEqual(block["npm"], "@ai-sdk/openai-compatible")
        self.assertEqual(block["name"], "My Gateway")
        self.assertEqual(block["options"]["baseURL"], "https://example.test/v1")
        self.assertEqual(block["options"]["apiKey"], "{env:MY_BENCHMARK_KEY}")
        self.assertEqual(block["models"], {"my-model": {"name": "my-model"}})
        self.assertNotIn("MY_BENCHMARK_KEY=", json.dumps(generated))
        self.assertNotIn("secret", json.dumps(generated).lower())

    def test_generated_config_accepts_a_hyphenated_provider_id(self) -> None:
        provider = resolved(
            "openai",
            base_url="https://gateway.test/v1",
            opencode_provider="my-provider-v2",
        )
        generated = providers.opencode_custom_config(provider, "gpt-5.1")
        self.assertIn("my-provider-v2", generated["provider"])
        self.assertEqual(generated["provider"]["my-provider-v2"]["models"], {"gpt-5.1": {"name": "gpt-5.1"}})


class RouteAuditTest(unittest.TestCase):
    def setUp(self) -> None:
        self.provider = resolved("openai")

    def test_successful_route(self) -> None:
        log = "\n".join(
            [
                "some preamble",
                "resolved provider for model gpt-5.1: provider=openai",
                'streaming chat request url="https://api.openai.com/v1/chat/completions"',
                "done",
            ]
        )
        route = providers.provider_route_from_log(log, self.provider)
        self.assertEqual(route["ogcode_provider"], "openai")
        self.assertEqual(
            route["ogcode_url"], "https://api.openai.com/v1/chat/completions"
        )
        self.assertIs(route["provider_route_ok"], True)

    def test_provider_mismatch_is_a_failure(self) -> None:
        log = "resolved provider for model gpt-5.1: provider=anthropic"
        route = providers.provider_route_from_log(log, self.provider)
        self.assertEqual(route["ogcode_provider"], "anthropic")
        self.assertIs(route["provider_route_ok"], False)

    def test_host_mismatch_is_a_failure_even_when_provider_matches(self) -> None:
        log = "\n".join(
            [
                "resolved provider for model gpt-5.1: provider=openai",
                "streaming chat request url=https://evil.example/v1/chat",
            ]
        )
        route = providers.provider_route_from_log(log, self.provider)
        self.assertIs(route["provider_route_ok"], False)
        self.assertEqual(route["ogcode_url"], "https://evil.example/v1/chat")

    def test_last_route_line_wins(self) -> None:
        log = "\n".join(
            [
                "resolved provider for model gpt-5.1: provider=anthropic",
                "resolved provider for model gpt-5.1: provider=openai",
                "streaming chat request url=https://wrong.example/v1",
                "streaming chat request url=https://api.openai.com/v1/chat",
            ]
        )
        route = providers.provider_route_from_log(log, self.provider)
        self.assertEqual(route["ogcode_provider"], "openai")
        self.assertEqual(route["ogcode_url"], "https://api.openai.com/v1/chat")
        self.assertIs(route["provider_route_ok"], True)

    def test_missing_route_is_unknown_not_a_failure(self) -> None:
        for log in ("", "unrelated output\nanother line"):
            with self.subTest(log=log):
                route = providers.provider_route_from_log(log, self.provider)
                self.assertEqual(
                    route,
                    {
                        "ogcode_provider": None,
                        "ogcode_url": None,
                        "provider_route_ok": None,
                    },
                )

    def test_anthropic_route_without_a_base_url(self) -> None:
        provider = resolved("anthropic")
        route = providers.provider_route_from_log(
            "resolved provider for model claude: provider=anthropic\n"
            "streaming chat request url=https://api.anthropic.com/v1/messages",
            provider,
        )
        self.assertIs(route["provider_route_ok"], True)

    def test_url_only_log_does_not_confirm_a_route(self) -> None:
        route = providers.provider_route_from_log(
            "streaming chat request url=https://api.openai.com/v1/chat",
            self.provider,
        )
        self.assertIsNone(route["ogcode_provider"])
        self.assertEqual(route["ogcode_url"], "https://api.openai.com/v1/chat")
        self.assertIs(route["provider_route_ok"], False)


class RedactedEnvTest(unittest.TestCase):
    def test_key_values_are_never_printed(self) -> None:
        secret = "sk-do-not-print-this"
        provider = resolved(
            "custom",
            base_url="https://example.test/v1",
            api_key_env="BENCHMARK_API_KEY",
        )
        text = providers.redacted_env(
            provider, {"BENCHMARK_API_KEY": secret, "HOME": "/tmp"}
        )
        self.assertIn("api key          BENCHMARK_API_KEY=<set>", text)
        self.assertIn("base URL         https://example.test/v1", text)
        self.assertIn("ogcode slot      openai", text)
        self.assertIn("benchmark (generated config)", text)
        self.assertNotIn(secret, text)

    def test_missing_key(self) -> None:
        text = providers.redacted_env(resolved("openai"), {})
        self.assertIn("OPENAI_API_KEY=<missing>", text)
        self.assertIn("openai (built-in)", text)

    def test_anthropic_slot(self) -> None:
        text = providers.redacted_env(
            resolved("anthropic"), {"ANTHROPIC_API_KEY": "secret"}
        )
        self.assertIn("ANTHROPIC_API_KEY=<set>", text)
        self.assertIn("ogcode slot      anthropic", text)
        self.assertIn("anthropic (built-in)", text)
        self.assertNotIn("secret", text)


if __name__ == "__main__":
    unittest.main()
