"""End-to-end CLI tests for ``run_benchmark.py``.

Every invocation is either ``--list-configs``, ``--help``, ``--version`` or
``--dry-run``, and every child gets a provider-scrubbed environment.  A real
run is only exercised up to the missing-key guard, before ``runner.run`` is
reached, so these tests never make a network call.
"""

from __future__ import annotations

import json
import pathlib
import sys
import tempfile
import unittest

try:
    from . import support
except ImportError:
    import support

from benchlib import config


class CliTestCase(unittest.TestCase):
    def setUp(self) -> None:
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.tmp = pathlib.Path(self._tmp.name)

    def run_cli(self, *args: str, env=None):
        return support.run_cli(*args, env=env)

    def assert_ok(self, proc) -> None:
        self.assertEqual(
            proc.returncode,
            0,
            msg=f"stdout:\n{proc.stdout}\nstderr:\n{proc.stderr}",
        )

    def assert_failed(self, proc, message: str) -> None:
        self.assertNotEqual(proc.returncode, 0)
        combined = proc.stdout + proc.stderr
        self.assertIn(message, combined)


class MetaCommandsTest(CliTestCase):
    def test_list_configs(self) -> None:
        proc = self.run_cli("--list-configs")
        self.assert_ok(proc)
        lines = [line for line in proc.stdout.splitlines() if line.strip()]
        self.assertEqual(
            sorted(line.split()[0] for line in lines),
            [
                "anthropic",
                "custom-openai-compatible",
                "ollama-cloud",
                "ollama-cloud-deepseek",
                "openai",
                "openrouter",
                "openrouter-deepseek",
            ],
        )
        self.assertIn("model=glm-5.3-flash", proc.stdout)
        self.assertIn("model=deepseek/deepseek-v4.1-flash", proc.stdout)
        self.assertIn("model=gpt-5.1", proc.stdout)
        self.assertNotIn("INVALID", proc.stdout)
        self.assertEqual(proc.stderr, "")

    def test_version(self) -> None:
        proc = self.run_cli("--version")
        self.assert_ok(proc)
        self.assertEqual(proc.stdout.strip(), f"ogcode-benchmark {support.VERSION}")

    def test_help_lists_the_documented_flags(self) -> None:
        proc = self.run_cli("--help")
        self.assert_ok(proc)
        for flag in (
            "--config",
            "--list-configs",
            "--tasks",
            "--agents",
            "--reps",
            "--jobs",
            "--model",
            "--ogcode-model",
            "--opencode-model",
            "--timeout",
            "--verify-timeout",
            "--index-timeout",
            "--out",
            "--pricing",
            "--dry-run",
        ):
            with self.subTest(flag=flag):
                self.assertIn(flag, proc.stdout)
        self.assertIn("Presets:", proc.stdout)


