"""Configuration loading, validation and resolution tests."""

from __future__ import annotations

import copy
import json
import os
import pathlib
import tempfile
import unittest
from unittest import mock

try:
    from . import support
except ImportError:  # ``python3 -m unittest discover -s benchmark/tests``
    import support

from benchlib import config


def config_data() -> dict:
    """The smallest valid config: a known kind and a model id."""
    return {"provider": {"kind": "openai"}, "model": {"id": "test-model"}}


class ConfigTestCase(unittest.TestCase):
    def setUp(self) -> None:
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.tmp = pathlib.Path(self._tmp.name)
        self._counter = 0

    def load(self, data: dict, name: str | None = None) -> config.BenchmarkConfig:
        self._counter += 1
        return support.load_config_data(
            self.tmp, data, name or f"config-{self._counter}.json"
        )

    def assert_rejected(self, data: dict, message: str) -> None:
        with self.assertRaises(SystemExit) as caught:
            self.load(data)
        self.assertIn(message, str(caught.exception))


class BundledConfigTest(ConfigTestCase):
    EXPECTED = {
        "anthropic": {
            "kind": "anthropic",
            "env": "ANTHROPIC_API_KEY",
            "model": "claude-sonnet-4-5",
            "ogcode": "claude-sonnet-4-5",
            "opencode": "anthropic/claude-sonnet-4-5",
            "output": "results/anthropic",
        },
        "custom-openai-compatible": {
            "kind": "custom",
            "env": "BENCHMARK_API_KEY",
            "model": "your-model-id",
            "ogcode": "your-model-id",
            "opencode": "benchmark/your-model-id",
            "output": "results/custom-openai-compatible",
        },
        "ollama-cloud": {
            "kind": "ollama-cloud",
            "env": "OLLAMA_API_KEY",
            "model": "glm-5.3-flash",
            "ogcode": "glm-5.3-flash",
            "opencode": "ollama-cloud/glm-5.3-flash",
            "output": "results/ollama-cloud",
        },
        "ollama-cloud-deepseek": {
            "kind": "ollama-cloud",
            "env": "OLLAMA_API_KEY",
            "model": "deepseek-v4.1-flash",
            "ogcode": "deepseek-v4.1-flash",
            "opencode": "ollama-cloud/deepseek-v4.1-flash",
            "output": "results/ollama-cloud-deepseek",
        },
        "openai": {
            "kind": "openai",
            "env": "OPENAI_API_KEY",
            "model": "gpt-5.1",
            "ogcode": "gpt-5.1",
            "opencode": "openai/gpt-5.1",
            "output": "results/openai",
        },
        "openrouter": {
            "kind": "openrouter",
            "env": "OPENROUTER_API_KEY",
            "model": "z-ai/glm-4.6",
            "ogcode": "z-ai/glm-4.6",
            "opencode": "openrouter/z-ai/glm-4.6",
            "output": "results/openrouter",
        },
        "openrouter-deepseek": {
            "kind": "openrouter",
            "env": "OPENROUTER_API_KEY",
            "model": "deepseek/deepseek-v4.1-flash",
            "ogcode": "deepseek/deepseek-v4.1-flash",
            "opencode": "openrouter/deepseek/deepseek-v4.1-flash",
            "output": "results/openrouter-deepseek",
        },
    }

    def test_every_preset_loads_by_name_and_by_path(self) -> None:
        self.assertEqual(config.available_presets(), sorted(self.EXPECTED))
        for preset, expected in self.EXPECTED.items():
            with self.subTest(preset=preset):
                by_name = config.load_config(preset)
                by_path = config.load_config(
                    str(support.CONFIGS_DIR / f"{preset}.json")
                )

                for cfg in (by_name, by_path):
                    self.assertEqual(cfg.name, preset)
                    self.assertEqual(cfg.provider.kind, expected["kind"])
                    self.assertEqual(cfg.provider.api_key_env, expected["env"])
                    self.assertEqual(cfg.model.id, expected["model"])
                    self.assertEqual(cfg.agent_model("ogcode"), expected["ogcode"])
                    self.assertEqual(cfg.agent_model("opencode"), expected["opencode"])
                    self.assertEqual(cfg.run.output_dir, expected["output"])
                    self.assertEqual(cfg.run.agents, ["ogcode", "opencode"])
                    self.assertEqual(cfg.run.reps, 5)
                    self.assertEqual(cfg.run.jobs, 1)
                    self.assertEqual(cfg.run.timeout_s, 600)
                    self.assertEqual(cfg.run.verify_timeout_s, 300)
                    self.assertEqual(cfg.run.index_timeout_s, 900)
                    self.assertEqual(config.task_names(cfg), support.SIX_TASKS)

                self.assertEqual(by_name.source, by_path.source)

    def test_bundled_configs_are_schema_shaped(self) -> None:
        schema = json.loads((support.BENCHMARK_ROOT / "config.schema.json").read_text())
        self.assertEqual(set(schema["properties"]), {
            "$schema", "name", "description", "provider", "model", "agents", "run",
        })
        self.assertEqual(set(schema["required"]), {"name", "provider", "model"})

        for preset in config.available_presets():
            with self.subTest(preset=preset):
                data = json.loads(
                    (support.CONFIGS_DIR / f"{preset}.json").read_text()
                )
                self.assertTrue(set(schema["required"]) <= set(data))
                self.assertTrue(set(data) <= set(schema["properties"]))
                provider = data["provider"]
                self.assertIn(provider["kind"], schema["properties"]["provider"]["properties"]["kind"]["enum"])
                self.assertIn("id", data["model"])
                self.assertTrue(
                    set(provider)
                    <= set(schema["properties"]["provider"]["properties"])
                )
                self.assertTrue(
                    set(data) - {"$schema"} <= set(config.BenchmarkConfig.__dataclass_fields__)
                )

    def test_configs_contain_no_literal_secret(self) -> None:
        # Configs name an environment variable; they never carry the value.
        for preset in config.available_presets():
            with self.subTest(preset=preset):
                raw = json.loads(
                    (support.CONFIGS_DIR / f"{preset}.json").read_text()
                )
                self.assertNotIn("api_key", raw["provider"])
                self.assertRegex(
                    raw["provider"]["api_key_env"],
                    r"^[A-Za-z_][A-Za-z0-9_]*$",
                )
                serialized = json.dumps(config.load_config(preset).to_json())
                self.assertNotIn("sk-", serialized)
                self.assertNotIn("Bearer ", serialized)

    def test_serialization_never_includes_environment_values(self) -> None:
        fake_key = "sk-test-not-a-real-secret-12345"
        with mock.patch.dict(
            os.environ,
            {"OLLAMA_API_KEY": fake_key, "BENCHMARK_API_KEY": fake_key},
            clear=False,
        ):
            payload = config.load_config("ollama-cloud").to_json()
        serialized = json.dumps(payload)
        self.assertNotIn(fake_key, serialized)
        self.assertEqual(payload["provider"]["api_key_env"], "OLLAMA_API_KEY")
        self.assertNotIn("api_key", payload["provider"])
        self.assertEqual(
            set(payload),
            {
                "name",
                "description",
                "source",
                "provider",
                "model",
                "agents",
                "run",
            },
        )
        self.assertEqual(
            set(payload["run"]),
            {
                "reps",
                "tasks",
                "agents",
                "jobs",
                "timeout_s",
                "verify_timeout_s",
                "index_timeout_s",
                "output_dir",
            },
        )
        self.assertEqual(payload["run"]["tasks"], [])
        self.assertEqual(payload["run"]["agents"], ["ogcode", "opencode"])

    def test_custom_preset_base_url_and_missing_pricing(self) -> None:
        cfg = config.load_config("custom-openai-compatible")
        self.assertEqual(cfg.provider.kind, "custom")
        self.assertEqual(cfg.provider.base_url, "https://your-endpoint.example/v1")
        self.assertIsNone(cfg.model.pricing)
        price, source = config.resolve_pricing(cfg)
        self.assertIsNone(price)
        self.assertEqual(source, "missing")


