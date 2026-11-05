"""Statement rendering: one line per category, then the total."""

from .money import format_cents


def categories_in_order(account):
    return [category for category, _ in account.by_category()]


def statement(account):
    lines = []
    for category, cents in account.by_category():
        lines.append(f"{category:<12}{format_cents(cents):>10}")
    lines.append(f"{'total':<12}{format_cents(account.balance()):>10}")
    return "\n".join(lines)
