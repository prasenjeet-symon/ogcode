"""Coupon codes.

A code is looked up case-insensitively and ignores surrounding whitespace. The
table is small on purpose; the real service keeps it in the promotions
database, and this module is the seam that would move there.
"""

from dataclasses import dataclass

from shop.errors import NotFoundError, ValidationError


@dataclass(frozen=True)
class Coupon:
    """One coupon: what kind it is and how much it is worth."""

    code: str
    kind: str  # "percent" or "fixed"
    value: int  # basis points for "percent", cents for "fixed"


# code -> (kind, value)
COUPONS = {
    "WELCOME10": ("percent", 1000),
    "SPRING15": ("percent", 1500),
    "SAVE5": ("fixed", 500),
    "BIG25": ("fixed", 2500),
}


def normalize_code(code) -> str:
    """The canonical spelling of a coupon code; empty for a blank code."""
    if code is None:
        return ""
    return str(code).strip().upper()


def lookup(code) -> Coupon:
    """The coupon for ``code``, raising if there is no such code."""
    key = normalize_code(code)
    if not key:
        raise ValidationError("empty coupon code")
    entry = COUPONS.get(key)
    if entry is None:
        raise NotFoundError(f"unknown coupon {key}")
    return Coupon(code=key, kind=entry[0], value=entry[1])
