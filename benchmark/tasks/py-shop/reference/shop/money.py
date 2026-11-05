"""Money handling for the shop service.

Every amount the service stores is an integer number of cents. Floating point
is allowed at the edges (a CSV of prices, an HTTP body, a gateway response) and
converted here, once, so the rest of the code never compares floats.
"""

from decimal import Decimal, InvalidOperation, ROUND_HALF_UP

from shop.errors import ValidationError


def to_cents(amount) -> int:
    """Convert a dollar amount to integer cents, rounding half up at the cent.

    Accepts ``"12.34"``, ``12.34``, ``Decimal("12.34")`` and whole dollars as
    an ``int``. Anything else is a ``ValidationError`` rather than a silent
    zero.
    """
    if isinstance(amount, int):
        return amount * 100
    try:
        value = Decimal(str(amount))
    except (InvalidOperation, ValueError, TypeError):
        raise ValidationError(f"not an amount: {amount!r}") from None
    if not value.is_finite():
        raise ValidationError(f"not an amount: {amount!r}")
    return int((value * 100).quantize(Decimal("1"), rounding=ROUND_HALF_UP))


def format_cents(cents: int) -> str:
    """Render integer cents as a fixed two-decimal string, sign in front."""
    cents = int(cents)
    sign = "-" if cents < 0 else ""
    cents = abs(cents)
    return f"{sign}{cents // 100}.{cents % 100:02d}"


def mul_rate(cents: int, bps: int) -> int:
    """Scale ``cents`` by ``bps`` basis points, rounded half up at the cent.

    One basis point is a hundredth of a percent, so 825 bps is 8.25%.
    """
    return (int(cents) * int(bps) + 5000) // 10000


def allocate(total_cents: int, weights) -> list[int]:
    """Split ``total_cents`` across ``weights``, proportionally.

    The parts always add back up to ``total_cents``: every part gets its floor
    share, and the cents left over go to the largest fractional parts, earliest
    weight first when two are equal. A zero weight gets nothing but still keeps
    its slot in the returned list.
    """
    weights = [max(int(w), 0) for w in weights]
    if not weights:
        raise ValidationError("allocate needs at least one weight")
    total = int(total_cents)
    denom = sum(weights)
    if denom <= 0:
        raise ValidationError("allocate needs a positive total weight")
    exact = [total * w / denom for w in weights]
    parts = [int(x) for x in exact]
    leftover = total - sum(parts)
    if leftover > 0:
        ranked = sorted(range(len(parts)), key=lambda i: (-(exact[i] - parts[i]), i))
        for i in ranked[:leftover]:
            parts[i] += 1
    return parts
