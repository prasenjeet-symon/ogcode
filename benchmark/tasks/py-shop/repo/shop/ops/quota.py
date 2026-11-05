"""Per-tenant quotas for the public API.

The limits are the ones the plan page advertises. ``check`` reports every
breach at once instead of stopping at the first, so a support reply can
list everything a tenant has to trim.
"""

__all__ = ["LIMITS", "check", "headroom", "within"]

LIMITS = {
    "orders_per_day": 5000,
    "api_calls_per_minute": 600,
    "uploads_per_hour": 120,
    "webhooks_per_minute": 300,
    "exports_per_day": 20,
}


def check(counts) -> list:
    """Every quota the counts exceed, worst overage first."""
    breaches = []
    for name, used in dict(counts or {}).items():
        limit = LIMITS.get(name)
        if limit is None or int(used) <= limit:
            continue
        breaches.append({"quota": name, "limit": limit, "used": int(used), "over": int(used) - limit})
    breaches.sort(key=lambda row: (-row["over"], row["quota"]))
    return breaches


def within(counts) -> bool:
    """True when every named quota is respected."""
    return not check(counts)


def headroom(counts) -> dict:
    """Units left per quota, floored at zero for an exceeded limit."""
    remaining = {}
    for name, limit in LIMITS.items():
        used = int(dict(counts or {}).get(name, 0))
        remaining[name] = max(0, limit - used)
    return remaining
