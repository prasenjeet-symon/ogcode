"""Short-lived reservations against stock that has not shipped yet."""

from shop.errors import ValidationError

__all__ = ["ReservationBook", "HOLD_MINUTES"]

HOLD_MINUTES = 20


class ReservationBook:
    """Reservations keyed by the reference checkout gave them."""

    def __init__(self, hold_minutes: int = HOLD_MINUTES):
        self._holds = {}
        self._closed = []
        self.hold_minutes = int(hold_minutes)

    def reserve(self, sku: str, qty: int, reference: str) -> dict:
        """Hold ``qty`` units for ``reference``. One hold per reference."""
        key = str(reference or "").strip()
        if not key:
            raise ValidationError("a reservation needs a reference")
        if key in self._holds:
            raise ValidationError("reference %r is already held" % (key,))
        if int(qty) <= 0:
            raise ValidationError("qty must be positive")
        hold = {
            "reference": key,
            "sku": str(sku),
            "qty": int(qty),
            "minutes": self.hold_minutes,
            "state": "held",
        }
        self._holds[key] = hold
        return dict(hold)

    def release(self, reference: str) -> dict:
        """Drop a hold without consuming it."""
        hold = self._holds.pop(str(reference), None)
        if hold is None:
            raise ValidationError("no open reservation for %r" % (reference,))
        closed = dict(hold, state="released")
        self._closed.append(closed)
        return closed

    def commit(self, reference: str) -> dict:
        """Consume a hold: the stock has shipped."""
        hold = self._holds.pop(str(reference), None)
        if hold is None:
            raise ValidationError("no open reservation for %r" % (reference,))
        closed = dict(hold, state="committed")
        self._closed.append(closed)
        return closed

    def open_holds(self) -> list:
        """Open holds in the order they were taken."""
        return [dict(self._holds[key]) for key in sorted(self._holds)]

    def held_for(self, sku: str) -> int:
        """Units currently held for ``sku``."""
        return sum(hold["qty"] for hold in self._holds.values() if hold["sku"] == str(sku))

    def closed(self) -> list:
        """Everything released or committed, oldest first."""
        return [dict(row) for row in self._closed]
