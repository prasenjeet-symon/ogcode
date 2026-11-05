"""Thirty Bees order sync.

Orders leave Thirty Bees in its own vocabulary -- waiting, paid, sent, canceled -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "thirty_bees"
LABEL = "Thirty Bees"
AUTH_HEADER = "X-Thirty Bees-Token"
PAGE_SIZE = 10
MIN_TOTAL_CENTS = 2000
CANONICAL_STATUSES = ('new',
 'pending',
 'paid',
 'fulfilling',
 'shipped',
 'delivered',
 'cancelled',
 'refunded')
ACTIVE_STATUSES = ('new', 'paid', 'shipped')
STATUS_ALIASES = {'waiting': 'new', 'paid': 'paid', 'sent': 'shipped', 'canceled': 'cancelled'}

SEED = [{'order_id': 'TB-7250',
  'status': 'paid',
  'total_cents': 3375,
  'units': 2,
  'channel': 'mobile',
  'created_at': '2026-02-02T13:15:00Z'},
 {'order_id': 'TB-7251',
  'status': 'sent',
  'total_cents': 5000,
  'units': 3,
  'channel': 'marketplace',
  'created_at': '2026-02-03T14:15:00Z'},
 {'order_id': 'TB-7252',
  'status': 'canceled',
  'total_cents': 1625,
  'units': 4,
  'channel': 'web',
  'created_at': '2026-02-04T15:15:00Z'},
 {'order_id': 'TB-7253',
  'status': 'waiting',
  'total_cents': 3250,
  'units': 1,
  'channel': 'mobile',
  'created_at': '2026-02-05T16:15:00Z'}]


class Client:
    """Talk to a Thirty Bees store without leaving the process."""

    def __init__(self, token: str = "", channel: str = "web"):
        """Seed the queue, the id index and the write history."""
        self.token = token
        self.channel = channel
        self._orders = [dict(row) for row in SEED]
        self._by_id = {row["order_id"]: row for row in self._orders}
        self._cursor = 0
        self.history = []

    def fetch_orders(self) -> list:
        """Only orders worth syncing, above the vendor minimum."""
        return [dict(row) for row in self._orders if int(row["total_cents"]) >= MIN_TOTAL_CENTS]

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
