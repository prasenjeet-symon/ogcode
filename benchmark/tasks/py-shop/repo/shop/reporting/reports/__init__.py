"""Report builders.

Every module here exposes one function::

    build(orders, **kwargs) -> dict

The returned dict always names its own report, so a dashboard can tell
which builder produced a payload without being told.
"""

import importlib
import pkgutil

__all__ = ["all_reports", "report_names", "run"]


def report_names() -> list:
    """Report module names in this package."""
    found = [info.name for info in pkgutil.iter_modules(__path__)]
    return sorted(name for name in found if not name.startswith("_"))


def all_reports() -> dict:
    """Report name to module, keyed by module name."""
    modules = [importlib.import_module(__name__ + "." + name) for name in report_names()]
    return {module.__name__.rsplit(".", 1)[-1]: module for module in modules}


def run(name: str, orders: list, **kwargs) -> dict:
    """Run one report by name. A missing report is an ordinary KeyError."""
    return all_reports()[name].build(orders, **kwargs)
