# Methodology

## Scope

This benchmark compares `ogcode` and `opencode` on six fixed
software-engineering tasks. It measures four things:

- whether the task's verifier passes after the agent stops,
- wall-clock time for the agent process,
- turns and tool calls as reported by each agent,
- token counts and a dollar cost computed from one shared price table.

It does not measure general coding ability, model quality or developer
experience. Six hand-built fixtures are too narrow for that, and a result is
always tied to the model, provider, agent versions, prices and machine used for
that run.

## Anatomy of one attempt

1. Copy `tasks/<task>/repo/` into a fresh temporary directory and initialize a
   git repository with one commit. Junk paths such as `.ogcode/`, `ogcode.json`,
   `__pycache__/`, `node_modules/`, `.pytest_cache/`, `.opencode/` and `*.pyc`
   are excluded from the fixture commit, the measured diff and the pristine
   comparison. The copy happens before git filtering, so generated files left
   in `repo/` still reach the worktree; keep the source tree clean. The fixture
   is snapshotted under the result directory so it can be restored if a run
   mutates it outside its worktree.
2. Run the task's verifier against the untouched copy. It must fail. If it
   passes, the attempt stops before any agent is called.
3. For ogcode only, restore the repository document index built once for this
   fixture, or build it if the cache has no entry, and check that it produced
   at least one page. The build command is run with its own timeout. Build and
   restore durations are recorded separately and neither counts as agent time.
4. Run the agent in the worktree, autonomously, with the prompt read from
   `task.md` (stripped) and a wall-clock timeout. There is no turn cap.
5. Capture a git diff, then run the verifier again. The verifier restores the
   task's test files from the fixture commit before running the suite, so edits
   to those tests are discarded for grading.
6. Record tokens, turns, tool calls, the shared cost, the diff, the verifier
   output tail, the index result, tool audit and any error. The row is appended
   to `raw.jsonl`; the temporary worktree is removed unless `--keep` was set.

Both agents get the same task, fixture, timeout and verifier. The mechanics
that differ are the ones each CLI requires: model-string syntax, output format,
and the autonomy flag. opencode is invoked with `--auto`; ogcode runs its
normal non-interactive `run` command.

## Fairness invariants

| Invariant | How it is enforced |
|---|---|
| Same task text | The prompt is the text of `tasks/<task>/task.md`, stripped and passed as one argument. |
| Same starting tree | A fresh copy of `repo/` is committed for every attempt; each attempt gets its own temporary directory. |
| No cross-run state | Worktrees are deleted after the row unless `--keep`; fixtures are snapshotted and drift is restored. |
| Red before the agent | The verifier must exit non-zero before the agent starts, or the run stops. |
| Consistent grading | The same `verify.sh` runs before and after; test files are restored from the fixture commit first. |
| Same wall-clock limit | `run.timeout_s` applies to both agent processes. |
| Same concurrency | `run.jobs` controls the whole job list; both agents are scheduled under the same setting. |
| Deterministic provider route | Other provider credentials are removed; only the selected key and base URL are supplied. |
| Isolation | ogcode project config denies all skills and disables globally configured MCP servers; opencode gets a temporary `XDG_CONFIG_HOME` with an empty MCP config. |
| One price basis | Both agents are priced with the same formula and the same resolved price table. |
| No turn cap | Neither agent is stopped at a fixed number of turns; only the wall-clock timeout or the agent itself ends a run. |

The index is deliberately asymmetric: ogcode reads a document index, so the
harness builds it for ogcode and excludes that setup time from the measured
run. opencode has no equivalent step. This is a product difference, not a
like-for-like task cost, and reports state that the index was excluded.

The index depends on the fixture, not on the task question, so it is built once
per fixture and cached under `~/.cache/ogcode-benchmark/indexes`. The cache key
is the fixture content digest plus the model, provider kind, base URL and
ogcode version; changing any of them builds a new entry instead of reusing a
map labelled by another model. Restoring copies the cached database into the
attempt's `.ogcode/` and rewrites the recorded absolute paths to the new
worktree, so every attempt starts from the same index at the cost of a file
copy. `--build-index` performs the builds as a separate step; a normal run reuses
what is cached and builds only what is missing; `--rebuild-index` forces a
rebuild. Each row records `index.origin` as `built` or `restored`.

The harness does not put either agent in a container or block network access.
The isolation is a throwaway worktree, a scrubbed provider environment, and
disabled skills/MCP configuration. Agents run as the local user.

## Red-before, green-after

The red/green gate protects against a task that is already solved and against
a verifier that cannot detect the intended fix:

- **Red as shipped:** `tasks/<task>/repo/` fails `verify.sh` before the agent
  runs. A fixture that is already green is not a task; the run stops with an
  error rather than recording a meaningless pass.
