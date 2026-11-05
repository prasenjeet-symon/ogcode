"""Adobe Commerce order sync.

Orders leave Adobe Commerce in its own vocabulary -- pending, processing, complete, canceled -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "adobe_commerce"
LABEL = "Adobe Commerce"
AUTH_HEADER = "X-Adobe Commerce-Token"
PAGE_SIZE = 30
MIN_TOTAL_CENTS = 1000
CANONICAL_STATUSES = ('new',
 'pending',
 'paid',
 'fulfilling',
 'shipped',
 'delivered',
 'cancelled',
 'refunded')
ACTIVE_STATUSES = ('new', 'paid')
STATUS_ALIASES = {'pending': 'new',
 'processing': 'paid',
 'complete': 'delivered',
 'canceled': 'cancelled'}

SEED = [{'order_id': 'AC-7070',
  'status': 'canceled',
  'total_cents': 2625,
  'units': 4,
  'channel': 'mobile',
  'created_at': '2026-02-02T15:21:00Z'},
 {'order_id': 'AC-7071',
  'status': 'pending',
  'total_cents': 4250,
  'units': 1,
  'channel': 'marketplace',
  'created_at': '2026-02-03T16:21:00Z'},
 {'order_id': 'AC-7072',
  'status': 'processing',
  'total_cents': 5875,
  'units': 2,
  'channel': 'web',
  'created_at': '2026-02-04T17:21:00Z'},
 {'order_id': 'AC-7073',
  'status': 'complete',
  'total_cents': 2500,
  'units': 3,
  'channel': 'mobile',
  'created_at': '2026-02-05T08:21:00Z'}]


class Client:
    """Talk to a Adobe Commerce store without leaving the process."""

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
        """Write the status back into the queue after a linear scan."""
        for row in self._orders:
            if row["order_id"] == order_id:
                row["status"] = status
                return {"order_id": order_id, "status": status, "vendor": VENDOR}
        raise ValidationError("%s does not know order %r" % (VENDOR, order_id))
