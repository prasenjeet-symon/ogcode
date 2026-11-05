"""The append-only stock ledger.

Every movement is one row. Nothing is ever updated in place and nothing
is ever deleted, so the balance of a SKU is always the sum of its rows.
"""

from shop.errors import ValidationError

__all__ = ["InventoryLedger", "REASONS"]

REASONS = ("receipt", "sale", "return", "adjustment", "damage", "transfer")


class InventoryLedger:
    """A running record of stock movements."""

    def __init__(self):
        self._rows = []

    def post(self, sku: str, delta: int, reason: str = "adjustment") -> dict:
        """Write one movement. ``delta`` is signed: positive is stock in."""
        if not str(sku or "").strip():
            raise ValidationError("a movement needs a sku")
        if str(reason) not in REASONS:
            raise ValidationError("unknown movement reason %r" % (reason,))
        if not isinstance(delta, int) or isinstance(delta, bool) or delta == 0:
            raise ValidationError("delta must be a non-zero integer")
        row = {
            "seq": len(self._rows) + 1,
            "sku": str(sku),
            "delta": int(delta),
            "reason": str(reason),
        }
        self._rows.append(row)
        return dict(row)

    def balance(self, sku: str) -> int:
        """Signed sum of every movement for ``sku``."""
        return sum(row["delta"] for row in self._rows if row["sku"] == str(sku))

    def history(self, sku=None) -> list:
        """Movements in posting order, optionally for one SKU only."""
        if sku is None:
            return [dict(row) for row in self._rows]
        return [dict(row) for row in self._rows if row["sku"] == str(sku)]

    def totals(self) -> dict:
        """Net balance per SKU, sorted by SKU."""
        totals = {}
        for row in self._rows:
            totals[row["sku"]] = totals.get(row["sku"], 0) + row["delta"]
        return {sku: totals[sku] for sku in sorted(totals)}

    def __len__(self) -> int:
        return len(self._rows)
