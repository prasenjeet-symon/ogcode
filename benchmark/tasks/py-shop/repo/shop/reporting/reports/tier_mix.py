"""Order mix by loyalty tier.

Answers the question the loyalty team asks every quarter: are the higher
tiers actually carrying the revenue, or just the discount budget?"""

__all__ = ["build"]


def build(orders: list[dict], **kwargs) -> dict:
    """Orders and value per tier, tiers in name order."""
    counts = {}
    for order in orders or []:
        customer = order.get("customer") or {}
        tier = str(customer.get("tier") or "none")
        bucket = counts.setdefault(tier, {"tier": tier, "orders": 0, "cents": 0})
        bucket["orders"] += 1
        bucket["cents"] += int(order.get("subtotal_cents") or 0)
    rows = [counts[key] for key in sorted(counts)]
    return {"report": "tier_mix", "rows": rows, "tiers": len(rows)}
