"""Gateway adapters.

Every module here exposes the same four names, so the capture path never
branches on the vendor name::

    VENDOR              the string we store on a payment row
    STATUS_MAP          the vendor vocabulary, for debugging
    normalize(status)   -> canonical status
    supports_refund()   -> bool

The canonical statuses are fixed by our own state machine: authorized,
captured, failed, pending, refunded, voided, disputed, unknown.
"""

import importlib
import pkgutil

__all__ = ["all_gateways", "canonical_statuses", "gateway", "refunding_gateways"]


def module_names() -> list:
    """Every gateway module in this package, sorted by name."""
    found = [info.name for info in pkgutil.iter_modules(__path__)]
    return sorted(name for name in found if not name.startswith("_"))


def all_gateways() -> list:
    """Every adapter module, ordered by VENDOR so output stays stable."""
    modules = [importlib.import_module(__name__ + "." + name) for name in module_names()]
    return sorted(modules, key=lambda module: module.VENDOR)


def gateway(vendor: str):
    """The adapter for ``vendor``; KeyError names what was asked for."""
    for module in all_gateways():
        if module.VENDOR == vendor:
            return module
    raise KeyError("no gateway adapter for vendor %r" % (vendor,))


def refunding_gateways() -> list:
    """Vendors whose API can issue a refund without a support ticket."""
    return [module.VENDOR for module in all_gateways() if module.supports_refund()]


def canonical_statuses() -> list:
    """The union of every canonical status any adapter can return."""
    seen = set()
    for module in all_gateways():
        seen.update(module.STATUS_MAP.values())
    return sorted(seen)
