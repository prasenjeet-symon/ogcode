"""Basket size distribution.

Four buckets, fixed, because the merchandising dashboard has four slots
and a fifth would need a design conversation nobody has had yet."""

__all__ = ["build"]


def build(orders: list[dict], **kwargs) -> dict:
    """Order counts in each basket-size bucket."""
    buckets = {"1": 0, "2-3": 0, "4-6": 0, "7+": 0}
    for order in orders or []:
        count = int(order.get("items_count") or len(order.get("lines") or []))
        if count <= 1:
            key = "1"
        elif count <= 3:
            key = "2-3"
        elif count <= 6:
            key = "4-6"
        else:
            key = "7+"
        buckets[key] += 1
    return {
        "report": "basket_size_histogram",
        "buckets": buckets,
        "orders": sum(buckets.values()),
    }
