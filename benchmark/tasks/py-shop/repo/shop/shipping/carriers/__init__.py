"""Carrier adapters.

Every module here answers the same three questions, so checkout never has
to know which carrier it is talking to::

    CARRIER                          the carrier slug we bill against
    quote(weight_g, zone)            what the parcel costs, in cents
    parse_tracking(payload)          the carrier document, normalised

A carrier that refuses a zone raises ``ValidationError``; the comparison
below treats that as "not offered" rather than as a failure.
"""

import importlib
import pkgutil

__all__ = ["all_carriers", "carrier_names", "module_for"]


def carrier_names() -> list:
    """Adapter module names in this package."""
    found = [info.name for info in pkgutil.iter_modules(__path__)]
    return sorted(name for name in found if not name.startswith("_"))


def all_carriers() -> list:
    """Import and return every adapter, ordered by carrier slug."""
    modules = [importlib.import_module(__name__ + "." + name) for name in carrier_names()]
    return sorted(modules, key=lambda module: module.CARRIER)


def module_for(carrier: str):
    """The adapter registered for ``carrier``, or ``None``."""
    for module in all_carriers():
        if module.CARRIER == carrier:
            return module
    return None
