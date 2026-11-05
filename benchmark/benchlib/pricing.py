"""Shared price arithmetic.

Every agent's dollar figure is computed from the same token counts and the same
price table.  This is deliberate: provider billing APIs expose different notions
of a "cost", and agent-reported costs are retained only as a cross-check.  A
single formula applied to both sides is the only way the comparison is about the
agents rather than the vendors' accounting.

Prices are USD per one million tokens.  ``input`` means uncached input; cache
reads and writes are priced separately when the provider reports them.
"""

from __future__ import annotations

import pathlib
from typing import Any, Optional

from . import config, util


def shared_cost(tokens: dict[str, Any], price: dict[str, float]) -> float:
    """USD for one run, priced from provider-reported token counts."""
    cache_read = float(price.get("cache_read", price["input"]))
    cache_write = float(price.get("cache_write", price["input"]))
    return (
        int(tokens.get("input", 0) or 0) * float(price["input"])
        + int(tokens.get("cache_read", 0) or 0) * cache_read
        + int(tokens.get("cache_write", 0) or 0) * cache_write
        + int(tokens.get("output", 0) or 0) * float(price["output"])
    ) / 1_000_000


def resolve_price(
    cfg: config.BenchmarkConfig,
    pricing_path: Optional[pathlib.Path] = None,
) -> tuple[Optional[dict[str, float]], str]:
    """Return ``(price, source)``; source is ``config``, a path, or ``missing``."""
    return config.resolve_pricing(cfg, pricing_path)


def describe_price(price: Optional[dict[str, float]]) -> str:
    """Compact one-line price description for logs and ``--dry-run``."""
    if price is None:
        return "missing"
    parts = [f"input ${price['input']:g}/M", f"output ${price['output']:g}/M"]
    if "cache_read" in price:
        parts.append(f"cache-read ${price['cache_read']:g}/M")
    if "cache_write" in price:
        parts.append(f"cache-write ${price['cache_write']:g}/M")
    return ", ".join(parts)


def read_pricing_table(path: pathlib.Path) -> dict[str, Any]:
    """Read and shape-check a package price table."""
    table = util.read_json(path)
    if not isinstance(table, dict) or not isinstance(table.get("models"), dict):
        raise SystemExit(f"{path}: expected an object with a 'models' object")
    return table
