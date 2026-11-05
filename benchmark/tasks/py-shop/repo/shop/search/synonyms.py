"""Merchandising synonyms.

A shopper types ``sneakers``; the catalogue says ``trainers``. Each group
below has a canonical head and lookups go through that head, so either
spelling reaches the same postings. Heads are stored in the singular form
the normaliser produces, which is why they all end without an ``s``.
"""

__all__ = ["GROUPS", "HEAD", "canonical", "expand", "known"]

GROUPS = (
    ("trainer", ("sneaker", "running shoe", "athletic shoe")),
    ("jumper", ("sweater", "pullover", "knitwear")),
    ("trouser", ("pant", "slack", "jean")),
    ("bag", ("backpack", "rucksack", "daypack")),
    ("mug", ("cup", "tumbler", "beaker")),
    ("lamp", ("light", "lantern", "sconce")),
    ("charger", ("adapter", "power supply", "psu")),
    ("headphone", ("earphone", "earbud", "headset")),
    ("notebook", ("journal", "pad", "notepad")),
    ("wallet", ("purse", "billfold", "cardholder")),
)

HEAD = {}
for _head, _members in GROUPS:
    HEAD[_head] = _head
    for _member in _members:
        HEAD[_member] = _head


def canonical(term: str) -> str:
    """The group head for a term, or the term itself when it has no group."""
    word = str(term).strip().lower()
    return HEAD.get(word, word)


def expand(term: str) -> frozenset:
    """Every spelling that should match ``term``, canonical included."""
    head = canonical(term)
    for name, members in GROUPS:
        if name == head:
            return frozenset((head,) + tuple(members))
    return frozenset((head,))


def known() -> list:
    """Every group head, sorted, for the admin synonym screen."""
    return sorted(name for name, _ in GROUPS)
