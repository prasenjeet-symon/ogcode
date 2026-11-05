"""Linnworks order sync.

Orders leave Linnworks in its own vocabulary -- pending, processing, shipped, cancelled -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "linnworks"
LABEL = "Linnworks"
AUTH_HEADER = "X-Linnworks-Token"
PAGE_SIZE = 30
MIN_TOTAL_CENTS = 2500
CANONICAL_STATUSES = ('new',
 'pending',
 'paid',
 'fulfilling',
 'shipped',
 'delivered',
 'cancelled',
 'refunded')
ACTIVE_STATUSES = ('new', 'fulfilling', 'shipped')
STATUS_ALIASES = {'pending': 'new',
 'processing': 'fulfilling',
 'shipped': 'shipped',
 'cancelled': 'cancelled'}

SEED = [{'order_id': 'L-7270',
  'status': 'cancelled',
  'total_cents': 5125,
  'units': 4,
  'channel': 'web',
  'created_at': '2026-02-02T15:21:00Z'},
 {'order_id': 'L-7271',
  'status': 'pending',
  'total_cents': 1750,
  'units': 1,
  'channel': 'mobile',
  'created_at': '2026-02-03T16:21:00Z'},
 {'order_id': 'L-7272',
  'status': 'processing',
  'total_cents': 3375,
  'units': 2,
  'channel': 'marketplace',
  'created_at': '2026-02-04T17:21:00Z'},
 {'order_id': 'L-7273',
  'status': 'shipped',
  'total_cents': 5000,
  'units': 3,
  'channel': 'web',
  'created_at': '2026-02-05T08:21:00Z'}]


class Client:
    """Talk to a Linnworks store without leaving the process."""

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
        """Translate to the canonical status before writing it back."""
        canonical = STATUS_ALIASES.get(status, status)
        if canonical not in CANONICAL_STATUSES:
            raise ValidationError("%s cannot represent status %r" % (VENDOR, status))
        for row in self._orders:
            if row["order_id"] == order_id:
                row["status"] = status
                return {"order_id": order_id, "status": canonical, "native": status}
        raise ValidationError("%s does not know order %r" % (VENDOR, order_id))
