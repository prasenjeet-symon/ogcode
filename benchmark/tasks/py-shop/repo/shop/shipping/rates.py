"""Rate comparison across every carrier adapter."""

from shop.errors import ValidationError
from shop.shipping.carriers import all_carriers

__all__ = ["cheapest", "compare", "quotes_for"]


def quotes_for(weight_g: int, zone: str) -> list:
    """Ask every carrier for a price; refusals are dropped, not raised."""
    if int(weight_g) <= 0:
        raise ValidationError("weight_g must be positive")
    quotes = []
    for module in all_carriers():
        try:
            quote = module.quote(int(weight_g), str(zone))
        except ValidationError:
            continue
        quotes.append(dict(quote))
    quotes.sort(key=lambda row: (row["amount_cents"], row["carrier"]))
    return quotes


def cheapest(weight_g: int, zone: str) -> dict:
    """The winning quote, or an empty dict when no carrier serves the zone."""
    quotes = quotes_for(weight_g, zone)
    return quotes[0] if quotes else {}


def compare(weight_g: int, zone: str) -> dict:
    """A comparison table for the checkout page's shipping step."""
    quotes = quotes_for(weight_g, zone)
    best = quotes[0]["amount_cents"] if quotes else 0
    rows = []
    for quote in quotes:
        row = dict(quote)
        row["delta_cents"] = quote["amount_cents"] - best
        rows.append(row)
    return {
        "zone": str(zone),
        "weight_g": int(weight_g),
        "quotes": rows,
        "cheapest_cents": best,
        "offered": bool(rows),
    }