- **Green after a fix:** the verified result is produced after the agent stops
  and after the task's test files are restored from the base revision. A pass
  means the suite passed with the original tests.

`sh check_fixtures.sh` checks a second condition outside a measured run: after
copying `tasks/<task>/reference/` over the fixture, the same verifier must
pass. That proves the task is solvable and the verifier works. It does not
prove the reference is the only possible solution, and it is not a substitute
for reviewing the task.

Because the verifier restores only the paths named in `verify.sh`, every file
that can change the result of the suite must be one of those paths, or must be
an implementation file the agent is allowed to change. The diff is captured
before restoration, so test edits remain visible in the raw row even though
they do not affect the pass/fail result.

## Timing boundaries

`duration_s` is measured around the agent child process only. It includes
process startup, model calls, tool use and any waiting for the provider. It
excludes:

- copying and committing the fixture,
- the pre-run verification,
- the ogcode index build or restore,
- the post-run verification,
- report generation.

A timed-out process is killed as a process group, so subprocesses cannot keep
editing the worktree after the row is recorded. The row gets `timed_out: true`
and an error such as `timed out after 600s`; the attempt remains unsolved.

`total_duration_s` in a report is the sum of agent durations for the rows in
the sample. It is not the wall-clock duration of the whole campaign,
especially when `--jobs` is greater than 1. The default is one job to keep
timing comparable. With more jobs, agents contend for the same CPU, disk and
network, so wall-clock times are not directly comparable with a serial run.

## Resource accounting

The sampler runs beside the agent process, outside the timed duration. Every
100 ms it walks the process tree rooted at the agent PID and records the sum of
resident set size across the tree, the sum of physical footprint where the
platform reports it, the largest single process and the process count. It keeps
the peak of each series, their median, and the full series for the JSON report.

CPU time is not sampled: after the child exits the harness reads the kernel's
own accounting (`RUSAGE_CHILDREN` delta, microseconds of user and system time)
for the process group. `cpu_per_wall` divides that total by the attempt's wall
time, so `1.0` means one core busy for the whole run and values above `1.0`
mean parallelism.

Limits that matter when reading these columns:

- A spike shorter than the sample interval can be missed. Peak RSS is the peak
  of the samples, not an OS high-water mark.
- Peak values include process startup and every child the agent spawned, so
  they are larger than a steady-state reading.
- macOS resident size includes shared pages each process reports separately;
  summing a tree over-counts shared frameworks. Use it to compare agents
  measured the same way, not as an absolute memory figure.
- Concurrent `--jobs` make CPU and memory numbers incomparable between rows.
- The probe samples the agent process only. Provider-side compute, network
  transfer and the user's browser are outside it.

## Token and cost accounting

Each agent reports its own token counts:

- ogcode: the JSON result document printed by
  `ogcode run --output-format json`. The harness looks for a document carrying
  a `tokens` object.
- opencode: the newline-delimited JSON event stream. The harness sums the
  token fields from every `step_finish` event: input, output, reasoning, cache
  read, cache write and total.

The normalized fields are `input`, `output`, `reasoning`, `cache_read`,
`cache_write` and `total`. Missing fields become zero. For opencode, `total`
falls back to input plus output when the event stream does not provide one.
The harness does not independently tokenize the prompt or completion, and it
does not reconcile provider-specific definitions of cached input.

Turns are also agent-specific: ogcode reports `num_turns`; opencode's count is
the number of `step_finish` events. Tool calls are each agent's own record:
ogcode's count comes from rows with `agent=build` in `.ogcode/tool_calls.csv`,
while opencode's count comes from streamed `tool_use` events. The harness
copies ogcode's full tool-call CSV to `toolcalls/` for inspection; it counts
only the measured run's rows.

Each run's dollar figure is computed from its normalized token counts before
the row is written:

```text
cost = (input_tokens        * input_price
      + cache_read_tokens   * cache_read_price
      + cache_write_tokens  * cache_write_price
      + output_tokens       * output_price) / 1,000,000
```

Prices are USD per one million tokens. `cache_read` and `cache_write` fall back
to the input price when the price object omits them. Reasoning tokens are kept
for audit but are not a separately priced category. The price comes from
`model.pricing` when present, otherwise from the selected price table
(`pricing.json` or `--pricing`); a real run stops if neither provides a price.

The agent's own reported cost is stored as `cost_reported_usd` and shown only
as a cross-check. Provider billing APIs expose different notions of cost, and
the point of the shared formula is to compare agents on one basis. A large
drift between reported and shared cost is a signal to inspect the token
accounting, not to prefer one number silently.

## Aggregation and sample size

`raw.jsonl` has one row per attempt. A report groups rows by `(task, agent)`.

- **Per task:** solved count, median cost, median turns, median tool calls,
  median seconds, and median input/output/cache-read tokens across the
  repetitions.
