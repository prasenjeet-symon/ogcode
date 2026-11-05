# ogcode-benchmark

`ogcode-benchmark` is a standalone, dependency-free harness for comparing the
`ogcode` and `opencode` coding agents on the same software-engineering tasks.
Each attempt starts from a fresh copy of a fixture, is checked to be failing
before the agent starts, runs the agent non-interactively under a wall-clock
timeout, and is verified again after the agent stops. The harness reports solve
rate, wall-clock time, turns, tool calls, token counts and a dollar cost
computed from one shared price table.

Version 1.0.2. It measures cost, wall-clock time, tool calls and solve rate on
six fixed tasks. It is not a general model-capability leaderboard, and its
results should not be read as one. Version 1.0.2 adds a per-fixture index cache
(build once with `--build-index`, restore into every attempt) and per-run peak
RSS and CPU accounting, plus a serve-mode probe that needs no model calls.
Version 1.0.1 refreshed the bundled sample and docs; 1.0.0 produced the r8
sample.

## Quick start

Requirements:

- Python 3.10 or newer. The harness uses only the Python standard library.
- `git`.
- The `ogcode` and `opencode` CLIs on `PATH`, or explicit paths passed with
  `--ogcode` and `--opencode`.
- One provider API key.
- For the language fixtures: Python for the four Python tasks, Go for
  `go-bugfix`, and Node.js for `js-bugfix`.

Run from the package root (the `benchmark/` directory in the source tree, or
`ogcode-benchmark/` after unzipping the release archive):

```sh
export OLLAMA_API_KEY="..."

python3 run_benchmark.py --dry-run
python3 run_benchmark.py
python3 report.py --raw results/ollama-cloud/raw.jsonl
```

`--dry-run` validates the plan and prints the effective settings without
network access or model calls. The default run uses the `ollama-cloud` preset,
five repetitions of all six tasks with both agents: 60 attempts. A real run
spends money.

The report files are written next to `raw.jsonl`. The run writes `raw.jsonl`,
`metadata.json`, `logs/`, `toolcalls/` and internal `.pristine/` fixture
snapshots; `report.py` creates `report.md`, `report.csv` and `summary.json`.

## Provider presets

| Preset | `provider.kind` | Default model | Credential variable |
|---|---|---|---|
| `ollama-cloud` (default) | `ollama-cloud` | `glm-5.3-flash` | `OLLAMA_API_KEY` |
| `openrouter` | `openrouter` | `z-ai/glm-4.6` | `OPENROUTER_API_KEY` |
| `anthropic` | `anthropic` | `claude-sonnet-4-5` | `ANTHROPIC_API_KEY` |
| `openai` | `openai` | `gpt-5.1` | `OPENAI_API_KEY` |
| `custom-openai-compatible` | `custom` | `your-model-id` (template) | `BENCHMARK_API_KEY` (template) |

Presets can be selected by name or by path:

```sh
python3 run_benchmark.py --config openrouter
python3 run_benchmark.py --config configs/custom-openai-compatible.json
python3 run_benchmark.py --list-configs
```

For `ollama-cloud`, `openrouter`, `openai` and `custom`, ogcode is routed
through its OpenAI-compatible slot; opencode uses its built-in provider for the
first-party presets, or a generated OpenAI-compatible config for a custom
endpoint. For `anthropic`, both use the Anthropic API. The harness removes other
provider credentials from the child environment, so an exported key for a
different provider cannot silently change the route. See
[docs/providers.md](docs/providers.md) for the full routing and environment
matrix.

## Commands

All commands are run from the package root.

| Flag | Meaning |
|---|---|
| `--config NAME\|PATH` | Preset name under `configs/` or a JSON config path. Default: `ollama-cloud`. |
| `--list-configs` | List bundled presets with their provider kind and model, then exit. |
| `--version` | Print the package version, then exit. |
| `--tasks LIST` | Comma-separated task names, or `all`. Default: the config's `run.tasks`. |
| `--agents LIST` | Comma-separated agents from `ogcode,opencode`. Default: the config's `run.agents`. |
| `--reps N` | Repetitions per task and agent. Default: 5 in the bundled configs. |
| `--jobs N` | Agent runs to execute concurrently. Default: 1. |
| `--model ID` | Override the canonical model id and clear per-agent model overrides. |
| `--ogcode-model ID` | Override only the model string passed to ogcode. |
| `--opencode-model ID` | Override only the model string passed to opencode. |
| `--timeout SECONDS` | Wall-clock limit per agent run. Default: 600. |
| `--verify-timeout SECONDS` | Limit per verification. Default: 300. |
| `--index-timeout SECONDS` | Limit for the ogcode index build. Default: 900. |
| `--build-index` | Build the index for every selected task once, store it in the cache, and exit. |
| `--rebuild-index` | Rebuild cached index entries instead of restoring them. |
| `--index-cache DIR` | Index cache directory. Default: `~/.cache/ogcode-benchmark/indexes`. |
| `--out DIR` | Results directory. Default: `run.output_dir` from the config. |
| `--pricing PATH` | Price table JSON. Default: `pricing.json`. |
| `--ogcode PATH` | ogcode binary or absolute path. Default: `ogcode`. |
| `--opencode PATH` | opencode binary or absolute path. Default: `opencode`. |
| `--keep` | Keep each temporary worktree and record its path in the row. |
| `--dry-run` | Validate and print the plan without spending money. |

