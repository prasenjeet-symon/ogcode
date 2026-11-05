"""Reaction Commerce order sync.

Orders leave Reaction Commerce in its own vocabulary -- new, core_order_approved, shipped, canceled -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "reaction"
LABEL = "Reaction Commerce"
AUTH_HEADER = "X-Reaction Commerce-Token"
PAGE_SIZE = 10
MIN_TOTAL_CENTS = 2500
CANONICAL_STATUSES = ('new',
 'pending',
 'paid',
 'fulfilling',
 'shipped',
 'delivered',
 'cancelled',
 'refunded')
ACTIVE_STATUSES = ('new', 'paid', 'shipped')
STATUS_ALIASES = {'new': 'new',
 'core_order_approved': 'paid',
 'shipped': 'shipped',
 'canceled': 'cancelled'}

SEED = [{'order_id': 'R-7200',
  'status': 'new',
  'total_cents': 4000,
  'units': 1,
  'channel': 'marketplace',
  'created_at': '2026-02-02T08:00:00Z'},
 {'order_id': 'R-7201',
  'status': 'core_order_approved',
  'total_cents': 5625,
  'units': 2,
  'channel': 'web',
  'created_at': '2026-02-03T09:00:00Z'},
 {'order_id': 'R-7202',
  'status': 'shipped',
  'total_cents': 2250,
  'units': 3,
  'channel': 'mobile',
  'created_at': '2026-02-04T10:00:00Z'},
 {'order_id': 'R-7203',
  'status': 'canceled',
  'total_cents': 3875,
  'units': 4,
  'channel': 'marketplace',
  'created_at': '2026-02-05T11:00:00Z'}]


class Client:
    """Talk to a Reaction Commerce store without leaving the process."""

    def __init__(self, token: str = "", channel: str = "web"):
        """Seed the queue, the id index and the write history."""
        self.token = token
        self.channel = channel
        self._orders = [dict(row) for row in SEED]
        self._by_id = {row["order_id"]: row for row in self._orders}
        self._cursor = 0
        self.history = []

    def fetch_orders(self) -> list:
        """Newest first, cut to one page."""
        rows = sorted(self._orders, key=lambda row: row["created_at"], reverse=True)
        return [dict(row) for row in rows[:PAGE_SIZE]]

    def push_status(self, order_id: str, status: str) -> dict:
        """Translate to the canonical status before writing it back."""
        canonical = STATUS_ALIASES.get(status, status)
        if canonical not in CANONICAL_STATUSES:
            raise ValidationError("%s cannot represent status %r" % (VENDOR, status))
        for row in self._orders:
            if row["order_id"] == order_id:
                row["status"] = status
                return {"order_id": order_id, "status": canonical, "native": status}
        raise ValidationError("%s does not know order %r" % (VENDOR, order_id))
