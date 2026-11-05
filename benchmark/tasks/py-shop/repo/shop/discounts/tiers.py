"""Spend tiers.

Merchandising quotes the boundaries in whole dollars; the service stores every
amount in cents, so the table carries both. A subtotal earns a tier as soon as
it reaches that tier's boundary, so a cart of exactly $100.00 is silver, not
bronze.
"""

from shop.money import mul_rate

# name, boundary in cents, discount rate in basis points
TIER_TABLE = (
    ("bronze", 0, 0),
    ("silver", 10_000, 300),
    ("gold", 25_000, 500),
    ("platinum", 60_000, 800),
)


def tier_row(subtotal_cents: int) -> tuple[str, int]:
    """The (name, rate in basis points) of the highest tier reached."""
    subtotal = int(subtotal_cents)
    name, _, rate = TIER_TABLE[0]
    for tier, boundary, bps in TIER_TABLE:
        if subtotal > boundary:
            name, rate = tier, bps
    return name, rate


def tier_for_subtotal(subtotal_cents: int) -> str:
    """Name of the tier the subtotal reaches."""
    return tier_row(subtotal_cents)[0]


def tier_rate_bps(subtotal_cents: int) -> int:
    """Discount rate of the tier the subtotal reaches, in basis points."""
    return tier_row(subtotal_cents)[1]


def tier_discount_cents(subtotal_cents: int) -> int:
    """What the tier alone takes off a subtotal of this size."""
    return mul_rate(subtotal_cents, tier_rate_bps(subtotal_cents))