Examples:

```sh
# One task, one agent, three repetitions.
python3 run_benchmark.py --config openrouter --tasks py-bugfix --agents ogcode --reps 3

# Build the index once per fixture, then run without paying for it again.
python3 run_benchmark.py --config openrouter --build-index
python3 run_benchmark.py --config openrouter --reps 5

# Parallel jobs and a separate results directory.
python3 run_benchmark.py --config anthropic --jobs 2 --out results/anthropic-parallel

# Check a custom endpoint's plan without contacting it.
python3 run_benchmark.py --config configs/custom-openai-compatible.json --dry-run
```

`--dry-run` prints the version, resolved config, provider kind and display
name, base URL, whether the key variable is set, routing slots, model, resolved
price and its source, tasks, agents, repetitions, jobs, timeouts, output
directory, agent binaries and the exact commands that would run. It ends with
warnings for a missing key, a missing price or a missing binary, and states
that no model calls were made. It does not check whether a key is valid or an
endpoint is reachable.

`report.py` reads a result directory and regenerates reports:

```sh
python3 report.py --raw results/ollama-cloud/raw.jsonl
python3 report.py --raw results/ollama-cloud/raw.jsonl --out /tmp/report
python3 report.py --raw results/ollama-cloud/raw.jsonl --model my-label
```

`--raw` defaults to `results/raw.jsonl` in the package, so pass the path of the
run you want to report. `--out` defaults to the directory containing the raw
file. The script prints the Markdown report to stdout and writes `report.md`,
`report.csv` and `summary.json`.

## Serve-mode resource probe

`serve_probe.py` measures the cost of running each server at rest, with no
model calls. It starts `ogcode serve` and `opencode serve` on free loopback
ports, samples the process tree every 100 ms from spawn until the server has
been idle for `--idle-s`, and stops it again.

```sh
python3 serve_probe.py --reps 3 --json results/serve-probe.json \
  --markdown results/serve-probe.md
```

| Flag | Meaning |
|---|---|
| `--reps N` | Cold starts per agent. Default: 3. |
| `--idle-s SECONDS` | Idle window sampled after the server reports ready. Default: 5. |
| `--settle-s SECONDS` | Grace period between readiness and the first steady sample. Default: 2. |
| `--timeout-s SECONDS` | Limit for one server to become ready. Default: 120. |
| `--agents LIST` | Comma-separated agents from `ogcode,opencode`. Default: both. |
| `--ogcode PATH` / `--opencode PATH` | Binary to probe. Default: `ogcode` / `opencode`. |
| `--json PATH` | Write machine-readable results. |
| `--markdown PATH` | Write a Markdown table. |

Readiness is a successful TCP connect to the port the server printed or was
given, so it measures process start plus listener setup, not first request
latency. Each agent runs in a throwaway scratch directory with an isolated
`XDG_CONFIG_HOME` (opencode) or `OGCODE_LOG_DIR` (ogcode), so neither picks up
the user's own configuration. The probe reports median and maximum time to
ready, peak and steady resident set size for the whole process tree, total CPU
consumed, binary size and the peak process count. It makes no model calls and
needs no API key.

## Configuration

A config is a JSON object validated by `benchlib/config.py`. The bundled files
use `"$schema": "../config.schema.json"`; `config.schema.json` is provided for
editor completion and early validation. The loader is authoritative.

