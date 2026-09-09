#!/usr/bin/env python3
"""Pick the empirically-hardest SWE-bench Lite instances.

"Hardest" here is not a proxy -- it is how few of the SWE-bench leaderboard
submissions ever solved the instance. The per-instance verdicts live in
github.com/swe-bench/experiments: every Lite submission ships a
``results/results.json`` whose ``resolved`` key lists the instance ids it
solved. This reads all of them, counts resolutions per instance, and takes the
lowest.

The catch that shapes everything below: **35 of the 300 Lite instances have
never been solved by any submission**, so the primary signal saturates 35 wide.
Ranking within that tie needs a secondary key, and the honest one is
reference-patch complexity -- the size and spread of the gold fix and how many
tests it must satisfy -- which is itself a difficulty proxy. So the ten this
yields are "never solved by anyone, and of those the largest, most demanding
fixes".

The result computed on 2026-09-08 (from 84 Lite submissions) is baked in as
``HARDEST_10`` so a benchmark run is reproducible without the network. Recompute
and diff against it with ``--verify``; regenerate with ``--n 10``.
"""

from __future__ import annotations

import argparse
import json
import sys
import urllib.request
from collections import defaultdict

# github.com/swe-bench/experiments layout. The API call lists the submission
# folders; the raw CDN serves each results.json (and unlike the API is not
# rate-limited to 60/hour, which matters at ~84 fetches).
_API = "https://api.github.com/repos/swe-bench/experiments/contents/evaluation/lite"
_RAW = ("https://raw.githubusercontent.com/swe-bench/experiments/main"
        "/evaluation/lite/{sub}/results/results.json")

# Computed 2026-09-08 over 84 list-format Lite submissions. Every id below was
# resolved by 0 of them; the order is by descending reference-patch complexity
# (see patch_complexity), which is the tie-breaker within the never-solved set.
HARDEST_10 = [
    "django__django-11019",
    "django__django-16820",
    "pallets__flask-5063",
    "sphinx-doc__sphinx-7686",
    "sympy__sympy-19254",
    "sympy__sympy-20322",
    "scikit-learn__scikit-learn-25638",
    "sympy__sympy-14317",
    "sympy__sympy-14308",
    "django__django-11564",
]


def _get(url: str) -> bytes:
    req = urllib.request.Request(url, headers={"User-Agent": "ogcode-bench"})
    with urllib.request.urlopen(req, timeout=45) as r:
        return r.read()


def list_submissions() -> list[str]:
    return [e["name"] for e in json.loads(_get(_API))]


def fetch_resolved(sub: str) -> list[str] | None:
    """The instance ids `sub` resolved, or None for the 2024-era count-only
    format that carries no per-instance data (and so cannot vote)."""
    try:
        data = json.loads(_get(_RAW.format(sub=sub)))
    except Exception as exc:  # noqa: BLE001 -- a missing/parse-failed report just abstains
        print(f"  warn: {sub}: {exc}", file=sys.stderr)
        return None
    resolved = data.get("resolved")
    if not isinstance(resolved, list):
        return None
    # Any id mentioned in any list is one this submission knew about -- used as
    # the denominator so an instance is not penalised for submissions that
    # post-date its addition to the set.
    known: set[str] = set()
    for value in data.values():
        if isinstance(value, list):
            known.update(value)
    fetch_resolved._known[sub] = known  # type: ignore[attr-defined]
    return resolved


fetch_resolved._known = {}  # type: ignore[attr-defined]


def solve_rates() -> tuple[dict[str, dict], int]:
    """Map every seen instance id to {resolved_by, seen_by}, plus the number of
    submissions that voted."""
    resolved_by: dict[str, int] = defaultdict(int)
    seen_by: dict[str, int] = defaultdict(int)
    voters = 0
    for sub in list_submissions():
        resolved = fetch_resolved(sub)
        if resolved is None:
            continue
        voters += 1
        for iid in fetch_resolved._known[sub]:  # type: ignore[attr-defined]
            seen_by[iid] += 1
        for iid in resolved:
            resolved_by[iid] += 1
    rates = {
        iid: {"resolved_by": resolved_by[iid], "seen_by": seen_by[iid]}
        for iid in seen_by
    }
    return rates, voters


