"""Rank the catalogue against a free-text query.

A name match outranks a description match and a tag match outranks both
slightly, which is enough to keep the obvious product on top."""

from shop.errors import NotFoundError, ValidationError

__all__ = ["HANDLE", "ROUTE", "handle"]

HANDLE = "search_products"
ROUTE = ('GET', '/api/products/search')
DEFAULT_LIMIT = 20


def handle(request: dict) -> dict:
    """Search the product list."""
    req = dict(request or {})
    query = str(req.get("q") or "").strip().lower()
    if not query:
        raise ValidationError("q is required")
    terms = [term for term in query.split() if term]
    hits = []
    for product in req.get("products") or []:
        name = str(product.get("name") or "").lower()
        description = str(product.get("description") or "").lower()
        tags = " ".join(str(tag).lower() for tag in product.get("tags") or [])
        score = 0
        for term in terms:
            if term in name:
                score += 5
            if term in tags:
                score += 3
            if term in description:
                score += 1
        if score:
            hits.append({"sku": str(product.get("sku")), "score": score})
    hits.sort(key=lambda hit: (-hit["score"], hit["sku"]))
    limit = int(req.get("limit") or DEFAULT_LIMIT)
    return {"query": query, "hits": hits[:limit], "total": len(hits)}
