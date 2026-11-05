# Reproducing results

There are three separate things you can reproduce:

1. the report for the fresh r8 sample, offline and at no cost;
2. the report for the historical r7 sample, offline and at no cost;
3. a new run of the same configuration with the current harness, which spends
   money and will not necessarily produce either sample's numbers.

The two samples disagree, and that is the point of shipping both.
`examples/ollama-cloud-r8/` was produced by the packaged 1.0.0 harness and is the
fresh result: ogcode is 17.5% more expensive and 31.3% slower at the median.
`examples/ollama-cloud-r7/` is a historical sample produced by a retired runner:
ogcode is 25.3% cheaper and 27.2% faster there. The reversal is accounted for by
provider prefix caching, not by an agent change. Read
[`examples/ollama-cloud-r8/README.md`](../examples/ollama-cloud-r8/README.md) and
[`examples/ollama-cloud-r7/README.md`](../examples/ollama-cloud-r7/README.md)
for provenance before treating either as a baseline.

## Requirements

- Python 3.10 or newer; the harness and reporter use only the standard library.
- `git` for the fixture worktrees.
- `ogcode` and `opencode` on `PATH`, or paths passed with `--ogcode` and
  `--opencode`, for a fresh run.
- One provider API key for a fresh run. Reporting either raw log needs no key
  and no network.

All commands below run from the package root, the `benchmark/` directory in
the source tree or `ogcode-benchmark/` after unzipping the release archive.

## Regenerate the r8 report offline

The r8 rows were written by this harness and carry the full current schema. The
sidecar `metadata.json` records the versions, prices, fixtures and host.

```sh
python3 report.py \
  --raw examples/ollama-cloud-r8/raw.jsonl \
  --out /tmp/ollama-cloud-r8
```

`--out` is optional; without it the reporter writes beside the raw log. Use a
separate directory if you want to keep the shipped files untouched. The reporter
prints the Markdown report and writes `report.md`, `report.csv` and
`summary.json` under `/tmp/ollama-cloud-r8/`. It never modifies the raw log.

The command should produce the documented aggregates:

| Metric | ogcode | opencode |
|---|---|---|
| Solved | 30/30 | 30/30 |
| Total cost | $0.2841 | $0.2419 |
| Median cost per task | $0.0076 | $0.0063 |
| Median wall time | 29.4 s | 22.4 s |
| Total agent time | 917.1 s | 749.3 s |

Both agents solved every run (60/60 green). The report records 17.5% higher
total cost, 19.5% higher median cost and 31.3% higher median wall time for
ogcode in this sample. Compare the regenerated files with
`examples/ollama-cloud-r8/report.md` and `summary.json`; `generated_at` and
formatting can differ, so compare the aggregates rather than requiring a
byte-for-byte match.

Confirm the sample size:

```sh
wc -l examples/ollama-cloud-r8/raw.jsonl
# 60
```

### Why r8 is not r7

The agents did essentially the same work. ogcode's prompt tokens per turn moved
from 20,107 to 20,403 and its turns from 202 to 215, but its uncached input per
turn rose from 2,556 to 5,212 and its cache share fell from 87.3% to 74.5%.
opencode's token mix was almost unchanged. The reported cost difference
decomposes exactly into that mix at the shared prices.

The provider's cache behaviour changed between the two samples. The package was
smoke-tested 24 minutes before the r8 campaign with the same harness, task and
fresh-worktree scheme; those runs cached the full prefix after the first
request. During the campaign the cached prefix stopped extending at about
14,600 tokens, leaving about 4,000 uncached tokens per turn. The per-turn evidence is in
the r8 README. Treat the cache regime, not just the agent version, as part of
the provenance.

## Regenerate the historical r7 report offline

The r7 raw log has one JSON object per line. The sidecar `metadata.json` beside
it supplies provider, config and pricing labels for the reporter.

```sh
python3 report.py \
  --raw examples/ollama-cloud-r7/raw.jsonl \
  --out /tmp/ollama-cloud-r7
```

It should produce the historical aggregates:

| Metric | ogcode | opencode |
|---|---|---|
| Solved | 30/30 | 30/30 |
| Total cost | $0.2005 | $0.2686 |
| Median cost per task | $0.0057 | $0.0066 |
| Median wall time | 16.0 s | 22.0 s |
| Total agent time | 560.7 s | 831.6 s |

Compare the regenerated files with `examples/ollama-cloud-r7/report.md` and
`summary.json`; compare aggregates, not bytes. Confirm the sample size with the
same `wc -l` check (60 rows).

### Historical-schema caveat