class SelectionTest(ConfigTestCase):
    def test_path_selection(self) -> None:
        path = support.CONFIGS_DIR / "openrouter.json"
        cfg = config.load_config(str(path))
        self.assertEqual(cfg.source, path.resolve())
        self.assertEqual(cfg.name, "openrouter")

    def test_unknown_preset_lists_alternatives(self) -> None:
        with self.assertRaises(SystemExit) as caught:
            config.load_config("does-not-exist")
        message = str(caught.exception)
        self.assertIn("unknown config 'does-not-exist'", message)
        self.assertIn("available presets:", message)
        self.assertIn("ollama-cloud", message)

    def test_missing_path_is_not_guessed_as_a_preset(self) -> None:
        for selector in ("missing/config.json", "missing-config.json"):
            with self.subTest(selector=selector):
                with self.assertRaises(SystemExit) as caught:
                    config.resolve_config_path(selector)
                self.assertIn(
                    f"config file not found: {selector}", str(caught.exception)
                )


class DefaultsTest(ConfigTestCase):
    def test_minimal_config_defaults(self) -> None:
        cfg = self.load(config_data())
        self.assertEqual(cfg.name, "config-1")
        self.assertEqual(cfg.description, "")
        self.assertEqual(cfg.run.reps, 5)
        self.assertEqual(cfg.run.tasks, [])
        self.assertEqual(cfg.run.agents, ["ogcode", "opencode"])
        self.assertEqual(cfg.run.jobs, 1)
        self.assertEqual(cfg.run.timeout_s, 600)
        self.assertEqual(cfg.run.verify_timeout_s, 300)
        self.assertEqual(cfg.run.index_timeout_s, 900)
        self.assertEqual(cfg.run.output_dir, "results")
        self.assertEqual(cfg.agents["ogcode"].extra_args, [])
        self.assertEqual(cfg.agents["opencode"].extra_args, [])
        self.assertEqual(cfg.agent_model("ogcode"), "test-model")
        self.assertEqual(cfg.agent_model("opencode"), "openai/test-model")

    def test_default_agent_models_are_provider_aware(self) -> None:
        cases = {
            "ollama-cloud": ("glm-5.3-flash", "ollama-cloud/glm-5.3-flash"),
            "openrouter": ("z-ai/glm-4.6", "openrouter/z-ai/glm-4.6"),
            "openai": ("gpt-5.1", "openai/gpt-5.1"),
            "anthropic": ("claude-sonnet-4-5", "anthropic/claude-sonnet-4-5"),
        }
        for preset, (ogcode, opencode) in cases.items():
            with self.subTest(preset=preset):
                data = json.loads(
                    (support.CONFIGS_DIR / f"{preset}.json").read_text()
                )
                data.pop("agents", None)
                cfg = self.load(data, f"{preset}-no-agents.json")
                self.assertEqual(cfg.agent_model("ogcode"), ogcode)
                self.assertEqual(cfg.agent_model("opencode"), opencode)

    def test_explicit_agent_models_win(self) -> None:
        data = config_data()
        data["agents"] = {
            "ogcode": {"model": "custom-ogcode", "extra_args": ["--flag", "value"]},
            "opencode": {"model": "custom-provider/custom-model"},
        }
        cfg = self.load(data)
        self.assertEqual(cfg.agent_model("ogcode"), "custom-ogcode")
        self.assertEqual(cfg.agent_model("opencode"), "custom-provider/custom-model")
        self.assertEqual(cfg.agents["ogcode"].extra_args, ["--flag", "value"])
        self.assertEqual(cfg.agents["opencode"].extra_args, [])

    def test_config_summary_is_one_line(self) -> None:
        cfg = config.load_config("ollama-cloud")
        summary = config.config_summary(cfg)
        self.assertNotIn("\n", summary)
        self.assertTrue(summary.startswith("ollama-cloud"))
        self.assertIn("ollama-cloud", summary.split())
        self.assertIn("model=glm-5.3-flash", summary)


