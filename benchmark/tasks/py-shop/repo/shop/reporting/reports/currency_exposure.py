"""Settlement currency exposure.

The treasury team needs to know how much of the book is in each currency
before the daily hedge, so this counts orders and value per code."""

from shop.config import DEFAULT_CURRENCY

__all__ = ["build"]


def build(orders: list[dict], **kwargs) -> dict:
    """Orders and value per settlement currency."""
    rows = {}
    for order in orders or []:
        code = str(order.get("currency") or DEFAULT_CURRENCY)
        entry = rows.setdefault(code, {"currency": code, "orders": 0, "cents": 0})
        entry["orders"] += 1
        entry["cents"] += int(order.get("subtotal_cents") or 0)
    home = str(kwargs.get("home_currency", DEFAULT_CURRENCY))
    ordered = [rows[key] for key in sorted(rows)]
    foreign = [row for row in ordered if row["currency"] != home]
    return {
        "report": "currency_exposure",
        "rows": ordered,
        "foreign_cents": sum(row["cents"] for row in foreign),
        "home_currency": home,
    }
