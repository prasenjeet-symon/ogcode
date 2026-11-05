# Ollama Cloud r8 — fresh 60-run sample

This directory is a **fresh sample produced by the packaged 1.0.0 harness**, not a
reconstruction. It answers the question the r7 example could not: does the r7
headline reproduce under the current runner?

It does not. In this sample ogcode is **17.5% more expensive** and **31.3% slower**
than opencode at the median. The report, raw rows and metadata are shipped
unmodified.

## Provenance

- Harness: `ogcode-benchmark 1.0.0`, extracted from
  `public/ogcode-benchmark-1.0.0.zip` (staged tree byte-identical to
  `benchmark/` before this example was added). The current archive is 1.0.1, a
docs and evidence refresh whose harness code is unchanged from 1.0.0.
- Command: `python3 run_benchmark.py --config ollama-cloud --reps 5
  --out results/ollama-cloud-r8`, one job at a time.
- Window: 2026-11-04 16:24:39–17:02:50 UTC.
- Agents: `ogcode v0.58.3-dirty (darwin/arm64)` and `opencode 1.18.30`.
- Model: `glm-5.3-flash` on Ollama Cloud. Prices: $0.15/M input, $0.50/M output,
  $0.03/M cache read.
- Fixtures: all six were checked red-as-shipped and the reference copy green with
  `check_fixtures.sh` before the run. Every one of the 60 attempts passed
  verification; there were no errors, timeouts or route failures.

## Result headline

| Metric | ogcode | opencode |
| --- | --- | --- |
| Solved | 30/30 | 30/30 |
| Total cost | $0.2841 | $0.2419 |
| Median cost per task | $0.0076 | $0.0063 |
| Median wall time | 29.4 s | 22.4 s |
| Total agent time | 917.1 s | 749.3 s |

- Total cost is **17.5% higher** for ogcode (+$0.0422); median per-task cost is
  **19.5% higher**; median wall time is **31.3% higher**; total agent time is
  **22.4% higher**.
- ogcode is cheaper on only two of six tasks by per-task median: `js-bugfix`
  (ratio 0.89) and `py-feature` (0.97). It is more expensive on `go-bugfix`
  (1.25), `py-bugfix` (1.31), `py-ledger` (1.53) and `py-shop` (1.01).
- This is the opposite direction from r7, where ogcode was 25.3% cheaper,
  14.1% cheaper at the median, and 27.2% faster at the median, and cheaper on
  all six tasks. **r8 is not a reproduction of the r7 headline.** It is a new
  sample that measures a different provider-cache regime.

"30/30" means 30 runs per agent — six tasks times five repetitions — not 30
distinct bugs.

## Why the numbers moved

The agents did essentially the same amount of work in both samples. The change
is in how many prompt tokens the provider served from its cache:

| Per-run aggregate | r7 | r8 |
| --- | --- | --- |
| ogcode turns | 202 | 215 |
| ogcode prompt tokens per turn | 20,107 | 20,403 |
| ogcode uncached input per turn | 2,556 | 5,212 |
| ogcode cache share | 87.3% | 74.5% |
| opencode turns | 201 | 180 |
| opencode prompt tokens per turn | 22,175 | 21,691 |
| opencode uncached input per turn | 4,316 | 4,497 |
| opencode cache share | 80.5% | 79.3% |

Prompt tokens per turn are `(input + cache_read) / turns`; cache share is
`cache_read / (input + cache_read)`. The ogcode prompt size and turn count barely
moved, while its uncached input more than doubled. opencode's token mix is almost
unchanged.

The whole cost increase decomposes exactly into the change in token mix, at the
shared prices:

| Component | r7 → r8 | Cost effect |
| --- | --- | --- |
| Uncached input | 516,334 → 1,120,576 (+604,242) | +$0.0906 |
| Cache read | 3,545,344 → 3,266,176 (−279,168) | −$0.0084 |
| Output | 33,470 → 36,081 (+2,611) | +$0.0013 |
| **Total** | | **+$0.0836** |

The r8-minus-r7 cost difference is $0.0836 ($0.2841 − $0.2005), and it is fully
accounted for by that token-mix change. No other component contributes: the
shared-price table and the agent-reported cost agree to 3.5e-18 over all 60
runs. The comparison is therefore highly sensitive to cache accounting: a
cache-read token costs one fifth of an input token in this price table.

