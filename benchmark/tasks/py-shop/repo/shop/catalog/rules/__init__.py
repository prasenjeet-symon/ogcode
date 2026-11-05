"""The merchandising rule catalogue.

Every module in this package exposes the same two functions, which is all
the checkout pipeline needs to know about it::

    applies(order)        -> bool   can the rule fire on this order
    discount_cents(order) -> int    how many cents it takes off

Rules are found by walking the package, so adding a module here is enough
to put it in front of the pipeline. Nothing else in the service hard-codes a
rule name: the catalogue is the only registration point.
"""

import importlib
import pkgutil

__all__ = ["all_rules", "evaluate", "module_names"]


def module_names() -> list:
    """Rule module names in this package, private helpers excluded."""
    found = [info.name for info in pkgutil.iter_modules(__path__)]
    return sorted(name for name in found if not name.startswith("_"))


def all_rules() -> list:
    """Import every rule module once and hand them back in RULE_ID order."""
    modules = [importlib.import_module(__name__ + "." + name) for name in module_names()]
    return sorted(modules, key=lambda module: module.RULE_ID)


def evaluate(order: dict) -> list:
    """Every rule that fires on ``order``, worthiest first."""
    hits = []
    for module in all_rules():
        if not module.applies(order):
            continue
        cents = int(module.discount_cents(order))
        if cents <= 0:
            continue
        hits.append(
            {
                "rule_id": module.RULE_ID,
                "name": module.__name__.rsplit(".", 1)[-1],
                "title": module.TITLE,
                "cents": cents,
            }
        )
    hits.sort(key=lambda hit: (-hit["cents"], hit["rule_id"]))
    return hits
