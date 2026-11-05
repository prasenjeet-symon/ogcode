# Ollama Cloud r7 — historical 60-run sample

This directory is a **historical sample**, not a result produced by the current
package. It preserves the r7 cost comparison from 2026-11-04: six tasks, five
repetitions, two agents, 60 runs total on Ollama Cloud `glm-5.3-flash`.

The raw log predates the current harness. It was written by the pre-1.0
`bench/cost/run_bench.py` runner, which did not record provider, pricing or
config fields. `metadata.json` supplies that provenance in the shape the
current `benchlib.runner` writes and `benchlib.reporting` reads; it was
reconstructed (see [Metadata provenance](#metadata-provenance)) and no secret
is stored in it.

## Fresh sample does not reproduce this headline

[`../ollama-cloud-r8/`](../ollama-cloud-r8/) is a fresh 60-run sample produced by
the packaged 1.0.0 harness with the same agents, model and prices. It points the
other way: ogcode is 17.5% more expensive and 31.3% slower at the median. The
difference is a provider prefix-cache regime change during the day, not a change
in the agents' workload; that directory's README has the per-turn evidence.
This directory remains the unchanged historical r7 record.

## Result headline

| Metric | ogcode | opencode |
| --- | --- | --- |
| Solved | 30/30 | 30/30 |
| Total cost | $0.2005 | $0.2686 |
| Median cost per task | $0.0057 | $0.0066 |
| Median wall time | 16.0 s | 22.0 s |
| Total agent time | 560.7 s | 831.6 s |

- Total cost is **25.3% lower** for ogcode ($0.2005 vs $0.2686); the median
  per-task cost is **14.1% lower** ($0.0057 vs $0.0066). Both agents were
  cheaper on all six tasks.
- All **60/60 runs passed** verification, with zero errors and zero timeouts.
- "30/30" means 30 runs per agent — six tasks times five repetitions — **not
  30 distinct bugs**. The suite contains six task fixtures.

Costs are computed from the reported token counts at $0.15/M input tokens,
$0.50/M output tokens and $0.03/M cache-read tokens.

## Files

| File | Description |
| --- | --- |
| `raw.jsonl` | Original 60-row JSONL log, kept byte-for-byte as produced. |
| `metadata.json` | Reconstructed run metadata (schema 1) for the current reporter. |
| `report.md` | Report generated from `raw.jsonl` by `benchmark/report.py`. |
| `report.csv` | Per-run table generated alongside `report.md`. |
| `summary.json` | Machine-readable rollup generated alongside `report.md`. |

## Reproduction

The original run used the retired cost harness:

```sh
python3 bench/cost/run_bench.py --reps 5 --out bench/cost/results/r7
```

`bench/cost` is no longer part of the tree. The equivalent run with the current
harness, from the `benchmark/` directory, is:

```sh
python3 run_benchmark.py --config ollama-cloud --reps 5 --out results/ollama-cloud
```

Then regenerate this directory's report from the raw log at the repository
root:

```sh
python3 benchmark/report.py \
  --raw benchmark/examples/ollama-cloud-r7/raw.jsonl \
  --out benchmark/examples/ollama-cloud-r7
```

The reporter writes `report.md`, `report.csv` and `summary.json`; it never
modifies `raw.jsonl`.

## Metadata provenance

`metadata.json` follows the current schema but is explicitly marked
`"historical": true` with a `note` explaining the reconstruction:

- Provider details (`ollama-cloud`, `https://ollama.com/v1`, `OLLAMA_API_KEY`,
  ogcode slot `openai`, opencode provider `ollama-cloud`), pricing and the
  sampling timeouts come from the packaged `configs/ollama-cloud.json` and the
  provider preset.
- Agent versions (`ogcode v0.58.3-dirty`, `opencode 1.18.30`) and the model id
  come from the run that produced the log and are corroborated by the raw
  rows.
- Task fixture digests were computed with the current `fixtures.fixture_digest`
  over the packaged task fixtures. They identify the fixtures as they exist
  now, not necessarily the exact bytes used in 2026.
- `started_at` and `finished_at` are the first and last `ts` values in
  `raw.jsonl`; the old harness did not write wall-clock start/end metadata.
- No host block is present because the raw log and the original results
  directory did not record one.

## Caveats

- This sample is **not regenerated** by the current harness, and it did not
  reproduce when the packaged harness was run on 2026-11-04; see
  [`../ollama-cloud-r8/`](../ollama-cloud-r8/). Its numbers should not be
  compared with fresh runs without noting the harness and provider-cache
  changes.
- The old log lacks the fields the current reporter prefers (`provider_kind`,
  `agent_model`, `config_name`, `pricing_source`, `provider_route_ok`). Those
  per-run columns are blank in `report.csv`; the report header, the summary's
  pricing source, and the provider/config labels come from `metadata.json`.
- The example is illustrative: one model, one provider, one date.
