"""Catalog pricing: quantity breaks and the sale calendar."""

from shop.errors import ValidationError

# sku prefix -> units that unlock the break, and the rate in basis points
QUANTITY_BREAKS = {
    "BULK": (10, 1200),
    "CASE": (6, 800),
}
DEFAULT_BREAK = (12, 500)


def break_bps(sku: str, quantity: int) -> int:
    """The quantity-break rate for a sku at a given quantity, in basis points."""
    quantity = int(quantity)
    if quantity < 1:
        raise ValidationError(f"quantity must be positive, got {quantity}")
    units, rate = DEFAULT_BREAK
    prefix = str(sku).split("-")[0].upper()
    if prefix in QUANTITY_BREAKS:
        units, rate = QUANTITY_BREAKS[prefix]
    return rate if quantity >= units else 0


def unit_price_cents(price_cents: int, sku: str, quantity: int) -> int:
    """The unit price after any quantity break, in cents."""
    from shop.money import mul_rate

    price = int(price_cents)
    rate = break_bps(sku, quantity)
    return price - mul_rate(price, rate)
