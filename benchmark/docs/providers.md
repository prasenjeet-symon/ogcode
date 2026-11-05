# Providers

The benchmark supports five provider kinds: `ollama-cloud`, `openrouter`,
`anthropic`, `openai` and `custom`. A config names one kind; the harness
resolves the effective endpoint, key variable and model string before any run.

## Presets

| Preset | `provider.kind` | Default key variable | Default base URL | ogcode slot | opencode provider id | Bundled `model.id` |
|---|---|---|---|---|---|---|
| `ollama-cloud` | `ollama-cloud` | `OLLAMA_API_KEY` | `https://ollama.com/v1` | `openai` | `ollama-cloud` (built-in) | `glm-5.3-flash` |
| `openrouter` | `openrouter` | `OPENROUTER_API_KEY` | `https://openrouter.ai/api/v1` | `openai` | `openrouter` (built-in) | `z-ai/glm-4.6` |
| `anthropic` | `anthropic` | `ANTHROPIC_API_KEY` | Anthropic default | `anthropic` | `anthropic` (built-in) | `claude-sonnet-4-5` |
| `openai` | `openai` | `OPENAI_API_KEY` | `https://api.openai.com/v1` | `openai` | `openai` (built-in) | `gpt-5.1` |
| `custom-openai-compatible` | `custom` | `BENCHMARK_API_KEY` (template) | required from config | `openai` | `benchmark` (generated config) | `your-model-id` (template) |

`python3 run_benchmark.py --list-configs` prints the bundled presets with their
kind and default model. A preset is selected by name (`--config openrouter`)
or by path (`--config configs/custom-openai-compatible.json`).

## How routing works

The harness registers exactly one ogcode provider slot per run. It starts from
a copy of the environment with every known provider variable removed, then
adds only the selected endpoint's credentials:

- For `ollama-cloud`, `openrouter`, `openai` and `custom`, ogcode uses its
  OpenAI-compatible slot: `OPENAI_API_KEY` and, when the resolved base URL is
  present, `OPENAI_BASE_URL`.
- For `anthropic`, ogcode uses `ANTHROPIC_API_KEY` and, when configured,
  `ANTHROPIC_BASE_URL`.
- opencode uses its built-in provider id for the four first-party kinds. For a
  custom endpoint, a config override, or a non-default base URL, the harness
  writes a temporary opencode config and launches opencode with a temporary
  `XDG_CONFIG_HOME`.

The removed variables cover Anthropic, OpenAI, OpenRouter, Ollama, Gemini,
Google, Groq, DeepSeek, Mistral, xAI, Together, Fireworks, Cerebras and
`OPENCODE_CONFIG`. This matters on a developer machine where several keys are
exported: without the scrub, the selected provider could silently lose to
another credential. Verification runs with those provider credentials removed
as well.

For opencode's built-in providers the harness also supplies the provider's
native key variable: `OLLAMA_API_KEY` for `ollama-cloud` and
`OPENROUTER_API_KEY` for `openrouter`. The custom path supplies the variable
named by `provider.api_key_env`.

The child environment also sets `NO_COLOR=1`. Provider keys are passed through
the environment, never written to a config file or a command line.

### Generated opencode config

