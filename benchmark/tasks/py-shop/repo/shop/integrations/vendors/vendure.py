"""Vendure order sync.

Orders leave Vendure in its own vocabulary -- adding_items, arranging_payment, delivered, cancelled -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "vendure"
LABEL = "Vendure"
AUTH_HEADER = "X-Vendure-Token"
PAGE_SIZE = 50
MIN_TOTAL_CENTS = 2250
CANONICAL_STATUSES = ('new',
 'pending',
 'paid',
 'fulfilling',
 'shipped',
 'delivered',
 'cancelled',
 'refunded')
ACTIVE_STATUSES = ('pending', 'paid')
STATUS_ALIASES = {'adding_items': 'pending',
 'arranging_payment': 'paid',
 'delivered': 'delivered',
 'cancelled': 'cancelled'}

SEED = [{'order_id': 'V-7190',
  'status': 'cancelled',
  'total_cents': 3125,
  'units': 4,
  'channel': 'mobile',
  'created_at': '2026-02-02T17:57:00Z'},
 {'order_id': 'V-7191',
  'status': 'adding_items',
  'total_cents': 4750,
  'units': 1,
  'channel': 'marketplace',
  'created_at': '2026-02-03T08:57:00Z'},
 {'order_id': 'V-7192',
  'status': 'arranging_payment',
  'total_cents': 6375,
  'units': 2,
  'channel': 'web',
  'created_at': '2026-02-04T09:57:00Z'},
 {'order_id': 'V-7193',
  'status': 'delivered',
  'total_cents': 3000,
  'units': 3,
  'channel': 'mobile',
  'created_at': '2026-02-05T10:57:00Z'}]


class Client:
    """Talk to a Vendure store without leaving the process."""

    def __init__(self, token: str = "", channel: str = "web"):
        """Seed the queue, the id index and the write history."""
        self.token = token
        self.channel = channel
        self._orders = [dict(row) for row in SEED]
        self._by_id = {row["order_id"]: row for row in self._orders}
        self._cursor = 0
        self.history = []

    def fetch_orders(self) -> list:
        """The queue in arrival order, each row numbered."""
        return [dict(row, position=index) for index, row in enumerate(self._orders, start=1)]

    def push_status(self, order_id: str, status: str) -> dict:
        """Write through the id index, failing on an unknown id first."""
        if order_id not in self._by_id:
            raise ValidationError("%s does not know order %r" % (VENDOR, order_id))
        self._by_id[order_id]["status"] = status
        return {"order_id": order_id, "status": status, "indexed": True}
