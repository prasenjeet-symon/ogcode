"""blue-crest rate card.

Blue Crest keeps a minimum charge per parcel: small, light shipments are
lifted to the floor so the carrier never runs a parcel at a loss. The
result says whether the floor was what the customer actually paid."""

from shop.errors import ValidationError

CARRIER = "blue-crest"
ZONE_RATES = {"bay": 399, "valley": 585, "coast": 815, "inland": 1040}
PER_KG_CENTS = {"bay": 110, "valley": 135, "coast": 175, "inland": 205}
MIN_CHARGE_CENTS = 650
STATUS_MAP = {"pre_transit": "label_created", "transit": "in_transit", "out_for_delivery": "out_for_delivery", "delivered": "delivered", "failure": "exception", "return_to_sender": "returned"}


def quote(weight_g: int, zone: str) -> dict:
    """Price a parcel and report whether the minimum charge applied."""
    if zone not in ZONE_RATES:
        raise ValidationError("blue-crest has no rate for zone %r" % (zone,))
    cents = ZONE_RATES[zone] + int(weight_g) * PER_KG_CENTS[zone] // 1000
    return {
        "carrier": CARRIER,
        "zone": zone,
        "amount_cents": max(cents, MIN_CHARGE_CENTS),
        "raw_cents": cents,
        "floor_applied": cents < MIN_CHARGE_CENTS,
    }


def parse_tracking(payload: dict) -> dict:
    """Read a blue-crest parcel, whose history is the authoritative list."""
    record = dict(payload or {})
    history = record.get("status_history") or []
    events = [
        {"at": step.get("at"), "status": STATUS_MAP.get(str(step.get("status") or "").lower(), "unknown")}
        for step in history
    ]
    latest = str(record.get("latest") or "").strip().lower()
    status = STATUS_MAP.get(latest, "unknown")
    if status == "unknown" and events:
        status = events[-1]["status"]
    return {
        "carrier": CARRIER,
        "tracking_number": str(record.get("parcel_id") or ""),
        "status": status,
        "events": events,
    }
