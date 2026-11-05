"""Calder Pay (vendor id ``calder-pay``).

Calder answers with a single digit and a separate message field. It
is the only gateway we hold that cannot refund through the API; support does
those by hand in the portal.
"""

VENDOR = "calder-pay"
SETTLEMENT_DAYS = 1
FEE_BPS = 290
REFUND_WINDOW_DAYS = 0
MAX_FEE_CENTS = 900
MSG_CODES = {"0": "approved", "2": "review"}

STATUS_MAP = {
    '0': 'captured',
    '1': 'failed',
    '2': 'pending',
    '3': 'failed',
    '4': 'refunded',
}


def normalize(status: str) -> str:
    """The canonical status for whatever Calder Pay sent us.

    Calder sends its status as a string that may or may not carry
    leading zeroes, so the code is normalised through int() first.
    """
    raw = str(status if status is not None else "").strip()
    try:
        raw = str(int(raw))
    except ValueError:
        pass
    return STATUS_MAP.get(raw, "unknown")


def supports_refund() -> bool:
    """Whether Calder Pay lets us refund through the API."""
    return REFUND_WINDOW_DAYS > 0


def fee_cents(amount_cents: int) -> int:
    """Our cost for capturing ``amount_cents`` through Calder Pay."""
    if amount_cents <= 0:
        return 0
    return min(amount_cents * FEE_BPS // 10000, MAX_FEE_CENTS)
