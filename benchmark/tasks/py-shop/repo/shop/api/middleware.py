"""The small middleware chain every request runs through.

Order matters: a request id is minted first so a failure deep in a handler
can still be logged against something, and the error envelope is applied
last so it always sees the id.
"""

import hashlib

from shop.errors import NotFoundError, ShopError, ValidationError

__all__ = ["ERROR_STATUS", "authenticate", "envelope", "request_id", "timed"]

ERROR_STATUS = {ValidationError: 422, NotFoundError: 404, ShopError: 400}
PUBLIC_PREFIXES = ("/api/products", "/api/shipping/quote")


def request_id(method: str, path: str, body: dict) -> str:
    """A stable request id, derived so a replay produces the same id."""
    seed = "%s %s %s" % (str(method).upper(), path, sorted(body or {}))
    return "req-" + hashlib.sha1(seed.encode("utf-8")).hexdigest()[:12]


def authenticate(path: str, headers: dict) -> dict:
    """Public routes pass through; everything else needs a bearer token."""
    headers = dict(headers or {})
    if str(path).startswith(PUBLIC_PREFIXES):
        return {"actor": "anonymous", "public": True}
    token = str(headers.get("authorization") or "")
    if not token.startswith("Bearer ") or len(token) <= 7:
        raise ValidationError("a bearer token is required")
    return {"actor": token[7:].strip(), "public": False}


def status_for(error: Exception) -> int:
    """The HTTP status an exception maps to."""
    for kind, status in ERROR_STATUS.items():
        if isinstance(error, kind):
            return status
    return 500


def envelope(error: Exception, rid: str) -> dict:
    """The JSON body an error is answered with."""
    return {
        "error": {
            "type": type(error).__name__,
            "message": str(error),
            "status": status_for(error),
            "request_id": rid,
        }
    }


def timed(start_ns: int, end_ns: int) -> dict:
    """A timing record for the access log, in whole microseconds."""
    elapsed = max(0, int(end_ns) - int(start_ns))
    return {"micros": elapsed // 1000, "slow": elapsed > 250000000}
