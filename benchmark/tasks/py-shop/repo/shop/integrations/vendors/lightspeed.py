"""Lightspeed order sync.

Orders leave Lightspeed in its own vocabulary -- quoted, invoiced, delivered, voided -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "lightspeed"
LABEL = "Lightspeed"
AUTH_HEADER = "X-Lightspeed-Token"
PAGE_SIZE = 20
MIN_TOTAL_CENTS = 2250
CANONICAL_STATUSES = ('new',
 'pending',
 'paid',
 'fulfilling',
 'shipped',
 'delivered',
 'cancelled',
 'refunded')
ACTIVE_STATUSES = ('new', 'paid')
STATUS_ALIASES = {'quoted': 'new', 'invoiced': 'paid', 'delivered': 'delivered', 'voided': 'cancelled'}

SEED = [{'order_id': 'L-7260',
  'status': 'delivered',
  'total_cents': 4250,
  'units': 3,
  'channel': 'marketplace',
  'created_at': '2026-02-02T14:18:00Z'},
 {'order_id': 'L-7261',
  'status': 'voided',
  'total_cents': 5875,
  'units': 4,
  'channel': 'web',
  'created_at': '2026-02-03T15:18:00Z'},
 {'order_id': 'L-7262',
  'status': 'quoted',
  'total_cents': 2500,
  'units': 1,
  'channel': 'mobile',
  'created_at': '2026-02-04T16:18:00Z'},
 {'order_id': 'L-7263',
  'status': 'invoiced',
  'total_cents': 4125,
  'units': 2,
  'channel': 'marketplace',
  'created_at': '2026-02-05T17:18:00Z'}]


class Client:
    """Talk to a Lightspeed store without leaving the process."""

    def __init__(self, token: str = "", channel: str = "web"):
        """Seed the queue, the id index and the write history."""
        self.token = token
        self.channel = channel
        self._orders = [dict(row) for row in SEED]
        self._by_id = {row["order_id"]: row for row in self._orders}
        self._cursor = 0
        self.history = []

    def fetch_orders(self) -> list:
        """Read through the id index, ordered by order id."""
        self._by_id = {row["order_id"]: dict(row) for row in self._orders}
        return [self._by_id[order_id] for order_id in sorted(self._by_id)]

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
