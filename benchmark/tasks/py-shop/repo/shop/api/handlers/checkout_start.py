"""Open a draft order for a cart and hand back its reference.

The reference is derived from the email and the cart size, so retrying the
same request in the browser produces the same draft instead of two."""

from shop.errors import NotFoundError, ValidationError

__all__ = ["HANDLE", "ROUTE", "handle"]

HANDLE = "checkout_start"
ROUTE = ('POST', '/api/checkout')
import hashlib

DRAFT_PREFIX = "ORD-"


def handle(request: dict) -> dict:
    """Open a checkout draft."""
    req = dict(request or {})
    email = str(req.get("email") or "").strip().lower()
    if "@" not in email:
        raise ValidationError("a valid email is required")
    cart = [dict(line) for line in req.get("cart") or []]
    if not cart:
        raise ValidationError("the cart is empty")
    seed = email + "|" + ",".join(str(line.get("sku")) for line in cart)
    digest = hashlib.sha1(seed.encode("utf-8")).hexdigest()[:6].upper()
    return {
        "order_ref": DRAFT_PREFIX + digest,
        "email": email,
        "lines": len(cart),
        "draft": True,
    }
