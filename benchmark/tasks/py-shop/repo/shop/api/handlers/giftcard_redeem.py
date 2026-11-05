"""Draw down a gift card balance against an amount due.

The card is spent only up to what is owed, and whatever is left stays on the
card for next time."""

from shop.errors import NotFoundError, ValidationError

__all__ = ["HANDLE", "ROUTE", "handle"]

HANDLE = "giftcard_redeem"
ROUTE = ('POST', '/api/giftcards/redeem')



def handle(request: dict) -> dict:
    """Spend a gift card."""
    req = dict(request or {})
    card = dict(req.get("card") or {})
    code = str(card.get("code") or "").strip().upper()
    if not code:
        raise ValidationError("a gift card code is required")
    balance = int(card.get("balance_cents") or 0)
    if balance <= 0:
        raise ValidationError("gift card %s has no balance" % code)
    due = int(req.get("amount_cents") or balance)
    if due <= 0:
        raise ValidationError("amount_cents must be positive")
    applied = min(balance, due)
    card["balance_cents"] = balance - applied
    return {
        "card": card,
        "applied_cents": applied,
        "outstanding_cents": due - applied,
    }
