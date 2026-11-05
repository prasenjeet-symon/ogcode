"""Accept a review with a star rating and a body.

A review is stored unapproved: the store moderates before anything appears
on the product page, so this handler never publishes."""

from shop.errors import NotFoundError, ValidationError

__all__ = ["HANDLE", "ROUTE", "handle"]

HANDLE = "review_submit"
ROUTE = ('POST', '/api/reviews')
MIN_BODY = 20


def handle(request: dict) -> dict:
    """Accept a product review."""
    req = dict(request or {})
    sku = str(req.get("sku") or "").strip()
    if not sku:
        raise ValidationError("sku is required")
    rating = req.get("rating")
    if not isinstance(rating, int) or isinstance(rating, bool) or not 1 <= rating <= 5:
        raise ValidationError("rating must be an integer from 1 to 5")
    body = str(req.get("body") or "").strip()
    if len(body) < MIN_BODY:
        raise ValidationError("a review needs at least %d characters" % MIN_BODY)
    return {
        "review": {
            "sku": sku,
            "rating": rating,
            "body": body,
            "author": str(req.get("author") or "anonymous"),
            "approved": False,
        }
    }
