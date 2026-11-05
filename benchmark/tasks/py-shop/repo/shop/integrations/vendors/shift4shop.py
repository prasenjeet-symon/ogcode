"""Shift4Shop order sync.

Orders leave Shift4Shop in its own vocabulary -- new, processing, shipped, cancelled -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "shift4shop"
LABEL = "Shift4Shop"
AUTH_HEADER = "X-Shift4Shop-Token"
PAGE_SIZE = 50
MIN_TOTAL_CENTS = 1000
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
 'cancelled': 'cancelled'}

SEED = [{'order_id': 'S-7140',
  'status': 'shipped',
  'total_cents': 3750,
  'units': 3,
  'channel': 'marketplace',
  'created_at': '2026-02-02T12:42:00Z'},
 {'order_id': 'S-7141',
  'status': 'cancelled',
  'total_cents': 5375,
  'units': 4,
  'channel': 'web',
  'created_at': '2026-02-03T13:42:00Z'},
 {'order_id': 'S-7142',
  'status': 'new',
  'total_cents': 2000,
  'units': 1,
  'channel': 'mobile',
  'created_at': '2026-02-04T14:42:00Z'},
 {'order_id': 'S-7143',
  'status': 'processing',
  'total_cents': 3625,
  'units': 2,
  'channel': 'marketplace',
  'created_at': '2026-02-05T15:42:00Z'}]


class Client:
    """Talk to a Shift4Shop store without leaving the process."""

    def __init__(self, token: str = "", channel: str = "web"):
        """Seed the queue, the id index and the write history."""
        self.token = token
        self.channel = channel
        self._orders = [dict(row) for row in SEED]
        self._by_id = {row["order_id"]: row for row in self._orders}
        self._cursor = 0
        self.history = []

    def fetch_orders(self) -> list:
        """One page per call; the cursor wraps around at the end."""
        start = self._cursor
        page = self._orders[start:start + PAGE_SIZE]
        self._cursor = 0 if start + PAGE_SIZE >= len(self._orders) else start + PAGE_SIZE
        return [dict(row) for row in page]

    def push_status(self, order_id: str, status: str) -> dict:
        """Write through the id index, failing on an unknown id first."""
        if order_id not in self._by_id:
            raise ValidationError("%s does not know order %r" % (VENDOR, order_id))
        self._by_id[order_id]["status"] = status
        return {"order_id": order_id, "status": status, "indexed": True}