The r7 raw rows were written by the retired pre-1.0 `bench/cost/run_bench.py`
runner. They contain the tokens, cost, timing, diff and verification fields the
reporter needs, but not the current per-row fields such as `provider_kind`,
`agent_model`, `config_name`, `pricing_source` or `provider_route_ok`. Those
columns are blank in `report.csv`.

The bundled `metadata.json` is a reconstruction, explicitly marked
`"historical": true`; it was not recorded by the old runner. Its provider,
pricing, agent versions and sampling fields make the current reporter produce a
labelled report, and its fixture digests describe the fixtures as they exist
now. The original raw log is preserved byte-for-byte. Do not treat the example
as a reproducible baseline for a current run: the harness, agent versions,
provider prices and provider cache regime have changed.

## Run the documented configuration again

A dry run validates the plan and prints the key status, price, tasks, commands
and warnings without contacting a provider:

```sh
export OLLAMA_API_KEY="..."
python3 run_benchmark.py --dry-run
```

Then run the default configuration. The explicit `--out` keeps the new sample
separate from the bundled examples and from earlier results:

```sh
python3 run_benchmark.py \
  --config ollama-cloud \
  --reps 5 \
  --out results/ollama-cloud-rerun
```

This is 60 attempts (six tasks times five repetitions times two agents) and
spends money: about $0.53 at the r8 prices. The run writes `raw.jsonl`,
`metadata.json`, logs and tool-call audits. Generate the report:

```sh
python3 report.py --raw results/ollama-cloud-rerun/raw.jsonl
```

The report files land in `results/ollama-cloud-rerun/`. If you omit `--out`, the
bundled config writes to `results/ollama-cloud/` and the report belongs there.

Start with one task and one repetition if you only want to check that the
provider, binaries and fixture work:

```sh
python3 run_benchmark.py \
  --config ollama-cloud \
  --tasks py-bugfix \
  --reps 1 \
  --out results/ollama-cloud-smoke
python3 report.py --raw results/ollama-cloud-smoke/raw.jsonl
```

A smoke run is not a benchmark sample. It is one attempt per selected agent on
one task.

## Comparing a new run with the samples

Compare like with like:

- the same `model.id`, provider endpoint and price table;
- the same task set and five repetitions per task and agent;
- the same verifier timeouts and one job at a time for wall-clock comparisons;
- the agent versions recorded in `metadata.json`;
- the token mix (`input`, `cache_read`, cache share), not only the dollar total.

Both samples used `ogcode v0.58.3-dirty` and `opencode 1.18.30`, so the agent
versions alone do not explain the reversal. Model behaviour, provider latency,
prices and provider cache behaviour can differ. The documented numbers are one
sample each, not a target or a guarantee. Use the per-task medians and the
cross-checks, and state the sample size and agent versions alongside any
comparison.

Do not concatenate either example's `raw.jsonl` with a new one. The r7 rows have
a different schema, and the reporter's labels and route fields would be mixed;
the reporter also appends to a raw log rather than replacing it. Report each raw
file separately, or keep a new sample in its own result directory.

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `no rows in ...` | Wrong `--raw` path or an empty file. | Pass the `raw.jsonl` written by the run; check `wc -l`. |
| `report.md` is missing after a run | A run writes raw data and metadata, not reports. | Run `report.py --raw <run>/raw.jsonl`. |
| The report contains attempts from an earlier run | `raw.jsonl` is appended to, not replaced. | Use a fresh `--out` directory. |
| Dry run says the key is missing | The selected provider's key variable is unset. | Export the variable named in the dry-run output, or use a preset whose key you have. |
| Dry run says no price was found | No `model.pricing` and no matching `pricing.json` entry. | Add pricing to the config, add it to the table, or pass `--pricing`. |
| A binary is missing | The CLI is not on `PATH` and no explicit path was given. | Install it, or pass `--ogcode` and `--opencode`. |
| Provider and config labels are blank | The raw file has no current metadata and no sidecar. | Expected for old raw files; use a current run or a reconstructed `metadata.json`. |
| `provider_route_ok` is null | The route audit found no matching log lines. | Not a gate; inspect `logs/` if the route matters. Old rows have no route data. |
| Times differ from an example | Different hardware, provider latency, agent versions or `--jobs` above 1. | Compare medians; run with one job (the default). |
| Cost differs from an example | Different token mix, especially cache reads, or a different provider price. | Compare `input`, `cache_read` and cache share before comparing dollars. |
| Cost differs from the provider bill | The report uses the shared price table and agent-reported token counts. | Treat it as a comparison model, not an invoice; check the price source and token accounting. |

For provider routing, key variables and custom endpoints, see
[providers.md](providers.md). For the timing boundaries and aggregation rules,
see [methodology.md](methodology.md).
