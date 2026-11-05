"""CS-Cart order sync.

Orders leave CS-Cart in its own vocabulary -- open, processed, shipped, declined -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "cs_cart"
LABEL = "CS-Cart"
AUTH_HEADER = "X-CS-Cart-Token"
PAGE_SIZE = 50
MIN_TOTAL_CENTS = 1750
CANONICAL_STATUSES = ('new',
 'pending',
 'paid',
 'fulfilling',
 'shipped',
 'delivered',
 'cancelled',
 'refunded')
ACTIVE_STATUSES = ('new', 'paid', 'shipped')
STATUS_ALIASES = {'open': 'new', 'processed': 'paid', 'shipped': 'shipped', 'declined': 'cancelled'}

SEED = [{'order_id': 'CC-7240',
  'status': 'open',
  'total_cents': 2500,
  'units': 1,
  'channel': 'web',
  'created_at': '2026-02-02T12:12:00Z'},
 {'order_id': 'CC-7241',
  'status': 'processed',
  'total_cents': 4125,
  'units': 2,
  'channel': 'mobile',
  'created_at': '2026-02-03T13:12:00Z'},
 {'order_id': 'CC-7242',
  'status': 'shipped',
  'total_cents': 5750,
  'units': 3,
  'channel': 'marketplace',
  'created_at': '2026-02-04T14:12:00Z'},
 {'order_id': 'CC-7243',
  'status': 'declined',
  'total_cents': 2375,
  'units': 4,
  'channel': 'web',
  'created_at': '2026-02-05T15:12:00Z'}]


class Client:
    """Talk to a CS-Cart store without leaving the process."""

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
        """Translate to the canonical status before writing it back."""
        canonical = STATUS_ALIASES.get(status, status)
        if canonical not in CANONICAL_STATUSES:
            raise ValidationError("%s cannot represent status %r" % (VENDOR, status))
        for row in self._orders:
            if row["order_id"] == order_id:
                row["status"] = status
                return {"order_id": order_id, "status": canonical, "native": status}
        raise ValidationError("%s does not know order %r" % (VENDOR, order_id))
