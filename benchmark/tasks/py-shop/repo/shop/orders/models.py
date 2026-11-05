"""The cart, its lines, and the order checkout produces."""

from dataclasses import dataclass, field


@dataclass
class OrderLine:
    """One product on a cart, held at the price it was added at."""

    sku: str
    unit_price_cents: int
    quantity: int = 1
    discount_cents: int = 0

    @property
    def subtotal_cents(self) -> int:
        """Line total before any discount."""
        return int(self.unit_price_cents) * int(self.quantity)

    @property
    def total_cents(self) -> int:
        """Line total after the discount apportioned to it."""
        return self.subtotal_cents - int(self.discount_cents)


@dataclass
class Cart:
    """A list of lines, in the order the customer added them."""

    lines: list[OrderLine] = field(default_factory=list)

    @property
    def subtotal_cents(self) -> int:
        return sum(line.subtotal_cents for line in self.lines)

    @property
    def item_count(self) -> int:
        return sum(int(line.quantity) for line in self.lines)

    @property
    def is_empty(self) -> bool:
        return not self.lines


@dataclass
class Order:
    """A priced cart: the totals the customer is charged, and how they got there."""

    subtotal_cents: int
    discount_cents: int
    shipping_cents: int
    tax_cents: int
    total_cents: int
    tier: str = "bronze"
    discounts: list = field(default_factory=list)
    lines: list = field(default_factory=list)

    @property
    def line_count(self) -> int:
        return len(self.lines)

    def line_for(self, sku: str) -> OrderLine:
        """The line for a sku; raises KeyError when it is not on the order."""
        for line in self.lines:
            if line.sku == sku:
                return line
        raise KeyError(sku)
