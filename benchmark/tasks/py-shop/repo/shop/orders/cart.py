"""Cart construction: adding, merging and removing lines."""

from shop.config import settings
from shop.errors import ValidationError
from shop.orders.models import Cart, OrderLine


def add_item(cart: Cart, product, quantity: int = 1) -> OrderLine:
    """Add a product, merging into a line that already holds the same sku.

    Merging keeps the price the customer first saw, which is why the line
    stores its own unit price instead of looking the product up again.
    """
    quantity = int(quantity)
    if quantity < 1:
        raise ValidationError(f"quantity must be positive, got {quantity}")
    for line in cart.lines:
        if line.sku == product.sku and line.unit_price_cents == product.price_cents:
            line.quantity += quantity
            return line
    if len(cart.lines) >= settings.max_cart_lines:
        raise ValidationError(f"a cart holds at most {settings.max_cart_lines} lines")
    line = OrderLine(
        sku=product.sku,
        unit_price_cents=int(product.price_cents),
        quantity=quantity,
    )
    cart.lines.append(line)
    return line


def remove_item(cart: Cart, sku: str) -> bool:
    """Drop every line for a sku; returns whether anything was removed."""
    before = len(cart.lines)
    cart.lines = [line for line in cart.lines if line.sku != sku]
    return len(cart.lines) != before


def merge_duplicates(cart: Cart) -> Cart:
    """Fold lines that share a sku and price into one."""
    merged: list[OrderLine] = []
    for line in cart.lines:
        for kept in merged:
            if kept.sku == line.sku and kept.unit_price_cents == line.unit_price_cents:
                kept.quantity += line.quantity
                break
        else:
            merged.append(line)
    cart.lines = merged
    return cart
