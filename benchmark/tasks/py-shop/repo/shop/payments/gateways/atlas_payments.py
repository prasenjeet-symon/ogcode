"""Atlas Payments (vendor id ``atlas-payments``).

Atlas pads its status field to a fixed width for a fixed-width report
and never updated the REST path when it grew one.
"""

VENDOR = "atlas-payments"
SETTLEMENT_DAYS = 2
FEE_BPS = 200
REFUND_WINDOW_DAYS = 60
CURRENCIES = ("USD", "MXN")

STATUS_MAP = {
    'CAPTURED': 'captured',
    'DECLINED': 'failed',
    'AUTHORIZED': 'authorized',
    'REFUNDED': 'refunded',
    'PENDINGREVIEW': 'pending',
}


def normalize(status: str) -> str:
    """The canonical status for whatever Atlas Payments sent us.

    `PENDING REVIEW` and `PENDINGREVIEW` both occur, so all
    internal whitespace is dropped before the lookup.
    """
    key = "".join(str(status or "").split()).upper()
    return STATUS_MAP.get(key, "unknown")


def supports_refund() -> bool:
    """Whether Atlas Payments lets us refund through the API."""
    return REFUND_WINDOW_DAYS > 0


def fee_cents(amount_cents: int) -> int:
    """Our cost for capturing ``amount_cents`` through Atlas Payments."""
    if amount_cents <= 0:
        return 0
    return amount_cents * FEE_BPS // 10000