```json
{
  "$schema": "../config.schema.json",
  "name": "my-run",
  "description": "Example endpoint and model.",
  "provider": {
    "kind": "custom",
    "base_url": "https://llm.example.com/v1",
    "api_key_env": "MY_LLM_KEY",
    "name": "My endpoint",
    "opencode_provider": "benchmark"
  },
  "model": {
    "id": "my-model",
    "pricing": {
      "input": 0.2,
      "output": 0.8,
      "cache_read": 0.05,
      "cache_write": 0.2
    }
  },
  "agents": {
    "ogcode": {
      "model": "my-model",
      "extra_args": []
    },
    "opencode": {
      "model": "benchmark/my-model",
      "extra_args": []
    }
  },
  "run": {
    "reps": 5,
    "tasks": "all",
    "agents": ["ogcode", "opencode"],
    "jobs": 1,
    "timeout_s": 600,
    "verify_timeout_s": 300,
    "index_timeout_s": 900,
    "output_dir": "results/my-run"
  }
}
```

Fields:

- `name` is required by the schema and is recorded in metadata and reports.
  If omitted, the loader falls back to the config file stem.
- `description` is informational.
- `provider.kind` is one of `ollama-cloud`, `openrouter`, `anthropic`,
  `openai` or `custom`.
- `provider.base_url` and `provider.api_key_env` are required for `custom`.
  A custom endpoint must use an `http://` or `https://` URL.
- `provider.name` is a display name. `provider.opencode_provider` overrides the
  provider id opencode uses and causes a generated temporary opencode config,
  except for Anthropic, which must keep the `anthropic` provider id.
- `model.id` is the canonical model id used for pricing and metadata.
- `model.pricing` is optional. When present it overrides `pricing.json`;
  `cache_read` and `cache_write` are optional and fall back to the input price.
- `agents.ogcode.model` and `agents.opencode.model` are optional. By default
  ogcode receives the raw `model.id` and opencode receives
  `<opencode_provider>/<model.id>`. `extra_args` are inserted before the task
  prompt.
- `run.reps`, `run.tasks`, `run.agents`, `run.jobs`, `run.timeout_s`,
  `run.verify_timeout_s`, `run.index_timeout_s` and `run.output_dir` appear in
  the example above. `tasks` is `"all"` or a non-empty list of task names.
  Unknown keys are rejected.
- `run.output_dir` is resolved relative to the package root, even when the
  config file lives elsewhere. An absolute path is used as-is. `--out`
  overrides it.

The bundled configs use five repetitions, all tasks, both agents, one job, a
600-second run timeout, a 300-second verification timeout, a 900-second index
timeout and an output directory under `results/<config-name>`.

## How the measurement works

- A fresh temporary copy of the task fixture is created for every attempt and
  committed as a throwaway git repository. Runs do not share state.
- The task prompt is the text of `tasks/<task>/task.md`, stripped and passed
  as one argument rather than interpolated into a shell command.
- The shipped fixture must fail verification before the agent starts. If it
  already passes, the run stops instead of measuring anything.
- The agent runs autonomously with a wall-clock timeout. There is no turn cap.
- The verifier restores the task's test files from the fixture commit before
  running the suite, so edits to those tests are discarded. A run is solved
  only when the verifier exits 0 after the agent stops.
- For ogcode, project config denies all skills and disables globally configured
  MCP servers. opencode runs with a temporary `XDG_CONFIG_HOME` containing an
  empty MCP config. Other provider credentials are scrubbed from both child
  environments.
- For ogcode, the repository index is built once per fixture and kept in the
  index cache. Before each attempt it is restored into the new worktree, so the
  attempt pays a copy instead of a full build; index time is timed separately
  and excluded from the agent's wall-clock time.
- The cache key covers the fixture content, the model, the provider and its base
  URL, and the ogcode version. `--build-index` fills it before a campaign and
  exits; a plain run reuses it and builds only what is missing. The index labels
  come from a model, so a run on a different model rebuilds rather than borrows
  another model's map. `--rebuild-index` forces a rebuild.
- Turns, tool calls and tokens come from each agent's own output format. Both
  sides are priced with the same formula and price table, regardless of any
  cost the agent reports.
- Every agent run is sampled for resource use: resident set size and physical
  footprint of the whole process tree every 100 ms, and CPU time from the
  process's own accounting after it exits. Peak values cover startup and the
  whole agent run; wall time is the same clock the duration column uses.

See [docs/methodology.md](docs/methodology.md) for the invariants, timing
boundaries, aggregation and limitations.

## Tasks

| Task | Type | Fixture | What the agent must do |
|---|---|---|---|
| `py-bugfix` | Bug fix | Python | Make `python3 -m unittest -q test_stats` pass by fixing `stats.py`. |
| `go-bugfix` | Bug fix | Go | Make `go test ./...` pass by fixing `textutil.go`. |
| `js-bugfix` | Bug fix | Node.js | Make `node --test` pass by fixing `slugify.js`. |
| `py-ledger` | Bug fix | Python, multi-module package | Make `python3 -m unittest -q test_ledger` pass; failures span `ledger/money.py`, `ledger/fees.py` and `ledger/accounts.py`, with the statement produced by `ledger/report.py`. |
| `py-shop` | Bug fix | Python, larger multi-module service | Make `python3 -m unittest discover -q -s tests -t .` pass; failures are in the order-pricing path: cart totals, spend tiers, coupon stacking and tax. |
| `py-feature` | Feature | Python | Implement the missing `parse_duration` function in `duration.py`, as specified by its docstring, so `python3 -m unittest -q test_duration` passes. |

