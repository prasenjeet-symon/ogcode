"""Exception types shared across the shop service.

Every error the service raises on purpose derives from :class:`ShopError`, so a
caller can catch one class and still tell the cases apart.
"""


class ShopError(Exception):
    """Base class for every error the shop service raises."""


class ValidationError(ShopError):
    """A caller supplied a value the service cannot accept."""


class NotFoundError(ShopError):
    """A lookup found no record for the given key."""


class PaymentDeclined(ShopError):
    """A gateway refused a charge."""


class InventoryShortfall(ShopError):
    """A reservation asked for more units than are on hand."""
