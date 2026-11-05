"""The component health report behind ``GET /health``.

Every probe returns the same small dict, and ``overall`` folds them into the
one status the load balancer reads. A failing critical component takes the
whole service down; anything else only degrades it.
"""

__all__ = ["CRITICAL", "component", "merge", "overall"]

CRITICAL = ("database", "payments")


def component(name: str, ok: bool, detail: str = "", latency_ms: int = 0) -> dict:
    """One probe result in the shape the health page renders."""
    return {
        "name": str(name),
        "ok": bool(ok),
        "detail": str(detail),
        "latency_ms": int(latency_ms),
    }


def merge(*groups) -> list:
    """Flatten several probe lists into one, keeping their order."""
    merged = []
    for group in groups:
        for item in group:
            merged.append(dict(item))
    return merged


def overall(components) -> dict:
    """``ok``, ``degraded`` or ``down``, plus the names that failed."""
    checks = [dict(item) for item in components]
    failed = [str(item.get("name", "")) for item in checks if not item.get("ok")]
    if not checks:
        status = "unknown"
    elif not failed:
        status = "ok"
    elif any(name in CRITICAL for name in failed):
        status = "down"
    else:
        status = "degraded"
    return {"status": status, "checked": len(checks), "failed": sorted(failed)}
