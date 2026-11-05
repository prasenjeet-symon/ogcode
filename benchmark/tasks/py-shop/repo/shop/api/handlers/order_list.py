"""List orders, newest first, with an optional status and channel filter.

Sorting is stable on the created-at stamp, so a page of orders placed in the
same second keeps the order the caller supplied."""

from shop.errors import NotFoundError, ValidationError

__all__ = ["HANDLE", "ROUTE", "handle"]

HANDLE = "order_list"
ROUTE = ('GET', '/api/orders')
DEFAULT_LIMIT = 50


def handle(request: dict) -> dict:
    """List orders with a filter."""
    req = dict(request or {})
    status = str(req.get("status") or "").strip()
    channel = str(req.get("channel") or "").strip()
    rows = [dict(row) for row in req.get("orders") or []]
    if status:
        rows = [row for row in rows if str(row.get("status")) == status]
    if channel:
        rows = [row for row in rows if str(row.get("channel")) == channel]
    rows.sort(key=lambda row: str(row.get("created_at") or ""), reverse=True)
    limit = int(req.get("limit") or DEFAULT_LIMIT)
    if limit < 0:
        raise ValidationError("limit must not be negative")
    return {"orders": rows[:limit], "matched": len(rows), "returned": min(limit, len(rows))}
