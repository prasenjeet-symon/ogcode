"""Mint a label and tracking number for an order.

The tracking number is derived from the order and the carrier so a repeated
request reprints the same label instead of buying a second one."""

from shop.errors import NotFoundError, ValidationError

__all__ = ["HANDLE", "ROUTE", "handle"]

HANDLE = "shipping_label"
ROUTE = ('POST', '/api/shipping/labels')
import hashlib

CARRIERS = {"northpost": "NP", "swiftlink": "SW", "harbor_freight": "HF"}


def handle(request: dict) -> dict:
    """Mint a carrier label."""
    req = dict(request or {})
    order_id = str(req.get("order_id") or "").strip()
    carrier = str(req.get("carrier") or "").strip().lower()
    if not order_id:
        raise ValidationError("order_id is required")
    if carrier not in CARRIERS:
        raise ValidationError("unknown carrier %s" % carrier)
    seed = order_id + "|" + carrier
    tail = hashlib.sha1(seed.encode("utf-8")).hexdigest()[:10].upper()
    return {
        "label": {
            "order_id": order_id,
            "carrier": carrier,
            "tracking_number": CARRIERS[carrier] + tail,
            "format": str(req.get("format") or "pdf"),
        }
    }
