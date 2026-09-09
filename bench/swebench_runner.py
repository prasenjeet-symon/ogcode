#!/usr/bin/env python3
"""Run ogcode against SWE-bench Lite and grade it with the official harness.

SWE-bench is not an agent harness -- ``swebench.harness.run_evaluation`` only
*grades* patches. So this runner does the half SWE-bench leaves to you: it drives
ogcode to produce a patch per instance, writes a ``predictions.jsonl``, then
hands that to the grader. Two stages, one command.

Inference runs ogcode **inside each instance's own eval image** (repo already at
``/testbed`` with its dependencies installed), so ogcode's test-running loop --
the whole point of an agent over a one-shot patcher -- actually works. The image
is the same prebuilt one the grader uses, so it is pulled once and reused.

    export ANTHROPIC_API_KEY=...
    PYTHONPATH=bench python3 bench/swebench_runner.py \
      --model anthropic/claude-opus-4-8 \
      --binary-url https://.../ogcode_linux_x86_64_musl.tar.gz

With no ``--instance-ids`` it runs the ten hardest Lite instances (see
swebench_hardest.py). ``--no-eval`` stops after writing predictions so you can
inspect the patches before spending the grader's Docker time.

Caveats (all inherited from the DeepSWE notes in bench/README.md, because the
images share the same shape):

* Eval images are **linux/amd64 only** -- on an arm64 Mac run Docker with Rosetta
  (`colima start --vm-type=vz --vz-rosetta`), never bare QEMU, which silently
  corrupts the Go binary.
* The **published glibc binary will not run** on these images. Pass a portable /
  musl ``--binary-url`` (the repo Dockerfile already builds one statically).
* ~1.2 GB per instance image -- ~12 GB for the hardest ten.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
import time
from dataclasses import dataclass, field
from pathlib import Path
from urllib.parse import urlparse, urlunparse

DATASET = "princeton-nlp/SWE-bench_Lite"

# ogcode has exactly four provider slots. Anything behind a gateway (Gemini,
# DeepSeek, Groq, ...) goes through the openai slot with OPENAI_BASE_URL set.
_PROVIDER_SLOTS: dict[str, list[str]] = {
    "anthropic": ["ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL"],
    "openai": ["OPENAI_API_KEY", "OPENAI_BASE_URL"],
    "openrouter": ["OPENROUTER_API_KEY"],
    "ollama": ["OLLAMA_API_KEY", "OLLAMA_BASE_URL"],
}

_CONTAINER_HOST_GATEWAY = "host.docker.internal"
_LOOPBACK_HOSTS = {"localhost", "127.0.0.1", "0.0.0.0", "::1"}

_INSTRUCTIONS = (
    "The repository is checked out at /testbed, which is your working directory. "
    "Resolve the issue described above by editing the project's source. Do not "
    "edit the test files -- they are how your change will be graded. Run the "
    "project's own tests to check your work before you finish."
)

# Runs one attempt inside the instance image. Values arrive as env vars so
# nothing needs host-side shell quoting. See the module docstring for why this
# is correct even when ogcode commits its work.
_CONTAINER_SCRIPT = r"""
set -u
# Prefer the task's conda env so ogcode's own `python`/`pytest` runs hit the
# environment the fix must satisfy, not the image's system python. Best-effort:
# if the path is elsewhere ogcode still edits, just without running the tests.
if [ -d /opt/miniconda3/envs/testbed/bin ]; then
  export PATH=/opt/miniconda3/envs/testbed/bin:$PATH
fi
command -v curl >/dev/null 2>&1 || { apt-get update -qq && apt-get install -y -qq curl >/dev/null 2>&1; } || true
tmp="$(mktemp -d)"
if ! curl -fsSL "$OGCODE_BINARY_URL" -o "$tmp/o.tgz"; then
  echo "ogcode: download failed from $OGCODE_BINARY_URL" >&2; exit 3
fi
tar -xzf "$tmp/o.tgz" -C "$tmp" || { echo "ogcode: extract failed" >&2; exit 3; }
install -m 0755 "$tmp/ogcode" /usr/local/bin/ogcode 2>/dev/null || cp "$tmp/ogcode" /usr/local/bin/ogcode
if ! ogcode version >/dev/null 2>&1; then
  echo "ogcode: binary will not run on this image -- see the glibc note in bench/README.md" >&2
  exit 4
