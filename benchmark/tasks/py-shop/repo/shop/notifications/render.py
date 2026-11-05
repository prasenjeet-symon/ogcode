"""Fill a notification template from a context dict.

Templates are plain text with ``{placeholder}`` holes. Every hole has a
default, so a sender can render one with a partial context instead of
failing halfway through a send.
"""

from pathlib import Path

from shop.errors import NotFoundError, ValidationError

__all__ = ["DEFAULTS", "TEMPLATE_DIR", "list_templates", "render", "template_path"]

TEMPLATE_DIR = Path(__file__).resolve().parent / "templates"

DEFAULTS = {'address_line': '12 Alder Way, Springfield',
 'amount': '$12.00',
 'carrier': 'Northpost',
 'coupon_code': 'WELCOME10',
 'discount': '10%',
 'eta': 'Thursday',
 'expires_on': '2026-03-31',
 'first_name': 'Ada',
 'invoice_number': 'INV-88121',
 'item_name': 'torque wrench',
 'order_id': 'ORD-1042',
 'points': '320',
 'quantity': '3',
 'reset_url': 'https://example.com/reset/abc',
 'review_url': 'https://example.com/review/abc',
 'store_name': 'Ogcode Supply',
 'support_email': 'help@example.com',
 'total': '$36.00',
 'tracking_number': 'NP-55809',
 'unsubscribe_url': 'https://example.com/unsubscribe'}


def list_templates() -> list:
    """Template names, extension stripped, in sorted order."""
    return sorted(path.stem for path in TEMPLATE_DIR.glob("*.txt"))


def template_path(name: str) -> Path:
    """The file behind a template name, refusing anything path-like."""
    stem = str(name).strip()
    if stem.endswith(".txt"):
        stem = stem[:-4]
    if not stem or "/" in stem or chr(92) in stem or stem.startswith("."):
        raise NotFoundError("no template named %r" % (name,))
    path = TEMPLATE_DIR / (stem + ".txt")
    if not path.exists():
        raise NotFoundError("no template named %r" % (name,))
    return path


def render(template_name: str, context: dict) -> str:
    """The template with every hole filled, leaving no braces behind."""
    path = template_path(template_name)
    values = dict(DEFAULTS)
    for key, value in dict(context or {}).items():
        if key in values:
            values[key] = str(value)
    try:
        return path.read_text(encoding="utf-8").format(**values)
    except (KeyError, IndexError) as exc:
        raise ValidationError("template %r has an unknown hole: %s" % (template_name, exc))