When opencode cannot use a built-in provider, the harness writes a temporary
config of this form:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {},
  "provider": {
    "benchmark": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "My endpoint",
      "options": {
        "baseURL": "https://llm.example.com/v1",
        "apiKey": "{env:MY_LLM_KEY}"
      },
      "models": {
        "my-model": {
          "name": "my-model"
        }
      }
    }
  }
}
```

The key is an environment interpolation, not a literal, so the file on disk
contains no secret. The directory is removed after the run. The provider id is
`provider.opencode_provider` when set, otherwise `benchmark` for a custom
endpoint. An Anthropic config cannot override the opencode provider id; it must
remain `anthropic`.

## Model id forms

`model.id` in the config is the canonical id. It is recorded in metadata and
used for the price lookup. The two agents use different string forms:

- ogcode receives the raw model id.
- opencode receives `<opencode_provider>/<model.id>`.

Defaults, taken from the bundled Ollama Cloud config:

| Purpose | Value |
|---|---|
| Canonical `model.id` | `glm-5.3-flash` |
| ogcode `--model` | `glm-5.3-flash` |
| opencode `--model` | `ollama-cloud/glm-5.3-flash` |

`agents.ogcode.model` and `agents.opencode.model` can override either side.
An explicit opencode model must include its provider prefix.

`--model ID` sets the canonical id and clears explicit per-agent models, so
both agent model strings are derived from the new id. `--ogcode-model` and
`--opencode-model` override one agent's command-line model string without
changing `model.id`; pricing still uses `model.id`. A per-agent override that
no longer matches the selected provider can make that agent fail before
producing a result.

## Environment variables

The harness reads the selected key from `provider.api_key_env` in the parent
environment. `--dry-run` prints the variable name and whether it is set, for
example:

```text
api key          OLLAMA_API_KEY=<missing>
```

Export the variable before a real run:

```sh
export OLLAMA_API_KEY="..."
python3 run_benchmark.py --config ollama-cloud
```

`provider.api_key_env` can be changed in a config, but the preset's native
variable is still used for opencode's built-in provider where one exists. For a
custom endpoint, the variable named in the config is the one interpolated into
the generated opencode config.

## Custom OpenAI-compatible endpoint

A custom config needs `provider.base_url`, `provider.api_key_env` and
`model.id`. Save the complete example below as `configs/my-endpoint.json` so
its `$schema` path resolves, or adjust `$schema` for another location:

```json
{
  "$schema": "../config.schema.json",
  "name": "my-endpoint",
  "description": "OpenAI-compatible endpoint.",
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
      "model": "my-model"
    },
    "opencode": {
      "model": "benchmark/my-model"
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
    "output_dir": "results/my-endpoint"
  }
}
```

Notes:

- `base_url` must be an `http://` or `https://` URL.
- `opencode_provider` defaults to `benchmark` for a custom endpoint. If you
  change it, update an explicit `agents.opencode.model` to
  `<provider-id>/<model-id>` or omit that field and let the default apply.
- `pricing` can be omitted or set to `null` if the model is listed in
  `pricing.json`; a real run refuses to start without a resolved price.
- `$schema` is resolved relative to the config file. The bundled configs sit in
  `configs/`, so they use `../config.schema.json`; adjust or omit it elsewhere.
- A custom endpoint, a `provider.opencode_provider` override, or a non-default
  `base_url` on a first-party kind causes the generated opencode config. A
  first-party kind with its default base URL and no override uses the built-in
  opencode provider.

Check the plan before spending money:

```sh
python3 run_benchmark.py --config configs/custom-openai-compatible.json --dry-run
```

## Pricing

Prices are USD per one million tokens. The resolution order is:

1. `model.pricing` in the config, when present. The source is recorded as
   `config`.
2. The `models[<model.id>]` entry in the price table (`pricing.json` by
   default, or the file passed with `--pricing`). The source is the table path.
3. Otherwise the price is missing and a real run stops.

A config's embedded price wins even when `--pricing` points at a different
table. The price table must be a JSON object with a `models` object:

```json
{
  "models": {
    "my-model": {
      "input": 0.2,
      "output": 0.8,
      "cache_read": 0.05,
      "cache_write": 0.2
    }
  }
}
```

The shared cost formula for every run is:

```text
cost = (input_tokens        * input_price
      + cache_read_tokens   * cache_read_price
      + cache_write_tokens  * cache_write_price
      + output_tokens       * output_price) / 1,000,000
```

`cache_read` and `cache_write` fall back to the input price when the selected
table omits them. Reasoning tokens are retained in the raw row and CSV for
audit but are not priced as a separate category. Token counts are those each
agent reports; the harness does not independently tokenize.

Each run's dollar figure is computed with this formula from that run's token
counts, and reports aggregate the recorded figures. The agent-reported cost is
kept as a cross-check and is never the authoritative number. The report's
cross-checks section shows the largest gap between the two.

The bundled `pricing.json` records its sources and the date they were fetched
(2026-11-04). Prices and model ids change; verify current provider pricing
before publishing a comparison, or supply your own table.

