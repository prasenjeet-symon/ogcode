#!/usr/bin/env python3
"""Run ogcode against the Aider polyglot exercises.

The upstream benchmark harness drives aider itself, so this is a separate
runner over the same dataset (github.com/Aider-AI/polyglot-benchmark). It
follows the published protocol: the agent gets the exercise instructions, and
on a failing test run it gets a second attempt with the test output fed back.

Scores are therefore comparable to aider's leaderboard only loosely -- every
third party runs the dataset with its own runner. Treat this as a harness
regression test (does ogcode edit the right file, produce valid syntax, and
repair itself?) rather than as a published number.

    python3 bench/polyglot_runner.py --dataset ../polyglot-benchmark \
        --lang python,go --limit 5 --model glm-5.3-flash:cloud

Exercises run in a temp copy with .meta/ removed, since .meta holds the
reference solution.
"""

from __future__ import annotations

import argparse
import json
import shlex
import shutil
import statistics
import subprocess
import sys
import tempfile
import time
from dataclasses import dataclass, field, asdict
from pathlib import Path

# How each language's tests are run inside the exercise directory. Both work
# from a stock toolchain: Exercism's Python tests are unittest-based (no pytest
# needed) and its Go exercises ship a go.mod.
#
# javascript/java/cpp are deliberately absent: each needs a per-exercise
# dependency install (npm/gradle/cmake) that dominates the runtime and needs
# the network, which makes them a poor fit for a quick harness check.
LANGUAGES: dict[str, dict] = {
    "python": {
        "test_cmd": lambda test_files: [
            sys.executable, "-m", "unittest", *(f.stem for f in test_files)
        ],
    },
    "go": {
        "test_cmd": lambda _test_files: ["go", "test", "./..."],
    },
}


@dataclass
class Attempt:
    passed: bool
    wall_s: float
    turns: int | None = None
    finish: str | None = None
    input_tokens: int = 0
    output_tokens: int = 0
    cost_usd: float | None = None
    error: str | None = None


@dataclass
class Result:
    exercise: str
    language: str
    repeat: int = 1
    passed: bool = False
    attempts: list[Attempt] = field(default_factory=list)

    @property
    def wall_s(self) -> float:
        return sum(a.wall_s for a in self.attempts)

    @property
    def input_tokens(self) -> int:
        return sum(a.input_tokens for a in self.attempts)

    @property
    def output_tokens(self) -> int:
        return sum(a.output_tokens for a in self.attempts)

    @property
    def cost_usd(self) -> float | None:
        costs = [a.cost_usd for a in self.attempts if a.cost_usd is not None]
        return sum(costs) if costs else None

    @property
    def turns(self) -> int:
        return sum(a.turns or 0 for a in self.attempts)


def parse_ogcode_json(raw: str) -> dict | None:
    """Parse `ogcode run --output-format json` stdout.

    Releases before the stderr-logging fix emit one slog line to stdout ahead
    of the document, so skip to the first brace instead of failing outright.
    """
    start = raw.find("{")
    if start == -1:
        return None
    try:
        return json.loads(raw[start:])
    except json.JSONDecodeError:
        return None


def exercise_files(src: Path) -> tuple[list[str], list[str]]:
    """Return (solution_files, test_files) from the exercise's .meta/config.json.

    The manifest is what makes this language-agnostic -- it names which file the
    agent is meant to edit and which file holds the tests, so the runner never
    has to guess from naming conventions.
    """
    cfg = json.loads((src / ".meta" / "config.json").read_text())
    files = cfg.get("files", {})
    return list(files.get("solution", [])), list(files.get("test", []))


def build_prompt(src: Path, solution: list[str], tests: list[str]) -> str:
    docs = []
    for name in ("instructions.md", "instructions.append.md"):
        path = src / ".docs" / name
        if path.exists():
            docs.append(path.read_text().strip())
    body = "\n\n".join(docs)
    return (
        f"{body}\n\n"
        f"Implement your solution in: {', '.join(solution)}\n"
        f"Do not modify the test files: {', '.join(tests)}\n"
        "Run the tests yourself and make them pass before you finish."
    )