class DryRunTest(CliTestCase):
    def test_ollama_cloud_plan(self) -> None:
        proc = self.run_cli("--config", "ollama-cloud", "--dry-run")
        self.assert_ok(proc)
        out = proc.stdout
        self.assertIn(f"ogcode-benchmark {support.VERSION}", out)
        self.assertIn(
            f"config           ollama-cloud ({support.CONFIGS_DIR / 'ollama-cloud.json'})",
            out,
        )
        self.assertIn("provider         ollama-cloud — Ollama Cloud", out)
        self.assertIn("base URL         https://ollama.com/v1", out)
        self.assertIn("api key          OLLAMA_API_KEY=<missing>", out)
        self.assertIn("ogcode slot      openai", out)
        self.assertIn("opencode         ollama-cloud (built-in)", out)
        self.assertIn("model            glm-5.3-flash", out)
        self.assertIn(
            "price            input $0.15/M, output $0.5/M, cache-read $0.03/M (source: config)",
            out,
        )
        self.assertIn(
            "tasks            go-bugfix, js-bugfix, py-bugfix, py-feature, "
            "py-ledger, py-shop",
            out,
        )
        self.assertIn("agents           ogcode, opencode", out)
        self.assertIn("reps             5", out)
        self.assertIn("jobs             1", out)
        self.assertIn("timeouts         run=600s verify=300s index=900s", out)
        self.assertIn(
            f"out              {(config.PACKAGE_ROOT / 'results/ollama-cloud').resolve()}",
            out,
        )
        self.assertIn("commands (task prompt abbreviated):", out)
        self.assertIn(
            "ogcode run --model glm-5.3-flash --output-format json '<task prompt>'",
            out,
        )
        self.assertIn(
            "opencode run --model ollama-cloud/glm-5.3-flash --format json --auto '<task prompt>'",
            out,
        )
        self.assertIn("warnings:", out)
        self.assertIn(
            "- OLLAMA_API_KEY is not set; a real run will stop here", out
        )
        self.assertIn("Dry run only: no model calls were made.", out)
        self.assertNotIn("no price for this model", out)

    def test_openrouter_plan(self) -> None:
        proc = self.run_cli("--config", "openrouter", "--dry-run")
        self.assert_ok(proc)
        out = proc.stdout
        self.assertIn("provider         openrouter — OpenRouter", out)
        self.assertIn("base URL         https://openrouter.ai/api/v1", out)
        self.assertIn("api key          OPENROUTER_API_KEY=<missing>", out)
        self.assertIn("model            z-ai/glm-4.6", out)
        self.assertIn(
            "price            input $0.5/M, output $2/M, cache-read $0.1/M (source: config)",
            out,
        )
        self.assertIn(
            "opencode run --model openrouter/z-ai/glm-4.6 "
            "--format json --auto '<task prompt>'",
            out,
        )

    def test_anthropic_plan_uses_the_native_slot(self) -> None:
        proc = self.run_cli("--config", "anthropic", "--dry-run")
        self.assert_ok(proc)
        out = proc.stdout
        self.assertIn("provider         anthropic — Anthropic", out)
        self.assertIn("api key          ANTHROPIC_API_KEY=<missing>", out)
        self.assertIn("ogcode slot      anthropic", out)
        self.assertIn("opencode         anthropic (built-in)", out)
        self.assertNotIn("base URL", out)
        self.assertIn(
            "ogcode run --model claude-sonnet-4-5 --output-format json",
            out,
        )

    def test_custom_endpoint_warns_about_key_and_price(self) -> None:
        proc = self.run_cli(
            "--config", "custom-openai-compatible", "--dry-run"
        )
        self.assert_ok(proc)
        out = proc.stdout
        self.assertIn("provider         custom — Custom OpenAI-compatible endpoint", out)
        self.assertIn("base URL         https://your-endpoint.example/v1", out)
        self.assertIn("api key          BENCHMARK_API_KEY=<missing>", out)
        self.assertIn(
            "opencode         benchmark (generated temp config)", out
        )
        self.assertIn("model            your-model-id", out)
        self.assertIn("price            missing (source: missing)", out)
        self.assertIn(
            "- BENCHMARK_API_KEY is not set; a real run will stop here", out
        )
        self.assertIn(
            "- no price for this model in the config or pricing.json; "
            "a real run will stop here",
            out,
        )

    def test_present_key_is_reported_without_printing_it(self) -> None:
        secret = "sk-test-value-must-not-leak"
        proc = self.run_cli(
            "--config",
            "ollama-cloud",
            "--dry-run",
            env=support.cli_env(OLLAMA_API_KEY=secret),
        )
        self.assert_ok(proc)
        self.assertIn("api key          OLLAMA_API_KEY=<set>", proc.stdout)
        self.assertNotIn(secret, proc.stdout + proc.stderr)
        self.assertNotIn("OLLAMA_API_KEY is not set", proc.stdout)

    def test_path_selection(self) -> None:
        path = support.CONFIGS_DIR / "openrouter.json"
        proc = self.run_cli("--config", str(path), "--dry-run")
        self.assert_ok(proc)
        self.assertIn(f"config           openrouter ({path})", proc.stdout)

    def test_task_and_agent_overrides(self) -> None:
        proc = self.run_cli(
            "--config",
            "ollama-cloud",
            "--tasks",
            "py-bugfix,js-bugfix",
            "--agents",
            "ogcode",
            "--dry-run",
        )
        self.assert_ok(proc)
        out = proc.stdout
        self.assertIn("tasks            py-bugfix, js-bugfix", out)
        self.assertNotIn("go-bugfix", out)
        self.assertIn("agents           ogcode", out)
        self.assertNotIn("agents           ogcode, opencode", out)
        self.assertIn("  ogcode ", out)
        self.assertNotIn("  opencode ", out)

    def test_all_tasks_override(self) -> None:
        proc = self.run_cli(
            "--config", "ollama-cloud", "--tasks", "all", "--dry-run"
        )
        self.assert_ok(proc)
        self.assertIn(
            "tasks            go-bugfix, js-bugfix, py-bugfix, py-feature, "
            "py-ledger, py-shop",
            proc.stdout,
        )

    def test_model_override_clears_agent_models(self) -> None:
        proc = self.run_cli(
            "--config",
            "ollama-cloud",
            "--model",
            "replacement-model",
            "--dry-run",
        )
        self.assert_ok(proc)
        out = proc.stdout
        self.assertIn("model            replacement-model", out)
        self.assertIn(
            "ogcode run --model replacement-model --output-format json",
            out,
        )
        self.assertIn(
            "opencode run --model ollama-cloud/replacement-model "
            "--format json --auto",
            out,
        )

    def test_explicit_output_directory(self) -> None:
        out_dir = (self.tmp / "cli-out").resolve()
        proc = self.run_cli(
            "--config", "ollama-cloud", "--out", str(out_dir), "--dry-run"
        )
        self.assert_ok(proc)
        self.assertIn(f"out              {out_dir}", proc.stdout)

    def test_explicit_pricing_table(self) -> None:
        pricing = self.tmp / "prices.json"
        pricing.write_text(
            json.dumps(
                {"models": {"replacement-model": {"input": 1.0, "output": 2.0}}}
            )
        )
        proc = self.run_cli(
            "--config",
            "custom-openai-compatible",
            "--model",
            "replacement-model",
            "--pricing",
            str(pricing),
            "--dry-run",
        )
        self.assert_ok(proc)
        out = proc.stdout
        self.assertIn(
            f"price            input $1/M, output $2/M (source: {pricing.resolve()})", out
        )
        self.assertNotIn("no price for this model", out)

    def test_reps_and_jobs_overrides(self) -> None:
        proc = self.run_cli(
            "--config",
            "ollama-cloud",
            "--reps",
            "2",
            "--jobs",
            "3",
            "--dry-run",
        )
        self.assert_ok(proc)
        self.assertIn("reps             2", proc.stdout)
        self.assertIn("jobs             3", proc.stdout)

    def test_dry_run_performs_no_real_run(self) -> None:
        # Scrub every provider credential and point both binaries at Python:
        # a dry run must still succeed, which proves it never invokes them.
        proc = self.run_cli(
            "--config",
            "ollama-cloud",
            "--ogcode",
            sys.executable,
            "--opencode",
            sys.executable,
            "--dry-run",
            env=support.cli_env(),
        )
        self.assert_ok(proc)
        self.assertIn("Dry run only: no model calls were made.", proc.stdout)


