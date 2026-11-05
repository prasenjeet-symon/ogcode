"""Average order value.

Cancelled and empty baskets are excluded: they drag an average down without
describing anything a merchant can act on."""

__all__ = ["build"]


def build(orders: list[dict], **kwargs) -> dict:
    """Count, total and average merchandise value of real orders."""
    kept = [order for order in orders or [] if int(order.get("subtotal_cents") or 0) > 0]
    total = sum(int(order.get("subtotal_cents") or 0) for order in kept)
    count = len(kept)
    return {
        "report": "average_order_value",
        "orders": count,
        "total_cents": total,
        "average_cents": total // count if count else 0,
    }
