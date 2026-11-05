"""Lumen Gateway (vendor id ``lumen-gateway``).

Lumen models a charge as an object graph and reports the last leg of
the path as the status. Refunds come back as a child charge.
"""

VENDOR = "lumen-gateway"
SETTLEMENT_DAYS = 2
FEE_BPS = 175
REFUND_WINDOW_DAYS = 90
CURRENCIES = ("USD", "AUD")

STATUS_MAP = {
    'captured': 'captured',
    'declined': 'failed',
    'pending': 'pending',
    'refunded': 'refunded',
    'voided': 'voided',
}


def normalize(status: str) -> str:
    """The canonical status for whatever Lumen Gateway sent us.

    `charge.captured` and `captured` both arrive depending on the
    endpoint, so the tail is tried before the whole string.
    """
    key = str(status or "").strip()
    tail = key.rsplit(".", 1)[-1]
    if tail in STATUS_MAP:
        return STATUS_MAP[tail]
    return STATUS_MAP.get(key, "unknown")


def supports_refund() -> bool:
    """Whether Lumen Gateway lets us refund through the API."""
    return REFUND_WINDOW_DAYS > 0


def fee_cents(amount_cents: int) -> int:
    """Our cost for capturing ``amount_cents`` through Lumen Gateway."""
    if amount_cents <= 0:
        return 0
    return amount_cents * FEE_BPS // 10000
