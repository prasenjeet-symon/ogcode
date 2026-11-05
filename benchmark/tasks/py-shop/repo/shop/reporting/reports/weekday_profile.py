"""Order volume by day of week.

Used to schedule the support rota. Orders with an unreadable timestamp
are skipped rather than lumped into a fake weekday."""

import datetime

__all__ = ["build"]


def build(orders: list[dict], **kwargs) -> dict:
    """Order counts and value for each weekday name."""
    names = ("monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday")
    counts = {name: {"weekday": name, "orders": 0, "cents": 0} for name in names}
    skipped = 0
    for order in orders or []:
        stamp = str(order.get("created_at") or "")[:10]
        try:
            day = datetime.date.fromisoformat(stamp)
        except ValueError:
            skipped += 1
            continue
        entry = counts[names[day.weekday()]]
        entry["orders"] += 1
        entry["cents"] += int(order.get("subtotal_cents") or 0)
    return {
        "report": "weekday_profile",
        "rows": [counts[name] for name in names],
        "skipped": skipped,
    }
