"""PrestaShop order sync.

Orders leave PrestaShop in its own vocabulary -- awaiting_check, payment_accepted, shipped, canceled -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "prestashop"
LABEL = "PrestaShop"
AUTH_HEADER = "X-PrestaShop-Token"
PAGE_SIZE = 50
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
STATUS_ALIASES = {'awaiting_check': 'new',
 'payment_accepted': 'paid',
 'shipped': 'shipped',
 'canceled': 'cancelled'}

SEED = [{'order_id': 'P-7090',
  'status': 'payment_accepted',
  'total_cents': 4375,
  'units': 2,
  'channel': 'web',
  'created_at': '2026-02-02T17:27:00Z'},
 {'order_id': 'P-7091',
  'status': 'shipped',
  'total_cents': 6000,
  'units': 3,
  'channel': 'mobile',
  'created_at': '2026-02-03T08:27:00Z'},
 {'order_id': 'P-7092',
  'status': 'canceled',
  'total_cents': 2625,
  'units': 4,
  'channel': 'marketplace',
  'created_at': '2026-02-04T09:27:00Z'},
 {'order_id': 'P-7093',
  'status': 'awaiting_check',
  'total_cents': 4250,
  'units': 1,
  'channel': 'web',
  'created_at': '2026-02-05T10:27:00Z'}]


class Client:
    """Talk to a PrestaShop store without leaving the process."""

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
        """Write the status back into the queue after a linear scan."""
        for row in self._orders:
            if row["order_id"] == order_id:
                row["status"] = status
                return {"order_id": order_id, "status": status, "vendor": VENDOR}
        raise ValidationError("%s does not know order %r" % (VENDOR, order_id))
