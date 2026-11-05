"""Customers bucketed by lifetime value.

Bands are in whole dollars but the boundaries are stored in cents, because
everything the service computes is in cents."""

__all__ = ["build"]
BANDS = ((10000, "under-100"), (50000, "100-499"), (200000, "500-1999"))


def build(orders: list[dict], **kwargs) -> dict:
    """Customer counts and value per lifetime-value band."""
    bands = {name: {"band": name, "customers": 0, "cents": 0} for _, name in BANDS}
    bands["2000+"] = {"band": "2000+", "customers": 0, "cents": 0}
    seen = set()
    for order in orders or []:
        customer = order.get("customer") or {}
        key = str(customer.get("id") or "")
        if not key or key in seen:
            continue
        seen.add(key)
        lifetime = int(customer.get("lifetime_cents") or 0)
        name = "2000+"
        for limit, label in BANDS:
            if lifetime < limit:
                name = label
                break
        bands[name]["customers"] += 1
        bands[name]["cents"] += lifetime
    return {"report": "customer_ltv_bands", "rows": list(bands.values()), "customers": len(seen)}
