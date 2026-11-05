"""Report aggregation tests against the checked-in r7 example and synthetic rows."""

from __future__ import annotations

import json
import pathlib
import tempfile
import unittest

try:
    from . import support
except ImportError:
    import support

from benchlib import reporting

CSV_HEADER = [
    "task",
    "agent",
    "rep",
    "passed",
    "duration_s",
    "turns",
    "tool_calls",
    "input_tokens",
    "output_tokens",
    "cache_read_tokens",
    "reasoning_tokens",
    "cost_shared_usd",
    "cost_reported_usd",
    "error",
    "cache_write_tokens",
    "model",
    "provider_kind",
    "agent_model",
    "config_name",
    "pricing_source",
    "provider_route_ok",
    "index_pages",
    "peak_rss_bytes",
    "peak_footprint_bytes",
    "cpu_user_s",
    "cpu_sys_s",
    "cpu_total_s",
    "cpu_per_wall",
]


class R7ExampleTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.rows = reporting.load(support.RAW_R7)
        cls.metadata = reporting.load_metadata(support.RAW_R7)

    def test_raw_log_loads_sixty_rows(self) -> None:
        self.assertEqual(len(self.rows), 60)
        self.assertEqual(
            {row["agent"] for row in self.rows}, {"ogcode", "opencode"}
        )
        for agent in ("ogcode", "opencode"):
            with self.subTest(agent=agent):
                self.assertEqual(
                    sum(1 for row in self.rows if row["agent"] == agent), 30
                )
        self.assertEqual(
            {row["task"] for row in self.rows}, set(support.SIX_TASKS)
        )
        self.assertEqual({row["model"] for row in self.rows}, {"glm-5.3-flash"})

    def test_every_run_passed(self) -> None:
        for agent in ("ogcode", "opencode"):
            with self.subTest(agent=agent):
                passed = sum(
                    1
                    for row in self.rows
                    if row["agent"] == agent and row.get("passed")
                )
                self.assertEqual(passed, 30)

    def test_agent_summaries_match_the_published_totals(self) -> None:
        summaries = {
            agent: reporting.agent_summary(
                [row for row in self.rows if row["agent"] == agent]
            )
            for agent in ("ogcode", "opencode")
        }
        self.assertAlmostEqual(
            summaries["ogcode"]["total_cost_usd"], 0.20054542, delta=1e-6
        )
        self.assertAlmostEqual(
            summaries["opencode"]["total_cost_usd"], 0.26861161, delta=1e-6
        )
        self.assertEqual(summaries["ogcode"]["runs"], 30)
        self.assertEqual(summaries["ogcode"]["solved"], 30)
        self.assertEqual(summaries["opencode"]["runs"], 30)
        self.assertEqual(summaries["opencode"]["solved"], 30)
        self.assertAlmostEqual(
            summaries["ogcode"]["median_cost_per_task_usd"],
            0.005670339999999999,
            delta=1e-9,
        )
        self.assertAlmostEqual(
            summaries["opencode"]["median_cost_per_task_usd"],
            0.006599305,
            delta=1e-9,
        )
        self.assertAlmostEqual(
            summaries["ogcode"]["median_tool_calls"], 6.0, delta=1e-9
        )
        self.assertAlmostEqual(
            summaries["opencode"]["median_tool_calls"], 7.5, delta=1e-9
        )
        self.assertEqual(
            set(summaries["ogcode"]["token_totals"]),
            {"input", "output", "reasoning", "cache_read", "cache_write"},
        )

    def test_metadata_sidecar_loads(self) -> None:
        self.assertIsNotNone(self.metadata)
        assert self.metadata is not None
        self.assertEqual(self.metadata["provider"]["kind"], "ollama-cloud")
        self.assertEqual(self.metadata["benchmark"]["config"], "ollama-cloud-r7")

    def test_summary_schema_and_comparison(self) -> None:
        summary = reporting.build_summary(
            self.rows, "glm-5.3-flash", self.metadata
        )
        self.assertEqual(
            set(summary),
            {
                "schema",
                "generated_at",
                "model",
                "provider",
                "config",
                "pricing_source",
                "runs",
                "tasks",
                "agents",
                "cross_checks",
                "comparison",
            },
        )
        self.assertEqual(summary["schema"], 1)
        self.assertEqual(summary["model"], "glm-5.3-flash")
        self.assertEqual(summary["provider"], "ollama-cloud")
        self.assertEqual(summary["config"], "ollama-cloud-r7")
        self.assertEqual(summary["pricing_source"], "config")
        self.assertEqual(summary["runs"], 60)
        self.assertEqual(summary["tasks"], support.SIX_TASKS)
        self.assertRegex(
            summary["generated_at"], r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$"
        )

        comparison = summary["comparison"]["ogcode_vs_opencode"]
        self.assertAlmostEqual(
            comparison["total_cost_reduction_pct"], 25.34000298795722, delta=1e-6
        )
        self.assertAlmostEqual(
            comparison["median_cost_per_task_reduction_pct"],
            14.076709592904107,
            delta=1e-6,
        )
        self.assertAlmostEqual(
            comparison["median_duration_reduction_pct"],
            27.239654388358343,
            delta=1e-6,
        )
        self.assertAlmostEqual(
            comparison["total_duration_reduction_pct"],
            32.57936507936508,
            delta=1e-6,
        )
        self.assertAlmostEqual(
            comparison["median_tool_calls_ratio"], 0.8, delta=1e-9
        )

    def test_cross_checks(self) -> None:
        checks = reporting.cross_checks(self.rows)
        self.assertEqual(checks["reported_cost_runs"], 60)
        self.assertLess(checks["max_reported_cost_drift_usd"], 1e-12)
        self.assertEqual(checks["failed_verification"], 0)
        self.assertEqual(checks["errors_or_timeouts"], 0)
        self.assertEqual(checks["indexed_ogcode_runs"], 30)
        self.assertEqual(checks["index_pages_min"], 3)
        self.assertEqual(checks["index_pages_max"], 266)
        self.assertAlmostEqual(checks["index_median_build_s"], 7.23, delta=1e-9)
        self.assertEqual(checks["audited_tool_runs"], 30)
        self.assertEqual(checks["provider_routes_checked"], 0)
        self.assertEqual(checks["provider_route_failures"], 0)

    def test_markdown_headline_and_non_overclaiming_language(self) -> None:
        markdown = reporting.build_markdown(
            self.rows, "glm-5.3-flash", self.metadata
        )
        self.assertIn("# Cost per task: ogcode vs opencode", markdown)
        self.assertIn(
            "Model: `glm-5.3-flash` · Provider: `ollama-cloud` · "
            "Config: `ollama-cloud-r7` · 60 runs",
            markdown,
        )
        self.assertIn("## Headline", markdown)
        self.assertIn(
            "- **Cost:** $0.2005 vs $0.2686 total (-25.3%); "
            "$0.0057 vs $0.0066 median per task (-14.1%).",
            markdown,
        )
        self.assertIn("## Per task (median of the repetitions)", markdown)
        self.assertIn(
            "| Task | Agent | Solved | $ / run | Turns | Tool calls | Seconds "
            "| Input tok | Output tok | Cache read tok |",
            markdown,
        )
        self.assertIn("## Overall", markdown)
        self.assertIn(
            "## Ratios (ogcode divided by opencode, per-task medians)", markdown
        )
        self.assertIn("## Cross-checks", markdown)
        self.assertIn("- Both agents solved every run (60/60 green).", markdown)
        self.assertIn("- Runs that failed verification: 0.", markdown)
        self.assertIn("- Runs that errored or timed out: 0.", markdown)
        self.assertIn(
            "- Index built before 30 ogcode runs: 3-266 pages, median 7s to build, "
            "excluded from every figure above.",
            markdown,
        )
        self.assertIn("Tool calls are each agent's own record", markdown)

        lowered = markdown.lower()
        for overclaim in ("cheaper", "better", "proves", "superior", "wins"):
            with self.subTest(word=overclaim):
                self.assertNotIn(overclaim, lowered)

    def test_csv_shape_and_old_rows(self) -> None:
        table = reporting.build_csv(self.rows)
        self.assertEqual(table[0], CSV_HEADER)
        self.assertEqual(len(table), 61)

        positions = {name: index for index, name in enumerate(CSV_HEADER)}
        old_rows = [
            row
            for row in table[1:]
            if row[positions["config_name"]] == ""
            and row[positions["provider_kind"]] == ""
        ]
        self.assertEqual(len(old_rows), 60)
        for row in old_rows:
            self.assertEqual(row[positions["provider_kind"]], "")
            self.assertEqual(row[positions["agent_model"]], "")
            self.assertEqual(row[positions["config_name"]], "")
            self.assertEqual(row[positions["pricing_source"]], "")
            self.assertIsNone(row[positions["provider_route_ok"]])
        indexes = [row[positions["index_pages"]] for row in table[1:]]
        self.assertEqual(sum(1 for value in indexes if value is not None), 30)


