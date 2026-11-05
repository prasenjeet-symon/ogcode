"""cartwright rate card.

Cartwright charges a flat fee for the size class a parcel falls into and
ignores distance entirely, which is why the zone table exists only to
reject addresses the carrier will not collect from."""

from shop.errors import ValidationError

CARRIER = "cartwright"
ZONE_RATES = {"anywhere": 0, "highlands": 0}
TIERS = ((500, "letter"), (2000, "parcel"), (10000, "box"), (25000, "crate"))
TIER_CENTS = {"letter": 190, "parcel": 540, "box": 1180, "crate": 2450, "freight": 3900}
STATUS_MAP = {"received": "label_created", "in_network": "in_transit", "on_route": "out_for_delivery", "completed": "delivered", "returned_to_sender": "returned", "refused": "exception"}


def _tier(weight_g: int) -> str:
    for limit, name in TIERS:
        if weight_g <= limit:
            return name
    return "freight"


def quote(weight_g: int, zone: str) -> dict:
    """Price a parcel from its weight class, not from its zone."""
    if zone not in ZONE_RATES:
        raise ValidationError("cartwright will not collect from zone %r" % (zone,))
    tier = _tier(int(weight_g))
    return {"carrier": CARRIER, "zone": zone, "amount_cents": TIER_CENTS[tier], "tier": tier}


def parse_tracking(payload: dict) -> dict:
    """Read a cartwright consignment, whose progress list is nested."""
    record = dict(payload or {})
    consignment = record.get("consignment") or {}
    steps = record.get("progress") or []
    events = [
        {"at": step.get("at"), "status": STATUS_MAP.get(str(step.get("state") or "").lower(), "unknown")}
        for step in steps
    ]
    status = events[-1]["status"] if events else "unknown"
    return {
        "carrier": CARRIER,
        "tracking_number": str(consignment.get("id") or ""),
        "status": status,
        "events": events,
    }