## The provider's cache behaviour changed during the window

The packaged harness was exercised three times shortly before the campaign with
one task and one repetition (`py-bugfix`). Those smoke runs used the same binary,
model, config, timeout and a fresh `tempfile.mkdtemp` worktree per attempt:

| UTC start | First main request (uncached / cached) | Later requests, mean (uncached / cached) |
| --- | --- | --- |
| 15:56:58 | 17,581 / 0 | 441 / 18,208 |
| 15:59:55 | 17,577 / 0 | 444 / 18,406 |
| 16:12:22 | 17,577 / 0 | 462 / 18,197 |

The same task in the campaign, 24 minutes after the last smoke run:

| UTC start | First main request (uncached / cached) | Later requests, mean (uncached / cached) |
| --- | --- | --- |
| 16:36:41 | 10,280 / 7,296 | 4,249 / 14,592 |
| 16:37:14 | 10,282 / 7,296 | 3,810 / 15,287 |
| 16:37:48 | 10,283 / 7,296 | 4,007 / 14,592 |
| 16:38:07 | 10,285 / 7,296 | 4,063 / 14,592 |
| 16:38:29 | 10,284 / 7,296 | 4,276 / 14,592 |

The first request still found 7,296 tokens cached, and later requests mostly
held at exactly 14,592 tokens (15,287 in one repetition) while the conversation
grew. In the smoke runs the cached prefix extended to the full conversation
after the first request. The change is therefore not the harness's fresh
worktree path: the smoke runs had fresh paths too and cached fully. It is a change on the provider side of the
OpenAI-compatible chat-completions route used by ogcode
(`https://ollama.com/v1/chat/completions`); opencode's native route did not show
a comparable change. The provider's internal reason is not observable from here.

The r7 rows, read from the same ogcode usage ledger, show the earlier regime:
the first main request carried about 13.1k uncached and 4.5k cached tokens, then
later turns paid 369–488 uncached tokens against roughly 18k cached. The retired
pre-1.0 runner that produced r7 is no longer in the tree, so its exact
pre-warming or worktree behaviour cannot be inspected, but its per-call cache
pattern is the normal "extend the prefix each turn" one.

The practical consequence: r8 measures the provider's cache regime as much as
it measures the agents. It is a valid sample of what a user would have paid at
that time, but it is not evidence that the r7 advantage disappeared because of
an agent change.

## Files

| File | Description |
| --- | --- |
| `raw.jsonl` | 60-row JSONL log, as written by the run. |
| `metadata.json` | Run metadata recorded by the harness (versions, prices, fixtures, host). |
| `report.md` | Report generated from `raw.jsonl` by `benchmark/report.py`. |
| `report.csv` | Per-run table generated alongside `report.md`. |
| `summary.json` | Machine-readable rollup generated alongside `report.md`. |

`metadata.json` records the absolute path of the extracted package under
`benchmark.config.config_path`; it is provenance from the run machine, not a
package requirement.

## Regenerate the report offline

From the package root, with no key and no network:

```sh
python3 report.py \
  --raw examples/ollama-cloud-r8/raw.jsonl \
  --out /tmp/ollama-cloud-r8
```

The command writes `report.md`, `report.csv` and `summary.json` and never
modifies `raw.jsonl`. `generated_at` and formatting can differ; compare the
aggregates, not the bytes. `wc -l examples/ollama-cloud-r8/raw.jsonl` is 60.

## Run it again

```sh
export OLLAMA_API_KEY="..."
python3 run_benchmark.py \
  --config ollama-cloud \
  --reps 5 \
  --out results/ollama-cloud-rerun
python3 report.py --raw results/ollama-cloud-rerun/raw.jsonl
```

This spends about $0.53 at r8 prices. A new run may land in a different cache
regime again: record the timestamp and compare `input` and `cache_read`, not
just the dollar total.

## Caveats

- One sample: one model, one provider, one date, six tasks, five repetitions.
  The report shows medians but no confidence intervals and runs no significance
  tests. A difference of a few runs is not evidence of one.
- Cost is a model of provider billing, not a bill. Provider token accounting and
  cache accounting can differ from the model.
- r7 remains a historical sample produced by a retired runner with a
  reconstructed metadata sidecar. Do not concatenate its rows with this file or
  treat its headline as a target.
- The provider's cache behaviour changed within the same day. A future run can
  reverse the comparison again without any change to either agent.