fi
cd /testbed || { echo "no /testbed in image" >&2; exit 5; }
git config --global --add safe.directory /testbed 2>/dev/null || true
git config user.email bench@ogcode.local 2>/dev/null || true
git config user.name ogcode-bench 2>/dev/null || true
# Pre-seed a hermetic config. Two jobs: it disables MCP/skills, and because
# ogcode's EnsureProjectFile no-ops when ogcode.json already exists, it stops
# ogcode from rewriting the repo's tracked .gitignore (which would otherwise
# land in the patch). Excluded from the diff below regardless.
printf '%s' '{"mcp":{},"skills":{}}' > /testbed/ogcode.json
BASE="$(git rev-parse HEAD)"
# INDEX FIRST. `ogcode index` builds the on-disk workspace index in
# /testbed/.ogcode that codebase_map and deep_search read -- an unbuilt index
# leaves those tools blind, which throws away the main reason to run an agent
# over a one-shot patcher. Non-fatal: a failed index still lets the run proceed.
if [ "$OGCODE_INDEX" = "1" ]; then
  ogcode index $OGCODE_INDEX_MODEL_FLAG </dev/null >/out/ogcode.index.log 2>&1 \
    || echo "ogcode: index step failed (continuing to run)" >&2
fi
ogcode run --output-format json --model "$OGCODE_MODEL" --max-turns "$OGCODE_MAX_TURNS" \
  $OGCODE_AGENT_FLAG -- "$OGCODE_PROMPT" </dev/null >/out/ogcode.json 2>/out/ogcode.stderr.log
echo $? > /out/ogcode.exit
# Stage everything (new files included) and diff against the base commit. Correct
# whether ogcode left its work unstaged OR committed it: on a clean tree the
# index equals HEAD, so `diff --cached BASE` still yields the committed delta.
# The excludes drop ogcode's own artifacts (index db, seeded config, any
# .gitignore touch) so only the agent's real edits reach the grader.
git add -A 2>/dev/null || true
git diff --cached "$BASE" -- . ':(exclude).ogcode' ':(exclude)ogcode.json' ':(exclude).gitignore' ':(exclude)MEMORY.md' \
    > /out/model.patch 2>/dev/null \
  || git diff "$BASE" -- . ':(exclude).ogcode' ':(exclude)ogcode.json' ':(exclude).gitignore' ':(exclude)MEMORY.md' \
    > /out/model.patch 2>/dev/null || true
