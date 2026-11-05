"""Warehouse adapters.

Each site answers the same two questions, which is what lets the planner
treat the network as one pool::

    stock_level(sku) -> int          units on the site's shelves
    allocate(sku, qty) -> dict       what the site will commit to

Allocation is a read-modify-write against the module's own ``STOCK``
table. That is deliberate: the fixture keeps the sites in memory so the
planner can be exercised without a database.
"""

import importlib
import pkgutil

__all__ = ["all_warehouses", "network_stock", "warehouse_for"]


def warehouse_names() -> list:
    """Adapter module names in this package."""
    found = [info.name for info in pkgutil.iter_modules(__path__)]
    return sorted(name for name in found if not name.startswith("_"))


def all_warehouses() -> list:
    """Every adapter, ordered by warehouse slug."""
    modules = [importlib.import_module(__name__ + "." + name) for name in warehouse_names()]
    return sorted(modules, key=lambda module: module.WAREHOUSE)


def warehouse_for(name: str):
    """The adapter registered for ``name``, or ``None``."""
    for module in all_warehouses():
        if module.WAREHOUSE == name:
            return module
    return None


def network_stock(sku: str) -> dict:
    """Units of ``sku`` at every site, plus the network total."""
    per_site = {module.WAREHOUSE: module.stock_level(sku) for module in all_warehouses()}
    return {"sku": str(sku), "sites": per_site, "total": sum(per_site.values())}