def stage(src: Path, workdir: Path) -> None:
    """Copy the exercise, minus .meta -- which contains the reference solution."""
    shutil.copytree(src, workdir, ignore=shutil.ignore_patterns(".meta"))
    # ogcode merges any ogcode.json found in this directory or a parent. The
    # temp dir has no repo root to stop the upward search, so write an explicit
    # empty one to keep runs hermetic and MCP-free.
    (workdir / "ogcode.json").write_text(json.dumps({"mcp": {}, "skills": {}}))


def run_tests(lang: str, workdir: Path, tests: list[str], timeout: int) -> tuple[bool, str]:
    cmd = LANGUAGES[lang]["test_cmd"]([Path(t) for t in tests])
    try:
        proc = subprocess.run(
            cmd, cwd=workdir, capture_output=True, text=True, timeout=timeout
        )
    except subprocess.TimeoutExpired:
        return False, f"test run exceeded {timeout}s"
    except FileNotFoundError as exc:
        return False, f"toolchain missing: {exc}"
    # Judge on the return code, never on parsing the output.
    output = (proc.stdout or "") + (proc.stderr or "")
    return proc.returncode == 0, output


def agent_command(args, prompt: str) -> str:
    """Render the agent invocation, shell-quoting the substituted values."""
    return args.agent_cmd.format(
        prompt=shlex.quote(prompt),
        model=shlex.quote(args.model),
        max_turns=args.max_turns,
    )


def run_agent(args, workdir: Path, prompt: str, timeout: int) -> tuple[Attempt, str]:
    cmd = agent_command(args, prompt)
    started = time.monotonic()
    try:
        proc = subprocess.run(
            cmd, cwd=workdir, capture_output=True, text=True, shell=True,
            timeout=timeout, stdin=subprocess.DEVNULL,
        )
    except subprocess.TimeoutExpired:
        return Attempt(passed=False, wall_s=time.monotonic() - started,
                       error=f"agent exceeded {timeout}s"), ""
    wall = time.monotonic() - started

    # Token/turn metrics are best-effort: ogcode emits a JSON summary on stdout,
    # most other agents do not. Their absence is not an error -- pass rate,
    # first-attempt rate and wall clock stay comparable across every agent, and
    # those are the columns a cross-agent ranking rests on.
    summary = parse_ogcode_json(proc.stdout) or {}
    tokens = summary.get("tokens") or {}
    attempt = Attempt(
        passed=False,  # set by the caller after the tests run
        wall_s=wall,
        turns=summary.get("num_turns"),
        finish=summary.get("finish"),
        input_tokens=tokens.get("input", 0) or 0,
        output_tokens=tokens.get("output", 0) or 0,
        cost_usd=summary.get("cost_usd"),
    )
    # Exit status is NOT a success signal: agents disagree about it (mini-swe-agent
    # exits non-zero on a normal finish), so the tests are the only arbiter. The
    # one exception is 127, which means the command itself does not exist -- a
    # broken --agent-cmd rather than a failed attempt, and worth stopping on.
    if proc.returncode == 127:
        tail = (proc.stderr or "command not found").strip().splitlines()[-1:]
        attempt.error = f"agent command not found: {tail[0][:200] if tail else ''}"
    return attempt, summary.get("result", "")


