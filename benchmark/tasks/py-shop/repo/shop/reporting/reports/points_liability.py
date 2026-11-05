"""Outstanding loyalty points.

Points are a liability: the shop owes merchandise for them. The report
values the balance at the redemption rate finance set last year."""

__all__ = ["build"]
POINT_VALUE_CENTS = 1


def build(orders: list[dict], **kwargs) -> dict:
    """Points outstanding and their cash value."""
    seen = {}
    for order in orders or []:
        customer = order.get("customer") or {}
        key = str(customer.get("id") or "")
        if key:
            seen[key] = int(customer.get("points") or 0)
    points = sum(seen.values())
    return {
        "report": "points_liability",
        "customers": len(seen),
        "points": points,
        "value_cents": points * POINT_VALUE_CENTS,
    }
