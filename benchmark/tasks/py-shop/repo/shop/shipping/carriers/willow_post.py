"""willow-post rate card.

Willow Post is the last of the residential-first carriers: addresses in
its residential zones pay a small delivery premium, because the round
ends at a front door rather than a loading dock."""

from shop.errors import ValidationError

CARRIER = "willow-post"
ZONE_RATES = {"urban": 430, "suburban": 575, "rural": 825, "remote_rural": 1180}
PER_KG_CENTS = {"urban": 115, "suburban": 140, "rural": 180, "remote_rural": 235}
RESIDENTIAL_ZONES = frozenset({"suburban", "rural", "remote_rural"})
RESIDENTIAL_CENTS = 165
STATUS_MAP = {"label": "label_created", "moving": "in_transit", "final_mile": "out_for_delivery", "delivered": "delivered", "lost": "exception"}


def quote(weight_g: int, zone: str) -> dict:
    """Price a parcel, adding the residential premium where it applies."""
    if zone not in ZONE_RATES:
        raise ValidationError("willow-post does not deliver to zone %r" % (zone,))
    residential = zone in RESIDENTIAL_ZONES
    cents = ZONE_RATES[zone] + int(weight_g) * PER_KG_CENTS[zone] // 1000
    if residential:
        cents += RESIDENTIAL_CENTS
    return {
        "carrier": CARRIER,
        "zone": zone,
        "amount_cents": cents,
        "residential": residential,
    }


def parse_tracking(payload: dict) -> dict:
    """Read a willow-post document, which nests events under tracking."""
    record = dict(payload or {})
    tracking = record.get("tracking") or {}
    events = [
        {"at": step.get("ts"), "status": STATUS_MAP.get(str(step.get("code") or "").lower(), "unknown")}
        for step in tracking.get("events") or []
    ]
    status = STATUS_MAP.get(str(record.get("status") or "").strip().lower(), "unknown")
    return {
        "carrier": CARRIER,
        "tracking_number": str(tracking.get("number") or ""),
        "status": status,
        "events": events,
    }