class SyntheticRowsTest(unittest.TestCase):
    def setUp(self) -> None:
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.tmp = pathlib.Path(self._tmp.name)

    def test_old_row_without_provider_fields_still_renders(self) -> None:
        rows = [
            {
                "task": "go-bugfix",
                "agent": "ogcode",
                "rep": 1,
                "passed": True,
                "duration_s": 1.5,
                "turns": 2,
                "tool_calls": 3,
                "cost_shared_usd": 0.01,
                "cost_reported_usd": 0.01,
                "tokens": {"input": 100, "output": 20},
                "model": "legacy-model",
            }
        ]
        summary = reporting.build_summary(rows, "legacy-model")
        self.assertIsNone(summary["provider"])
        self.assertIsNone(summary["config"])
        self.assertIsNone(summary["pricing_source"])
        self.assertNotIn("comparison", summary)

        markdown = reporting.build_markdown(rows, "legacy-model", None)
        self.assertNotIn("Provider:", markdown)
        self.assertNotIn("Config:", markdown)
        self.assertNotIn("## Headline", markdown)
        self.assertIn("Model: `legacy-model` · 1 runs", markdown)

        table = reporting.build_csv(rows)
        positions = {name: index for index, name in enumerate(table[0])}
        row = table[1]
        self.assertEqual(row[positions["provider_kind"]], "")
        self.assertEqual(row[positions["agent_model"]], "")
        self.assertEqual(row[positions["config_name"]], "")
        self.assertEqual(row[positions["pricing_source"]], "")
        self.assertIsNone(row[positions["provider_route_ok"]])
        self.assertIsNone(row[positions["index_pages"]])
        for name in (
            "peak_rss_bytes",
            "peak_footprint_bytes",
            "cpu_user_s",
            "cpu_sys_s",
            "cpu_total_s",
            "cpu_per_wall",
        ):
            with self.subTest(column=name):
                self.assertIsNone(row[positions[name]])

    def test_rows_with_resources_render_the_resource_section(self) -> None:
        def row(agent: str, rep: int, peak: int, cpu: float) -> dict:
            return {
                "task": "go-bugfix",
                "agent": agent,
                "rep": rep,
                "passed": True,
                "duration_s": 10.0,
                "cost_shared_usd": 0.01,
                "tokens": {"input": 100, "output": 20},
                "model": "m",
                "resources": {
                    "backend": "libproc",
                    "interval_s": 0.1,
                    "samples": 90,
                    "peak_tree_rss_bytes": peak,
                    "cpu_user_s": cpu * 0.8,
                    "cpu_sys_s": cpu * 0.2,
                    "cpu_total_s": cpu,
                    "cpu_per_wall": 4.0,
                },
            }

        rows = [
            row("ogcode", 1, 200 * 1024 * 1024, 40.0),
            row("ogcode", 2, 220 * 1024 * 1024, 44.0),
            row("opencode", 1, 300 * 1024 * 1024, 30.0),
            row("opencode", 2, 320 * 1024 * 1024, 34.0),
        ]
        summary = reporting.build_summary(rows, "m")
        us = summary["agents"]["ogcode"]
        self.assertEqual(us["resource_runs"], 2)
        self.assertEqual(us["median_peak_rss_bytes"], 210 * 1024 * 1024)
        self.assertEqual(us["max_peak_rss_bytes"], 220 * 1024 * 1024)
        self.assertEqual(us["median_cpu_s"], 42.0)
        self.assertEqual(us["total_cpu_s"], 84.0)
        resources = summary["comparison"]["ogcode_vs_opencode"]["resources"]
        self.assertAlmostEqual(resources["median_peak_rss_ratio"], 210 / 310)
        self.assertAlmostEqual(resources["median_cpu_ratio"], 42 / 32)

        markdown = reporting.build_markdown(rows, "m")
        self.assertIn("## Resources (sampled while each run executed)", markdown)
        self.assertIn("| ogcode | 2 | 210.0 MiB | 220.0 MiB | 42.0 | 84.0 | 4.00 |", markdown)
        self.assertIn("- **Memory:** 210.0 MiB vs 310.0 MiB median peak RSS", markdown)

        table = reporting.build_csv(rows)
        positions = {name: index for index, name in enumerate(table[0])}
        self.assertEqual(table[1][positions["peak_rss_bytes"]], 200 * 1024 * 1024)
        self.assertEqual(table[1][positions["cpu_per_wall"]], 4.0)

    def test_single_agent_report_has_no_headline(self) -> None:
        rows = [
            {
                "task": "go-bugfix",
                "agent": "ogcode",
                "rep": 1,
                "passed": False,
                "cost_shared_usd": 0.0,
                "tokens": {},
                "model": "m",
                "error": "boom",
            }
        ]
        markdown = reporting.build_markdown(rows, "m", None)
        self.assertNotIn("## Headline", markdown)
        self.assertNotIn("## Ratios", markdown)
        self.assertIn("- Runs that failed verification: 1.", markdown)
        self.assertIn("- Runs that errored or timed out: 1.", markdown)
        summary = reporting.build_summary(rows, "m")
        self.assertEqual(summary["cross_checks"]["errors_or_timeouts"], 1)

    def test_load_skips_blank_lines_and_non_objects(self) -> None:
        raw = self.tmp / "raw.jsonl"
        raw.write_text(
            "\n"
            '{"task": "go-bugfix", "agent": "ogcode"}\n'
            "\n"
            "[1, 2, 3]\n"
            "null\n"
            "\n"
        )
        rows = reporting.load(raw)
        self.assertEqual(len(rows), 1)
        self.assertEqual(rows[0]["task"], "go-bugfix")

    def test_load_rejects_invalid_json(self) -> None:
        raw = self.tmp / "bad.jsonl"
        raw.write_text('{"task": "go-bugfix"}\n{not json}\n')
        with self.assertRaises(SystemExit) as caught:
            reporting.load(raw)
        self.assertIn("invalid JSON line", str(caught.exception))

    def test_load_metadata_variants(self) -> None:
        self.assertIsNone(reporting.load_metadata(self.tmp / "missing.jsonl"))

        good = self.tmp / "good"
        good.mkdir()
        (good / "metadata.json").write_text('{"provider": {"kind": "openai"}}')
        self.assertEqual(
            reporting.load_metadata(good / "raw.jsonl"),
            {"provider": {"kind": "openai"}},
        )

        bad = self.tmp / "bad"
        bad.mkdir()
        (bad / "metadata.json").write_text("{broken")
        self.assertIsNone(reporting.load_metadata(bad / "raw.jsonl"))

        not_an_object = self.tmp / "list"
        not_an_object.mkdir()
        (not_an_object / "metadata.json").write_text("[]")
        self.assertIsNone(reporting.load_metadata(not_an_object / "raw.jsonl"))

    def test_write_report_writes_all_three_files(self) -> None:
        rows = reporting.load(support.RAW_R7)
        metadata = reporting.load_metadata(support.RAW_R7)
        out = self.tmp / "out"
        markdown, summary = reporting.write_report(
            rows, out, "glm-5.3-flash", metadata
        )
        self.assertEqual(
            (out / "report.md").read_text(), markdown
        )
        self.assertEqual(
            json.loads((out / "summary.json").read_text()), summary
        )
        csv_lines = (out / "report.csv").read_text().splitlines()
        self.assertEqual(len(csv_lines), 61)
        self.assertEqual(csv_lines[0].split(","), CSV_HEADER)

    def test_write_report_rejects_no_rows(self) -> None:
        with self.assertRaises(SystemExit) as caught:
            reporting.write_report([], self.tmp / "empty")
        self.assertIn("no result rows", str(caught.exception))


