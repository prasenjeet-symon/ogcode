"""Pause a subscription for a bounded number of days.

Pausing forever is deliberately not offered: a customer who wants to stop
should cancel, which keeps the churn number honest."""

from shop.errors import NotFoundError, ValidationError

__all__ = ["HANDLE", "ROUTE", "handle"]

HANDLE = "subscription_pause"
ROUTE = ('POST', '/api/subscriptions/{id}/pause')
MAX_PAUSE_DAYS = 90


def handle(request: dict) -> dict:
    """Pause a subscription."""
    req = dict(request or {})
    subscription = dict(req.get("subscription") or {})
    if not subscription.get("id"):
        raise ValidationError("a subscription is required")
    if str(subscription.get("state")) == "cancelled":
        raise ValidationError("a cancelled subscription cannot be paused")
    days = int(req.get("days") or 30)
    if not 1 <= days <= MAX_PAUSE_DAYS:
        raise ValidationError("a pause is between 1 and %d days" % MAX_PAUSE_DAYS)
    subscription["state"] = "paused"
    subscription["paused_days"] = days
    return {"subscription": subscription, "resumes_in_days": days}