def diff_churn(patch: str) -> tuple[int, int, int]:
    """(changed lines, hunks, files) in a unified diff -- context lines excluded."""
    changed = hunks = files = 0
    for line in patch.splitlines():
        if line.startswith(("+++", "---")):
            continue
        if line.startswith("diff --git"):
            files += 1
        elif line.startswith("@@"):
            hunks += 1
        elif line and line[0] in "+-":
            changed += 1
    return changed, hunks, max(files, 1)


def _as_list(value) -> list:
    return value if isinstance(value, list) else json.loads(value)


def patch_complexity(row: dict) -> dict:
    """Difficulty proxy for one dataset row. Churn dominates; tests-to-satisfy
    and hunk spread add on. Lite fixes are nearly all single-file, so file count
    barely discriminates and is weighted lightly."""
    changed, hunks, files = diff_churn(row["patch"])
    f2p = len(_as_list(row["FAIL_TO_PASS"]))
    p2p = len(_as_list(row["PASS_TO_PASS"]))
    score = changed + 2 * f2p + 3 * hunks + 5 * files
    return {"score": score, "changed_lines": changed, "hunks": hunks,
            "files": files, "fail_to_pass": f2p, "pass_to_pass": p2p}


def rank_hardest(n: int, load_rows) -> list[dict]:
    """Rank instances hardest-first and return the top `n`.

    `load_rows(ids)` returns {instance_id: dataset_row} -- injected so this stays
    independent of how the dataset is loaded (see swebench_runner.load_lite).
    """
    rates, voters = solve_rates()
    print(f"voting submissions: {voters}   instances seen: {len(rates)}",
          file=sys.stderr)

    # Only the never-solved set can be the hardest, and it is far larger than any
    # sane n, so rank within it alone.
    zero = [iid for iid, r in rates.items() if r["resolved_by"] == 0]
    print(f"never-solved instances: {len(zero)}", file=sys.stderr)
    rows = load_rows(zero)

    scored = []
    for iid in zero:
        c = patch_complexity(rows[iid])
        scored.append({"instance_id": iid, "repo": rows[iid]["repo"],
                       "resolved_by": 0, "seen_by": rates[iid]["seen_by"], **c})
    # score desc, then most-attempted-yet-unsolved, then id for determinism.
    scored.sort(key=lambda s: (-s["score"], -s["seen_by"], s["instance_id"]))
    return scored[:n]


def _print_table(rows: list[dict]) -> None:
    print(f"\n{'instance_id':<42}{'repo':<26}{'score':>6}{'chg':>5}"
          f"{'hnk':>5}{'F2P':>5}{'P2P':>6}{'seen':>6}")
    print("-" * 101)
    for s in rows:
        print(f"{s['instance_id']:<42}{s['repo']:<26}{s['score']:>6}"
              f"{s['changed_lines']:>5}{s['hunks']:>5}{s['fail_to_pass']:>5}"
              f"{s['pass_to_pass']:>6}{s['seen_by']:>6}")


def main() -> int:
    ap = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--n", type=int, default=10, help="how many to select")
    ap.add_argument("--verify", action="store_true",
                    help="recompute and diff against the baked HARDEST_10")
    ap.add_argument("--json", action="store_true", help="emit JSON, not a table")
    args = ap.parse_args()

    # Imported here so `import swebench_hardest` (for HARDEST_10) never drags in
    # the dataset/`datasets` dependency.
    from swebench_runner import load_lite

    rows = rank_hardest(args.n, load_lite)
    ids = [r["instance_id"] for r in rows]

    if args.verify:
        if ids[:len(HARDEST_10)] == HARDEST_10:
            print("OK: baked HARDEST_10 still matches the live leaderboard")
            return 0
        print("DRIFT: baked HARDEST_10 no longer matches. Recomputed:")
        _print_table(rows)
        print("\nIDs:", " ".join(ids))
        return 1

    if args.json:
        print(json.dumps(rows, indent=2))
    else:
        _print_table(rows)
        print("\nIDs:", " ".join(ids))
    return 0


if __name__ == "__main__":
    sys.exit(main())
