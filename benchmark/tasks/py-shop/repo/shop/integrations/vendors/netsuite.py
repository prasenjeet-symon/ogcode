"""NetSuite order sync.

Orders leave NetSuite in its own vocabulary -- pending_approval, pending_fulfillment, billed, closed -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "netsuite"
LABEL = "NetSuite"
AUTH_HEADER = "X-NetSuite-Token"
PAGE_SIZE = 50
MIN_TOTAL_CENTS = 1250
CANONICAL_STATUSES = ('new',
 'pending',
 'paid',
 'fulfilling',
 'shipped',
 'delivered',
 'cancelled',
 'refunded')
ACTIVE_STATUSES = ('new', 'fulfilling', 'paid')
STATUS_ALIASES = {'pending_approval': 'new',
 'pending_fulfillment': 'fulfilling',
 'billed': 'paid',
 'closed': 'refunded'}

SEED = [{'order_id': 'N-7290',
  'status': 'pending_fulfillment',
  'total_cents': 1875,
  'units': 2,
  'channel': 'marketplace',
  'created_at': '2026-02-02T17:27:00Z'},
 {'order_id': 'N-7291',
  'status': 'billed',
  'total_cents': 3500,
  'units': 3,
  'channel': 'web',
  'created_at': '2026-02-03T08:27:00Z'},
 {'order_id': 'N-7292',
  'status': 'closed',
  'total_cents': 5125,
  'units': 4,
  'channel': 'mobile',
  'created_at': '2026-02-04T09:27:00Z'},
 {'order_id': 'N-7293',
  'status': 'pending_approval',
  'total_cents': 1750,
  'units': 1,
  'channel': 'marketplace',
  'created_at': '2026-02-05T10:27:00Z'}]


class Client:
    """Talk to a NetSuite store without leaving the process."""

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
        """Translate to the canonical status before writing it back."""
        canonical = STATUS_ALIASES.get(status, status)
        if canonical not in CANONICAL_STATUSES:
            raise ValidationError("%s cannot represent status %r" % (VENDOR, status))
        for row in self._orders:
            if row["order_id"] == order_id:
                row["status"] = status
                return {"order_id": order_id, "status": canonical, "native": status}
        raise ValidationError("%s does not know order %r" % (VENDOR, order_id))
