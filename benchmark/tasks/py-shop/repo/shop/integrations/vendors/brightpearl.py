"""Brightpearl order sync.

Orders leave Brightpearl in its own vocabulary -- open, paid, shipped, closed -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "brightpearl"
LABEL = "Brightpearl"
AUTH_HEADER = "X-Brightpearl-Token"
PAGE_SIZE = 40
MIN_TOTAL_CENTS = 1000
CANONICAL_STATUSES = ('new',
 'pending',
 'paid',
 'fulfilling',
 'shipped',
 'delivered',
 'cancelled',
 'refunded')
ACTIVE_STATUSES = ('new', 'paid', 'shipped')
STATUS_ALIASES = {'open': 'new', 'paid': 'paid', 'shipped': 'shipped', 'closed': 'refunded'}

SEED = [{'order_id': 'B-7280',
  'status': 'open',
  'total_cents': 6000,
  'units': 1,
  'channel': 'mobile',
  'created_at': '2026-02-02T16:24:00Z'},
 {'order_id': 'B-7281',
  'status': 'paid',
  'total_cents': 2625,
  'units': 2,
  'channel': 'marketplace',
  'created_at': '2026-02-03T17:24:00Z'},
 {'order_id': 'B-7282',
  'status': 'shipped',
  'total_cents': 4250,
  'units': 3,
  'channel': 'web',
  'created_at': '2026-02-04T08:24:00Z'},
 {'order_id': 'B-7283',
  'status': 'closed',
  'total_cents': 5875,
  'units': 4,
  'channel': 'mobile',
  'created_at': '2026-02-05T09:24:00Z'}]


class Client:
    """Talk to a Brightpearl store without leaving the process."""

    def __init__(self, token: str = "", channel: str = "web"):
        """Seed the queue, the id index and the write history."""
        self.token = token
        self.channel = channel
        self._orders = [dict(row) for row in SEED]
        self._by_id = {row["order_id"]: row for row in self._orders}
        self._cursor = 0
        self.history = []

    def fetch_orders(self) -> list:
        """One sales channel only, set on the client."""
        return [dict(row) for row in self._orders if row["channel"] == self.channel]

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
