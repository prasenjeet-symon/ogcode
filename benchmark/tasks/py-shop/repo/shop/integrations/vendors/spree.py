"""Spree order sync.

Orders leave Spree in its own vocabulary -- cart, complete, canceled, returned -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "spree"
LABEL = "Spree"
AUTH_HEADER = "X-Spree-Token"
PAGE_SIZE = 30
MIN_TOTAL_CENTS = 1750
CANONICAL_STATUSES = ('new',
 'pending',
 'paid',
 'fulfilling',
 'shipped',
 'delivered',
 'cancelled',
 'refunded')
ACTIVE_STATUSES = ('pending', 'paid')
STATUS_ALIASES = {'cart': 'pending',
 'complete': 'paid',
 'canceled': 'cancelled',
 'returned': 'refunded'}

SEED = [{'order_id': 'S-7170',
  'status': 'complete',
  'total_cents': 6375,
  'units': 2,
  'channel': 'marketplace',
  'created_at': '2026-02-02T15:51:00Z'},
 {'order_id': 'S-7171',
  'status': 'canceled',
  'total_cents': 3000,
  'units': 3,
  'channel': 'web',
  'created_at': '2026-02-03T16:51:00Z'},
 {'order_id': 'S-7172',
  'status': 'returned',
  'total_cents': 4625,
  'units': 4,
  'channel': 'mobile',
  'created_at': '2026-02-04T17:51:00Z'},
 {'order_id': 'S-7173',
  'status': 'cart',
  'total_cents': 6250,
  'units': 1,
  'channel': 'marketplace',
  'created_at': '2026-02-05T08:51:00Z'}]


class Client:
    """Talk to a Spree store without leaving the process."""

    def __init__(self, token: str = "", channel: str = "web"):
        """Seed the queue, the id index and the write history."""
        self.token = token
        self.channel = channel
        self._orders = [dict(row) for row in SEED]
        self._by_id = {row["order_id"]: row for row in self._orders}
        self._cursor = 0
        self.history = []

    def fetch_orders(self) -> list:
        """Every row, with the unit count lifted out of the vendor shape."""
        out = []
        for row in self._orders:
            record = dict(row)
            record["unit_count"] = int(record.pop("units", 0))
            out.append(record)
        return out

    def push_status(self, order_id: str, status: str) -> dict:
        """Write through the id index, failing on an unknown id first."""
        if order_id not in self._by_id:
            raise ValidationError("%s does not know order %r" % (VENDOR, order_id))
        self._by_id[order_id]["status"] = status
        return {"order_id": order_id, "status": status, "indexed": True}
