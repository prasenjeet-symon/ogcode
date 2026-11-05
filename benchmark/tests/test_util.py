"""Small utility, pricing and agent-command tests."""

from __future__ import annotations

import hashlib
import json
import os
import pathlib
import subprocess
import sys
import tempfile
import unittest

try:
    from . import support
except ImportError:
    import support

from benchlib import agents, pricing, util


class UtilTestCase(unittest.TestCase):
    def setUp(self) -> None:
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.tmp = pathlib.Path(self._tmp.name).resolve()

    def make_git_repo(self) -> pathlib.Path:
        dest = self.tmp / "repo"
        dest.mkdir()
        util.git(dest, "init", "-q")
        (dest / "file.txt").write_text("hello\n")
        util.git(dest, "add", "-A")
        util.git(
            dest,
            "-c",
            "user.email=bench@ogcode.in",
            "-c",
            "user.name=bench",
            "-c",
            "commit.gpgsign=false",
            "commit",
            "-qm",
            "test",
        )
        return dest


class TimeTest(unittest.TestCase):
    def test_iso_now(self) -> None:
        value = util.iso_now()
        self.assertRegex(value, r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$")
        self.assertEqual(len(value), 20)

    def test_utc_stamp(self) -> None:
        value = util.utc_stamp()
        self.assertRegex(value, r"^\d{8}T\d{6}Z$")
        self.assertEqual(len(value), 16)


class BinaryTest(UtilTestCase):
    def test_finds_a_binary_on_path(self) -> None:
        self.assertTrue(util.find_binary("git"))

    def test_missing_binary_reports_the_fallbacks(self) -> None:
        missing = "definitely-not-a-real-binary-xyz"
        with self.assertRaises(SystemExit) as caught:
            util.find_binary(missing)
        self.assertIn(f"{missing} not found on PATH", str(caught.exception))
        self.assertIn("also tried: none", str(caught.exception))

    def test_fallback_is_used(self) -> None:
        missing = "definitely-not-a-real-binary-xyz"
        self.assertEqual(
            util.find_binary(missing, [sys.executable]), sys.executable
        )

    def test_missing_fallback_is_reported(self) -> None:
        missing = "definitely-not-a-real-binary-xyz"
        fallback = str(self.tmp / "not-here")
        with self.assertRaises(SystemExit) as caught:
            util.find_binary(missing, [fallback])
        self.assertIn(fallback, str(caught.exception))


class RunCommandTest(UtilTestCase):
    def test_captures_output_environment_and_exit_code(self) -> None:
        result = util.run_cmd(
            [
                sys.executable,
                "-c",
                "import os, sys; print(os.getcwd()); print(os.environ.get('PWD')); "
                "print(os.environ.get('BENCHMARK_TEST_VALUE')); "
                "sys.stderr.write('on-stderr')",
            ],
            self.tmp,
            30,
            {"PATH": os.environ.get("PATH", ""), "BENCHMARK_TEST_VALUE": "visible"},
        )
        self.assertFalse(result["timed_out"])
        self.assertEqual(result["code"], 0)
        lines = result["stdout"].splitlines()
        self.assertEqual(lines[0], str(self.tmp))
        self.assertEqual(lines[1], str(self.tmp))
        self.assertEqual(lines[2], "visible")
        self.assertEqual(result["stderr"], "on-stderr")
        self.assertGreaterEqual(result["duration_s"], 0.0)

    def test_timeout_kills_the_child(self) -> None:
        result = util.run_cmd(
            [sys.executable, "-c", "import time; time.sleep(30)"],
            self.tmp,
            1,
            {"PATH": os.environ.get("PATH", "")},
        )
        self.assertTrue(result["timed_out"])
        self.assertLess(result["duration_s"], 10)
        self.assertNotEqual(result["code"], 0)


class GitTest(UtilTestCase):
    def test_git_returns_stdout_and_raises_on_failure(self) -> None:
        repo = self.make_git_repo()
        self.assertEqual(
            util.git(repo, "rev-parse", "--is-inside-work-tree").strip(), "true"
        )
        with self.assertRaises(subprocess.CalledProcessError):
            util.git(repo, "definitely-not-a-git-command")

    def test_empty_file_and_sha256(self) -> None:
        path = self.tmp / "payload.bin"
        path.write_bytes(b"")
        self.assertEqual(util.sha256_file(path), hashlib.sha256(b"").hexdigest())
        path.write_bytes(b"hello\n")
        self.assertEqual(
            util.sha256_file(path), hashlib.sha256(b"hello\n").hexdigest()
        )

    def test_relative_path(self) -> None:
        root = self.tmp / "root"
        inside = root / "sub" / "file.txt"
        inside.parent.mkdir(parents=True)
        inside.write_text("x")
        self.assertEqual(util.relative_path(inside, root), "sub/file.txt")
        outside = self.tmp / "outside.txt"
        outside.write_text("x")
        self.assertEqual(util.relative_path(outside, root), str(outside.resolve()))


class JsonTest(UtilTestCase):
    def test_write_and_read_round_trip(self) -> None:
        path = self.tmp / "nested" / "value.json"
        value = {"a": 1, "b": [True, None, "text"]}
        util.write_json(path, value)
        self.assertEqual(util.read_json(path), value)
        text = path.read_text()
        self.assertTrue(text.endswith("\n"))
        self.assertIn('  "a": 1', text)

    def test_read_missing_file(self) -> None:
        with self.assertRaises(SystemExit) as caught:
            util.read_json(self.tmp / "missing.json")
        self.assertIn("cannot read", str(caught.exception))

    def test_read_invalid_json(self) -> None:
        path = self.tmp / "bad.json"
        path.write_text("{not json")
        with self.assertRaises(SystemExit) as caught:
            util.read_json(path)
        self.assertIn(f"invalid JSON in {path}", str(caught.exception))


class PricingTest(unittest.TestCase):
    def test_shared_cost_uses_all_token_kinds(self) -> None:
        tokens = {
            "input": 1_000_000,
            "output": 1_000_000,
            "reasoning": 1_000_000,
            "cache_read": 1_000_000,
            "cache_write": 1_000_000,
        }
        price = {
            "input": 1.0,
            "output": 2.0,
            "cache_read": 0.25,
            "cache_write": 0.5,
        }
        self.assertAlmostEqual(
            pricing.shared_cost(tokens, price), 1 + 2 + 0.25 + 0.5
        )

    def test_shared_cost_falls_back_to_input_for_cache(self) -> None:
        tokens = {"input": 1_000_000, "cache_read": 1_000_000}
        self.assertAlmostEqual(
            pricing.shared_cost(tokens, {"input": 2.0, "output": 10.0}), 4.0
        )

    def test_shared_cost_treats_missing_and_none_as_zero(self) -> None:
        self.assertEqual(
            pricing.shared_cost(
                {"input": None, "output": "1000000"}, {"input": 1, "output": 2}
            ),
            2.0,
        )
        self.assertEqual(pricing.shared_cost({}, {"input": 1, "output": 2}), 0.0)

    def test_describe_price(self) -> None:
        self.assertEqual(pricing.describe_price(None), "missing")
        self.assertEqual(
            pricing.describe_price({"input": 1.0, "output": 2.0}),
            "input $1/M, output $2/M",
        )
        self.assertEqual(
            pricing.describe_price(
                {
                    "input": 0.15,
                    "output": 0.5,
                    "cache_read": 0.03,
                    "cache_write": 0.25,
                }
            ),
            "input $0.15/M, output $0.5/M, cache-read $0.03/M, cache-write $0.25/M",
        )

    def test_read_pricing_table(self) -> None:
        table = pricing.read_pricing_table(support.BENCHMARK_ROOT / "pricing.json")
        self.assertIn("models", table)
        self.assertIn("glm-5.3-flash", table["models"])

    def test_read_pricing_table_shape(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            path = pathlib.Path(tmp) / "prices.json"
            path.write_text(json.dumps({"models": "not-a-dict"}))
            with self.assertRaises(SystemExit) as caught:
                pricing.read_pricing_table(path)
        self.assertIn("expected an object with a 'models' object", str(caught.exception))


class AgentCommandTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.cfg = support.config.load_config("ollama-cloud")
        cls.provider = support.providers.resolve_provider(cls.cfg.provider)

    def test_ogcode_command(self) -> None:
        command = agents.agent_command(
            "ogcode", self.cfg, self.provider, {"ogcode": "/bin/og"}, "fix the bug"
        )
        self.assertEqual(
            command,
            [
                "/bin/og",
                "run",
                "--model",
                "glm-5.3-flash",
                "--output-format",
                "json",
                "fix the bug",
            ],
        )

    def test_opencode_command(self) -> None:
        command = agents.agent_command(
            "opencode", self.cfg, self.provider, {"opencode": "/bin/oc"}, "fix the bug"
        )
        self.assertEqual(
            command,
            [
                "/bin/oc",
                "run",
                "--model",
                "ollama-cloud/glm-5.3-flash",
                "--format",
                "json",
                "--auto",
                "fix the bug",
            ],
        )

    def test_extra_args_come_before_the_prompt(self) -> None:
        cfg = support.config.load_config("ollama-cloud")
        cfg.agents["ogcode"].extra_args = ["--verbose", "--effort", "high"]
        command = agents.agent_command(
            "ogcode", cfg, self.provider, {"ogcode": "og"}, "fix it"
        )
        self.assertEqual(command[-4:], ["--verbose", "--effort", "high", "fix it"])

    def test_command_preview_hides_the_prompt(self) -> None:
        command = agents.agent_command(
            "ogcode", self.cfg, self.provider, {"ogcode": "og"}, "secret task text"
        )
        preview = agents.command_preview(command)
        self.assertNotIn("secret task text", preview)
        self.assertTrue(preview.endswith("'<task prompt>'"))
        self.assertIn("--model glm-5.3-flash", preview)

    def test_unknown_agent_is_rejected(self) -> None:
        with self.assertRaises(SystemExit) as caught:
            agents.agent_command(
                "gemini", self.cfg, self.provider, {}, "prompt"
            )
        self.assertIn("unknown agent 'gemini'", str(caught.exception))

    def test_isolation_config_blocks_skills_and_external_mcp(self) -> None:
        isolation = agents.ogcode_isolation()
        self.assertEqual(isolation["skills"]["permissions"]["*"], "deny")
        self.assertIsInstance(isolation["mcp"], dict)
        for name, entry in isolation["mcp"].items():
            with self.subTest(name=name):
                self.assertIs(entry["disabled"], True)
        json.dumps(isolation)

    def test_normalize_tokens(self) -> None:
        self.assertEqual(
            agents.normalize_tokens(
                {
                    "input": "12",
                    "output": 3,
                    "reasoning": None,
                    "cache_read": 4.0,
                    "cache_write": "5",
                    "total": 24,
                }
            ),
            {
                "input": 12,
                "output": 3,
                "reasoning": 0,
                "cache_read": 4,
                "cache_write": 5,
                "total": 24,
            },
        )
        self.assertEqual(
            agents.normalize_tokens({}),
            {
                "input": 0,
                "output": 0,
                "reasoning": 0,
                "cache_read": 0,
                "cache_write": 0,
                "total": 0,
            },
        )


if __name__ == "__main__":
    unittest.main()