class HelpersTest(unittest.TestCase):
    def test_median(self) -> None:
        self.assertEqual(reporting.median([]), 0.0)
        self.assertEqual(reporting.median([3]), 3.0)
        self.assertEqual(reporting.median([1, 5, 2]), 2.0)
        self.assertEqual(reporting.median([1, 2, 3, 4]), 2.5)

    def test_group(self) -> None:
        rows = [
            {"task": "a", "agent": "ogcode"},
            {"task": "a", "agent": "opencode"},
            {"task": "a", "agent": "ogcode"},
        ]
        grouped = reporting.group(rows)
        self.assertEqual(set(grouped), {("a", "ogcode"), ("a", "opencode")})
        self.assertEqual(len(grouped[("a", "ogcode")]), 2)

    def test_money_and_ratio(self) -> None:
        self.assertEqual(reporting.money(0.20054542), "$0.2005")
        self.assertEqual(reporting.money(0), "$0.0000")
        self.assertEqual(reporting.ratio(1.0, 4.0), 0.25)
        self.assertEqual(reporting.ratio(1.0, 0.0), 0.0)

    def test_percent_change(self) -> None:
        self.assertAlmostEqual(reporting.percent_change(80.0, 100.0), -20.0)
        self.assertAlmostEqual(reporting.percent_change(50.0, 40.0), 25.0)
        self.assertEqual(reporting.percent_change(10.0, 0.0), 0.0)

    def test_empty_agent_summary(self) -> None:
        self.assertEqual(reporting.agent_summary([]), {})


if __name__ == "__main__":
    unittest.main()
