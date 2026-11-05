"""Feature flags resolved per tenant.

A flag is a rollout percentage plus an explicit tenant allow-list; a tenant
in the list always gets the flag and everyone else gets it when their stable
bucket falls under the percentage. The bucket is a hash of flag name and
tenant, never a random number, so a tenant never flickers between builds.
"""

from shop.errors import ValidationError

__all__ = ["DEFAULTS", "bucket", "enabled", "listing"]

DEFAULTS = {
    "new-checkout": {"percent": 25, "tenants": ("acme", "globex")},
    "one-page-cart": {"percent": 50, "tenants": ()},
    "async-webhooks": {"percent": 100, "tenants": ()},
    "search-synonyms": {"percent": 10, "tenants": ("initech",)},
    "split-shipments": {"percent": 0, "tenants": ("umbrella",)},
}


def bucket(name: str, tenant: str) -> int:
    """A stable 0-99 bucket for the flag and tenant pair."""
    value = 2166136261
    for byte in (str(name) + ":" + str(tenant)).encode("utf-8"):
        value = (value ^ byte) * 16777619 & 0xFFFFFFFF
    return value % 100


def enabled(name: str, tenant: str = "") -> bool:
    """Whether the tenant sees the flag; an unknown flag is a bug."""
    if name not in DEFAULTS:
        raise ValidationError("unknown feature flag %r" % (name,))
    spec = DEFAULTS[name]
    if str(tenant) in spec["tenants"]:
        return True
    return bucket(name, tenant) < int(spec["percent"])


def listing() -> list:
    """Every flag with its rollout, sorted by name."""
    return [
        {"name": name, "percent": int(DEFAULTS[name]["percent"]), "tenants": list(DEFAULTS[name]["tenants"])}
        for name in sorted(DEFAULTS)
    ]
