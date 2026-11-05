"""Request handlers, one module per route.

Every module exposes ``handle(request) -> dict`` and the route it answers,
which is all the router needs to dispatch without a registration step.
"""

import importlib
import pkgutil

__all__ = ["all_handlers", "handler_names", "route_table"]


def handler_names() -> list:
    """Handler module names in this package."""
    found = [info.name for info in pkgutil.iter_modules(__path__)]
    return sorted(name for name in found if not name.startswith("_"))


def all_handlers() -> dict:
    """Handler name to module."""
    modules = [importlib.import_module(__name__ + "." + name) for name in handler_names()]
    return {module.HANDLE: module for module in modules}


def route_table() -> dict:
    """``(method, path)`` to handler name."""
    return {module.ROUTE: name for name, module in all_handlers().items()}