class ValidationTest(ConfigTestCase):
    def test_required_top_level_sections(self) -> None:
        for missing in ("provider", "model"):
            with self.subTest(missing=missing):
                data = config_data()
                del data[missing]
                with self.assertRaises(SystemExit) as caught:
                    self.load(data)
                self.assertIn(f": {missing} is required", str(caught.exception))

    def test_unknown_keys_are_rejected_everywhere(self) -> None:
        cases = []

        data = config_data()
        data["bogus"] = 1
        cases.append((data, "has unknown keys: bogus"))

        data = config_data()
        data["provider"]["extra"] = True
        cases.append((data, "provider has unknown keys: extra"))

        data = config_data()
        data["model"]["extra"] = True
        cases.append((data, "model has unknown keys: extra"))

        data = config_data()
        data["agents"] = {"gemini": {}}
        cases.append((data, "agents has unknown names: gemini"))

        data = config_data()
        data["agents"] = {"ogcode": {"temperature": 1}}
        cases.append((data, "agents.ogcode has unknown keys: temperature"))

        data = config_data()
        data["run"] = {"parallel": True}
        cases.append((data, "run has unknown keys: parallel"))

        for index, (data, message) in enumerate(cases):
            with self.subTest(case=index):
                with self.assertRaises(SystemExit) as caught:
                    self.load(data)
                self.assertIn(message, str(caught.exception))

    def test_provider_custom_requires_endpoint_and_key_name(self) -> None:
        data = config_data()
        data["provider"] = {"kind": "custom"}
        self.assert_rejected(data, "provider.base_url is required for a custom endpoint")

        data["provider"]["base_url"] = "https://example.test/v1"
        self.assert_rejected(data, "provider.api_key_env is required for a custom endpoint")

        data["provider"]["api_key_env"] = "BENCHMARK_API_KEY"
        cfg = self.load(data)
        self.assertEqual(cfg.provider.base_url, "https://example.test/v1")

    def test_provider_field_validation(self) -> None:
        cases = []

        data = config_data()
        data["provider"]["kind"] = "gemini"
        cases.append((data, "provider.kind must be one of:"))

        data = config_data()
        data["provider"]["base_url"] = "ftp://example.test/v1"
        cases.append((data, "provider.base_url must start with http:// or https://"))

        data = config_data()
        data["provider"]["api_key_env"] = "NOT-A-NAME"
        cases.append((data, "provider.api_key_env must be an environment variable name"))

        data = config_data()
        data["provider"]["opencode_provider"] = "not a provider id"
        cases.append((data, "provider.opencode_provider must be a provider id"))

        data = config_data()
        data["provider"]["name"] = "  "
        cases.append((data, "provider.name must be a non-empty string"))

        for index, (data, message) in enumerate(cases):
            with self.subTest(case=index):
                self.assert_rejected(data, message)

    def test_provider_base_url_trailing_slash_is_stripped(self) -> None:
        data = config_data()
        data["provider"]["base_url"] = "https://example.test/v1///"
        cfg = self.load(data)
        self.assertEqual(cfg.provider.base_url, "https://example.test/v1")

    def test_hyphenated_opencode_provider_id_is_accepted(self) -> None:
        data = config_data()
        data["provider"]["opencode_provider"] = "my-provider.v2"
        cfg = self.load(data)
        self.assertEqual(cfg.provider.opencode_provider, "my-provider.v2")

    def test_model_id_and_pricing_validation(self) -> None:
        cases = []

        data = config_data()
        data["model"]["id"] = "   "
        cases.append((data, "model.id must be a non-empty string"))

        data = config_data()
        data["model"]["pricing"] = {"input": 1.0}
        cases.append((data, "model.pricing must include 'output'"))

        data = config_data()
        data["model"]["pricing"] = {"input": -1, "output": 2}
        cases.append((data, "model.pricing.input must be a non-negative number"))

        data = config_data()
        data["model"]["pricing"] = {"input": True, "output": 2}
        cases.append((data, "model.pricing.input must be a non-negative number"))

        data = config_data()
        data["model"]["pricing"] = {"input": 1, "output": 2, "batch": 3}
        cases.append((data, "model.pricing has unknown keys: batch"))

        data = config_data()
        data["model"]["pricing"] = [1, 2]
        cases.append((data, "model.pricing must be a JSON object"))

        for index, (data, message) in enumerate(cases):
            with self.subTest(case=index):
                self.assert_rejected(data, message)

    def test_run_field_validation(self) -> None:
        cases = []

        for field in ("reps", "jobs", "timeout_s", "verify_timeout_s", "index_timeout_s"):
            data = config_data()
            data["run"] = {field: 0}
            cases.append((data, f"run.{field} must be a positive integer"))

        data = config_data()
        data["run"] = {"reps": True}
        cases.append((data, "run.reps must be a positive integer"))

        data = config_data()
        data["run"] = {"tasks": []}
        cases.append((data, "run.tasks must contain at least one task"))

        data = config_data()
        data["run"] = {"tasks": ["go-bugfix", ""]}
        cases.append((data, "run.tasks must be a list of non-empty strings"))

        data = config_data()
        data["run"] = {"tasks": "some"}
        cases.append((data, "run.tasks must be 'all' or a list of task names"))

        data = config_data()
        data["run"] = {"agents": []}
        cases.append((data, "run.agents must contain at least one agent"))

        data = config_data()
        data["run"] = {"agents": ["ogcode", "gemini"]}
        cases.append((data, "run.agents has unknown names: gemini"))

        data = config_data()
        data["run"] = {"output_dir": "  "}
        cases.append((data, "run.output_dir must be a non-empty string"))

        for index, (data, message) in enumerate(cases):
            with self.subTest(case=index):
                self.assert_rejected(data, message)

    def test_agent_entry_validation(self) -> None:
        data = config_data()
        data["agents"] = {"ogcode": {"model": " "}}
        self.assert_rejected(data, "agents.ogcode.model must be a non-empty string")

        data = config_data()
        data["agents"] = {"ogcode": {"extra_args": "not-a-list"}}
        self.assert_rejected(data, "agents.ogcode.extra_args must be a list of strings")

    def test_duplicate_agents_are_normalized(self) -> None:
        # The loader deliberately de-duplicates (benchlib/config.py:293) so a
        # repeated name cannot silently double the repetition count.  The
        # schema also marks run.agents unique, so duplicates are a file-level
        # mistake that is normalized rather than a reason to abort a run.
        data = config_data()
        data["run"] = {"agents": ["opencode", "ogcode", "opencode"]}
        cfg = self.load(data)
        self.assertEqual(cfg.run.agents, ["opencode", "ogcode"])

    def test_description_must_be_a_string(self) -> None:
        data = config_data()
        data["description"] = 7
        self.assert_rejected(data, "description must be a string")

    def test_name_defaults_to_the_file_stem(self) -> None:
        cfg = support.load_config_data(self.tmp, config_data(), "my-benchmark.json")
        self.assertEqual(cfg.name, "my-benchmark")


