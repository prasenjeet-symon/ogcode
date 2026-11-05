#!/usr/bin/env python3
"""Run the ogcode vs opencode cost benchmark.

    python3 run_benchmark.py --config ollama-cloud --reps 5
    python3 run_benchmark.py --config openrouter --tasks py-bugfix --agents ogcode
    python3 run_benchmark.py --config configs/custom-openai-compatible.json --dry-run

Configuration is a JSON file selected by ``--config``.  Bundled presets cover
Ollama Cloud, OpenRouter, Anthropic, OpenAI and any OpenAI-compatible custom
endpoint.  See README.md and docs/providers.md for the full guide.
"""

from __future__ import annotations

from benchlib.cli import main

if __name__ == "__main__":
    raise SystemExit(main())
