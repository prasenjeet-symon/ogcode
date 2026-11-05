#!/usr/bin/env python3
"""Build markdown, CSV and JSON reports from a benchmark ``raw.jsonl``.

    python3 report.py
    python3 report.py --raw results/ollama-cloud/raw.jsonl

The report uses medians across repetitions, recomputes every dollar figure from
the shared price table, and prints cross-checks for agent-reported cost,
verification failures, indexing and provider routing.  ``summary.json`` is
written beside the human report for dashboards or release automation.
"""

from __future__ import annotations

import argparse
import pathlib
from typing import Optional

from benchlib import config, reporting


def main(argv: Optional[list[str]] = None) -> int:
    parser = argparse.ArgumentParser(
        prog="report.py",
        description="Aggregate benchmark raw.jsonl into report.md, report.csv and summary.json.",
    )
    default_raw = config.PACKAGE_ROOT / "results" / "raw.jsonl"
    parser.add_argument("--raw", default=str(default_raw), help="path to raw.jsonl")
    parser.add_argument(
        "--out",
        help="output directory (default: the directory containing --raw)",
    )
    parser.add_argument("--model", help="override the model label in the report")
    args = parser.parse_args(argv)

    raw = pathlib.Path(args.raw).expanduser().resolve()
    rows = reporting.load(raw)
    if not rows:
        raise SystemExit(f"no rows in {raw}")
    out_dir = (
        pathlib.Path(args.out).expanduser().resolve()
        if args.out
        else raw.parent
    )
    metadata = reporting.load_metadata(raw)
    markdown, summary = reporting.write_report(
        rows, out_dir, model=args.model, metadata=metadata
    )
    print(markdown)
    print(f"wrote {out_dir / 'report.md'}, {out_dir / 'report.csv'} and {out_dir / 'summary.json'}")
    if summary.get("comparison"):
        comparison = summary["comparison"]["ogcode_vs_opencode"]
        print(
            "cost reduction: "
            f"{comparison['total_cost_reduction_pct']:.1f}% total, "
            f"{comparison['median_cost_per_task_reduction_pct']:.1f}% median per task"
        )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
