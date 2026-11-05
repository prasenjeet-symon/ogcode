"""Shipping charged versus shipping waived.

Finance watches this after a free-shipping promotion: a rise in waived
orders with no matching rise in basket size is a promotion losing money."""

__all__ = ["build"]


def build(orders: list[dict], **kwargs) -> dict:
    """Collected shipping, waived orders and the share waived."""
    orders_list = list(orders or [])
    collected = sum(int(order.get("shipping_cents") or 0) for order in orders_list)
    waived = sum(1 for order in orders_list if int(order.get("shipping_cents") or 0) == 0)
    total = len(orders_list)
    return {
        "report": "shipping_recovery",
        "orders": total,
        "collected_cents": collected,
        "waived_orders": waived,
        "waived_share": round(waived / total, 3) if total else 0.0,
    }
