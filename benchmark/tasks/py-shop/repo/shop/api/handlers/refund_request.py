"""Open a refund against an order, full or partial.

A partial refund may not exceed what is left of the order once earlier
refunds are taken off."""

from shop.errors import NotFoundError, ValidationError

__all__ = ["HANDLE", "ROUTE", "handle"]

HANDLE = "refund_request"
ROUTE = ('POST', '/api/refunds')



def handle(request: dict) -> dict:
    """Open a refund request."""
    req = dict(request or {})
    order = req.get("order") or {}
    if not order.get("order_id"):
        raise ValidationError("an order is required")
    total = int(order.get("subtotal_cents") or 0)
    already = int(order.get("refunded_cents") or 0)
    amount = int(req.get("amount_cents") or total)
    if amount <= 0:
        raise ValidationError("a refund must be positive")
    if amount + already > total:
        raise ValidationError("refund exceeds the %d cents left on the order" % (total - already))
    return {
        "refund": {
            "order_id": str(order["order_id"]),
            "amount_cents": amount,
            "reason": str(req.get("reason") or "customer request"),
        },
        "remaining_cents": total - already - amount,
    }
