"""Marketplace adapters.

Each module exposes the same small surface, so the sync worker never has to
know which marketplace it is talking to::

    VENDOR                       the slug we key the feed on
    Client().fetch_orders()      the queue as this vendor sends it
    Client().push_status(id, s)  a status write, answered in canonical form

A vendor that does not know an order id raises ``ValidationError``; the
worker treats that as "skip and retry later", never as a hard failure.
"""

import importlib
import pkgutil

__all__ = ["all_vendors", "client_for", "vendor_names"]


def vendor_names() -> list:
    """Adapter module names in this package."""
    found = [info.name for info in pkgutil.iter_modules(__path__)]
    return sorted(name for name in found if not name.startswith("_"))


def all_vendors() -> list:
    """Every adapter, ordered by vendor slug."""
    modules = [importlib.import_module(__name__ + "." + name) for name in vendor_names()]
    return sorted(modules, key=lambda module: module.VENDOR)


def client_for(name: str):
    """A fresh client for a vendor slug, or ``None`` when unregistered."""
    for module in all_vendors():
        if module.VENDOR == name:
            return module.Client()
    return None
