"""Zen Cart order sync.

Orders leave Zen Cart in its own vocabulary -- pending, processing, shipped, cancelled -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "zen_cart"
LABEL = "Zen Cart"
AUTH_HEADER = "X-Zen Cart-Token"
PAGE_SIZE = 40
MIN_TOTAL_CENTS = 1500
CANONICAL_STATUSES = ('new',
 'pending',
 'paid',
 'fulfilling',
 'shipped',
 'delivered',
 'cancelled',
 'refunded')
ACTIVE_STATUSES = ('new', 'paid', 'shipped')
STATUS_ALIASES = {'pending': 'new',
 'processing': 'paid',
 'shipped': 'shipped',
 'cancelled': 'cancelled'}

SEED = [{'order_id': 'ZC-7230',
  'status': 'cancelled',
  'total_cents': 1625,
  'units': 4,
  'channel': 'marketplace',
  'created_at': '2026-02-02T11:09:00Z'},
 {'order_id': 'ZC-7231',
  'status': 'pending',
  'total_cents': 3250,
  'units': 1,
  'channel': 'web',
  'created_at': '2026-02-03T12:09:00Z'},
 {'order_id': 'ZC-7232',
  'status': 'processing',
  'total_cents': 4875,
  'units': 2,
  'channel': 'mobile',
  'created_at': '2026-02-04T13:09:00Z'},
 {'order_id': 'ZC-7233',
  'status': 'shipped',
  'total_cents': 1500,
  'units': 3,
  'channel': 'marketplace',
  'created_at': '2026-02-05T14:09:00Z'}]


class Client:
    """Talk to a Zen Cart store without leaving the process."""

    def __init__(self, token: str = "", channel: str = "web"):
        """Seed the queue, the id index and the write history."""
        self.token = token
        self.channel = channel
        self._orders = [dict(row) for row in SEED]
        self._by_id = {row["order_id"]: row for row in self._orders}
        self._cursor = 0
        self.history = []

    def fetch_orders(self) -> list:
        """Every row, with the native status mapped to ours."""
        out = []
        for row in self._orders:
            record = dict(row)
            record["status"] = STATUS_ALIASES.get(row["status"], "unknown")
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
