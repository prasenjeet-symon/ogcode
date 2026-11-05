"""Catalog models: what the service sells."""

from dataclasses import dataclass

from shop.errors import ValidationError
from shop.money import to_cents


@dataclass(frozen=True)
class Product:
    """One sellable item. Prices are integer cents, never floats."""

    sku: str
    name: str
    category: str
    price_cents: int
    taxable: bool = True

    def __post_init__(self):
        if not self.sku:
            raise ValidationError("product needs a sku")
        if int(self.price_cents) < 0:
            raise ValidationError(f"negative price for {self.sku}")

    @classmethod
    def from_row(cls, row: dict) -> "Product":
        """Build a product from a catalog export row."""
        return cls(
            sku=str(row["sku"]).strip(),
            name=str(row.get("name") or row["sku"]).strip(),
            category=str(row.get("category") or "uncategorized").strip(),
            price_cents=to_cents(row["price"]),
            taxable=bool(row.get("taxable", True)),
        )

    @property
    def price(self) -> str:
        """The price as a display string."""
        from shop.money import format_cents

        return format_cents(self.price_cents)
