"""Brightline Pay (vendor id ``brightline-pay``).

Brightline ships a free-text status written by its own support staff.
There is no enumeration to map against, so we match on words.
"""

VENDOR = "brightline-pay"
SETTLEMENT_DAYS = 1
FEE_BPS = 300
REFUND_WINDOW_DAYS = 90
MAX_FEE_CENTS = 750
CAPTURED_WORDS = ("settle", "captur", "paid", "succeed")
FAILED_WORDS = ("declin", "fail", "reject", "insufficient")

STATUS_MAP = {
    'refunded': 'refunded',
    'pending': 'pending',
}


def normalize(status: str) -> str:
    """The canonical status for whatever Brightline Pay sent us.

    Failure is checked after success because `settled then
    reversed` must read as captured, not failed.
    """
    key = str(status or "").strip().lower()
    for word in CAPTURED_WORDS:
        if word in key:
            return "captured"
    for word in FAILED_WORDS:
        if word in key:
            return "failed"
    if key in STATUS_MAP:
        return STATUS_MAP[key]
    return "unknown"


def supports_refund() -> bool:
    """Whether Brightline Pay lets us refund through the API."""
    return REFUND_WINDOW_DAYS > 0


def fee_cents(amount_cents: int) -> int:
    """Our cost for capturing ``amount_cents`` through Brightline Pay."""
    if amount_cents <= 0:
        return 0
    return min(amount_cents * FEE_BPS // 10000, MAX_FEE_CENTS)
