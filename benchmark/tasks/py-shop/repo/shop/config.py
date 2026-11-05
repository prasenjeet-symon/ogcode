"""Runtime settings for the shop service.

The real deployment reads these from the environment; the test suite uses the
defaults, so nothing here touches the network or the filesystem.
"""

from dataclasses import dataclass

DEFAULT_CURRENCY = "USD"

# Tier boundaries are quoted in whole dollars by the merchandising team.
TIER_BOUNDARIES = ("silver", "gold", "platinum")


@dataclass(frozen=True)
class Settings:
    """Process-wide settings. Immutable on purpose: nothing mutates config."""

    currency: str = DEFAULT_CURRENCY
    tax_rate_bps: int = 0
    low_stock_threshold: int = 5
    max_cart_lines: int = 50
    free_shipping_cents: int = 5000
    order_prefix: str = "ORD"


settings = Settings()
