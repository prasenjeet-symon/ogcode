"""Magento order sync.

Orders leave Magento in its own vocabulary -- pending, processing, complete, canceled, closed -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "magento"
LABEL = "Magento"
AUTH_HEADER = "X-Magento-Token"
PAGE_SIZE = 40
MIN_TOTAL_CENTS = 1750
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
 'canceled': 'cancelled',
 'closed': 'refunded'}

SEED = [{'order_id': 'M-7030',
  'status': 'canceled',
  'total_cents': 4125,
  'units': 4,
  'channel': 'web',
  'created_at': '2026-02-02T11:09:00Z'},
 {'order_id': 'M-7031',
  'status': 'closed',
  'total_cents': 5750,
  'units': 1,
  'channel': 'mobile',
  'created_at': '2026-02-03T12:09:00Z'},
 {'order_id': 'M-7032',
  'status': 'pending',
  'total_cents': 2375,
  'units': 2,
  'channel': 'marketplace',
  'created_at': '2026-02-04T13:09:00Z'},
 {'order_id': 'M-7033',
  'status': 'processing',
  'total_cents': 4000,
  'units': 3,
  'channel': 'web',
  'created_at': '2026-02-05T14:09:00Z'}]


class Client:
    """Talk to a Magento store without leaving the process."""

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
        """Write the status back into the queue after a linear scan."""
        for row in self._orders:
            if row["order_id"] == order_id:
                row["status"] = status
                return {"order_id": order_id, "status": status, "vendor": VENDOR}
        raise ValidationError("%s does not know order %r" % (VENDOR, order_id))