There are five bug-fix tasks and one feature task. Prompts tell the agent not
to modify tests; the verifier restores them anyway. Each repetition runs the
same six tasks, so a 60-run sample is six tasks times five repetitions times
two agents, not 60 distinct problems.

## Samples

Two 60-run samples on Ollama Cloud with `glm-5.3-flash` are bundled. They use the
same agent versions (`ogcode v0.58.3-dirty`, `opencode 1.18.30`) and the same
prices, and they disagree:

| Metric | r7 (historical) | r8 (fresh) |
|---|---|---|
| Harness | retired pre-1.0 runner | this package, 1.0.0 (code unchanged in 1.0.1) |
| Solved | 30/30 vs 30/30 | 30/30 vs 30/30 |
| ogcode total cost | $0.2005 | $0.2841 |
| opencode total cost | $0.2686 | $0.2419 |
| ogcode median wall time | 16.0 s | 29.4 s |
| opencode median wall time | 22.0 s | 22.4 s |
| ogcode vs opencode | 25.3% cheaper, 27.2% faster | 17.5% more expensive, 31.3% slower |

Both samples are 60/60 green. r7 was the earlier result; r8 is a fresh sample
from this package and **does not reproduce the r7 headline**.

The reversal is accounted for by provider prefix caching, not by a change in the
agents' workload. Between the two samples ogcode's prompt size per turn barely
moved (20,107 to 20,403 tokens) and its turns moved from 202 to 215, but its
uncached input per turn rose from 2,556 to 5,212 tokens and its cache share fell
from 87.3% to 74.5%. opencode's token mix was almost unchanged. The whole cost
difference decomposes into that mix at the shared prices; see
[`examples/ollama-cloud-r8/README.md`](examples/ollama-cloud-r8/README.md) for
the per-turn evidence, including smoke runs 24 minutes before the campaign that
cached fully under the same harness and fresh worktree scheme.

This is why neither sample is a target. r7 was produced by a runner that is no
longer in the tree, and its raw log predates the current result schema and
provider fields; its `metadata.json` is a reconstruction marked historical. r8
records the provider-cache regime of its own date. Treat both as worked
examples and record the timestamp, versions and token mix with any comparison.
See [docs/reproducing-results.md](docs/reproducing-results.md).

## Output

A run directory has this shape:

```text
results/<config>/
  raw.jsonl
  metadata.json
  report.md          # created by report.py
  report.csv         # created by report.py
  summary.json       # created by report.py
  .pristine/         # internal fixture snapshots
  logs/
    <task>-<agent>-rep<rep>-<stamp>/
      index.stdout.log
      index.stderr.log
      <project>-<hash>/
        run.log
  toolcalls/
    <task>-<agent>-rep<rep>-<stamp>.csv
```

- `raw.jsonl` has one JSON object per attempt: task, agent, repetition,
  pass/fail, duration, exit code, timeout flag, tokens, turns, shared and
  reported cost, diff summary, final message, verification output, error,
  index result, tool audit and provider-route audit where available. Rows
  written by this version also carry `resources`: the sampler backend, sample
  count and interval, peak tree RSS and footprint, peak single-process RSS,
  maximum process count, median tree RSS, and `cpu_user_s`, `cpu_sys_s`,
  `cpu_total_s`, `wall_s` and `cpu_per_wall`. Rows from older runs have no
  resource block and report `n/a` there. It is appended to; use a fresh `--out`
  directory for a clean sample.
- `metadata.json` is written at the start and updated at the end. It records
  the benchmark version, config and path, provider, resolved price, agent
  binaries and versions, models, extra arguments, task fixture hashes,
  sampling parameters, host platform and Python version, and the full config.
  For ogcode runs it also records the index cache directory, the rebuild flag
  and one entry per task: cache key, pages, and whether it was `built` or
  restored from the cache.
- `logs/` and `toolcalls/` are written for ogcode runs. opencode tool calls
  are counted from its streamed `tool_use` events and have no CSV audit.
- `--keep` leaves each temporary worktree in place and adds its path to the
  corresponding raw row.