def run_exercise(args, lang: str, src: Path, repeat: int = 1) -> Result:
    result = Result(exercise=src.name, language=lang, repeat=repeat)
    solution, tests = exercise_files(src)
    if not solution or not tests:
        result.attempts.append(Attempt(False, 0.0, error="config.json lists no solution/test files"))
        return result

    with tempfile.TemporaryDirectory(prefix=f"polyglot-{src.name}-") as tmp:
        workdir = Path(tmp) / src.name
        stage(src, workdir)
        prompt = build_prompt(src, solution, tests)

        for attempt_no in range(1, args.attempts + 1):
            attempt, _ = run_agent(args, workdir, prompt, args.agent_timeout)
            if attempt.error:
                result.attempts.append(attempt)
                break

            passed, output = run_tests(lang, workdir, tests, args.test_timeout)
            attempt.passed = passed
            result.attempts.append(attempt)
            if passed:
                result.passed = True
                break
            if attempt_no < args.attempts:
                # The published protocol: show the agent its failing tests and
                # let it try again.
                prompt = (
                    "Your previous attempt failed its tests. Fix the "
                    f"implementation in {', '.join(solution)}.\n\n"
                    f"Test output:\n{output[-4000:]}"
                )
    return result


def collect(dataset: Path, langs: list[str], limit: int | None) -> list[tuple[str, Path]]:
    found: list[tuple[str, Path]] = []
    for lang in langs:
        root = dataset / lang / "exercises" / "practice"
        if not root.is_dir():
            print(f"warning: no exercises for {lang} under {root}", file=sys.stderr)
            continue
        dirs = sorted(d for d in root.iterdir() if d.is_dir())
        if limit:
            dirs = dirs[:limit]
        found += [(lang, d) for d in dirs]
    return found


