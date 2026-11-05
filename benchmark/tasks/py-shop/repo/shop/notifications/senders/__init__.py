"""Delivery channels.

Every sender answers the same call, so the notification worker picks one by
name and never branches on the channel::

    CHANNEL                the name the worker asks for
    send(message) -> dict  the provider receipt, or ``ValidationError``
"""

import importlib
import pkgutil

from shop.errors import ValidationError

__all__ = ["all_senders", "send_via", "sender_names"]


def sender_names() -> list:
    """Sender module names in this package."""
    found = [info.name for info in pkgutil.iter_modules(__path__)]
    return sorted(name for name in found if not name.startswith("_"))


def all_senders() -> dict:
    """Channel name to module."""
    modules = [importlib.import_module(__name__ + "." + name) for name in sender_names()]
    return {module.CHANNEL: module for module in modules}


def send_via(channel: str, message: dict) -> dict:
    """Send through the named channel, refusing an unknown one."""
    senders = all_senders()
    if channel not in senders:
        raise ValidationError("no sender for channel %r" % (channel,))
    return senders[channel].send(dict(message or {}))
