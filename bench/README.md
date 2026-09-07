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
