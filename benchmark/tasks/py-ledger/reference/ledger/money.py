"""Money is integer cents everywhere. No floats survive an import of this file."""


def parse_amount(text):
    """Read '12.34' as 1234. More than two decimals is a mistake, not input."""
    negative = text.startswith("-")
    if negative:
        text = text[1:]
    if text.count(".") > 1:
        raise ValueError(f"not an amount: {text!r}")
    whole, _, frac = text.partition(".")
    if len(frac) > 2:
        raise ValueError(f"not an amount: {text!r}")
    frac = (frac + "00")[:2]
    cents = int(whole or "0") * 100 + int(frac or "0")
    return -cents if negative else cents


def format_cents(cents):
    sign = "-" if cents < 0 else ""
    cents = abs(cents)
    return f"{sign}{cents // 100}.{cents % 100:02d}"


def percent_of(cents, permille):
    """permille thousandths of cents, rounded half away from zero.

    A fee of 0.5 cent is a whole cent, not a coin toss: the ledger never
    rounds to even, so the same figures always produce the same statement.
    """
    if permille < 0:
        raise ValueError("permille must not be negative")
    return (cents * permille + 500) // 1000
