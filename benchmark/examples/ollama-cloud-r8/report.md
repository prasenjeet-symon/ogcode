# Cost per task: ogcode vs opencode

Model: `glm-5.3-flash` · Provider: `ollama-cloud` · Config: `ollama-cloud` · 60 runs

## Headline

- **Cost:** $0.2841 vs $0.2419 total (+17.5%); $0.0076 vs $0.0063 median per task (+19.5%).
- **Time:** 29.4s vs 22.4s median run (+31.3%); 917.1s vs 749.3s total agent time (+22.4%).
- **Solved:** 30/30 vs 30/30 runs.
- Both agents solved every run (60/60 green).

## Per task (median of the repetitions)

| Task | Agent | Solved | $ / run | Turns | Tool calls | Seconds | Input tok | Output tok | Cache read tok |
|---|---|---|---|---|---|---|---|---|---|
| go-bugfix | ogcode | 5/5 | $0.0076 | 6 | 5 | 41 | 32156 | 706 | 80256 |
| go-bugfix | opencode | 5/5 | $0.0061 | 5 | 7 | 22 | 21337 | 1219 | 76544 |
| js-bugfix | ogcode | 5/5 | $0.0051 | 4 | 3 | 21 | 22163 | 410 | 51072 |
| js-bugfix | opencode | 5/5 | $0.0057 | 5 | 5 | 20 | 20959 | 516 | 77312 |
| py-bugfix | ogcode | 5/5 | $0.0076 | 6 | 5 | 21 | 31524 | 847 | 80256 |
| py-bugfix | opencode | 5/5 | $0.0058 | 5 | 6 | 13 | 20655 | 709 | 76480 |
| py-feature | ogcode | 5/5 | $0.0064 | 5 | 4 | 17 | 27577 | 559 | 65664 |
| py-feature | opencode | 5/5 | $0.0066 | 5 | 5 | 21 | 21903 | 1891 | 78720 |
| py-ledger | ogcode | 5/5 | $0.0132 | 9 | 14 | 41 | 48545 | 2654 | 153216 |
| py-ledger | opencode | 5/5 | $0.0086 | 6 | 11 | 32 | 25161 | 3090 | 104576 |
| py-shop | ogcode | 5/5 | $0.0139 | 10 | 14 | 33 | 52922 | 1919 | 182400 |
| py-shop | opencode | 5/5 | $0.0137 | 10 | 19 | 40 | 34827 | 3870 | 214016 |

## Overall

| Agent | Solved | Total $ | Median $ / task | Mean $ / task | Median turns | Median tool calls | Median seconds |
|---|---|---|---|---|---|---|---|
| ogcode | 30/30 | $0.2841 | $0.0076 | $0.0090 | 6 | 6 | 29 |
| opencode | 30/30 | $0.2419 | $0.0063 | $0.0078 | 5 | 6 | 22 |

## Ratios (ogcode divided by opencode, per-task medians)

| Task | $ ratio | Input tok ratio | Output tok ratio | Cache read ratio |
|---|---|---|---|---|
| go-bugfix | 1.25 | 1.51 | 0.58 | 1.05 |
| js-bugfix | 0.89 | 1.06 | 0.79 | 0.66 |
| py-bugfix | 1.31 | 1.53 | 1.19 | 1.05 |
| py-feature | 0.97 | 1.26 | 0.30 | 0.83 |
| py-ledger | 1.53 | 1.93 | 0.86 | 1.47 |
| py-shop | 1.01 | 1.52 | 0.50 | 0.85 |

## Cross-checks

- Agent-reported cost vs shared price table: max drift $0.000000 over 60 runs.
- Runs that failed verification: 0.
- Runs that errored or timed out: 0.
- Index built before 30 ogcode runs: 3-297 pages, median 10s to build, excluded from every figure above.
- Tool calls are each agent's own record: ogcode's `.ogcode/tool_calls.csv` (copied to `toolcalls/`, 30 runs) and opencode's streamed `tool_use` events. Per-run logs, when retained, are under `logs/`.
- Provider route checked in 30 ogcode runs: all resolved through the configured provider slot.
