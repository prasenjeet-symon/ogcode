"""Northwind Pay (vendor id ``northwind-pay``).

The oldest integration we have. Northwind speaks uppercase words and
settles in two days, and its sandbox still returns the pre-2019 vocabulary for
refunded charges.
"""

VENDOR = "northwind-pay"
SETTLEMENT_DAYS = 2
FEE_BPS = 190
REFUND_WINDOW_DAYS = 120
MIN_FEE_CENTS = 25
CURRENCIES = ("USD", "CAD")

STATUS_MAP = {
    'AUTHORIZED': 'authorized',
    'CAPTURED': 'captured',
    'SETTLED': 'captured',
    'PENDING': 'pending',
    'DECLINED': 'failed',
    'REFUNDED': 'refunded',
    'VOIDED': 'voided',
}


def normalize(status: str) -> str:
    """The canonical status for whatever Northwind Pay sent us.

    Northwind sends the status verbatim, padded with spaces by its
    XML-era client, so stripping is the whole job.
    """
    key = str(status or "").strip()
    return STATUS_MAP.get(key, "unknown")


def supports_refund() -> bool:
    """Whether Northwind Pay lets us refund through the API."""
    return REFUND_WINDOW_DAYS > 0


def fee_cents(amount_cents: int) -> int:
    """Our cost for capturing ``amount_cents`` through Northwind Pay."""
    if amount_cents <= 0:
        return 0
    return max(amount_cents * FEE_BPS // 10000, MIN_FEE_CENTS)