- **Overall per agent:** solved count, total cost, total agent time, and
  medians of turns, tool calls and seconds across that agent's rows.
- **Median cost per task (overall):** the median of the six per-task medians,
  so each task contributes one number regardless of how many repetitions it
  has.
- **Mean cost per task:** the mean of those per-task medians.
- **Ratios:** ogcode divided by opencode, using the per-task medians.

The bundled configuration uses five repetitions. With six tasks and two
agents, the default run is 60 attempts: 6 tasks × 5 repetitions × 2 agents. The
same six tasks recur; these are not 60 different problems. `--jobs` changes
scheduling, not the number of attempts.

Five repetitions give a median that is less sensitive to one unusually slow or
cheap run than a mean, but they do not support confidence intervals, hypothesis
tests or claims that a small difference is real. A single repetition is a smoke
test. A difference should be read alongside the cross-checks, the spread of
the raw rows and the fact that model calls are nondeterministic.

## Cross-checks

Each report includes the checks that can be computed from the rows:

- number of runs with an agent-reported cost, and the maximum gap between that
  cost and the shared-price cost,
- number of runs that failed verification,
- number of runs with an error or timeout,
- number of ogcode runs with an index, how many of those were restored from
  the cache versus built, the minimum and maximum page count, and the median
  build time over the runs that built an index,
- number of runs with an ogcode tool audit,
- number of provider routes checked and number that failed the audit.

Fields a run does not record are omitted or counted as missing rather than
inferred. Older raw logs can therefore produce a report with no provider
metadata and no route checks; that is a property of the historical data, not
evidence of a failed run.

`metadata.json` carries the context needed to interpret a result directory:
benchmark version, config and config path, provider and resolved price, agent
binaries and versions, models and extra arguments, task fixture hashes,
sampling parameters, host platform, Python version and the full config. Keep it
with `raw.jsonl`; reports use it when present.

## Threats to validity

1. **Task coverage.** Six fixtures, five bug-fix tasks and one feature task, in
   Python, Go and JavaScript. They exercise specific bugs and one specified
   feature, not general software engineering. Passing all six says nothing
   about a different language, codebase or task.
2. **Model and provider confounding.** A config fixes one model on one
   provider. Comparing results across presets changes the model, the provider,
   the price and possibly the serving stack at once. The benchmark is not a
   model leaderboard.
3. **Sample size and variance.** Five repetitions are useful for a robust
   central estimate but are not a statistical experiment. No confidence
   intervals or significance tests are reported. Small differences in cost,
   time or tool calls may be noise.
4. **Price model.** Prices are a table snapshot, not a bill. Discounts, credits,
   free tiers, taxes and provider-specific cache rules are not modelled. The
   bundled table is dated; model aliases and prices can change without a
   fixture change. Provider token accounting may classify cached input
   differently.
5. **Timing.** Wall-clock time includes provider latency and network
   conditions. Machine load, cold caches, rate limits, retries and concurrency
   all affect it. The index build is excluded, but it is still an asymmetric
   product requirement.
6. **Agent mechanics.** The agents use different CLIs, output formats, autonomy
   flags, tool-call accounting and context/index strategies. Those differences
   are part of what is being compared, but they mean turns and tool calls are
   not perfectly equivalent units.
7. **Verification.** The tests are a proxy for correctness. They are restored
   before grading, but a solution can still special-case test inputs, and only
   paths listed in `verify.sh` are restored. There is no human review of the
   diff. A pass means "the suite passed", not "the change is good".
8. **Environment.** Agents run with the local user's permissions and network
   access, outside a sandbox. Provider outages, credential problems and local
   resource limits can turn into unsolved runs or timeouts.
9. **Historical data.** The bundled example's raw rows predate the current
   fields, and its `metadata.json` is a reconstruction marked historical. Its
   agent versions and schema are not the same as a new run's. Do not mix it with
   new rows without checking compatibility.
10. **Resource sampling.** RSS and CPU are sampled, not enforced: a 100 ms
    interval misses shorter spikes, tree RSS double-counts shared pages, and the
    sampler itself perturbs the run slightly. Values are comparable between
    agents sampled on the same host in the same session, not across machines,
    operating systems or kernel versions.
11. **Reproducibility.** Model aliases can move to new weights, prices change,
    and hardware differs. A rerun with the same command is a new sample, not a
    replay of the old numbers.

## Reading a result responsibly

Use the benchmark to compare these two agents on these six tasks under one
config, with the raw rows and metadata available for inspection. Report the
sample size, repetitions, model, provider, agent versions, price source and
whether the index was excluded. Prefer medians and the cross-checks to a single
dramatic run. Do not present the result as a general capability ranking, and do
not describe a difference as significant without an appropriate statistical
design.

For the task contract and the red/green fixture check, see
[adding-tasks.md](adding-tasks.md). For provider setup and pricing overrides,
see [providers.md](providers.md).
