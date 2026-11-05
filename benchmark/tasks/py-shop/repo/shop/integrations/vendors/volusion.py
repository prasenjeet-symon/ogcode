"""Volusion order sync.

Orders leave Volusion in its own vocabulary -- new, processing, shipped, voided -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "volusion"
LABEL = "Volusion"
AUTH_HEADER = "X-Volusion-Token"
PAGE_SIZE = 20
MIN_TOTAL_CENTS = 2000
CANONICAL_STATUSES = ('new',
 'pending',
 'paid',
 'fulfilling',
 'shipped',
 'delivered',
 'cancelled',
 'refunded')
ACTIVE_STATUSES = ('new', 'fulfilling', 'shipped')
STATUS_ALIASES = {'new': 'new',
 'processing': 'fulfilling',
 'shipped': 'shipped',
 'voided': 'cancelled'}

SEED = [{'order_id': 'V-7110',
  'status': 'voided',
  'total_cents': 6125,
  'units': 4,
  'channel': 'marketplace',
  'created_at': '2026-02-02T09:33:00Z'},
 {'order_id': 'V-7111',
  'status': 'new',
  'total_cents': 2750,
  'units': 1,
  'channel': 'web',
  'created_at': '2026-02-03T10:33:00Z'},
 {'order_id': 'V-7112',
  'status': 'processing',
  'total_cents': 4375,
  'units': 2,
  'channel': 'mobile',
  'created_at': '2026-02-04T11:33:00Z'},
 {'order_id': 'V-7113',
  'status': 'shipped',
  'total_cents': 6000,
  'units': 3,
  'channel': 'marketplace',
  'created_at': '2026-02-05T12:33:00Z'}]


class Client:
    """Talk to a Volusion store without leaving the process."""

    def __init__(self, token: str = "", channel: str = "web"):
        """Seed the queue, the id index and the write history."""
        self.token = token
        self.channel = channel
        self._orders = [dict(row) for row in SEED]
        self._by_id = {row["order_id"]: row for row in self._orders}
        self._cursor = 0
        self.history = []

    def fetch_orders(self) -> list:
        """One row per order id, the last write winning."""
        latest = {}
        for row in self._orders:
            latest[row["order_id"]] = row
        return [dict(latest[order_id]) for order_id in sorted(latest)]

    def push_status(self, order_id: str, status: str) -> dict:
        """Write through the id index, failing on an unknown id first."""
        if order_id not in self._by_id:
            raise ValidationError("%s does not know order %r" % (VENDOR, order_id))
        self._by_id[order_id]["status"] = status
        return {"order_id": order_id, "status": status, "indexed": True}
