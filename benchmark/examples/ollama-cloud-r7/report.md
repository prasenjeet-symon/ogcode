# Cost per task: ogcode vs opencode

Model: `glm-5.3-flash` · Provider: `ollama-cloud` · Config: `ollama-cloud-r7` · 60 runs

## Headline

- **Cost:** $0.2005 vs $0.2686 total (-25.3%); $0.0057 vs $0.0066 median per task (-14.1%).
- **Time:** 16.0s vs 22.0s median run (-27.2%); 560.7s vs 831.6s total agent time (-32.6%).
- **Solved:** 30/30 vs 30/30 runs.
- Both agents solved every run (60/60 green).

## Per task (median of the repetitions)

| Task | Agent | Solved | $ / run | Turns | Tool calls | Seconds | Input tok | Output tok | Cache read tok |
|---|---|---|---|---|---|---|---|---|---|
| go-bugfix | ogcode | 5/5 | $0.0057 | 6 | 5 | 16 | 15360 | 872 | 95424 |
| go-bugfix | opencode | 5/5 | $0.0060 | 5 | 8 | 17 | 21290 | 1123 | 76544 |
| js-bugfix | ogcode | 5/5 | $0.0054 | 6 | 5 | 14 | 15138 | 451 | 96256 |
| js-bugfix | opencode | 5/5 | $0.0060 | 5 | 6 | 14 | 21528 | 438 | 77696 |
| py-bugfix | ogcode | 5/5 | $0.0057 | 6 | 5 | 16 | 15312 | 841 | 96640 |
| py-bugfix | opencode | 5/5 | $0.0059 | 5 | 6 | 16 | 20945 | 974 | 76928 |
| py-feature | ogcode | 5/5 | $0.0049 | 5 | 4 | 12 | 15281 | 483 | 78656 |
| py-feature | opencode | 5/5 | $0.0072 | 5 | 6 | 25 | 22735 | 1628 | 79552 |
| py-ledger | ogcode | 5/5 | $0.0064 | 6 | 12 | 20 | 18359 | 1181 | 104128 |
| py-ledger | opencode | 5/5 | $0.0086 | 5 | 11 | 34 | 26125 | 2898 | 83840 |
| py-shop | ogcode | 5/5 | $0.0102 | 10 | 16 | 32 | 22082 | 2897 | 198784 |
| py-shop | opencode | 5/5 | $0.0140 | 12 | 21 | 48 | 37396 | 4562 | 255616 |

## Overall

| Agent | Solved | Total $ | Median $ / task | Mean $ / task | Median turns | Median tool calls | Median seconds |
|---|---|---|---|---|---|---|---|
| ogcode | 30/30 | $0.2005 | $0.0057 | $0.0064 | 6 | 6 | 16 |
| opencode | 30/30 | $0.2686 | $0.0066 | $0.0079 | 5 | 8 | 22 |

## Ratios (ogcode divided by opencode, per-task medians)

| Task | $ ratio | Input tok ratio | Output tok ratio | Cache read ratio |
|---|---|---|---|---|
| go-bugfix | 0.94 | 0.72 | 0.78 | 1.25 |
| js-bugfix | 0.90 | 0.70 | 1.03 | 1.24 |
| py-bugfix | 0.96 | 0.73 | 0.86 | 1.26 |
| py-feature | 0.68 | 0.67 | 0.30 | 0.99 |
| py-ledger | 0.75 | 0.70 | 0.41 | 1.24 |
| py-shop | 0.73 | 0.59 | 0.64 | 0.78 |

## Cross-checks

- Agent-reported cost vs shared price table: max drift $0.000000 over 60 runs.
- Runs that failed verification: 0.
- Runs that errored or timed out: 0.
- Index built before 30 ogcode runs: 3-266 pages, median 7s to build, excluded from every figure above.
- Tool calls are each agent's own record: ogcode's `.ogcode/tool_calls.csv` (copied to `toolcalls/`, 30 runs) and opencode's streamed `tool_use` events. Per-run logs, when retained, are under `logs/`.
