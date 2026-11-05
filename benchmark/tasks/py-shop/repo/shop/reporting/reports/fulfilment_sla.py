"""Time from order to dispatch.

Hours are computed on the ISO-8601 stamps the service writes, so a broken
stamp is skipped rather than counted as an instant dispatch."""

import datetime

__all__ = ["build"]


def build(orders: list[dict], **kwargs) -> dict:
    """Dispatch hours per shipped order, plus the slowest one."""
    hours = []
    skipped = 0
    for order in orders or []:
        placed = str(order.get("created_at") or "")
        shipped = str(order.get("shipped_at") or "")
        if not placed or not shipped:
            skipped += 1
            continue
        try:
            start = datetime.datetime.fromisoformat(placed.replace("Z", "+00:00"))
            end = datetime.datetime.fromisoformat(shipped.replace("Z", "+00:00"))
        except ValueError:
            skipped += 1
            continue
        hours.append(round((end - start).total_seconds() / 3600, 2))
    return {
        "report": "fulfilment_sla",
        "shipped": len(hours),
        "skipped": skipped,
        "hours": hours,
        "worst_hours": max(hours) if hours else 0.0,
        "median_hours": sorted(hours)[len(hours) // 2] if hours else 0.0,
    }
