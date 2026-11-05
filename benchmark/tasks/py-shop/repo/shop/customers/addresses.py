"""Address normalisation and the domestic/international split."""

from shop.errors import ValidationError

__all__ = ["DOMESTIC_COUNTRIES", "is_domestic", "normalize", "one_line"]

DOMESTIC_COUNTRIES = frozenset({"US", "CA", "MX"})

REQUIRED = ("line1", "city", "country")


def normalize(address: dict) -> dict:
    """Upper-case the codes, tidy the text, and refuse incomplete rows."""
    row = dict(address or {})
    clean = {}
    for field in ("line1", "line2", "city", "region", "postcode"):
        value = str(row.get(field) or "").strip()
        clean[field] = value
    clean["country"] = str(row.get("country") or "").strip().upper()
    missing = [field for field in REQUIRED if not clean.get(field)]
    if missing:
        raise ValidationError("address is missing %s" % ", ".join(missing))
    clean["city"] = clean["city"].title()
    clean["region"] = clean["region"].upper()
    clean["postcode"] = clean["postcode"].upper().replace(" ", "")
    if len(clean["country"]) != 2:
        raise ValidationError("country must be a two-letter code")
    return clean


def is_domestic(address: dict) -> bool:
    """True when the address is in a country the shop ships to itself."""
    country = str((address or {}).get("country") or "").strip().upper()
    return country in DOMESTIC_COUNTRIES


def one_line(address: dict) -> str:
    """The address as a single line for a packing slip."""
    row = normalize(address)
    parts = [row["line1"], row["line2"], row["city"], row["region"], row["postcode"]]
    return ", ".join(part for part in parts if part)
