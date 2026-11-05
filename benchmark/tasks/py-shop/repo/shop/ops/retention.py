"""How long each table's rows survive.

Retention is policy rather than code: the nightly job asks this module
which tables it may prune and how old a row has to be before it goes.
"""

from datetime import date, timedelta

from shop.errors import ValidationError

__all__ = ["POLICIES", "purge_before", "retention_days", "should_purge"]

POLICIES = {
    "webhook_events": {"days": 14, "key": "received_at"},
    "search_queries": {"days": 45, "key": "asked_at"},
    "cart_snapshots": {"days": 60, "key": "captured_at"},
    "payment_attempts": {"days": 400, "key": "attempted_at"},
    "audit_entries": {"days": 730, "key": "recorded_at"},
}


def retention_days(table: str) -> int:
    """The window for a table, refusing one nobody set a policy for."""
    if table not in POLICIES:
        raise ValidationError("no retention policy for table %r" % (table,))
    return int(POLICIES[table]["days"])


def should_purge(table: str, age_days: int) -> bool:
    """Whether a row that old is past the window and may go."""
    return int(age_days) >= retention_days(table)


def purge_before(table: str, today: str, days=None) -> str:
    """The ISO date before which rows of ``table`` may be removed."""
    window = retention_days(table) if days is None else int(days)
    if window <= 0:
        raise ValidationError("retention window for %r must be positive" % (table,))
    start = date.fromisoformat(str(today)[:10])
    return (start - timedelta(days=window)).isoformat()