## Route audit

For ogcode runs, the harness reads `run.log` under the run's log directory and
looks for lines containing:

- `resolved provider for model` with `provider=<id>`
- `streaming chat request` with `url=<url>`

It records `ogcode_provider`, `ogcode_url` and `provider_route_ok` in the raw
row. `provider_route_ok` is true when the reported provider id matches the
expected ogcode slot (`openai` or `anthropic`) and, when both a URL and a base
URL are present, the URL host matches the base URL host. It is false on a
mismatch and null when neither log line is available.

This is a post-run audit, not a gate. Log formats can change between releases;
a null result is not a failure, and a measured run is not discarded for it.
Inspect it with:

```sh
grep -R -E 'resolved provider for model|streaming chat request' results/ollama-cloud/logs
```

`summary.json` reports the number of routes checked and the number of failures
under `cross_checks`. opencode has no equivalent audit: its provider is fixed
by the built-in provider id or by the generated temporary config.

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| Dry run says `<VAR> is not set; a real run will stop here` | The selected key variable is absent from the environment. | Export it, or change `provider.api_key_env` to a variable you have. |
| Real run says `<VAR> is not set; export it or choose a config...` | Same, checked again at run time. | Export the variable before running. |
| Dry run says `no price for this model in the config or pricing.json` | No embedded price and no matching table entry. | Add `model.pricing`, add `models[<model.id>]` to the table, or pass `--pricing`. |
| Real run says `no price for model '<id>'. Add model.pricing to <config source> or an entry to pricing.json before spending money.` | Same. | Fix the price before running; the harness will not spend without one. |
| `ogcode not found on PATH (also tried: ~/.local/bin/ogcode)` | Binary missing from `PATH` and the fallback location. | Install ogcode, or pass `--ogcode /absolute/path`. |
| `opencode not found on PATH (also tried: ~/.opencode/bin/opencode)` | Binary missing from `PATH` and the fallback location. | Install opencode, or pass `--opencode /absolute/path`. |
| Dry run says `ogcode binary not found: ...` or `opencode binary not found: ...` | Same, reported without stopping the dry run. | Install it or pass the path. |
| `a custom provider needs provider.base_url` | Custom config has no endpoint. | Add an `http(s)://` base URL. |
| `a custom provider needs provider.api_key_env` | Custom config has no key variable name. | Add `provider.api_key_env`. |
| `provider.opencode_provider is not supported for Anthropic` | Tried to override the Anthropic provider id. | Remove the override; Anthropic must keep `anthropic`. |
| `fixture <name> passes before the agent runs` | The shipped fixture is not actually failing. | Run `sh check_fixtures.sh <name>` and fix the task fixture. |
| `could not index <task> (N pages): ...` | The ogcode index build failed or produced no pages. | Read `index.stderr.log` under the task's index cache entry (or `logs/.../index.stderr.log` from the attempt that built it), reduce fixture size or raise `--index-timeout`. |
| `could not parse ogcode json output` | ogcode crashed or its JSON output format changed. | Inspect the run log and stderr, and check the installed ogcode version. |
| Row has `timed_out: true` and `error: timed out after Ns` | The agent exceeded the wall-clock limit. | Raise `--timeout`; the run remains unsolved. |
| `provider_route_ok` is false | Log shows a different provider or a different URL host from the selected endpoint. | Check the key and base URL; do not mix the sample with another route. |
| `provider_route_ok` is null and the audit fields are null | No matching log line was found. | Not a failure; inspect `run.log` if the route matters. |
| Authentication fails although the key is set | A dry run checks presence, not validity, and model access can be restricted. | Check the key, endpoint and model on the provider side, then inspect the agent log. |
| A price entry exists but is reported missing | The key is not exactly `model.id`; for OpenRouter it includes the slash. | Use the canonical id, not opencode's `provider/model` string. |
| No `report.md` after a run | Reports are generated separately. | Run `python3 report.py --raw results/<config>/raw.jsonl`. |
| `unknown task(s): ...` | `--tasks` names a task with no `tasks/<name>/task.md`. | Use `--tasks all` or one of the six task names. |
