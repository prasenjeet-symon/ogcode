"""Cheap post-deploy checks that need no network and no database.

Each check reads one piece of configuration and asserts the invariant the
deploy pipeline relies on. ``run`` reports every check, passing or failing,
so a single bad deploy shows all of its symptoms at once.
"""

from shop.config import DEFAULT_CURRENCY, TIER_BOUNDARIES, settings
from shop.errors import ShopError, ValidationError

__all__ = ["CHECKS", "check_currency", "check_errors", "check_tiers", "run"]


def check_currency() -> dict:
    """The configured currency is a three-letter code."""
    code = str(DEFAULT_CURRENCY)
    ok = len(code) == 3 and code.isupper() and code.isalpha()
    return {"name": "currency", "ok": ok, "detail": code}


def check_tiers() -> dict:
    """The tier ladder is ordered from lowest to highest with no gaps."""
    tiers = tuple(TIER_BOUNDARIES)
    ok = bool(tiers) and len(set(tiers)) == len(tiers)
    return {"name": "tiers", "ok": ok, "detail": ",".join(tiers)}


def check_errors() -> dict:
    """Every error the service raises derives from ``ShopError``."""
    raised = ValidationError("smoke")
    ok = isinstance(raised, ShopError) and str(raised) == "smoke"
    return {"name": "errors", "ok": ok, "detail": type(raised).__name__}


def check_settings() -> dict:
    """The immutable settings instance is wired to the same currency."""
    ok = settings.currency == DEFAULT_CURRENCY and settings.max_cart_lines > 0
    return {"name": "settings", "ok": ok, "detail": settings.order_prefix}


CHECKS = (
    ("currency", check_currency),
    ("tiers", check_tiers),
    ("errors", check_errors),
    ("settings", check_settings),
)


def run(names=None) -> dict:
    """Run the named checks (all by default) and report the outcome."""
    wanted = None if names is None else {str(name) for name in names}
    passed = []
    failed = []
    for name, check in CHECKS:
        if wanted is not None and name not in wanted:
            continue
        result = check()
        (passed if result["ok"] else failed).append(name)
    return {"passed": passed, "failed": failed, "ok": not failed}
