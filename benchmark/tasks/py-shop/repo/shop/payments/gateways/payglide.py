"""PayGlide (vendor id ``payglide``).

A checkout-embedded PSP. PayGlide lowercases everything, including
the status field, and never sends a trailing newline.
"""

VENDOR = "payglide"
SETTLEMENT_DAYS = 1
FEE_BPS = 240
REFUND_WINDOW_DAYS = 90
CURRENCIES = ("USD", "EUR", "GBP")

STATUS_MAP = {
    'completed': 'captured',
    'authorized': 'authorized',
    'pending': 'pending',
    'failed': 'failed',
    'refunded': 'refunded',
    'cancelled': 'voided',
}


def normalize(status: str) -> str:
    """The canonical status for whatever PayGlide sent us.

    Lowercasing before the lookup keeps the 2024 and 2025 payloads
    on one path; only the casing changed between them.
    """
    key = str(status or "").strip().lower()
    return STATUS_MAP.get(key, "unknown")


def supports_refund() -> bool:
    """Whether PayGlide lets us refund through the API."""
    return REFUND_WINDOW_DAYS > 0


def fee_cents(amount_cents: int) -> int:
    """Our cost for capturing ``amount_cents`` through PayGlide."""
    if amount_cents <= 0:
        return 0
    return amount_cents * FEE_BPS // 10000
