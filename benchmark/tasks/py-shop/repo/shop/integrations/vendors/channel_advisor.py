"""ChannelAdvisor order sync.

Orders leave ChannelAdvisor in its own vocabulary -- pending, processing, shipped, cancelled -- so each row keeps
the native status while ``STATUS_ALIASES`` publishes ours. The queue
lives in memory: no credentials, no network, and a test can drive a full
sync in a few milliseconds."""

from shop.errors import ValidationError

VENDOR = "channel_advisor"
LABEL = "ChannelAdvisor"
AUTH_HEADER = "X-ChannelAdvisor-Token"
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
ACTIVE_STATUSES = ('new', 'fulfilling', 'shipped')
STATUS_ALIASES = {'pending': 'new',
 'processing': 'fulfilling',
 'shipped': 'shipped',
 'cancelled': 'cancelled'}

SEED = [{'order_id': 'CA-7380',
  'status': 'shipped',
  'total_cents': 4750,
  'units': 3,
  'channel': 'marketplace',
  'created_at': '2026-02-02T16:54:00Z'},
 {'order_id': 'CA-7381',
  'status': 'cancelled',
  'total_cents': 6375,
  'units': 4,
  'channel': 'web',
  'created_at': '2026-02-03T17:54:00Z'},
 {'order_id': 'CA-7382',
  'status': 'pending',
  'total_cents': 3000,
  'units': 1,
  'channel': 'mobile',
  'created_at': '2026-02-04T08:54:00Z'},
 {'order_id': 'CA-7383',
  'status': 'processing',
  'total_cents': 4625,
  'units': 2,
  'channel': 'marketplace',
  'created_at': '2026-02-05T09:54:00Z'}]


class Client:
    """Talk to a ChannelAdvisor store without leaving the process."""

    def __init__(self, token: str = "", channel: str = "web"):
        """Seed the queue, the id index and the write history."""
        self.token = token
        self.channel = channel
        self._orders = [dict(row) for row in SEED]
        self._by_id = {row["order_id"]: row for row in self._orders}
        self._cursor = 0
        self.history = []

    def fetch_orders(self) -> list:
        """One sales channel only, set on the client."""
        return [dict(row) for row in self._orders if row["channel"] == self.channel]

    def push_status(self, order_id: str, status: str) -> dict:
        """Write the status and remember the move for the audit trail."""
        previous = None
        for row in self._orders:
            if row["order_id"] == order_id:
                previous = row["status"]
                row["status"] = status
                break
        else:
            raise ValidationError("%s does not know order %r" % (VENDOR, order_id))
        self.history.append({"order_id": order_id, "from": previous, "to": status})
        return {"order_id": order_id, "status": status, "previous": previous, "moves": len(self.history)}
