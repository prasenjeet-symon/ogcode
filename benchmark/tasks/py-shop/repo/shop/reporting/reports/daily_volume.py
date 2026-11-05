"""Orders and value per calendar day.

The timestamp is read from ``created_at`` with a plain string slice: the
service stores ISO-8601 in UTC, so the first ten characters are the date."""

__all__ = ["build"]


def build(orders: list[dict], **kwargs) -> dict:
    """One row per day, oldest first."""
    days = {}
    for order in orders or []:
        stamp = str(order.get("created_at") or "")
        day = stamp[:10] if len(stamp) >= 10 else "unknown"
        entry = days.setdefault(day, {"day": day, "orders": 0, "cents": 0})
        entry["orders"] += 1
        entry["cents"] += int(order.get("subtotal_cents") or 0)
    rows = [days[key] for key in sorted(days)]
    return {"report": "daily_volume", "rows": rows, "days": len(rows)}
