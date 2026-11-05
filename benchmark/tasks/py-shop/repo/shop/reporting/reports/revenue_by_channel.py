"""Revenue by sales channel.

The commercial team compares channels on merchandise value, so shipping
and tax are deliberately left out of the totals here."""

__all__ = ["build"]


def build(orders: list[dict], **kwargs) -> dict:
    """Orders and merchandise cents per channel, channels sorted."""
    rows = {}
    for order in orders or []:
        channel = str(order.get("channel") or "unknown")
        row = rows.setdefault(channel, {"channel": channel, "orders": 0, "cents": 0})
        row["orders"] += 1
        row["cents"] += int(order.get("subtotal_cents") or 0)
    return {
        "report": "revenue_by_channel",
        "rows": [rows[key] for key in sorted(rows)],
        "orders": len(orders or []),
    }
