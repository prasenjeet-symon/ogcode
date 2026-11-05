"""Sylius order sync.

Orders leave Sylius in its own vocabulary -- cart, new, fulfilled, cancelled -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "sylius"
LABEL = "Sylius"
AUTH_HEADER = "X-Sylius-Token"
PAGE_SIZE = 20
MIN_TOTAL_CENTS = 1500
CANONICAL_STATUSES = ('new',
 'pending',
 'paid',
 'fulfilling',
 'shipped',
 'delivered',
 'cancelled',
 'refunded')
ACTIVE_STATUSES = ('pending', 'new')
STATUS_ALIASES = {'cart': 'pending', 'new': 'new', 'fulfilled': 'delivered', 'cancelled': 'cancelled'}

SEED = [{'order_id': 'S-7160',
  'status': 'cart',
  'total_cents': 5500,
  'units': 1,
  'channel': 'mobile',
  'created_at': '2026-02-02T14:48:00Z'},
 {'order_id': 'S-7161',
  'status': 'new',
  'total_cents': 2125,
  'units': 2,
  'channel': 'marketplace',
  'created_at': '2026-02-03T15:48:00Z'},
 {'order_id': 'S-7162',
  'status': 'fulfilled',
  'total_cents': 3750,
  'units': 3,
  'channel': 'web',
  'created_at': '2026-02-04T16:48:00Z'},
 {'order_id': 'S-7163',
  'status': 'cancelled',
  'total_cents': 5375,
  'units': 4,
  'channel': 'mobile',
  'created_at': '2026-02-05T17:48:00Z'}]


class Client:
    """Talk to a Sylius store without leaving the process."""

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
        """Write through the id index, failing on an unknown id first."""
        if order_id not in self._by_id:
            raise ValidationError("%s does not know order %r" % (VENDOR, order_id))
        self._by_id[order_id]["status"] = status
        return {"order_id": order_id, "status": status, "indexed": True}
