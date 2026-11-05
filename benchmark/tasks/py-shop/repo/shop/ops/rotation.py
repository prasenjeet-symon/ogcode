"""Credential rotation schedule.

Every secret has an interval and a last rotation date; the status call
answers the only question the on-call calendar cares about, which is how
many days are left and whether the grace window has already passed.
"""

from datetime import date, timedelta

from shop.errors import ValidationError

__all__ = ["ROTATION", "WARN_DAYS", "due_on", "interval_days", "status"]

ROTATION = {
    "api-token": 90,
    "webhook-secret": 180,
    "signing-key": 365,
    "database-password": 60,
    "smtp-password": 120,
}

WARN_DAYS = 14


def interval_days(key: str) -> int:
    """The rotation interval for a secret, refusing an unknown one."""
    if key not in ROTATION:
        raise ValidationError("no rotation interval for %r" % (key,))
    return int(ROTATION[key])


def due_on(key: str, last_rotated: str) -> str:
    """The ISO date the secret comes due."""
    start = date.fromisoformat(str(last_rotated)[:10])
    return (start + timedelta(days=interval_days(key))).isoformat()


def status(key: str, last_rotated: str, today: str) -> dict:
    """Due date, days left and a state of ``ok``, ``due`` or ``overdue``."""
    due = date.fromisoformat(due_on(key, last_rotated))
    now = date.fromisoformat(str(today)[:10])
    left = (due - now).days
    if left < 0:
        state = "overdue"
    elif left <= WARN_DAYS:
        state = "due"
    else:
        state = "ok"
    return {"key": key, "due_on": due.isoformat(), "days_left": left, "state": state}
