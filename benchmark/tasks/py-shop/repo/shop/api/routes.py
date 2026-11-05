"""The route table and the dispatcher that walks it.

Paths are matched literally. The fixture has no path parameters: a route
that needs an id reads it from the request body instead, which keeps the
matcher small enough to read in one screen.
"""

from shop.api.handlers import all_handlers, route_table
from shop.errors import NotFoundError, ValidationError

__all__ = ["ROUTES", "dispatch", "resolve"]

ROUTES = route_table()


def resolve(method: str, path: str):
    """The handler name for a method and path, or ``None``."""
    return ROUTES.get((str(method).upper(), str(path)))


def dispatch(method: str, path: str, request: dict) -> dict:
    """Run the handler a route names, refusing anything unregistered."""
    name = resolve(method, path)
    if name is None:
        raise NotFoundError("no route for %s %s" % (method, path))
    handler = all_handlers()[name]
    answer = handler.handle(dict(request or {}))
    if not isinstance(answer, dict):
        raise ValidationError("handler %s returned %s" % (name, type(answer).__name__))
    return answer