def summarise(results: list[Result], repeats: int) -> None:
    if not results:
        print("no exercises run")
        return

    print()
    if repeats > 1:
        # One row per exercise: how often it was solved, and how much the wall
        # clock moved between identical runs.
        groups: dict[tuple[str, str], list[Result]] = {}
        for r in results:
            groups.setdefault((r.language, r.exercise), []).append(r)
        print(f"{'exercise':<30} {'lang':<8} {'solved':>8} {'wall min/med/max (s)':>24} {'turns':>7}")
        print("-" * 82)
        for (lang, name), runs in groups.items():
            walls = sorted(r.wall_s for r in runs)
            solved = sum(1 for r in runs if r.passed)
            turns = statistics.median([r.turns for r in runs])
            print(f"{name[:29]:<30} {lang:<8} {solved:>4}/{len(runs):<3} "
                  f"{walls[0]:>7.0f} /{statistics.median(walls):>7.0f} /{walls[-1]:>7.0f} "
                  f"{turns:>7.0f}")
    else:
        print(f"{'exercise':<34} {'lang':<8} {'result':<7} {'turns':>6} {'in':>9} {'out':>8} {'sec':>7}")
        print("-" * 82)
        for r in results:
            print(f"{r.exercise[:33]:<34} {r.language:<8} "
                  f"{'PASS' if r.passed else 'fail':<7} {r.turns:>6} "
                  f"{r.input_tokens:>9} {r.output_tokens:>8} {r.wall_s:>7.1f}")
    print("-" * 82)

    n = len(results)
    solved = [r for r in results if r.passed]
    print(f"solve rate        {len(solved)}/{n} runs  ({100 * len(solved) / n:.0f}%)")
    print(f"first-attempt     {sum(1 for r in solved if len(r.attempts) == 1)}/{n}")
    print(f"median turns      {statistics.median([r.turns for r in results]):.0f}")
    walls = sorted(r.wall_s for r in results)
    print(f"wall (s)          min={walls[0]:.0f} median={statistics.median(walls):.0f} max={walls[-1]:.0f}")
    if len(walls) > 1:
        # The spread is the point: if it exceeds the gap between two agents,
        # neither the ranking nor the sample size means anything yet.
        print(f"wall spread       {walls[-1] / max(walls[0], 0.1):.1f}x between fastest and slowest run")
    print(f"total tokens      in={sum(r.input_tokens for r in results):,} "
          f"out={sum(r.output_tokens for r in results):,}")
    costs = [r.cost_usd for r in results if r.cost_usd is not None]
    print(f"total cost        {('$%.4f' % sum(costs)) if costs else 'n/a (model not in ogcode catalog)'}")

    errors = [(r.exercise, a.error) for r in results for a in r.attempts if a.error]
    if errors:
        print(f"\nharness errors ({len(errors)}) — these are not task failures:")
        for name, err in errors[:10]:
            print(f"  {name}: {err}")


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--dataset", required=True, type=Path,
                    help="clone of github.com/Aider-AI/polyglot-benchmark")
    ap.add_argument("--lang", default="python,go",
                    help=f"comma-separated, from: {', '.join(LANGUAGES)}")
    ap.add_argument("--limit", type=int, default=5, help="exercises per language (0 = all)")
    ap.add_argument("--model", default="", help="substituted into --agent-cmd as {model}")
    ap.add_argument("--agent-name", default="", help="label for the report (defaults to the command's first word)")
    ap.add_argument(
        "--agent-cmd",
        default=("ogcode run --output-format json "
                 "--max-turns {max_turns} --model {model} -- {prompt}"),
        help=(
            "Shell command that runs one attempt in the exercise directory. "
            "{prompt}, {model} and {max_turns} are substituted (the first two "
            "shell-quoted). Examples:\n"
            "  ogcode:          the default\n"
            "  aider:           'aider --model {model} --yes --no-auto-commits --message {prompt}'\n"
            "  mini-swe-agent:  'mini -m {model} -t {prompt} -y'"
        ),
    )
    ap.add_argument("--max-turns", type=int, default=40)
    ap.add_argument("--attempts", type=int, default=2,
                    help="2 matches the published protocol (retry with test output)")
    ap.add_argument("--repeats", type=int, default=1,
                    help="independent runs per exercise; >1 exposes run-to-run variance, "
                         "which can exceed the difference between two agents")
    ap.add_argument("--agent-timeout", type=int, default=900)
    ap.add_argument("--test-timeout", type=int, default=300)
    ap.add_argument("--out", type=Path, help="write results.json here")
    args = ap.parse_args()

    langs = [l.strip() for l in args.lang.split(",") if l.strip()]
    unknown = [l for l in langs if l not in LANGUAGES]
    if unknown:
        print(f"error: unsupported language(s): {', '.join(unknown)}. "
              f"Supported: {', '.join(LANGUAGES)}", file=sys.stderr)
        return 2
    if not args.dataset.is_dir():
        print(f"error: dataset not found at {args.dataset}", file=sys.stderr)
        return 2

    todo = collect(args.dataset, langs, args.limit or None)
    if not todo:
        print("error: no exercises matched", file=sys.stderr)
        return 2

    agent_label = args.agent_name or args.agent_cmd.split()[0]
    total_runs = len(todo) * args.repeats
    print(f"agent={agent_label}  model={args.model or '(provider default)'}  "
          f"exercises={len(todo)}  repeats={args.repeats}  runs={total_runs}")
    results: list[Result] = []
    run_no = 0
    for rep in range(1, args.repeats + 1):
        for lang, src in todo:
            run_no += 1
            tag = f"{lang}/{src.name}" + (f" (run {rep}/{args.repeats})" if args.repeats > 1 else "")
            print(f"[{run_no}/{total_runs}] {tag} ... ", end="", flush=True)
            r = run_exercise(args, lang, src, repeat=rep)
            results.append(r)
            note = "PASS" if r.passed else "fail"
            errs = [a.error for a in r.attempts if a.error]
            print(f"{note}{' (' + errs[0] + ')' if errs else ''} "
                  f"[{r.turns} turns, {r.wall_s:.0f}s]")

    summarise(results, args.repeats)

    if args.out:
        args.out.parent.mkdir(parents=True, exist_ok=True)
        args.out.write_text(json.dumps([asdict(r) | {
            "passed": r.passed, "turns": r.turns, "wall_s": r.wall_s,
            "input_tokens": r.input_tokens, "output_tokens": r.output_tokens,
            "cost_usd": r.cost_usd,
        } for r in results], indent=2))
        print(f"\nwrote {args.out}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