class CliErrorTest(CliTestCase):
    def test_unknown_config(self) -> None:
        proc = self.run_cli("--config", "not-a-preset", "--dry-run")
        self.assert_failed(proc, "unknown config 'not-a-preset'")
        self.assertIn("available presets:", proc.stderr)

    def test_missing_config_path(self) -> None:
        proc = self.run_cli(
            "--config", "no/such/config.json", "--dry-run"
        )
        self.assert_failed(proc, "config file not found: no/such/config.json")

    def test_unknown_task(self) -> None:
        proc = self.run_cli(
            "--config",
            "ollama-cloud",
            "--tasks",
            "go-bugfix,not-a-task",
            "--dry-run",
        )
        self.assert_failed(proc, "unknown task(s): not-a-task")
        self.assertIn("available:", proc.stderr)

    def test_unknown_agent(self) -> None:
        proc = self.run_cli(
            "--config", "ollama-cloud", "--agents", "ogcode,gemini", "--dry-run"
        )
        self.assert_failed(proc, "unknown agent(s): gemini")

    def test_empty_agents(self) -> None:
        proc = self.run_cli(
            "--config", "ollama-cloud", "--agents", " , ", "--dry-run"
        )
        self.assert_failed(proc, "--agents must name at least one agent")

    def test_non_positive_numbers(self) -> None:
        for flag in ("--reps", "--jobs", "--timeout", "--verify-timeout", "--index-timeout"):
            with self.subTest(flag=flag):
                proc = self.run_cli(
                    "--config", "ollama-cloud", flag, "0", "--dry-run"
                )
                self.assert_failed(
                    proc, f"{flag} must be a positive integer"
                )

    def test_real_run_stops_before_network_without_a_key(self) -> None:
        proc = self.run_cli(
            "--config",
            "ollama-cloud",
            "--tasks",
            "py-bugfix",
            "--out",
            str(self.tmp / "out"),
            "--ogcode",
            sys.executable,
            "--opencode",
            sys.executable,
            env=support.cli_env(),
        )
        self.assert_failed(proc, "OLLAMA_API_KEY is not set")
        self.assertNotIn("Dry run only", proc.stdout)
        self.assertFalse((self.tmp / "out").exists())


if __name__ == "__main__":
    unittest.main()
