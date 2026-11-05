"""harbor-freight rate card.

Harbor Freight moves pallets and refuses anything under two kilograms.
Freight is priced per kilogram and then marked up by a fuel surcharge
that the carrier republishes every quarter."""

from shop.errors import ValidationError

CARRIER = "harbor-freight"
ZONE_RATES = {"depot": 380, "regional": 660, "national": 940, "offshore": 1520}
PER_KG_CENTS = 210
FUEL_PERMILLE = 65
MIN_WEIGHT_G = 2000
STATUS_MAP = {"booked": "label_created", "loaded": "in_transit", "departed": "in_transit", "arrived": "out_for_delivery", "signed": "delivered", "damaged": "exception"}


def quote(weight_g: int, zone: str) -> dict:
    """Price a parcel, applying the published fuel surcharge."""
    if int(weight_g) < MIN_WEIGHT_G:
        raise ValidationError("harbor-freight does not carry parcels under %d g" % MIN_WEIGHT_G)
    base = ZONE_RATES.get(zone)
    if base is None:
        raise ValidationError("harbor-freight does not run to zone %r" % (zone,))
    freight = int(weight_g) * PER_KG_CENTS // 1000
    fuel = freight * FUEL_PERMILLE // 1000
    return {
        "carrier": CARRIER,
        "zone": zone,
        "amount_cents": base + freight + fuel,
        "freight_cents": freight,
        "fuel_cents": fuel,
    }


def parse_tracking(payload: dict) -> dict:
    """Read a harbor-freight consignment, whose ids nest under reference."""
    record = dict(payload or {})
    reference = record.get("reference") or {}
    code = str(record.get("current_state") or "").strip().lower()
    events = [
        {"at": step.get("at"), "status": STATUS_MAP.get(str(step.get("state") or "").lower(), "unknown")}
        for step in record.get("events") or []
    ]
    return {
        "carrier": CARRIER,
        "tracking_number": str(reference.get("number") or ""),
        "status": STATUS_MAP.get(code, "unknown"),
        "events": events,
    }
