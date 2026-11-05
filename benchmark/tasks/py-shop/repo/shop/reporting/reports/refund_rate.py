"""Refund rate by channel.

An order counts as refunded when it carries a refunded amount, whether
the whole order came back or only one line did."""

__all__ = ["build"]


def build(orders: list[dict], **kwargs) -> dict:
    """Refunded orders and refunded cents per channel."""
    rows = {}
    for order in orders or []:
        channel = str(order.get("channel") or "unknown")
        entry = rows.setdefault(channel, {"channel": channel, "orders": 0, "refunds": 0, "cents": 0})
        entry["orders"] += 1
        refunded = int(order.get("refunded_cents") or 0)
        if refunded > 0:
            entry["refunds"] += 1
            entry["cents"] += refunded
    ordered = [rows[key] for key in sorted(rows)]
    for row in ordered:
        row["rate"] = round(row["refunds"] / row["orders"], 3) if row["orders"] else 0.0
    return {"report": "refund_rate", "rows": ordered, "refunds": sum(row["refunds"] for row in ordered)}
