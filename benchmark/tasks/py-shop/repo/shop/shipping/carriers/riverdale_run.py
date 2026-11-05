"""riverdale-run rate card.

Riverdale Run is the regional co-op. Its card is a base rate per zone
multiplied by a zone factor and by the number of whole kilograms, which
keeps the rural routes from losing money on every parcel."""

from shop.errors import ValidationError

CARRIER = "riverdale-run"
ZONE_RATES = {"core": 480, "belt": 640, "rural": 990}
ZONE_FACTOR = {"core": 1000, "belt": 1180, "rural": 1650}
MIN_KILOS = 1
STATUS_MAP = {"created": "label_created", "collected": "in_transit", "scan": "in_transit", "delivery": "out_for_delivery", "complete": "delivered", "refused": "returned"}


def quote(weight_g: int, zone: str) -> dict:
    """Price a parcel with the co-op zone multiplier."""
    base = ZONE_RATES.get(zone)
    if base is None:
        raise ValidationError("riverdale-run does not cover zone %r" % (zone,))
    kilos = max(MIN_KILOS, int(weight_g) // 1000)
    cents = base * ZONE_FACTOR[zone] * kilos // 1000
    return {"carrier": CARRIER, "zone": zone, "amount_cents": cents, "kilos": kilos}


def parse_tracking(payload: dict) -> dict:
    """Read a riverdale-run scan feed, which calls stages stages."""
    record = dict(payload or {})
    stage = str(record.get("stage") or "").strip().lower()
    events = [
        {"at": scan.get("time"), "status": STATUS_MAP.get(str(scan.get("stage") or "").lower(), "unknown")}
        for scan in record.get("scans") or []
    ]
    return {
        "carrier": CARRIER,
        "tracking_number": str(record.get("barcode") or ""),
        "status": STATUS_MAP.get(stage, "unknown"),
        "events": events,
    }