class TasksTest(ConfigTestCase):
    def test_all_expands_to_the_six_real_tasks(self) -> None:
        for selector in ("all", "", None):
            with self.subTest(selector=selector):
                data = config_data()
                run = {} if selector is None else {"tasks": selector}
                data["run"] = run
                cfg = self.load(data)
                self.assertEqual(cfg.run.tasks, [])
                self.assertEqual(config.task_names(cfg), support.SIX_TASKS)
                self.assertEqual(
                    support.fixtures.discover_tasks(), support.SIX_TASKS
                )

    def test_explicit_task_list_preserves_order(self) -> None:
        data = config_data()
        data["run"] = {"tasks": ["py-shop", "go-bugfix", "py-shop"]}
        cfg = self.load(data)
        self.assertEqual(config.task_names(cfg), ["py-shop", "go-bugfix", "py-shop"])

    def test_unknown_explicit_task_is_rejected(self) -> None:
        data = config_data()
        data["run"] = {"tasks": ["go-bugfix", "not-a-task", "also-bad"]}
        with self.assertRaises(SystemExit) as caught:
            config.task_names(self.load(data))
        message = str(caught.exception)
        self.assertIn("unknown task(s): not-a-task, also-bad", message)
        self.assertIn("available:", message)


class PricingTest(ConfigTestCase):
    def test_package_pricing_table_is_used_when_config_has_none(self) -> None:
        data = config_data()
        data["model"]["id"] = "glm-5.3-flash"
        cfg = self.load(data)
        price, source = config.resolve_pricing(cfg)
        self.assertEqual(
            price, {"input": 0.15, "output": 0.5, "cache_read": 0.03}
        )
        self.assertEqual(source, str(config.DEFAULT_PRICING))

    def test_config_pricing_wins_over_the_package_table(self) -> None:
        data = config_data()
        data["model"]["id"] = "glm-5.3-flash"
        data["model"]["pricing"] = {"input": 9.0, "output": 10.0}
        cfg = self.load(data)
        price, source = config.resolve_pricing(cfg)
        self.assertEqual(price, {"input": 9.0, "output": 10.0})
        self.assertEqual(source, "config")

    def test_explicit_pricing_table_path(self) -> None:
        path = self.tmp / "prices.json"
        path.write_text(
            json.dumps(
                {
                    "models": {
                        "glm-5.3-flash": {
                            "input": 1.5,
                            "output": 2.5,
                            "cache_write": 3.5,
                        }
                    }
                }
            )
        )
        data = config_data()
        data["model"]["id"] = "glm-5.3-flash"
        cfg = self.load(data)
        price, source = config.resolve_pricing(cfg, path)
        self.assertEqual(
            price,
            {"input": 1.5, "output": 2.5, "cache_write": 3.5},
        )
        self.assertEqual(source, str(path))

    def test_unknown_model_has_no_price(self) -> None:
        cfg = self.load(config_data())
        price, source = config.resolve_pricing(cfg)
        self.assertIsNone(price)
        self.assertEqual(source, "missing")

    def test_pricing_table_shape_is_checked(self) -> None:
        path = self.tmp / "bad-prices.json"
        path.write_text(json.dumps({"models": []}))
        with self.assertRaises(SystemExit) as caught:
            support.pricing.read_pricing_table(path)
        self.assertIn("expected an object with a 'models' object", str(caught.exception))


class OutputDirectoryTest(ConfigTestCase):
    def test_relative_config_path_is_package_relative(self) -> None:
        cfg = config.load_config("ollama-cloud")
        self.assertEqual(cfg.run.output_dir, "results/ollama-cloud")
        self.assertEqual(
            config.resolve_output_dir(cfg, None),
            (config.PACKAGE_ROOT / "results/ollama-cloud").resolve(),
        )

    def test_cli_output_wins(self) -> None:
        cfg = config.load_config("ollama-cloud")
        cli_out = self.tmp / "cli-out"
        self.assertEqual(
            config.resolve_output_dir(cfg, str(cli_out)), cli_out.resolve()
        )

    def test_absolute_config_path_is_returned_unchanged(self) -> None:
        absolute = self.tmp / "absolute-out"
        data = config_data()
        data["run"] = {"output_dir": str(absolute)}
        cfg = self.load(data)
        self.assertEqual(config.resolve_output_dir(cfg, None), absolute)


if __name__ == "__main__":
    unittest.main()