`report.md` contains per-task medians, an overall per-agent table, a
`## Resources` section, ratios and cross-checks. The resource section reports
median and maximum peak RSS and median and total CPU per agent, and is omitted
when no row in the sample was sampled. Sizes are printed in MiB (1024²);
`serve_probe.py` uses the same unit. `report.csv` has one row per attempt,
including `peak_rss_bytes`, `peak_footprint_bytes`, `cpu_user_s`, `cpu_sys_s`,
`cpu_total_s` and `cpu_per_wall`. `summary.json` holds the same aggregates in
machine-readable form; `comparison.ogcode_vs_opencode.resources` appears when
both agents have sampled runs.

## Reproduce the documented example

From the package root:

```sh
export OLLAMA_API_KEY="..."
python3 run_benchmark.py --config ollama-cloud --reps 5
python3 report.py --raw results/ollama-cloud/raw.jsonl
```

This runs 60 attempts. Compare `results/ollama-cloud/report.md` and
`summary.json` with both bundled samples. Do not expect the same numbers:
model behaviour, provider prices, latency, the machine and the provider's cache
regime can all differ. In particular, a new run can land on either side of the
r7/r8 split without any change to the agents.

Both reports can be regenerated offline without spending anything:

```sh
python3 report.py --raw examples/ollama-cloud-r7/raw.jsonl --out /tmp/ollama-cloud-r7
python3 report.py --raw examples/ollama-cloud-r8/raw.jsonl --out /tmp/ollama-cloud-r8
```

The r7 example includes a reconstructed `metadata.json` marked historical, and
its old raw rows still lack the current per-row provider fields. The r8 example
was recorded by this package and has the full schema. See
[docs/reproducing-results.md](docs/reproducing-results.md).

## Caveats

- The benchmark covers six specific tasks. Passing them does not establish
  general coding ability, and a difference of a few runs is not evidence of
  one.
- The default sample is five repetitions per task and agent. The reports show
  medians but no confidence intervals and run no significance tests.
- Cost is a model of provider billing, not a bill: it multiplies reported
  token counts by a price table. Prices change, discounts and credits are not
  modelled, and provider token accounting can differ. Check
  `pricing.json` and the price source recorded in `metadata.json`.
- Failed runs and timeouts remain in the sample and count as unsolved. Check
  the cross-checks before interpreting a small cost or time difference.
- `--jobs` greater than 1 introduces machine contention and makes wall-clock
  times less comparable. The bundled configs use one job. Sampled CPU and
  memory are affected even more: concurrent jobs share the machine, so their
  peak RSS overlaps and their CPU per wall ratio falls. Sample one job at a
  time for resource comparisons.
- Resource samples are taken every 100 ms, so a shorter spike between samples
  is missed. Peak RSS is the peak of what was observed, not a high-water mark
  the operating system recorded. The sampling itself adds a small cost to the
  measured run, and macOS reports resident size differently from Linux (the
  libproc backend also records physical footprint, which the ps fallback does
  not). Compare agents sampled in the same session on the same host, not
  numbers across machines or operating systems.
- `serve_probe.py` measures an idle server, not a working agent loop. Its
  numbers answer what the process costs while waiting; the benchmark's resource
  columns answer what it costs while solving a task.
- The ogcode index build is an asymmetric setup step: it is required for
  ogcode, timed separately and excluded from agent time. The harness caches one
  index per fixture, so a campaign builds each index once and later attempts
  restore it; the first build, and every `--rebuild-index`, still pays that cost.
- The verifier is the only pass criterion. A pass means the task's tests
  passed after the tests were restored; there is no human review of the diff.
- The r7 sample was produced by a retired runner and an older result schema;
  its metadata is a reconstruction. The r8 sample was produced by this package.
  Provider cache behaviour changed between them, so neither headline is an
  expectation for a new run.
- A dry run checks configuration and presence, not key validity, endpoint
  reachability or model availability.

## Packaging

The benchmark is a self-contained directory: no installation step and no
third-party Python packages are required. `sh package.sh` builds
`<parent>/public/ogcode-benchmark-1.0.1.zip` with a top-level
`ogcode-benchmark/` directory. In this repository the parent is the repository
root, so the archive lands in `public/`; from the repository root the equivalent
command is `sh benchmark/package.sh`. Unzip the archive and run the commands
from that directory. The archive is also usable directly from a source
checkout, where the package directory is named `benchmark/`.

## Documentation

- [Provider setup and routing](docs/providers.md)
- [Methodology and limitations](docs/methodology.md)
- [Adding a task](docs/adding-tasks.md)
- [Reproducing and comparing results](docs/reproducing-results.md)
