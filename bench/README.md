# Running ogcode against DeepSWE

[DeepSWE](https://deepswe.datacurve.ai/) is 113 authored (never-upstreamed)
software-engineering tasks graded by hand-written functional verifiers. It runs
on [Pier](https://github.com/datacurve-ai/pier), a Harbor-compatible harness.

`pier_ogcode.py` is a Pier agent adapter for ogcode. Pier loads custom agents by
import path, so nothing here needs to be merged into Pier itself.

## Setup

```bash
uv tool install datacurve-pier
git clone https://github.com/datacurve-ai/deep-swe
```

## Run

```bash
export ANTHROPIC_API_KEY=...
PYTHONPATH=bench pier run -p deep-swe/tasks \
  --agent-import-path pier_ogcode:OgCode \
  --model anthropic/claude-opus-4-8 \
  --limit 2 --n-attempts 1
```

`--model` takes Pier's `provider/model` form. The provider half selects one of
ogcode's four slots (`anthropic`, `openai`, `openrouter`, `ollama`) and the
model half is passed to `ogcode run --model`. Anything behind a gateway goes
through the `openai` slot with `OPENAI_BASE_URL` set — the adapter adds that
host to the sandbox allowlist automatically.

Per-run knobs:

```bash
--agent-kwarg version=0.31.0   # ogcode release to install (default: pinned)
--agent-kwarg max_turns=400    # ogcode run --max-turns
--agent-kwarg agent=plan       # ogcode run --agent
```

## Using a local Ollama router

```bash
export OLLAMA_BASE_URL=http://localhost:8090/v1
PYTHONPATH=bench pier run -p deep-swe/tasks \
  --agent-import-path pier_ogcode:OgCode \
  --model ollama/glm-5.3-flash:cloud \
  --limit 2 --n-attempts 1
```

A loopback base URL is rewritten to `host.docker.internal` for the sandbox and
added to the allowlist — inside a container `localhost` is the container, not
your machine. Docker Desktop resolves that name already; on Linux add
`--extra-hosts host.docker.internal:host-gateway`.

Two caveats for this route: models served this way are not in ogcode's static
catalog, so every task reports `cost_usd: null`; and the endpoint does no
prompt caching, so the whole system prompt and tool schema is re-sent every
turn (~25k input tokens per turn observed), which compounds fast against a
250-turn cap.

## How it works

- **Install** — downloads the pinned `ogcode_<v>_linux_<arch>.tar.gz` release
  into `/usr/local/bin`. Arch is detected, so amd64 and arm64 task images both
  work.
- **Run** — `ogcode run --output-format json` in `/app`, stdin from
  `/dev/null`, stdout to `/logs/agent/ogcode.json`, stderr to a sibling log.
- **Metrics** — the JSON summary's token counts, turn count and cost estimate
  are mapped onto Pier's `AgentContext`, which is where DeepSWE's cost and
  output-token figures come from.

DeepSWE tasks run the agent with `network_mode = "no-network"`. Only the hosts
in `network_allowlist()` are reachable: GitHub for the install, plus the
inference endpoint.

## Notes

- **The published Linux binary does not run on the task images.** DeepSWE
  images are Debian 12 (glibc 2.36); the v0.30.0 `linux_x86_64` release
  requires `GLIBC_2.38`, so the install step downloads and extracts fine and
  then dies on `ogcode version`. Until the release is built against an older
  glibc (or statically against musl, as `Dockerfile` already does), pass
  `--agent-kwarg binary_url=...` pointing at a portable build.

- **On an arm64 Mac under QEMU, ogcode cannot run in these containers at all.**
  Go's garbage collector is corrupted by QEMU x86_64 emulation: a twelve-line
  program that only allocates and compiles a regexp dies in
  `gcBgMarkStartWorkers`, and ogcode itself panics with
  `growslice: len out of range` followed by `marked free object in span`. This
  is not language-specific to the task -- ogcode is a Go binary, so it fails on
  Python tasks too. Use Rosetta emulation instead (Colima:
  `colima start --vm-type=vz --vz-rosetta`; Docker Desktop: enable Rosetta in
  settings) or run on an amd64 host. A run that happens *not* to panic is not
  trustworthy either -- the same corruption can alter behaviour silently.
- **Under Colima, the job directory must live inside `$HOME`.** Colima only
  mounts `$HOME` into its VM, so `-o /tmp/...` makes the verifier's
  `/logs/verifier` bind mount a black hole: no `reward.txt` appears and every
  trial fails with `RewardFileNotFoundError` regardless of merit.
- **The model endpoint must be on port 80 or 443.** Pier's squid template
  hardcodes `acl Safe_ports port 80 443` and denies everything else *before*
  consulting the domain allowlist, so a gateway on e.g. `:8090` returns an HTML
  `ERR_ACCESS_DENIED` page to the agent.
- **Task images are `linux/amd64` only.** On an arm64 host every task runs
  emulated, which is slow enough to matter against the 3h per-task agent
  timeout. Prefer `--env modal` or an amd64 box for a full sweep.
- **Disk.** Roughly 2 GB per task image, ~220 GB for all 113. Prune as you go
  or run a subset.
- **Cost is null for uncatalogued models.** `ogcode run` prices a run from the
  static model catalog in `internal/provider/models_catalog.go`. A model that
  is not listed there (any OpenRouter or Ollama model, or a gateway model)
  reports `cost_usd: null`, and Pier will show no cost for it.

## Validating the adapter

Without running a task:

```bash
PYTHONPATH=bench python -c "
from pier.agents.factory import AgentFactory
a = AgentFactory.create_agent_from_import_path('pier_ogcode:OgCode',
    logs_dir=__import__('pathlib').Path('/tmp'), model_name='anthropic/claude-opus-4-8')
print(a.name(), a.version(), a.network_allowlist().domains)"
```

---

# Aider Polyglot runner

`polyglot_runner.py` runs ogcode against the
[Aider polyglot exercises](https://github.com/Aider-AI/polyglot-benchmark) --
225 hard Exercism problems. Unlike DeepSWE this needs no prebuilt images, so it
runs natively on arm64 and costs cents.

```bash
git clone https://github.com/Aider-AI/polyglot-benchmark
OLLAMA_BASE_URL=http://localhost/llm/v1 python3 bench/polyglot_runner.py \
  --dataset polyglot-benchmark --lang python,go --limit 5 \
  --model glm-5.3-flash:cloud --out results.json
```

Each exercise is staged into a temp copy with `.meta/` removed (it holds the
reference solution), the agent gets `.docs/instructions.md`, and the language's
tests decide pass/fail on their exit code. A failing first attempt gets a
second one with the test output fed back, which is the published protocol.

Reported per run: solve rate, first-attempt rate, median turns, median wall
clock, total tokens, and cost when the model is in ogcode's catalog. Harness
errors (agent timeout, unparseable output, missing toolchain) are listed
separately from task failures -- they are bugs in the setup, not scores.

`python` and `go` are supported. `javascript`, `java` and `cpp` each need a
per-exercise dependency install (npm/gradle/cmake) that dominates runtime and
needs the network, so they are deliberately left out.

## Comparing against other agents

`--agent-cmd` is a shell template, so the same staging, prompting and scoring
drives any coding-agent CLI. `{prompt}` and `{model}` are substituted
shell-quoted; `{max_turns}` is substituted raw.

```bash
# ogcode (the default)
--agent-cmd 'ogcode run --output-format json --max-turns {max_turns} --model {model} -- {prompt}'

# mini-swe-agent -- the scaffold every DeepSWE leaderboard row uses
--agent-cmd 'mini -m {model} -t {prompt} -y -l 0'

# aider
--agent-cmd 'aider --model {model} --yes --no-auto-commits --message {prompt}'
```

Run the same `--lang`/`--limit` with each agent and the same model, and the
difference is your harness. That is a stronger comparison than any published
leaderboard row, because model version, machine, dataset and day are all held
fixed.

Two rules the runner follows to keep this fair:

- **Exit status is never a success signal.** Agents disagree about it --
  mini-swe-agent exits non-zero on a normal finish -- so only the language's
  test command decides pass/fail. The single exception is exit 127, which means
  the `--agent-cmd` itself is wrong and is reported as a harness error.
- **Token and turn metrics are best-effort.** ogcode emits a JSON summary on
  stdout; most agents do not. Their absence is not an error. Solve rate,
  first-attempt rate and wall clock stay comparable across every agent, and
  those are the columns a cross-agent ranking rests on.

mini-swe-agent needs a few env vars to run non-interactively against an
OpenAI-compatible endpoint (this keeps its config out of your home directory):

```bash
export MSWEA_GLOBAL_CONFIG_DIR=/tmp/mswea MSWEA_CONFIGURED=true \
       MSWEA_SILENT_STARTUP=1 MSWEA_COST_TRACKING=ignore_errors
export OPENAI_API_KEY=dummy OPENAI_API_BASE=http://localhost/llm/v1
```

## What it is and is not good for

**Good for:** a fast local regression on the mechanics -- does ogcode edit the
file it was told to, produce valid syntax, and repair itself from a failing
test run. Minutes, not hours; cents, not dollars.

**Not a scaffold ranking.** The upstream harness drives aider itself, so every
third party (including this runner) scores the dataset its own way and the
numbers are only loosely comparable. The exercises are also small,
self-contained, and public -- so contamination is likely, and long-horizon
behaviour (multi-file work, committing results) is not exercised at all. Use
Terminal-Bench on Harbor for a real harness comparison.

---

# SWE-bench Lite

[SWE-bench Lite](https://www.swebench.com/) is 300 real GitHub issues, each with
a repo snapshot and the tests that decide the fix. Its official harness
(`swebench.harness.run_evaluation`) only *grades* patches -- it does not run an
agent -- so `swebench_runner.py` does the other half: it drives ogcode to a patch
per instance, writes a `predictions.jsonl`, then hands that to the grader.

Inference runs ogcode **inside each instance's own eval image**, where the repo
is already checked out at the base commit with its dependencies installed, so
ogcode's test-running loop actually works. That image is the same prebuilt one
the grader uses, so it is pulled once and reused for both stages.

## Setup

```bash
pip install swebench          # pulls in `datasets`, which the runner uses too
```

## Run

```bash
export ANTHROPIC_API_KEY=...
PYTHONPATH=bench python3 bench/swebench_runner.py \
  --model anthropic/claude-opus-4-8 \
  --binary-url https://.../ogcode_linux_x86_64_musl.tar.gz
```

With no `--instance-ids` this runs the **ten hardest Lite instances** (see
*Selecting the hardest* below). `--model` is the same `provider/model` form the
other adapters use. Useful knobs:

```bash
--instance-ids django__django-11019 sympy__sympy-19254   # run specific ones
--limit 3                 # first N of the list (a cheap smoke test)
--agent plan              # ogcode --agent
--max-turns 400
--index-model <m>         # cheaper model for the index step (default: --model)
--no-index                # skip the pre-run index (measure the agent without it)
--no-eval                 # stop after predictions; grade later yourself
--max-workers 8           # grader parallelism
--no-pull                 # images already local
```

### GLM 5.3 flash through the local Ollama router

```bash
export OLLAMA_BASE_URL=http://localhost:8090/v1   # the colima router
export OLLAMA_API_KEY=...
PYTHONPATH=bench python3 bench/swebench_runner.py \
  --model ollama/glm-5.3-flash:cloud \
  --binary-url https://.../ogcode_linux_x86_64_musl.tar.gz
```

A loopback `OLLAMA_BASE_URL` is rewritten to `host.docker.internal` for the
container automatically (inside a container `localhost` is the container). Make
sure the router is reachable from there -- bound to `0.0.0.0`, or the host
gateway routed to `localhost` (Docker Desktop does this; on Colima add
`--extra-hosts` / bind the router wide). Unlike the Pier route there is no squid
proxy in the way, so the `:8090` port is fine.

Two things to expect on this route, both from the router doing no prompt caching:

* **`cost_usd` is null** -- a router model is not in ogcode's static catalog.
* **Every turn re-sends the whole system prompt and tools** (~25k input tokens
  observed), which compounds hard against `--max-turns` *and* against the index
  step below. On the paid Ollama Cloud backend that is real money -- prefer a
  cheap `--index-model`, and consider a smaller `--max-turns` for a first pass.

## Selecting the hardest

`swebench_hardest.py` computes "hardest" empirically: it reads every SWE-bench
Lite submission in [swe-bench/experiments](https://github.com/swe-bench/experiments)
and counts how many ever resolved each instance. **35 of the 300 have never been
solved by any submission**, so the primary signal saturates and the tie is broken
by reference-patch complexity (size and spread of the gold fix, tests to satisfy).
The ten this produced are baked into `HARDEST_10` for reproducibility; `--verify`
recomputes and diffs against it.

```bash
PYTHONPATH=bench python3 bench/swebench_hardest.py --n 10      # print the table
PYTHONPATH=bench python3 bench/swebench_hardest.py --verify    # still current?
```

Because all ten are 0/84 on the leaderboard, **expect ogcode to score at or near
0/10.** This slice is a mechanics-and-hard-behaviour probe -- does ogcode produce
a clean, applicable patch and repair itself on genuinely hard issues -- not a
headline number. For a representative figure, pass a broader `--instance-ids`
list (or all 300 ids from the dataset); mind the ~360 GB of images that implies.

## How it works

- **Index first** -- inside the container the runner runs `ogcode index` before
  `ogcode run`. `index` builds the on-disk workspace index in `/testbed/.ogcode`
  that ogcode's `codebase_map` and `deep_search` tools read; skip it and those
  tools have nothing to read, which is most of the reason to run ogcode over a
  one-shot patcher. On by default; `--no-index` measures the agent without it,
  `--index-model` runs it on a cheaper model. Indexing a large repo (django,
  sympy) is itself many IndexAgent calls and counts toward the per-instance
  timeout -- see the cost note in the GLM section above.
- **Inference** -- the runner then `docker run`s the eval image
  (`swebench/sweb.eval.x86_64.<id with __ → _1776_>:latest`), installs the
  `--binary-url` ogcode, runs `ogcode run --output-format json` in `/testbed`,
  and extracts `git diff --cached <base>` as the patch. That diff form is correct
  whether ogcode leaves its work unstaged or commits it.
- **Network isolation (`--isolate-network`, on by default)** -- the eval
  container runs on an `--internal` Docker network (`--isolated-subnet`, default
  `10.88.0.0/24`), which blocks all egress to the internet. The agent can still
  reach the host -- and thus the model router and the binary server -- at the
  network's fixed gateway IP (`--isolated-gateway`, default `10.88.0.1`), so
  loopback URLs are rewritten to that IP rather than `host.docker.internal`
  (unreachable on an internal network). **This is not optional hardening:**
  without it the container has full internet and ogcode fetches the upstream
  merged PR and applies it verbatim (observed: `pallets__flask-5063` "resolved"
  by copying the real fix), which makes the score meaningless. `--no-isolate-network`
  restores full internet for debugging and prints a contamination warning.
- **Patch hygiene** -- a hermetic `ogcode.json` is pre-seeded in `/testbed` so
  ogcode neither enables MCP/skills nor rewrites the repo's `.gitignore`; logs go
  to a mounted `/out`. The diff then excludes ogcode's own artifacts (`.ogcode/`,
  `ogcode.json`, `.gitignore`) so only real edits remain. Finally, any file
  section touching a path the instance's `test_patch` also touches is stripped
  (the grader force-applies `test_patch`, and editing the graded tests is
  contamination); stripped files are listed in the summary.
- **Grading** -- `run_evaluation` over the same `--instance_ids`, reusing the
  prebuilt images. Use **swebench 3.x** (`pip install 'swebench>=3,<4'`): 5.x
  drops `--namespace` and expects a per-instance `image` field the classic
  `princeton-nlp/SWE-bench_Lite` dataset lacks (`KeyError: 'image'`). The runner
  probes `run_evaluation -h` and passes `--namespace` only when supported. The
  grading container keeps normal network (the patch is already fixed, so there is
  nothing to contaminate). The run's report is read back for the resolved set.
- **Metrics** -- ogcode's JSON summary gives turns, tokens and cost; the report
  gives resolved/unresolved. Setup failures (image won't pull, binary won't run,
  agent timeout) are reported as **harness errors**, separately from task
  failures, exactly as the polyglot runner does.

## Notes

- **Eval images are `linux/amd64` only.** On an arm64 Mac run Docker with Rosetta
  (`colima start --vm-type=vz --vz-rosetta`, or enable it in Docker Desktop),
  never bare QEMU -- it corrupts the Go binary (see the DeepSWE notes above).
- **The published glibc binary will not run** on these images. `--binary-url` is
  required; point it at a portable/musl build (the repo `Dockerfile` builds one
  statically).
- **`--work-dir` must be on a Docker-shared path.** The runner bind-mounts a
  per-instance dir into the container as `/out`. Its default (`bench_swe_out` in
  the repo) is fine; a path Docker does not share into its VM -- `/var/folders/...`
  on Docker Desktop, anything outside `$HOME` under Colima -- silently mounts an
  empty dir, so no `ogcode.json` or patch comes back and every instance looks like
  a harness error. Same trap as the DeepSWE `-o` note above.
- **Disk.** ~1.2 GB per instance image -- ~12 GB for the hardest ten, ~360 GB for
  all 300. The hardest-ten default keeps it small.
- **Cost is null for uncatalogued models**, same as everywhere: a model absent
  from `internal/provider/models_catalog.go` reports `cost_usd: null`.

## Validating without a full run

The pure logic (image naming, provider slots, patch stripping, summary parsing)
has no Docker or dataset dependency:

```bash
PYTHONPATH=bench python3 -c "
import swebench_runner as R, swebench_hardest as H
print(R.instance_image('django__django-11019'))
print('hardest[0]:', H.HARDEST_10[0])
print(R.model_env('anthropic/claude-opus-4-8', environ={'ANTHROPIC_API_KEY':'x'}))"
```