"""


# --------------------------------------------------------------------- helpers

def load_lite(ids: list[str] | None = None) -> dict[str, dict]:
    """Return {instance_id: row} for SWE-bench Lite's test split.

    Uses the `datasets` library (a swebench dependency, so present wherever the
    grader is). Imported lazily so this module imports without it -- selection
    (swebench_hardest) and the pure helpers below do not need the dataset.
    """
    from datasets import load_dataset

    ds = load_dataset(DATASET, split="test")
    rows = {r["instance_id"]: r for r in ds}
    if ids is None:
        return rows
    missing = [i for i in ids if i not in rows]
    if missing:
        raise KeyError(f"instance ids not in {DATASET}: {', '.join(missing)}")
    return {i: rows[i] for i in ids}


def instance_image(instance_id: str, arch: str = "x86_64",
                   namespace: str = "swebench", tag: str = "latest") -> str:
    """Docker image for an instance. SWE-bench encodes the id into the tag with
    `__` -> `_1776_` (a tag cannot repeat the org separator), e.g.
    django__django-11019 -> swebench/sweb.eval.x86_64.django_1776_django-11019."""
    key = instance_id.replace("__", "_1776_")
    return f"{namespace}/sweb.eval.{arch}.{key}:{tag}"


def _reachable_from_container(url: str, host_addr: str = _CONTAINER_HOST_GATEWAY) -> str:
    """Point a loopback URL at the address the container reaches the host on;
    leave non-loopback URLs alone. Inside a container `localhost` is the
    container, not your machine. `host_addr` is `host.docker.internal` on the
    default bridge, or the internal network's gateway IP under --isolate-network
    (where host.docker.internal is not reachable)."""
    parsed = urlparse(url)
    if (parsed.hostname or "").lower() not in _LOOPBACK_HOSTS:
        return url
    netloc = f"{host_addr}:{parsed.port}" if parsed.port else host_addr
    return urlunparse(parsed._replace(netloc=netloc))


def model_env(model_name: str, environ=os.environ,
              host_addr: str = _CONTAINER_HOST_GATEWAY) -> tuple[str, dict[str, str]]:
    """Split `provider/model` and collect that slot's env vars from `environ`,
    rewriting any loopback base URL to `host_addr` for the container."""
    if "/" not in model_name:
        raise ValueError("model must be 'provider/model', e.g. anthropic/claude-opus-4-8")
    provider, bare = model_name.split("/", 1)
    keys = _PROVIDER_SLOTS.get(provider)
    if keys is None:
        raise ValueError(
            f"ogcode has no '{provider}' slot. Slots: {', '.join(sorted(_PROVIDER_SLOTS))}. "
            "Reach anything else through openai by setting OPENAI_BASE_URL and naming "
            "the model openai/<model>.")
    env: dict[str, str] = {}
    for key in keys:
        value = environ.get(key)
        if value:
            env[key] = _reachable_from_container(value, host_addr) if key.endswith("_BASE_URL") else value
    return bare, env


def ensure_isolated_network(args) -> None:
    """Create (idempotently) the internal Docker network the eval container runs
    on under --isolate-network. `--internal` blocks all egress to the internet;
    the container can still reach the host (hence the model router + binary
    server) at the network's fixed gateway IP. This is what stops the agent from
    fetching the upstream fix -- see bench/README.md."""
    if not args.isolate_network:
        return
    if _docker("network", "inspect", args.isolated_network).returncode == 0:
        return
    r = _docker("network", "create", "--internal",
                "--subnet", args.isolated_subnet, "--gateway", args.isolated_gateway,
                args.isolated_network)
    if r.returncode != 0:
        raise RuntimeError(
            f"could not create isolated network {args.isolated_network} "
            f"({args.isolated_subnet}): {r.stderr.strip()}. Pick a free --isolated-subnet "
            "or pass --no-isolate-network (unsandboxed; results may be contaminated).")


def build_prompt(row: dict) -> str:
    return f"{row['problem_statement'].strip()}\n\n{_INSTRUCTIONS}"


def parse_ogcode_json(raw: str) -> dict | None:
    """Parse `ogcode run --output-format json` output. Older releases print one
    slog line before the document, so skip to the first brace."""
    start = raw.find("{")
    if start == -1:
        return None
    try:
        return json.loads(raw[start:])
    except json.JSONDecodeError:
        return None


def _patch_target_paths(diff: str) -> set[str]:
    """The b-side paths a unified diff modifies."""
    paths: set[str] = set()
    for line in diff.splitlines():
        if line.startswith("+++ "):
            p = line[4:].strip()
            if p not in ("/dev/null",):
                paths.add(re.sub(r"^b/", "", p))
    return paths


def strip_test_files(model_patch: str, test_patch: str) -> tuple[str, list[str]]:
    """Drop any file section of `model_patch` that touches a file the instance's
    test_patch also touches. Grading force-applies test_patch on top, so a model
    edit to those files either loses to it or breaks the apply -- and an agent
    editing the graded tests is contamination regardless. Returns (patch, dropped)."""
    graded = _patch_target_paths(test_patch)
    if not graded or not model_patch.strip():
        return model_patch, []
    kept: list[str] = []
    dropped: list[str] = []
    section: list[str] = []
    section_path = None

    def flush():
        if section_path is None:
            return
        if section_path in graded:
            dropped.append(section_path)
        else:
            kept.extend(section)

    for line in model_patch.splitlines(keepends=True):
        if line.startswith("diff --git "):
            flush()
            section = [line]
            m = re.search(r" b/(\S+)", line)
            section_path = m.group(1) if m else None
        else:
            section.append(line)
    flush()
    return "".join(kept), dropped


# ----------------------------------------------------------------------- model

@dataclass
class Result:
    instance_id: str
    image: str
    wall_s: float = 0.0
    turns: int | None = None
    finish: str | None = None
    input_tokens: int = 0
    output_tokens: int = 0
    cost_usd: float | None = None
    patch_bytes: int = 0
    dropped_test_edits: list[str] = field(default_factory=list)
    indexed: bool | None = None        # did the pre-run index step complete?
    agent_exit: int | None = None
    harness_error: str | None = None   # setup failure, NOT a task failure
    resolved: bool | None = None       # filled in from the grader's report


def _docker(*args: str, **kw) -> subprocess.CompletedProcess:
    return subprocess.run(["docker", *args], capture_output=True, text=True, **kw)


def run_instance(args, iid: str, row: dict, prov_env: dict[str, str],
                 bare_model: str) -> tuple[Result, str]:
    """Run one instance to completion; return (Result, model_patch)."""
    out_dir = (args.work_dir / iid)
    out_dir.mkdir(parents=True, exist_ok=True)
    image = instance_image(iid, namespace=args.image_namespace, tag=args.image_tag)
    res = Result(instance_id=iid, image=image)

    if args.pull:
        pull = _docker("pull", "--platform", args.platform, image, timeout=args.pull_timeout)
        if pull.returncode != 0:
            res.harness_error = f"docker pull failed: {pull.stderr.strip().splitlines()[-1:] or ''}"
            return res, ""

    name = "ogc_" + re.sub(r"[^A-Za-z0-9_.-]", "_", iid)
    _docker("rm", "-f", name)  # clear any leftover from a killed run

    cmd = ["docker", "run", "--rm", "--name", name, "--platform", args.platform]
    if args.isolate_network:
        # Internet-blocked internal network: the agent can reach the model router
        # and binary server (host, at the gateway IP) but cannot fetch the
        # upstream fix. host.docker.internal is not reachable here, so loopback
        # URLs are rewritten to the gateway IP instead.
        host_addr = args.isolated_gateway
        cmd += ["--network", args.isolated_network]
    else:
        host_addr = _CONTAINER_HOST_GATEWAY
        cmd += ["--add-host", "host.docker.internal:host-gateway"]
    cmd += ["-v", f"{out_dir}:/out"]
    run_env = dict(prov_env)
    run_env["OGCODE_MODEL"] = bare_model
    run_env["OGCODE_MAX_TURNS"] = str(args.max_turns)
    run_env["OGCODE_BINARY_URL"] = _reachable_from_container(args.binary_url, host_addr)
    run_env["OGCODE_AGENT_FLAG"] = f"--agent {args.agent}" if args.agent else ""
    run_env["OGCODE_PROMPT"] = build_prompt(row)
    # Index the repo before fixing so codebase_map / deep_search have something
    # to read. Indexing may use a cheaper model than the fix (--index-model).
    run_env["OGCODE_INDEX"] = "1" if args.index else "0"
    idx_model = args.index_model or bare_model
    run_env["OGCODE_INDEX_MODEL_FLAG"] = f"--model {idx_model}" if idx_model else ""
    for key, value in run_env.items():
        cmd += ["-e", f"{key}={value}"]
    cmd += [image, "bash", "-lc", _CONTAINER_SCRIPT]

    started = time.monotonic()
    try:
        proc = subprocess.run(cmd, timeout=args.agent_timeout,
                              stdin=subprocess.DEVNULL, capture_output=True, text=True)
    except subprocess.TimeoutExpired:
        res.wall_s = time.monotonic() - started
        res.harness_error = f"agent exceeded {args.agent_timeout}s"
        _docker("kill", name)
        _docker("rm", "-f", name)
        return res, ""
    res.wall_s = time.monotonic() - started
    # Preserve the container's own stdout/stderr. When the binary won't run in the
    # image (e.g. a non-static musl binary on a glibc image), the failure message
    # lands here and nowhere in /out, so this is the only breadcrumb.
    container_log = ((proc.stdout or "") + (proc.stderr or "")).strip()
    if container_log:
        try:
            (out_dir / "container.log").write_text(container_log)
        except OSError:
            pass

    def _tail(text: str) -> str:
        lines = [ln for ln in text.splitlines() if ln.strip()]
        return lines[-1][:200] if lines else ""

    # ---- read what the container left in /out
    exit_path = out_dir / "ogcode.exit"
    res.agent_exit = int(exit_path.read_text().strip()) if exit_path.exists() else None
    if res.agent_exit in (3, 4, 5):
        errlog = out_dir / "ogcode.stderr.log"
        stderr_tail = _tail(errlog.read_text()) if errlog.exists() else _tail(container_log)
        res.harness_error = f"container setup failed (exit {res.agent_exit}): {stderr_tail}"
        return res, ""

    summary_path = out_dir / "ogcode.json"
    if not summary_path.exists():
        hint = _tail(container_log)
        res.harness_error = f"no ogcode.json produced ({hint})" if hint else "no ogcode.json produced"
        return res, ""
    summary = parse_ogcode_json(summary_path.read_text()) or {}
    tokens = summary.get("tokens") or {}
    res.turns = summary.get("num_turns")
    res.finish = summary.get("finish")
    res.input_tokens = tokens.get("input", 0) or 0
    res.output_tokens = tokens.get("output", 0) or 0
    res.cost_usd = summary.get("cost_usd")

    if args.index:
        idx_log = out_dir / "ogcode.index.log"
        res.indexed = (idx_log.exists()
                       and "Indexing complete" in idx_log.read_text(errors="replace"))

    patch_path = out_dir / "model.patch"
    patch = patch_path.read_text() if patch_path.exists() else ""
    patch, dropped = strip_test_files(patch, row.get("test_patch", "") or "")
    res.dropped_test_edits = dropped
    res.patch_bytes = len(patch)
    return res, patch


def write_prediction(fp, iid: str, model_label: str, patch: str) -> None:
    fp.write(json.dumps({
        "instance_id": iid,
        "model_name_or_path": model_label,
        "model_patch": patch,
    }) + "\n")
    fp.flush()


# ------------------------------------------------------------------ evaluation

def run_eval(args, ids: list[str]) -> dict:
    """Invoke the official grader over `ids` and return its report dict."""
    cmd = [sys.executable, "-m", "swebench.harness.run_evaluation",
           "--dataset_name", DATASET, "--split", args.split,
           "--predictions_path", str(args.out), "--run_id", args.run_id,
           "--max_workers", str(args.max_workers), "--instance_ids", *ids]
    # `--namespace` was removed in swebench 5.x (prebuilt images from the swebench
    # Docker Hub org are the default); older versions need it to pull rather than
    # build. Pass it only if this install's grader still accepts it.
    if args.image_namespace:
        try:
            h = subprocess.run([sys.executable, "-m", "swebench.harness.run_evaluation", "-h"],
                               capture_output=True, text=True, timeout=60)
            if "--namespace" in (h.stdout + h.stderr):
                cmd += ["--namespace", args.image_namespace]
        except Exception:  # noqa: BLE001 -- probe failure just means don't pass it
            pass
    print(f"\n$ {' '.join(cmd)}\n")
    proc = subprocess.run(cmd, cwd=args.work_dir)
    if proc.returncode != 0:
        print(f"warning: grader exited {proc.returncode}", file=sys.stderr)
    # run_evaluation writes <model>.<run_id>.json near its cwd; rglob to avoid
    # depending on how it sanitises the model label or which subdir it uses.
    reports = sorted(args.work_dir.rglob(f"*.{args.run_id}.json"),
                     key=lambda p: p.stat().st_mtime)
    if not reports:
        print("warning: no grader report found", file=sys.stderr)
        return {}
    return json.loads(reports[-1].read_text())


# --------------------------------------------------------------------- reporting

def summarise(results: list[Result], report: dict | None) -> None:
    print(f"\n{'instance_id':<42}{'status':<11}{'turns':>6}{'in':>10}"
          f"{'out':>9}{'sec':>7}")
    print("-" * 85)
    for r in results:
        if r.harness_error:
            status = "HARNESS"
        elif r.resolved is True:
            status = "RESOLVED"
        elif r.patch_bytes == 0:
            status = "empty"
        else:
            status = "fail"
        turns = r.turns if r.turns is not None else "-"
        print(f"{r.instance_id:<42}{status:<11}{str(turns):>6}"
              f"{r.input_tokens:>10}{r.output_tokens:>9}{r.wall_s:>7.0f}")
    print("-" * 85)

    n = len(results)
    graded = [r for r in results if not r.harness_error]
    if report is None:
        print("resolved          not graded (--no-eval)")
    elif graded:
        resolved = [r for r in graded if r.resolved is True]
        print(f"resolved          {len(resolved)}/{len(graded)} graded  "
              f"({100 * len(resolved) / len(graded):.0f}%)")
    else:
        print("resolved          0/0 graded")
    empty = [r for r in graded if r.patch_bytes == 0]
    if empty:
        print(f"empty patches     {len(empty)}  (agent produced no diff)")
    indexed = [r for r in results if r.indexed is not None]
    if indexed:
        print(f"indexed first     {sum(1 for r in indexed if r.indexed)}/{len(indexed)}")
    costs = [r.cost_usd for r in results if r.cost_usd is not None]
    print(f"total cost        {('$%.4f' % sum(costs)) if costs else 'n/a (model not in ogcode catalog)'}")
    print(f"total tokens      in={sum(r.input_tokens for r in results):,} "
          f"out={sum(r.output_tokens for r in results):,}")

    dropped = [(r.instance_id, r.dropped_test_edits) for r in results if r.dropped_test_edits]
    if dropped:
        print("\ntest-file edits stripped from patches (agent touched graded tests):")
        for iid, files in dropped:
            print(f"  {iid}: {', '.join(files)}")

    errors = [r for r in results if r.harness_error]
    if errors:
        print(f"\nharness errors ({len(errors)}/{n}) — setup bugs, NOT task failures:")
        for r in errors:
            print(f"  {r.instance_id}: {r.harness_error}")
    if report:
        rep_err = report.get("error_ids") or []
        if rep_err:
            print(f"\ngrader errors ({len(rep_err)}): {', '.join(rep_err)}")


# ---------------------------------------------------------------------- driver

def main() -> int:
    ap = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--model", default="anthropic/claude-opus-4-8",
                    help="provider/model; provider selects an ogcode slot")
    ap.add_argument("--binary-url", default="",
                    help="ogcode tarball to install in the image. The published "
                         "glibc build will NOT run here -- pass a portable/musl one.")
    ap.add_argument("--instance-ids", nargs="*",
                    help="instances to run (default: the ten hardest, from swebench_hardest)")
    ap.add_argument("--limit", type=int, default=0, help="cap the instance list (0 = all)")
    ap.add_argument("--agent", default="", choices=["", "build", "plan"],
                    help="ogcode --agent (default: ogcode's own default)")
    ap.add_argument("--index", dest="index", action="store_true", default=True,
                    help="run `ogcode index` before the fix so codebase_map / "
                         "deep_search have a populated index (default: on)")
    ap.add_argument("--no-index", dest="index", action="store_false")
    ap.add_argument("--index-model", default="",
                    help="model for the index step (default: same as --model). A "
                         "cheaper model here cuts the cost of indexing a large repo.")
    ap.add_argument("--max-turns", type=int, default=250)
    ap.add_argument("--agent-name", default="ogcode",
                    help="model_name_or_path written into predictions / the report")
    ap.add_argument("--agent-timeout", type=int, default=3 * 3600,
                    help="per-instance wall limit; matches SWE-bench's 3h default")

    ap.add_argument("--platform", default="linux/amd64")
    ap.add_argument("--image-namespace", default="swebench",
                    help="Docker Hub org of the prebuilt images and the grader's "
                         "--namespace (empty builds locally)")
    ap.add_argument("--image-tag", default="latest")
    ap.add_argument("--pull", dest="pull", action="store_true", default=True)
    ap.add_argument("--no-pull", dest="pull", action="store_false")
    ap.add_argument("--pull-timeout", type=int, default=1800)

    ap.add_argument("--isolate-network", dest="isolate_network", action="store_true", default=True,
                    help="run the eval container on an internet-blocked internal network so "
                         "the agent can reach the model router but NOT fetch the upstream fix. "
                         "Default: on. Required for a valid score.")
    ap.add_argument("--no-isolate-network", dest="isolate_network", action="store_false",
                    help="give the container full internet (UNSANDBOXED -- the agent can look "
                         "up the answer; results are not a valid capability measure)")
    ap.add_argument("--isolated-network", default="ogcode-bench-isolated",
                    help="name of the internal Docker network to create/use")
    ap.add_argument("--isolated-subnet", default="10.88.0.0/24",
                    help="subnet for the internal network (pick one free on the host)")
    ap.add_argument("--isolated-gateway", default="10.88.0.1",
                    help="gateway IP of --isolated-subnet; the address the container reaches "
                         "the host (model router, binary server) on")

    ap.add_argument("--eval", dest="eval", action="store_true", default=True)
    ap.add_argument("--no-eval", dest="eval", action="store_false",
                    help="stop after writing predictions, before grading")
    ap.add_argument("--split", default="test")
    ap.add_argument("--run-id", default="ogcode-hardest10")
    ap.add_argument("--max-workers", type=int, default=4)

    ap.add_argument("--work-dir", type=Path, default=Path("bench_swe_out"),
                    help="per-instance outputs, predictions and the report land here")
    ap.add_argument("--out", type=Path, help="predictions.jsonl (default: <work-dir>/predictions.jsonl)")
    args = ap.parse_args()

    if not args.binary_url:
        print("error: --binary-url is required (the published glibc binary will not "
              "run on the eval images; see bench/README.md).", file=sys.stderr)
        return 2

    if args.instance_ids:
        ids = list(args.instance_ids)
    else:
        from swebench_hardest import HARDEST_10
        ids = list(HARDEST_10)
    if args.limit:
        ids = ids[:args.limit]

    # Absolute: the per-instance dir is bind-mounted into the eval container, and
    # `docker -v` reads a relative path as a (invalid) named volume, not a host dir.
    args.work_dir = args.work_dir.resolve()
    args.work_dir.mkdir(parents=True, exist_ok=True)
    if args.out is None:
        args.out = args.work_dir / "predictions.jsonl"

    try:
        rows = load_lite(ids)
    except Exception as exc:  # noqa: BLE001
        print(f"error: could not load {DATASET}: {exc}", file=sys.stderr)
        return 2

    # The container reaches the host (model router, binary server) at this address.
    host_addr = args.isolated_gateway if args.isolate_network else _CONTAINER_HOST_GATEWAY
    bare_model, prov_env = model_env(args.model, host_addr=host_addr)
    if not prov_env:
        provider = args.model.split("/", 1)[0]
        print(f"error: no credentials found for the '{provider}' slot "
              f"({', '.join(_PROVIDER_SLOTS[provider])}).", file=sys.stderr)
        return 2

    try:
        ensure_isolated_network(args)
    except RuntimeError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2

    index_desc = "off" if not args.index else (args.index_model or "same as --model")
    net_desc = (f"isolated ({args.isolated_network})" if args.isolate_network
                else "OPEN INTERNET (unsandboxed -- results may be contaminated)")
    print(f"model={args.model}  index={index_desc}  network={net_desc}  "
          f"instances={len(ids)}  work-dir={args.work_dir}")
    results: list[Result] = []
    with open(args.out, "w") as preds:
        for i, iid in enumerate(ids, 1):
            print(f"[{i}/{len(ids)}] {iid} ... ", end="", flush=True)
            res, patch = run_instance(args, iid, rows[iid], prov_env, bare_model)
            results.append(res)
            write_prediction(preds, iid, args.agent_name, patch)
            idx_mark = ""
            if args.index and not res.harness_error:
                idx_mark = "indexed, " if res.indexed else "index failed, "
            note = res.harness_error or (idx_mark + f"{res.patch_bytes}B patch"
                                         + (f", {res.turns} turns" if res.turns else ""))
            print(f"{note} [{res.wall_s:.0f}s]")
    print(f"\nwrote {args.out}")

    report = None
    if args.eval:
        report = run_eval(args, ids)
        resolved_ids = set(report.get("resolved_ids") or [])
        for r in results:
            if not r.harness_error:
                r.resolved = r.instance_id in resolved_ids
    else:
        print("skipping grading (--no-eval); inspect the patches, then rerun "
              f"the grader with:\n  python -m swebench.harness.run_evaluation "
              f"--dataset_name {DATASET} --split {args.split} "
              f"--predictions_path {args.out} --run_id {args.run_id} "
              f"--instance_ids {' '.join(ids)}")

    summarise(results, report)
    return 0


if __name__ == "__main__":
    sys.exit(main())
