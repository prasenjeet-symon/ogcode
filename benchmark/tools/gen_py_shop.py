#!/usr/bin/env python3
"""Generate the filler half of the py-shop benchmark fixture repo.

The py-shop task ships a small hand-written order/money/checkout core plus this
generated bulk: catalog rules, payment gateways, carrier rate cards, warehouse
adapters, reports, API handlers, vendor adapters, notifications, search, ops
helpers, tests, docs, fixtures and scripts.

The output is byte-for-byte reproducible: no randomness, no clock reads, no
network, and every dict is emitted in a fixed order. Run it twice and diff the
tree; the only thing that changes is mtime.

    python3 tools/gen_py_shop.py [--check]

--check regenerates in memory and reports what on disk differs, writing nothing.
"""

import copy
import json
import pprint
import re
import sys
import textwrap
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent / "tasks" / "py-shop" / "repo"

# Paths owned by the hand-written half of the fixture. The generator refuses to
# write them even if a template is edited to name one by accident.
FORBIDDEN = (
    "shop/__init__.py",
    "shop/money.py",
    "shop/catalog/models.py",
    "shop/catalog/pricing.py",
    "shop/orders/",
    "shop/discounts/",
    "tests/test_money.py",
    "tests/test_tiers.py",
    "tests/test_checkout_discounts.py",
    "README.md",
    "pyproject.toml",
)

# Nothing generated may be a wall of text the benchmark has to read around.
MAX_LINES = 118

FILES = {}


def emit(rel, text):
    """Register one generated file, refusing forbidden paths and bloated files."""
    for bad in FORBIDDEN:
        if rel == bad or (bad.endswith("/") and rel.startswith(bad)):
            raise SystemExit("refusing to generate hand-written path: %s" % rel)
    if rel in FILES:
        raise SystemExit("duplicate path: %s" % rel)
    if not text.endswith("\n"):
        text += "\n"
    count = text.count("\n")
    if count > MAX_LINES:
        raise SystemExit("%s would be %d lines (limit %d)" % (rel, count, MAX_LINES))
    FILES[rel] = text


def block(code, indent=4):
    """Dedent a snippet and re-indent it, so specs read naturally in this file."""
    pad = " " * indent
    out = []
    for line in textwrap.dedent(code).strip("\n").split("\n"):
        out.append(pad + line if line.strip() else "")
    return "\n".join(out)


def fill(tpl, **kw):
    """Substitute @@NAME@@ placeholders. Refuses to emit an unfilled one."""
    text = textwrap.dedent(tpl)
    for key, value in kw.items():
        text = text.replace("@@" + key + "@@", str(value))
    left = re.findall(r"@@[A-Za-z_][A-Za-z0-9_]*@@", text)
    if left:
        raise SystemExit("unfilled placeholder(s): %s" % ", ".join(sorted(set(left))))
    return text.strip("\n") + "\n"


def render(path, tpl, **kw):
    emit(path, fill(tpl, **kw))


def as_literal(value, width=86):
    """A readable Python literal for values embedded in generated tests."""
    return pprint.pformat(value, width=width, sort_dicts=False)


def as_json(value):
    return json.dumps(value, indent=2, sort_keys=True) + "\n"


# --------------------------------------------------------------------------
# The canonical order shape. Rules, reports and handlers all read this, so the
# fixture stays self-consistent instead of drifting per module.
# --------------------------------------------------------------------------

BASE_ORDER = {
    "order_id": "ORD-1042",
    "channel": "web",
    "currency": "USD",
    "created_at": "2026-02-11T09:30:00Z",
    "subtotal_cents": 3600,
    "shipping_cents": 499,
    "items_count": 3,
    "coupon_codes": ["SPRING10"],
    "customer": {
        "id": "cus-77",
        "email": "ada@example.com",
        "tier": "gold",
        "orders_count": 4,
        "lifetime_cents": 18000,
        "points": 320,
        "accepts_marketing": True,
    },
    "lines": [
        {
            "sku": "SKU-A1",
            "qty": 3,
            "unit_cents": 1200,
            "category": "tools",
            "weight_g": 700,
        }
    ],
}


def order(**overrides):
    """A copy of the canonical order with the named top-level keys replaced."""
    made = copy.deepcopy(BASE_ORDER)
    for key, value in overrides.items():
        made[key] = copy.deepcopy(value)
    return made


def line(sku, qty, unit_cents, category="tools", weight_g=700):
    return {
        "sku": sku,
        "qty": qty,
        "unit_cents": unit_cents,
        "category": category,
        "weight_g": weight_g,
    }


# --------------------------------------------------------------------------
# shop/catalog/rules -- 28 merchandising rules, each with its own predicate.
# --------------------------------------------------------------------------

RULES = [
    {
        "slug": "bulk_quantity",
        "id": "R-010",
        "title": "Volume break on a single bulk line",
        "doc": "Volume break: a line ordered at five units or more earns 5% back.\n"
        "The credit is computed per qualifying line and capped, so one very large\n"
        "line cannot swallow the whole margin.",
        "consts": ["BULK_MIN_QTY = 5", "BULK_PERMILLE = 50", "BULK_CAP_CENTS = 1500"],
        "helpers": '''
        def _bulk_cents(order: dict) -> int:
            total = 0
            for row in order.get("lines") or []:
                qty = int(row.get("qty") or 0)
                if qty >= BULK_MIN_QTY:
                    total += qty * int(row.get("unit_cents") or 0)
            return total
        ''',
        "applies": '''
        return _bulk_cents(order) > 0
        ''',
        "discount": '''
        return min(_bulk_cents(order) // 1000 * BULK_PERMILLE, BULK_CAP_CENTS)
        ''',
        "sample": order(lines=[line("SKU-A1", 6, 1000)], subtotal_cents=6000),
        "cents": 300,
    },
    {
        "slug": "first_order",
        "id": "R-020",
        "title": "Welcome credit for a first order",
        "doc": "Welcome credit: a customer with no completed orders gets 10% off,\n"
        "paid out of a fixed pot. The credit is the smaller of the pot and a tenth\n"
        "of the subtotal, so a tiny basket does not get a free ride.",
        "consts": ["WELCOME_POT_CENTS = 500", "WELCOME_MIN_SUBTOTAL = 2000"],
        "applies": '''
        customer = order.get("customer") or {}
        subtotal = int(order.get("subtotal_cents") or 0)
        return int(customer.get("orders_count") or 0) == 0 and subtotal >= WELCOME_MIN_SUBTOTAL
        ''',
        "discount": '''
        if not applies(order):
            return 0
        return min(WELCOME_POT_CENTS, int(order.get("subtotal_cents") or 0) // 10)
        ''',
        "sample": order(customer=dict(BASE_ORDER["customer"], orders_count=0), subtotal_cents=2400),
        "cents": 240,
    },
    {
        "slug": "tier_credit",
        "id": "R-030",
        "title": "Loyalty tier percentage",
        "doc": "Loyalty tiers earn a percentage off the merchandise subtotal: gold\n"
        "takes 8%, platinum takes 12%. The discount is capped in cents and only\n"
        "applies to orders billed in the house currency.",
        "imports": ["from shop.config import DEFAULT_CURRENCY"],
        "consts": [
            'TIER_PERMILLE = {"gold": 80, "platinum": 120}',
            "TIER_CAP_CENTS = 4000",
        ],
        "helpers": '''
        def _tier(order: dict) -> str:
            customer = order.get("customer") or {}
            return str(customer.get("tier") or "").strip().lower()
        ''',
        "applies": '''
        if order.get("currency") != DEFAULT_CURRENCY:
            return False
        return _tier(order) in TIER_PERMILLE
        ''',
        "discount": '''
        if not applies(order):
            return 0
        subtotal = int(order.get("subtotal_cents") or 0)
        return min(subtotal * TIER_PERMILLE[_tier(order)] // 1000, TIER_CAP_CENTS)
        ''',
        "sample": order(subtotal_cents=3600),
        "cents": 288,
    },
    {
        "slug": "free_shipping",
        "id": "R-040",
        "title": "Free shipping above the house threshold",
        "doc": "Free shipping: once the merchandise subtotal clears the configured\n"
        "threshold the shipping line is credited back in full. The threshold is a\n"
        "setting, not a constant, so finance can move it without a code change.",
        "imports": ["from shop.config import settings"],
        "consts": [],
        "applies": '''
        subtotal = int(order.get("subtotal_cents") or 0)
        shipping = int(order.get("shipping_cents") or 0)
        return shipping > 0 and subtotal >= settings.free_shipping_cents
        ''',
        "discount": '''
        if not applies(order):
            return 0
        return int(order.get("shipping_cents") or 0)
        ''',
        "sample": order(subtotal_cents=5200, shipping_cents=699),
        "cents": 699,
    },
    {
        "slug": "category_bundle",
        "id": "R-050",
        "title": "Home bundle when two home lines appear",
        "doc": "Home bundle: two separate home-and-living lines on one order earn\n"
        "7% back on the home lines only. One line repeated does not count; the\n"
        "merchandising team wants cross-sell, not bulk.",
        "consts": [
            'BUNDLE_CATEGORY = "home"',
            "BUNDLE_MIN_LINES = 2",
            "BUNDLE_PERMILLE = 70",
            "BUNDLE_CAP_CENTS = 2500",
        ],
        "helpers": '''
        def _home(order: dict) -> tuple:
            rows = [r for r in order.get("lines") or [] if str(r.get("category") or "") == BUNDLE_CATEGORY]
            cents = sum(int(r.get("qty") or 0) * int(r.get("unit_cents") or 0) for r in rows)
            return len(rows), cents
        ''',
        "applies": '''
        count, _cents = _home(order)
        return count >= BUNDLE_MIN_LINES
        ''',
        "discount": '''
        _count, cents = _home(order)
        return min(cents * BUNDLE_PERMILLE // 1000, BUNDLE_CAP_CENTS)
        ''',
        "sample": order(
            lines=[line("SKU-H1", 1, 1500, "home"), line("SKU-H2", 1, 2000, "home")],
            subtotal_cents=3500,
        ),
        "cents": 245,
    },
    {
        "slug": "coupon_ladder",
        "id": "R-060",
        "title": "Stackable coupon codes, best one wins",
        "doc": "Coupon ladder: the seasonal codes each carry their own rate and only\n"
        "the most generous one on the order is applied. Codes are matched case\n"
        "insensitively because the storefront lowercases them before sending.",
        "consts": [],
        "helpers": '''
        COUPON_PERMILLE = {"SPRING10": 100, "SPRING15": 150, "SUMMER5": 50}


        def _codes(order: dict) -> set:
            return {str(code).strip().upper() for code in order.get("coupon_codes") or []}
        ''',
        "applies": '''
        return bool(_codes(order) & set(COUPON_PERMILLE))
        ''',
        "discount": '''
        best = max((COUPON_PERMILLE[c] for c in _codes(order) if c in COUPON_PERMILLE), default=0)
        return int(order.get("subtotal_cents") or 0) * best // 1000
        ''',
        "sample": order(coupon_codes=["spring15"], subtotal_cents=4000),
        "cents": 600,
    },
    {
        "slug": "seasonal_winter",
        "id": "R-070",
        "title": "Winter clearance flat credit",
        "doc": "Winter push: orders placed in December, January or February take a\n"
        "flat credit once the basket is worth having. The month is read from the\n"
        "order timestamp, not the wall clock, so replays stay reproducible.",
        "imports": ["from datetime import date"],
        "consts": ["WINTER_MONTHS = (12, 1, 2)", "WINTER_CENTS = 600", "WINTER_MIN_SUBTOTAL = 1500"],
        "helpers": '''
        def _month(order: dict) -> int:
            stamp = str(order.get("created_at") or "")[:10]
            try:
                return date.fromisoformat(stamp).month
            except ValueError:
                return 0
        ''',
        "applies": '''
        subtotal = int(order.get("subtotal_cents") or 0)
        return _month(order) in WINTER_MONTHS and subtotal >= WINTER_MIN_SUBTOTAL
        ''',
        "discount": '''
        return WINTER_CENTS if applies(order) else 0
        ''',
        "sample": order(created_at="2026-01-14T18:00:00Z"),
        "cents": 600,
    },
    {
        "slug": "points_redemption",
        "id": "R-080",
        "title": "Spend loyalty points in whole hundreds",
        "doc": "Points redemption: two points buy one cent, spent in whole hundreds\n"
        "of cents so the receipt never shows a ragged number. Whatever is left\n"
        "over stays on the account.",
        "consts": ["POINTS_PER_CENT = 2", "REDEEM_STEP_CENTS = 100", "REDEEM_CAP_CENTS = 800"],
        "helpers": '''
        def _points(order: dict) -> int:
            customer = order.get("customer") or {}
            return int(customer.get("points") or 0)
        ''',
        "applies": '''
        return _points(order) // POINTS_PER_CENT >= REDEEM_STEP_CENTS
        ''',
        "discount": '''
        spendable = _points(order) // POINTS_PER_CENT
        return min(spendable // REDEEM_STEP_CENTS * REDEEM_STEP_CENTS, REDEEM_CAP_CENTS)
        ''',
        "sample": order(customer=dict(BASE_ORDER["customer"], points=1230), subtotal_cents=3600),
        "cents": 600,
    },
    {
        "slug": "round_to_dollar",
        "id": "R-090",
        "title": "Round a ragged subtotal down to the dollar",
        "doc": "Receipt tidiness: a subtotal whose cents part is at least 50 rounds up\n"
        "to the next whole dollar at our expense. Anything smaller is left alone,\n"
        "because giving away four cents looks petty on a receipt.",
        "consts": ["ROUND_AT_CENTS = 50", "DOLLAR_CENTS = 100"],
        "applies": '''
        return int(order.get("subtotal_cents") or 0) % DOLLAR_CENTS >= ROUND_AT_CENTS
        ''',
        "discount": '''
        if not applies(order):
            return 0
        return DOLLAR_CENTS - int(order.get("subtotal_cents") or 0) % DOLLAR_CENTS
        ''',
        "sample": order(subtotal_cents=3679),
        "cents": 21,
    },
    {
        "slug": "referral_credit",
        "id": "R-100",
        "title": "Referral thank-you on the first paid basket",
        "doc": "Referral credit: a customer who arrived through a referral link gets a\n"
        "flat thank-you once their basket is big enough to be worth shipping. The\n"
        "credit can never exceed the subtotal.",
        "consts": ["REFERRAL_CENTS = 750", "REFERRAL_MIN_SUBTOTAL = 2500"],
        "applies": '''
        customer = order.get("customer") or {}
        subtotal = int(order.get("subtotal_cents") or 0)
        return bool(str(customer.get("referred_by") or "").strip()) and subtotal >= REFERRAL_MIN_SUBTOTAL
        ''',
        "discount": '''
        if not applies(order):
            return 0
        return min(REFERRAL_CENTS, int(order.get("subtotal_cents") or 0))
        ''',
        "sample": order(
            customer=dict(BASE_ORDER["customer"], referred_by="cus-12"),
            subtotal_cents=3600,
        ),
        "cents": 750,
    },
    {
        "slug": "newsletter_signup",
        "id": "R-110",
        "title": "Newsletter opt-in on a young account",
        "doc": "Newsletter nudge: an account that accepted marketing mail and has not\n"
        "yet ordered twice gets a flat credit. Older accounts are excluded because\n"
        "they have already been counted.",
        "consts": ["NEWSLETTER_CENTS = 400", "NEWSLETTER_MAX_ORDERS = 1"],
        "applies": '''
        customer = order.get("customer") or {}
        if not customer.get("accepts_marketing"):
            return False
        return int(customer.get("orders_count") or 0) <= NEWSLETTER_MAX_ORDERS
        ''',
        "discount": '''
        return NEWSLETTER_CENTS if applies(order) else 0
        ''',
        "sample": order(
            customer=dict(BASE_ORDER["customer"], accepts_marketing=True, orders_count=1),
            subtotal_cents=3000,
        ),
        "cents": 400,
    },
    {
        "slug": "heavy_freight",
        "id": "R-120",
        "title": "Freight credit on a heavy basket",
        "doc": "Freight credit: a basket weighing five kilograms or more is expensive\n"
        "to move, so we credit part of the freight back rather than let the basket\n"
        "abandon at the shipping step.",
        "consts": ["HEAVY_MIN_GRAMS = 5000", "FREIGHT_CREDIT_CENTS = 900"],
        "helpers": '''
        def _grams(order: dict) -> int:
            total = 0
            for row in order.get("lines") or []:
                total += int(row.get("qty") or 0) * int(row.get("weight_g") or 0)
            return total
        ''',
        "applies": '''
        return _grams(order) >= HEAVY_MIN_GRAMS
        ''',
        "discount": '''
        return FREIGHT_CREDIT_CENTS if applies(order) else 0
        ''',
        "sample": order(lines=[line("SKU-A1", 6, 1000, weight_g=900)], subtotal_cents=6000),
        "cents": 900,
    },
    {
        "slug": "multi_line",
        "id": "R-130",
        "title": "Small basket-mix credit",
        "doc": "Basket mix: an order with four or more distinct lines earns 3% back, up\n"
        "to the configured cart ceiling. It is a nudge towards discovery rather\n"
        "than a real discount.",
        "imports": ["from shop.config import settings"],
        "consts": ["MIX_MIN_LINES = 4", "MIX_PERMILLE = 30", "MIX_CAP_CENTS = 1200"],
        "applies": '''
        rows = order.get("lines") or []
        return MIX_MIN_LINES <= len(rows) <= settings.max_cart_lines
        ''',
        "discount": '''
        if not applies(order):
            return 0
        subtotal = int(order.get("subtotal_cents") or 0)
        return min(subtotal * MIX_PERMILLE // 1000, MIX_CAP_CENTS)
        ''',
        "sample": order(
            lines=[line("SKU-%d" % n, 1, 800) for n in range(1, 5)],
            subtotal_cents=3200,
        ),
        "cents": 96,
    },
    {
        "slug": "duplicate_sku",
        "id": "R-140",
        "title": "Credit for a SKU split across lines",
        "doc": "Duplicate lines: the storefront occasionally splits one SKU across two\n"
        "cart lines after a merge. We credit a quarter-dollar per surplus line\n"
        "instead of failing the checkout.",
        "consts": ["DUPLICATE_CENTS = 250", "DUPLICATE_CAP = 3"],
        "helpers": '''
        def _surplus(order: dict) -> int:
            seen = {}
            for row in order.get("lines") or []:
                sku = str(row.get("sku") or "")
                if sku:
                    seen[sku] = seen.get(sku, 0) + 1
            return sum(count - 1 for count in seen.values() if count > 1)
        ''',
        "applies": '''
        return _surplus(order) > 0
        ''',
        "discount": '''
        return min(_surplus(order) * DUPLICATE_CENTS, DUPLICATE_CAP * DUPLICATE_CENTS)
        ''',
        "sample": order(
            lines=[line("SKU-X1", 1, 900), line("SKU-X1", 1, 900), line("SKU-Y2", 1, 1200)],
            subtotal_cents=3000,
        ),
        "cents": 250,
    },
]

RULES.extend(
    [
        {
            "slug": "high_value",
            "id": "R-150",
            "title": "High-value basket percentage",
            "doc": "High value: baskets above two hundred dollars take 5% back, capped\n"
            "so a single furniture order does not wipe out the quarter. Only house-currency\n"
            "orders qualify; converted orders are handled by finance offline.",
            "imports": ["from shop.config import DEFAULT_CURRENCY"],
            "consts": ["HIGH_VALUE_MIN_CENTS = 20000", "HIGH_VALUE_PERMILLE = 50", "HIGH_VALUE_CAP_CENTS = 5000"],
            "applies": '''
            if order.get("currency") != DEFAULT_CURRENCY:
                return False
            return int(order.get("subtotal_cents") or 0) >= HIGH_VALUE_MIN_CENTS
            ''',
            "discount": '''
            if not applies(order):
                return 0
            subtotal = int(order.get("subtotal_cents") or 0)
            return min(subtotal * HIGH_VALUE_PERMILLE // 1000, HIGH_VALUE_CAP_CENTS)
            ''',
            "sample": order(subtotal_cents=24000),
            "cents": 1200,
        },
        {
            "slug": "clearance_category",
            "id": "R-160",
            "title": "Clearance lines at fifteen percent",
            "doc": "Clearance: anything filed under the clearance category takes 15%\n"
            "off its own line total. The credit is scoped to clearance lines, so mixing\n"
            "a clearance t-shirt into a tools order does not discount the tools.",
            "consts": ['CLEARANCE_CATEGORY = "clearance"', "CLEARANCE_PERMILLE = 150", "CLEARANCE_CAP_CENTS = 3000"],
            "helpers": '''
            def _clearance_cents(order: dict) -> int:
                total = 0
                for row in order.get("lines") or []:
                    if str(row.get("category") or "") == CLEARANCE_CATEGORY:
                        total += int(row.get("qty") or 0) * int(row.get("unit_cents") or 0)
                return total
            ''',
            "applies": '''
            return _clearance_cents(order) > 0
            ''',
            "discount": '''
            return min(_clearance_cents(order) * CLEARANCE_PERMILLE // 1000, CLEARANCE_CAP_CENTS)
            ''',
            "sample": order(lines=[line("SKU-C9", 2, 900, "clearance")], subtotal_cents=1800),
            "cents": 270,
        },
        {
            "slug": "store_credit",
            "id": "R-170",
            "title": "Apply a customer's store credit",
            "doc": "Store credit: a balance carried on the order is spent down, but never\n"
            "past the thousand-cent cap that support may release without an approval,\n"
            "and never past the subtotal itself.",
            "consts": ["CREDIT_RELEASE_CAP_CENTS = 1000"],
            "applies": '''
            credit = int(order.get("store_credit_cents") or 0)
            subtotal = int(order.get("subtotal_cents") or 0)
            return credit > 0 and subtotal > 0
            ''',
            "discount": '''
            if not applies(order):
                return 0
            credit = int(order.get("store_credit_cents") or 0)
            subtotal = int(order.get("subtotal_cents") or 0)
            return min(credit, CREDIT_RELEASE_CAP_CENTS, subtotal)
            ''',
            "sample": order(store_credit_cents=1400, subtotal_cents=5000),
            "cents": 1000,
        },
        {
            "slug": "weekend_promo",
            "id": "R-180",
            "title": "Weekend flat credit",
            "doc": "Weekend promo: Saturday and Sunday orders take a flat credit once the\n"
            "basket clears twenty dollars. The weekday comes from the order timestamp so\n"
            "a replayed order always scores the same way.",
            "imports": ["from datetime import date"],
            "consts": ["WEEKEND_DAYS = (5, 6)", "WEEKEND_CENTS = 350", "WEEKEND_MIN_SUBTOTAL = 2000"],
            "helpers": '''
            def _weekday(order: dict) -> int:
                stamp = str(order.get("created_at") or "")[:10]
                try:
                    return date.fromisoformat(stamp).weekday()
                except ValueError:
                    return -1
            ''',
            "applies": '''
            subtotal = int(order.get("subtotal_cents") or 0)
            return _weekday(order) in WEEKEND_DAYS and subtotal >= WEEKEND_MIN_SUBTOTAL
            ''',
            "discount": '''
            return WEEKEND_CENTS if applies(order) else 0
            ''',
            "sample": order(created_at="2026-02-14T12:00:00Z"),
            "cents": 350,
        },
        {
            "slug": "new_account_mix",
            "id": "R-190",
            "title": "First basket discovery credit per line",
            "doc": "Discovery credit: an account with no lifetime spend yet gets two\n"
            "dollars back per distinct line, up to six dollars. It rewards trying several\n"
            "things once rather than buying one expensive thing.",
            "consts": ["NEW_ACCOUNT_PER_LINE_CENTS = 200", "NEW_ACCOUNT_MAX_LINES = 3"],
            "applies": '''
            customer = order.get("customer") or {}
            rows = order.get("lines") or []
            return bool(rows) and int(customer.get("lifetime_cents") or 0) == 0
            ''',
            "discount": '''
            if not applies(order):
                return 0
            distinct = {str(row.get("sku") or "") for row in order.get("lines") or []}
            return min(len(distinct), NEW_ACCOUNT_MAX_LINES) * NEW_ACCOUNT_PER_LINE_CENTS
            ''',
            "sample": order(
                customer=dict(BASE_ORDER["customer"], lifetime_cents=0),
                lines=[line("SKU-D1", 1, 900), line("SKU-D2", 1, 1100)],
                subtotal_cents=2000,
            ),
            "cents": 400,
        },
        {
            "slug": "gift_wrap_waiver",
            "id": "R-200",
            "title": "Gift wrap fee waived",
            "doc": "Gift wrap: the wrapping fee rides on the order as its own field, and\n"
            "we waive it when the customer asked for gift wrapping. Paper costs less\n"
            "than the goodwill it buys at this volume.",
            "consts": ["GIFT_WRAP_CENTS = 250"],
            "applies": '''
            return order.get("gift_wrap") is True and int(order.get("subtotal_cents") or 0) > 0
            ''',
            "discount": '''
            return GIFT_WRAP_CENTS if applies(order) else 0
            ''',
            "sample": order(gift_wrap=True),
            "cents": 250,
        },
        {
            "slug": "store_pickup",
            "id": "R-210",
            "title": "Click-and-collect credit",
            "doc": "Store pickup: a click-and-collect order skips the parcel network, so\n"
            "the handling half of the shipping fee is credited back. Parcel orders keep\n"
            "their full shipping charge.",
            "consts": ['PICKUP_CHANNEL = "pickup"', "PICKUP_CREDIT_CENTS = 500"],
            "applies": '''
            return str(order.get("channel") or "") == PICKUP_CHANNEL
            ''',
            "discount": '''
            return PICKUP_CREDIT_CENTS if applies(order) else 0
            ''',
            "sample": order(channel="pickup", shipping_cents=0),
            "cents": 500,
        },
        {
            "slug": "subscription_renewal",
            "id": "R-220",
            "title": "Subscription renewal percentage",
            "doc": "Subscription renewals carry 6% off the merchandise subtotal, capped\n"
            "at nine dollars. The flag is set by the subscription service on the order\n"
            "itself, so no customer lookup is needed here.",
            "consts": ["SUBSCRIPTION_PERMILLE = 60", "SUBSCRIPTION_CAP_CENTS = 900"],
            "applies": '''
            return order.get("subscription") is True and int(order.get("subtotal_cents") or 0) > 0
            ''',
            "discount": '''
            if not applies(order):
                return 0
            subtotal = int(order.get("subtotal_cents") or 0)
            return min(subtotal * SUBSCRIPTION_PERMILLE // 1000, SUBSCRIPTION_CAP_CENTS)
            ''',
            "sample": order(subscription=True, subtotal_cents=12000),
            "cents": 720,
        },
        {
            "slug": "employee_discount",
            "id": "R-230",
            "title": "Staff discount at twenty percent",
            "doc": "Staff discount: an account flagged as staff takes 20% off, capped at\n"
            "eighty dollars per order. The cap is deliberate; laptops and espresso\n"
            "machines are the reason it exists.",
            "consts": ["STAFF_PERMILLE = 200", "STAFF_CAP_CENTS = 8000"],
            "applies": '''
            customer = order.get("customer") or {}
            return customer.get("employee") is True and int(order.get("subtotal_cents") or 0) > 0
            ''',
            "discount": '''
            if not applies(order):
                return 0
            subtotal = int(order.get("subtotal_cents") or 0)
            return min(subtotal * STAFF_PERMILLE // 1000, STAFF_CAP_CENTS)
            ''',
            "sample": order(
                customer=dict(BASE_ORDER["customer"], employee=True),
                subtotal_cents=5000,
            ),
            "cents": 1000,
        },
        {
            "slug": "bogo_unit",
            "id": "R-240",
            "title": "Buy-one-get-one on a multi-unit line",
            "doc": "Buy one get one: a line with two or more units gives away the cheapest\n"
            "single unit among the qualifying lines. Lines priced at zero are ignored so\n"
            "a free-sample row cannot become the giveaway.",
            "consts": ["BOGO_MIN_QTY = 2"],
            "helpers": '''
            def _unit_prices(order: dict) -> list:
                prices = []
                for row in order.get("lines") or []:
                    if int(row.get("qty") or 0) < BOGO_MIN_QTY:
                        continue
                    price = int(row.get("unit_cents") or 0)
                    if price > 0:
                        prices.append(price)
                return prices
            ''',
            "applies": '''
            return bool(_unit_prices(order))
            ''',
            "discount": '''
            prices = _unit_prices(order)
            return min(prices) if prices else 0
            ''',
            "sample": order(
                lines=[line("SKU-B1", 2, 1450), line("SKU-B2", 1, 900)],
                subtotal_cents=3800,
            ),
            "cents": 1450,
        },
        {
            "slug": "threshold_ladder",
            "id": "R-250",
            "title": "Spend ladder, one rung only",
            "doc": "Spend ladder: the basket earns the best rung it clears, and only that\n"
            "rung. The rungs are walked from the top, so an eighty-dollar basket gets the\n"
            "seventy-dollar rung rather than the sum of everything below it.",
            "consts": ["LADDER = ((10000, 1000), (7000, 600), (5000, 300))"],
            "applies": '''
            subtotal = int(order.get("subtotal_cents") or 0)
            return any(subtotal >= floor for floor, _cents in LADDER)
            ''',
            "discount": '''
            subtotal = int(order.get("subtotal_cents") or 0)
            for floor, cents in LADDER:
                if subtotal >= floor:
                    return cents
            return 0
            ''',
            "sample": order(subtotal_cents=8200),
            "cents": 600,
        },
        {
            "slug": "winback_coupon",
            "id": "R-260",
            "title": "Win-back coupon for a lapsed customer",
            "doc": "Win-back: the COMEBACK code is mailed to customers who have not ordered\n"
            "in a year. It is a flat five dollars and it does not stack, because the\n"
            "mailer tells the customer exactly what they are getting.",
            "consts": ['WINBACK_CODE = "COMEBACK"', "WINBACK_CENTS = 500"],
            "applies": '''
            codes = {str(code).strip().upper() for code in order.get("coupon_codes") or []}
            return WINBACK_CODE in codes
            ''',
            "discount": '''
            if not applies(order):
                return 0
            return min(WINBACK_CENTS, int(order.get("subtotal_cents") or 0))
            ''',
            "sample": order(coupon_codes=["COMEBACK"], subtotal_cents=2600),
            "cents": 500,
        },
        {
            "slug": "wholesale_tier",
            "id": "R-270",
            "title": "Wholesale account terms",
            "doc": "Wholesale: accounts on wholesale terms take 10% off, capped at sixty\n"
            "dollars, and only above a hundred-dollar opening order. The floor keeps\n"
            "single-unit staff purchases off the wholesale schedule.",
            "consts": [
                'WHOLESALE_TIER = "wholesale"',
                "WHOLESALE_PERMILLE = 100",
                "WHOLESALE_MIN_CENTS = 10000",
                "WHOLESALE_CAP_CENTS = 6000",
            ],
            "applies": '''
            customer = order.get("customer") or {}
            tier = str(customer.get("tier") or "").strip().lower()
            subtotal = int(order.get("subtotal_cents") or 0)
            return tier == WHOLESALE_TIER and subtotal >= WHOLESALE_MIN_CENTS
            ''',
            "discount": '''
            if not applies(order):
                return 0
            subtotal = int(order.get("subtotal_cents") or 0)
            return min(subtotal * WHOLESALE_PERMILLE // 1000, WHOLESALE_CAP_CENTS)
            ''',
            "sample": order(customer=dict(BASE_ORDER["customer"], tier="wholesale"), subtotal_cents=8000),
            "cents": 800,
        },
        {
            "slug": "app_channel",
            "id": "R-280",
            "title": "App order credit",
            "doc": "App orders: two or more items placed in the mobile app take a flat\n"
            "three dollars. The item floor exists so the credit never swallows a single\n"
            "cheap accessory whole.",
            "consts": ['APP_CHANNEL = "app"', "APP_MIN_ITEMS = 2", "APP_CENTS = 300"],
            "applies": '''
            if str(order.get("channel") or "") != APP_CHANNEL:
                return False
            return int(order.get("items_count") or 0) >= APP_MIN_ITEMS
            ''',
            "discount": '''
            return APP_CENTS if applies(order) else 0
            ''',
            "sample": order(channel="app", items_count=3),
            "cents": 300,
        },
    ]
)


def render_rule(spec):
    """One rule module: docstring, ids, thresholds, then the two entry points."""
    out = ['"""' + spec["doc"] + '"""', ""]
    for import_line in spec.get("imports", []):
        out.append(import_line)
    if spec.get("imports"):
        out.append("")
    out.append('RULE_ID = "%s"' % spec["id"])
    out.append('TITLE = "%s"' % spec["title"])
    out.extend(spec.get("consts", []))
    if spec.get("helpers"):
        out.extend(["", "", block(spec["helpers"], indent=0)])
    out.extend(["", "", "def applies(order: dict) -> bool:", block(spec["applies"])])
    out.extend(["", "", "def discount_cents(order: dict) -> int:", block(spec["discount"])])
    emit("shop/catalog/rules/%s.py" % spec["slug"], "\n".join(out) + "\n")


def render_catalog():
    """The rule modules plus the two catalog package modules and the taxonomy."""
    for spec in RULES:
        render_rule(spec)
    render(
        "shop/catalog/rules/__init__.py",
        '''
        """The merchandising rule catalogue.

        Every module in this package exposes the same two functions, which is all
        the checkout pipeline needs to know about it::

            applies(order)        -> bool   can the rule fire on this order
            discount_cents(order) -> int    how many cents it takes off

        Rules are found by walking the package, so adding a module here is enough
        to put it in front of the pipeline. Nothing else in the service hard-codes a
        rule name: the catalogue is the only registration point.
        """

        import importlib
        import pkgutil

        __all__ = ["all_rules", "evaluate", "module_names"]


        def module_names() -> list:
            """Rule module names in this package, private helpers excluded."""
            found = [info.name for info in pkgutil.iter_modules(__path__)]
            return sorted(name for name in found if not name.startswith("_"))


        def all_rules() -> list:
            """Import every rule module once and hand them back in RULE_ID order."""
            modules = [importlib.import_module(__name__ + "." + name) for name in module_names()]
            return sorted(modules, key=lambda module: module.RULE_ID)


        def evaluate(order: dict) -> list:
            """Every rule that fires on ``order``, worthiest first."""
            hits = []
            for module in all_rules():
                if not module.applies(order):
                    continue
                cents = int(module.discount_cents(order))
                if cents <= 0:
                    continue
                hits.append(
                    {
                        "rule_id": module.RULE_ID,
                        "name": module.__name__.rsplit(".", 1)[-1],
                        "title": module.TITLE,
                        "cents": cents,
                    }
                )
            hits.sort(key=lambda hit: (-hit["cents"], hit["rule_id"]))
            return hits
        ''',
    )
    render(
        "shop/catalog/__init__.py",
        '''
        """Product catalogue: taxonomy, availability and the rule catalogue."""

        from shop.catalog import availability, rules, taxonomy

        __all__ = ["availability", "rules", "taxonomy"]
        ''',
    )
    render(
        "shop/catalog/taxonomy.py",
        '''
        """The category tree shared by rules, facets and reporting.

        Categories are stored as a nested mapping because the tree is small and the
        nesting is the point: a facet filter on `hardware` has to reach `tools`
        underneath it without a second lookup table to maintain.
        """

        TREE = {
            "hardware": {"tools": {}, "fasteners": {}},
            "living": {"home": {}, "garden": {}},
            "apparel": {"outerwear": {}, "footwear": {}},
            "clearance": {},
            "digital": {},
        }


        def flatten(tree=None, parent=None, into=None):
            """A flat ``category -> parent`` map, parent ``None`` at the roots."""
            tree = TREE if tree is None else tree
            into = {} if into is None else into
            for name, children in tree.items():
                into[name] = parent
                flatten(children, name, into)
            return into


        def path_of(category: str) -> tuple:
            """The chain from the root down to ``category``, root first."""
            parents = flatten()
            if category not in parents:
                return ()
            chain = [category]
            while parents[chain[-1]] is not None:
                chain.append(parents[chain[-1]])
            return tuple(reversed(chain))


        def is_ancestor(ancestor: str, category: str) -> bool:
            """True when ``category`` sits anywhere under ``ancestor``."""
            return ancestor in path_of(category)


        def leaves(tree=None) -> list:
            """Every category with no children, sorted."""
            tree = TREE if tree is None else tree
            found = []
            for name, children in tree.items():
                if children:
                    found.extend(leaves(children))
                else:
                    found.append(name)
            return sorted(found)


        def depth(category: str) -> int:
            """How deep the category sits: roots are 1, so an unknown key is 0."""
            return len(path_of(category))
        ''',
    )
    render(
        "shop/catalog/availability.py",
        '''
        """Availability arithmetic over a warehouse snapshot.

        A snapshot is the shape the sync job writes: ``{"SKU-1": {"on_hand": 12,
        "reserved": 2}}``. Nothing here mutates it; the callers own the write path.
        """

        from shop.config import settings


        def on_hand(snapshot: dict, sku: str) -> int:
            """Units physically in the building, zero for an unknown SKU."""
            row = snapshot.get(sku) or {}
            return int(row.get("on_hand") or 0)


        def reserved(snapshot: dict, sku: str) -> int:
            """Units promised to carts that have not checked out yet."""
            row = snapshot.get(sku) or {}
            return int(row.get("reserved") or 0)


        def sellable(snapshot: dict, sku: str) -> int:
            """Units that may still be sold. Never negative, however odd the feed."""
            return max(on_hand(snapshot, sku) - reserved(snapshot, sku), 0)


        def is_low(snapshot: dict, sku: str, threshold: int = None) -> bool:
            """True when the sellable count is at or under the configured floor."""
            floor = settings.low_stock_threshold if threshold is None else threshold
            return sellable(snapshot, sku) <= floor


        def restock_eta(days: int) -> str:
            """The phrasing the storefront shows for an inbound shipment."""
            if days <= 0:
                return "in stock"
            if days == 1:
                return "back tomorrow"
            return "back in %d days" % days


        def shortage(snapshot: dict, wanted: dict) -> dict:
            """Per-SKU shortfall for a basket, empty when everything is coverable."""
            missing = {}
            for sku, qty in wanted.items():
                gap = int(qty) - sellable(snapshot, sku)
                if gap > 0:
                    missing[sku] = gap
            return missing
        ''',
    )


# --------------------------------------------------------------------------
# shop/payments -- 14 gateway adapters, each with its own status vocabulary.
# --------------------------------------------------------------------------

NORMALIZERS = {
    "exact": '''
    key = str(status or "").strip()
    return STATUS_MAP.get(key, "unknown")
    ''',
    "lower": '''
    key = str(status or "").strip().lower()
    return STATUS_MAP.get(key, "unknown")
    ''',
    "upper": '''
    key = str(status or "").strip().upper()
    return STATUS_MAP.get(key, "unknown")
    ''',
    "prefix": '''
    key = str(status or "").strip()
    for prefix in ("payment.", "charge.", "txn."):
        if key.startswith(prefix):
            key = key[len(prefix):]
            break
    return STATUS_MAP.get(key, "unknown")
    ''',
    "tail": '''
    key = str(status or "").strip()
    tail = key.rsplit(".", 1)[-1]
    if tail in STATUS_MAP:
        return STATUS_MAP[tail]
    return STATUS_MAP.get(key, "unknown")
    ''',
    "colon": '''
    key = str(status or "").strip().split(":", 1)[0]
    return STATUS_MAP.get(key, "unknown")
    ''',
    "code": '''
    raw = str(status if status is not None else "").strip()
    try:
        raw = str(int(raw))
    except ValueError:
        pass
    return STATUS_MAP.get(raw, "unknown")
    ''',
    "squash": '''
    key = "".join(str(status or "").split()).upper()
    return STATUS_MAP.get(key, "unknown")
    ''',
    "underscore": '''
    key = str(status or "").strip().replace("-", "_").lower()
    return STATUS_MAP.get(key, "unknown")
    ''',
    "tokens": '''
    key = str(status or "").strip().lower()
    for word in CAPTURED_WORDS:
        if word in key:
            return "captured"
    for word in FAILED_WORDS:
        if word in key:
            return "failed"
    if key in STATUS_MAP:
        return STATUS_MAP[key]
    return "unknown"
    ''',
    "payload": '''
    raw = status
    if isinstance(raw, dict):
        raw = raw.get("status", raw.get("state", ""))
    return STATUS_MAP.get(str(raw or "").strip().upper(), "unknown")
    ''',
    "longest": '''
    key = str(status or "").strip().lower()
    for prefix, canonical in PREFIXES:
        if key.startswith(prefix):
            return canonical
    return "unknown"
    ''',
    "twotier": '''
    key = str(status or "").strip().lower()
    if key in STATUS_MAP:
        return STATUS_MAP[key]
    return LEGACY_MAP.get(key, "unknown")
    ''',
    "alias": '''
    key = str(status or "").strip().lower()
    canonical = STATUS_MAP.get(key, key)
    return ALIASES.get(canonical, canonical if canonical in CANONICAL else "unknown")
    ''',
}

FEE_BODIES = {
    "plain": '''
    if amount_cents <= 0:
        return 0
    return amount_cents * FEE_BPS // 10000
    ''',
    "floor": '''
    if amount_cents <= 0:
        return 0
    return max(amount_cents * FEE_BPS // 10000, MIN_FEE_CENTS)
    ''',
    "ceil": '''
    if amount_cents <= 0:
        return 0
    return (amount_cents * FEE_BPS + 9999) // 10000
    ''',
    "cap": '''
    if amount_cents <= 0:
        return 0
    return min(amount_cents * FEE_BPS // 10000, MAX_FEE_CENTS)
    ''',
    "flat": '''
    if amount_cents <= 0:
        return 0
    return FLAT_FEE_CENTS + amount_cents * FEE_BPS // 10000
    ''',
}

GATEWAYS = [
    {
        "module": "northwind_pay",
        "vendor": "northwind-pay",
        "label": "Northwind Pay",
        "notes": "The oldest integration we have. Northwind speaks uppercase words and\n"
        "settles in two days, and its sandbox still returns the pre-2019 vocabulary for\n"
        "refunded charges.",
        "style": "exact",
        "fee": "floor",
        "settle": 2,
        "fee_bps": 190,
        "window": 120,
        "extra": ["MIN_FEE_CENTS = 25", 'CURRENCIES = ("USD", "CAD")'],
        "map": [
            ("AUTHORIZED", "authorized"),
            ("CAPTURED", "captured"),
            ("SETTLED", "captured"),
            ("PENDING", "pending"),
            ("DECLINED", "failed"),
            ("REFUNDED", "refunded"),
            ("VOIDED", "voided"),
        ],
        "norm_note": "Northwind sends the status verbatim, padded with spaces by its\n"
        "XML-era client, so stripping is the whole job.",
    },
    {
        "module": "payglide",
        "vendor": "payglide",
        "label": "PayGlide",
        "notes": "A checkout-embedded PSP. PayGlide lowercases everything, including\n"
        "the status field, and never sends a trailing newline.",
        "style": "lower",
        "fee": "plain",
        "settle": 1,
        "fee_bps": 240,
        "window": 90,
        "extra": ['CURRENCIES = ("USD", "EUR", "GBP")'],
        "map": [
            ("completed", "captured"),
            ("authorized", "authorized"),
            ("pending", "pending"),
            ("failed", "failed"),
            ("refunded", "refunded"),
            ("cancelled", "voided"),
        ],
        "norm_note": "Lowercasing before the lookup keeps the 2024 and 2025 payloads\n"
        "on one path; only the casing changed between them.",
    },
    {
        "module": "meridian_psp",
        "vendor": "meridian-psp",
        "label": "Meridian PSP",
        "notes": "Meridian is a UK acquirer, so its statuses are spelled the British\n"
        "way. The sandbox has been in title case since it launched.",
        "style": "exact",
        "fee": "ceil",
        "settle": 3,
        "fee_bps": 155,
        "window": 60,
        "extra": ['CURRENCIES = ("GBP", "EUR")'],
        "map": [
            ("Authorised", "authorized"),
            ("Settled", "captured"),
            ("Refused", "failed"),
            ("Chargeback", "disputed"),
            ("Pending", "pending"),
        ],
        "norm_note": "Meridian refuses to lowercase anything, and `Refused` is its\n"
        "word for what everyone else calls declined.",
    },
    {
        "module": "calder_pay",
        "vendor": "calder-pay",
        "label": "Calder Pay",
        "notes": "Calder answers with a single digit and a separate message field. It\n"
        "is the only gateway we hold that cannot refund through the API; support does\n"
        "those by hand in the portal.",
        "style": "code",
        "fee": "cap",
        "settle": 1,
        "fee_bps": 290,
        "window": 0,
        "extra": ["MAX_FEE_CENTS = 900", "MSG_CODES = {\"0\": \"approved\", \"2\": \"review\"}"],
        "map": [
            ("0", "captured"),
            ("1", "failed"),
            ("2", "pending"),
            ("3", "failed"),
            ("4", "refunded"),
        ],
        "norm_note": "Calder sends its status as a string that may or may not carry\n"
        "leading zeroes, so the code is normalised through int() first.",
    },
    {
        "module": "harborpay",
        "vendor": "harborpay",
        "label": "HarborPay",
        "notes": "HarborPay publishes webhooks with a namespace prefix on every event\n"
        "type, which leaks into the status field of its REST responses too.",
        "style": "prefix",
        "fee": "flat",
        "settle": 2,
        "fee_bps": 210,
        "window": 180,
        "extra": ["FLAT_FEE_CENTS = 15"],
        "map": [
            ("succeeded", "captured"),
            ("authorized", "authorized"),
            ("failed", "failed"),
            ("refunded", "refunded"),
            ("pending", "pending"),
            ("reversed", "voided"),
        ],
        "norm_note": "One of three namespaces may prefix the status, and only ever\n"
        "one, so the first matching prefix is stripped and the loop stops.",
    },
    {
        "module": "lumen_gateway",
        "vendor": "lumen-gateway",
        "label": "Lumen Gateway",
        "notes": "Lumen models a charge as an object graph and reports the last leg of\n"
        "the path as the status. Refunds come back as a child charge.",
        "style": "tail",
        "fee": "plain",
        "settle": 2,
        "fee_bps": 175,
        "window": 90,
        "extra": ['CURRENCIES = ("USD", "AUD")'],
        "map": [
            ("captured", "captured"),
            ("declined", "failed"),
            ("pending", "pending"),
            ("refunded", "refunded"),
            ("voided", "voided"),
        ],
        "norm_note": "`charge.captured` and `captured` both arrive depending on the\n"
        "endpoint, so the tail is tried before the whole string.",
    },
    {
        "module": "kestrel_pay",
        "vendor": "kestrel-pay",
        "label": "Kestrel Pay",
        "notes": "Kestrel appends a reason to the status with a colon, which is handy\n"
        "in the portal and useless in our state machine.",
        "style": "colon",
        "fee": "ceil",
        "settle": 1,
        "fee_bps": 265,
        "window": 45,
        "extra": [],
        "map": [
            ("captured", "captured"),
            ("declined", "failed"),
            ("pending", "pending"),
            ("refunded", "refunded"),
            ("authorized", "authorized"),
        ],
        "norm_note": "Everything after the first colon is the human-readable reason,\n"
        "so only the head is looked up.",
    },
    {
        "module": "pinegrove_psp",
        "vendor": "pinegrove-psp",
        "label": "Pinegrove PSP",
        "notes": "Pinegrove still runs the 2016 feed for merchants who never migrated.\n"
        "Statuses are uppercase with a hyphen separating an action from its outcome.",
        "style": "underscore",
        "fee": "floor",
        "settle": 3,
        "fee_bps": 135,
        "window": 30,
        "extra": ["MIN_FEE_CENTS = 10"],
        "map": [
            ("auth_ok", "authorized"),
            ("auth_declined", "failed"),
            ("captured_full", "captured"),
            ("refund_partial", "refunded"),
            ("refund_full", "refunded"),
            ("void_ok", "voided"),
        ],
        "norm_note": "The legacy feed hyphenates and uppercases; folding hyphens to\n"
        "underscores gives one lookup table for both feeds.",
    },
    {
        "module": "atlas_payments",
        "vendor": "atlas-payments",
        "label": "Atlas Payments",
        "notes": "Atlas pads its status field to a fixed width for a fixed-width report\n"
        "and never updated the REST path when it grew one.",
        "style": "squash",
        "fee": "plain",
        "settle": 2,
        "fee_bps": 200,
        "window": 60,
        "extra": ['CURRENCIES = ("USD", "MXN")'],
        "map": [
            ("CAPTURED", "captured"),
            ("DECLINED", "failed"),
            ("AUTHORIZED", "authorized"),
            ("REFUNDED", "refunded"),
            ("PENDINGREVIEW", "pending"),
        ],
        "norm_note": "`PENDING REVIEW` and `PENDINGREVIEW` both occur, so all\n"
        "internal whitespace is dropped before the lookup.",
    },
    {
        "module": "brightline_pay",
        "vendor": "brightline-pay",
        "label": "Brightline Pay",
        "notes": "Brightline ships a free-text status written by its own support staff.\n"
        "There is no enumeration to map against, so we match on words.",
        "style": "tokens",
        "fee": "cap",
        "settle": 1,
        "fee_bps": 300,
        "window": 90,
        "extra": [
            'MAX_FEE_CENTS = 750',
            'CAPTURED_WORDS = ("settle", "captur", "paid", "succeed")',
            'FAILED_WORDS = ("declin", "fail", "reject", "insufficient")',
        ],
        "map": [("refunded", "refunded"), ("pending", "pending")],
        "norm_note": "Failure is checked after success because `settled then\n"
        "reversed` must read as captured, not failed.",
    },
    {
        "module": "cobalt_psp",
        "vendor": "cobalt-psp",
        "label": "Cobalt PSP",
        "notes": "Cobalt embeds the status in a nested object that the webhook and the\n"
        "polling endpoint shape differently. Both end up here.",
        "style": "payload",
        "fee": "ceil",
        "settle": 2,
        "fee_bps": 225,
        "window": 120,
        "extra": [],
        "map": [
            ("CAPTURED", "captured"),
            ("DECLINED", "failed"),
            ("AUTHORIZED", "authorized"),
            ("REFUNDED", "refunded"),
            ("VOIDED", "voided"),
        ],
        "norm_note": "A dict is unwrapped before the lookup, so a caller may hand us\n"
        "either the whole event or just the status string.",
    },
    {
        "module": "dunmore_pay",
        "vendor": "dunmore-pay",
        "label": "Dunmore Pay",
        "notes": "Dunmore names its statuses as full sentences that all begin with the\n"
        "same word, so a prefix table is more honest than an exact map.",
        "style": "longest",
        "fee": "flat",
        "settle": 4,
        "fee_bps": 145,
        "window": 30,
        "extra": [
            "FLAT_FEE_CENTS = 30",
            "PREFIXES = (",
            '    ("payment captured", "captured"),',
            '    ("payment declined", "failed"),',
            '    ("payment refunded", "refunded"),',
            '    ("payment authorised", "authorized"),',
            '    ("payment pending", "pending"),',
            ")",
        ],
        "map": [],
        "norm_note": "The table is ordered longest-first, so `payment captured`\n"
        "wins over any shorter prefix that happens to share its opening.",
    },
    {
        "module": "eastgate_psp",
        "vendor": "eastgate-psp",
        "label": "Eastgate PSP",
        "notes": "Eastgate renamed every status in 2022 and kept the old names in the\n"
        "response for a year. Both spellings still show up in historical replays. It\n"
        "does not allow API refunds at all.",
        "style": "twotier",
        "fee": "floor",
        "settle": 3,
        "fee_bps": 165,
        "window": 0,
        "extra": ["MIN_FEE_CENTS = 20"],
        "map": [
            ("approved", "captured"),
            ("rejected", "failed"),
            ("reversed", "voided"),
            ("waiting", "pending"),
        ],
        "norm_note": "The current vocabulary is tried first; anything left over is\n"
        "looked up in the pre-2022 table.",
        "legacy": {"ok": "captured", "bad": "failed", "hold": "pending"},
    },
    {
        "module": "fernhill_pay",
        "vendor": "fernhill-pay",
        "label": "Fernhill Pay",
        "notes": "Fernhill runs its own spelling for two statuses and aliases them at\n"
        "the edge. The alias table is the only place it happens.",
        "style": "alias",
        "fee": "plain",
        "settle": 1,
        "fee_bps": 250,
        "window": 90,
        "extra": [
            'ALIASES = {"authorised": "authorized", "canceled": "voided", "capture_pending": "pending"}',
            'CANONICAL = ("authorized", "captured", "failed", "pending", "refunded", "voided", "disputed", "unknown")',
        ],
        "map": [
            ("succeeded", "captured"),
            ("authorised", "authorized"),
            ("canceled", "voided"),
            ("capture_pending", "pending"),
            ("declined", "failed"),
            ("refunded", "refunded"),
        ],
        "norm_note": "Aliases are folded after the primary lookup so the map itself\n"
        "can keep the spellings Fernhill actually sends.",
    },
]


def render_gateway(spec):
    """One gateway module, from its status table and chosen normaliser."""
    map_lines = ["    %r: %r," % (key, value) for key, value in spec["map"]]
    extra = list(spec["extra"])
    if spec.get("legacy"):
        body = ", ".join("%r: %r" % (k, v) for k, v in spec["legacy"].items())
        extra.append("LEGACY_MAP = {%s}" % body)
    refund = spec.get("refund") or "return REFUND_WINDOW_DAYS > 0"
    render(
        "shop/payments/gateways/%s.py" % spec["module"],
        '''
        """@@LABEL@@ (vendor id ``@@VENDOR@@``).

        @@NOTES@@
        """

        VENDOR = "@@VENDOR@@"
        SETTLEMENT_DAYS = @@SETTLE@@
        FEE_BPS = @@FEE_BPS@@
        REFUND_WINDOW_DAYS = @@WINDOW@@
        @@EXTRA@@

        STATUS_MAP = {
        @@MAP@@
        }


        def normalize(status: str) -> str:
            """The canonical status for whatever @@LABEL@@ sent us.

            @@NORM_NOTE@@
            """
        @@BODY@@


        def supports_refund() -> bool:
            """Whether @@LABEL@@ lets us refund through the API."""
        @@REFUND@@


        def fee_cents(amount_cents: int) -> int:
            """Our cost for capturing ``amount_cents`` through @@LABEL@@."""
        @@FEE@@
        ''',
        LABEL=spec["label"],
        VENDOR=spec["vendor"],
        NOTES=spec["notes"],
        SETTLE=spec["settle"],
        FEE_BPS=spec["fee_bps"],
        WINDOW=spec["window"],
        EXTRA="\n".join(extra),
        MAP="\n".join(map_lines) if map_lines else "    # no exact table: the matcher below owns the vocabulary",
        NORM_NOTE="\n    ".join(textwrap.dedent(spec["norm_note"]).strip().split("\n")),
        BODY=block(NORMALIZERS[spec["style"]]),
        REFUND=block(refund),
        FEE=block(FEE_BODIES[spec["fee"]]),
    )


def render_payments():
    for spec in GATEWAYS:
        render_gateway(spec)
    render(
        "shop/payments/gateways/__init__.py",
        '''
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
        ''',
    )
    render(
        "shop/payments/retries.py",
        '''
        """Retry policy for charge attempts.

        The schedule is exponential with a cap rather than a fixed table, because the
        gateway budget is shared: five attempts at most, then the payment parks in
        `failed` and the customer sees the decline.
        """

        BASE_DELAY_MS = 250
        MAX_DELAY_MS = 8000
        MAX_ATTEMPTS = 5

        RETRYABLE = ("timeout", "rate_limited", "gateway_error", "connection_reset")
        FINAL = ("declined", "invalid_card", "insufficient_funds", "fraud")


        def should_retry(status: str, attempt: int) -> bool:
            """Whether a payment in ``status`` gets another attempt."""
            if attempt >= MAX_ATTEMPTS:
                return False
            if attempt < 1:
                return False
            return str(status or "").strip().lower() in RETRYABLE


        def next_delay(attempt: int) -> int:
            """Milliseconds to wait before attempt ``attempt + 1``, capped."""
            if attempt < 1:
                return 0
            return min(BASE_DELAY_MS * (2 ** (attempt - 1)), MAX_DELAY_MS)


        def plan(status: str, attempt: int) -> dict:
            """What the caller should do next, as a dict the loop can log."""
            retry = should_retry(status, attempt)
            return {
                "retry": retry,
                "attempt": attempt,
                "delay_ms": next_delay(attempt) if retry else 0,
                "final": str(status or "").strip().lower() in FINAL,
                "reason": "retryable" if retry else "stop",
            }


        def schedule(attempts: int) -> list:
            """The whole delay ladder, for tests and for the ops runbook."""
            return [next_delay(n) for n in range(1, max(int(attempts), 0) + 1)]
        ''',
    )
    render(
        "shop/payments/ledger.py",
        '''
        """A double-entry payment ledger.

        Every movement is posted once, with a debit account and a credit account, and
        balances are derived rather than stored. The class deliberately has no
        persistence: the server wraps it around the payments table.
        """

        from shop.errors import ValidationError

        ACCOUNTS = ("cash", "gateway_clearing", "revenue", "fees", "refunds", "chargebacks")


        class Ledger:
            """An append-only list of postings between known accounts."""

            def __init__(self, accounts=ACCOUNTS):
                self.accounts = tuple(accounts)
                self._entries = []

            def post(self, debit: str, credit: str, cents: int, reference: str = "") -> dict:
                """Move ``cents`` from ``debit`` to ``credit`` and return the entry."""
                for account in (debit, credit):
                    if account not in self.accounts:
                        raise ValidationError("unknown account %r" % (account,))
                if debit == credit:
                    raise ValidationError("a posting needs two different accounts")
                amount = int(cents)
                if amount <= 0:
                    raise ValidationError("a posting is a positive number of cents")
                entry = {"seq": len(self._entries) + 1, "debit": debit, "credit": credit, "cents": amount, "reference": reference}
                self._entries.append(entry)
                return dict(entry)

            def entries(self, account: str = None) -> list:
                """Every posting, or only those touching ``account``."""
                if account is None:
                    return [dict(entry) for entry in self._entries]
                return [dict(entry) for entry in self._entries if account in (entry["debit"], entry["credit"])]

            def balance(self, account: str) -> int:
                """Net cents held by ``account``: credits in, debits out."""
                if account not in self.accounts:
                    raise ValidationError("unknown account %r" % (account,))
                total = 0
                for entry in self._entries:
                    if entry["credit"] == account:
                        total += entry["cents"]
                    if entry["debit"] == account:
                        total -= entry["cents"]
                return total

            def is_balanced(self) -> bool:
                """True when every account nets to zero across the whole book."""
                return all(self.balance(account) == 0 for account in self.accounts)

            def total_posted(self) -> int:
                """Gross cents moved, which is what the finance export counts."""
                return sum(entry["cents"] for entry in self._entries)
        ''',
    )
    render(
        "shop/payments/reconciliation.py",
        '''
        """Match the gateway settlement file against our own ledger.

        The two sides are keyed by the gateway reference, because that is the only
        field both systems agree on. A row present on one side only is a finding, not
        an error: the runbook says who owns each bucket.
        """


        def index_by_reference(rows: list) -> dict:
            """Reference -> row. A duplicate reference keeps the last row seen."""
            index = {}
            for row in rows or []:
                reference = str(row.get("reference") or "")
                if reference:
                    index[reference] = row
            return index


        def reconcile(entries: list, rows: list) -> dict:
            """Diff our entries against a settlement file.

            Returns four buckets plus a one-line summary the runbook quotes.
            """
            ours = index_by_reference(entries)
            theirs = index_by_reference(rows)
            matched, mismatch = [], []
            for reference in sorted(set(ours) & set(theirs)):
                left = int(ours[reference].get("cents") or 0)
                right = int(theirs[reference].get("cents") or 0)
                if left == right:
                    matched.append(reference)
                else:
                    mismatch.append({"reference": reference, "ours": left, "theirs": right})
            return {
                "matched": matched,
                "amount_mismatch": mismatch,
                "missing_in_gateway": sorted(set(ours) - set(theirs)),
                "missing_in_ledger": sorted(set(theirs) - set(ours)),
                "summary": "%d matched, %d mismatched, %d one-sided"
                % (len(matched), len(mismatch), len(set(ours) ^ set(theirs))),
            }


        def needs_attention(report: dict) -> bool:
            """True when a human has to look at the settlement run."""
            return bool(report.get("amount_mismatch") or report.get("missing_in_gateway") or report.get("missing_in_ledger"))
        ''',
    )
    render(
        "shop/payments/webhooks.py",
        '''
        """Inbound gateway webhooks: signing, verification and dispatch.

        Gateways sign the raw body, so the body is hashed before it is parsed. The
        timestamp is checked against a five minute window; a replayed old signature is
        rejected even though it verifies.
        """

        import hashlib
        import hmac
        import time

        from shop.errors import ValidationError

        MAX_SKEW_SECONDS = 300
        HANDLERS = {
            "payment.captured": "capture",
            "payment.refunded": "refund",
            "payment.failed": "fail",
            "chargeback.opened": "dispute",
        }


        def signature(secret: str, body: bytes, timestamp: int) -> str:
            """The hex signature a gateway with this secret would send."""
            message = str(int(timestamp)).encode("utf-8") + b"." + body
            return hmac.new(secret.encode("utf-8"), message, hashlib.sha256).hexdigest()


        def verify(secret: str, body: bytes, timestamp, received: str, now: int = None) -> bool:
            """True when the signature matches and the timestamp is fresh."""
            try:
                moment = int(timestamp)
            except (TypeError, ValueError):
                return False
            clock = int(time.time()) if now is None else int(now)
            if abs(clock - moment) > MAX_SKEW_SECONDS:
                return False
            expected = signature(secret, body, moment)
            return hmac.compare_digest(expected, str(received or ""))


        def dispatch(event_type: str, payload: dict) -> dict:
            """Route a verified event to the handler name the queue understands."""
            handler = HANDLERS.get(str(event_type or ""))
            if handler is None:
                raise ValidationError("no handler for event %r" % (event_type,))
            reference = str((payload or {}).get("reference") or "")
            if not reference:
                raise ValidationError("event %r carries no reference" % (event_type,))
            return {"handler": handler, "reference": reference, "event": event_type}


        def handled_events() -> list:
            """Event types we accept, sorted, for the gateway integration docs."""
            return sorted(HANDLERS)
        ''',
    )
    render(
        "shop/payments/__init__.py",
        '''
        """Payments: gateway adapters, retry policy, the ledger, settlement, webhooks."""

        from shop.payments import gateways, ledger, reconciliation, retries, webhooks

        __all__ = ["gateways", "ledger", "reconciliation", "retries", "webhooks"]
        ''',
    )


# --------------------------------------------------------------------------
# shop/shipping -- 12 carrier rate cards, quote comparison, tracking.
# --------------------------------------------------------------------------

CARRIERS = [
    {
        "slug": "northpost",
        "carrier": "northpost",
        "doc": "northpost rate card.\n"
        "\n"
        "Northpost bills a zone base rate, then one rate per started kilogram, then\n"
        "a flat handling fee. It ran the same-day pilot, so its numbers are the\n"
        "ones finance reconciles against most often.",
        "quote_doc": "Price one parcel against the four zones northpost publishes.",
        "track_doc": "Map a northpost tracking document onto the house vocabulary.",
        "consts": [
            'ZONE_RATES = {"local": 450, "regional": 620, "national": 780, "remote": 1150}',
            'PER_KG_CENTS = {"local": 120, "regional": 145, "national": 170, "remote": 260}',
            "HANDLING_CENTS = 75",
            'STATUS_MAP = {"LABEL": "label_created", "PICKED_UP": "in_transit", "IN_TRANSIT": "in_transit", "OUT_FOR_DELIVERY": "out_for_delivery", "DELIVERED": "delivered", "EXCEPTION": "exception", "RETURNED": "returned"}',
        ],
        "quote": '''
        if zone not in ZONE_RATES:
            raise ValidationError("northpost does not serve zone %r" % (zone,))
        if int(weight_g) <= 0:
            raise ValidationError("weight_g must be positive")
        kilos = int(weight_g) // 1000
        cents = ZONE_RATES[zone] + kilos * PER_KG_CENTS[zone] + HANDLING_CENTS
        return {"carrier": CARRIER, "zone": zone, "amount_cents": cents, "billable_g": int(weight_g)}
        ''',
        "track": '''
        record = dict(payload or {})
        code = str(record.get("status") or "").strip().upper()
        events = [
            {
                "at": step.get("timestamp"),
                "status": STATUS_MAP.get(str(step.get("status") or "").upper(), "unknown"),
            }
            for step in record.get("history") or []
        ]
        return {
            "carrier": CARRIER,
            "tracking_number": str(record.get("tracking_number") or ""),
            "status": STATUS_MAP.get(code, "unknown"),
            "events": events,
        }
        ''',
        "payload": {
            "tracking_number": "NP-884120",
            "status": "IN_TRANSIT",
            "history": [{"timestamp": "2026-02-12T08:10:00Z", "status": "PICKED_UP"}],
        },
    },
    {
        "slug": "swiftlink",
        "carrier": "swiftlink",
        "doc": "swiftlink rate card.\n"
        "\n"
        "Swiftlink sells parcels in half-kilogram blocks. A parcel is charged for\n"
        "the blocks it occupies, rounded up, and anything over ten kilograms is\n"
        "handed to the express network with a different rate band.",
        "quote_doc": "Price one parcel by rounding its weight up to 500 g blocks.",
        "track_doc": "Read a swiftlink shipment document, which prefixes every state.",
        "consts": [
            'ZONE_RATES = {"metro": 520, "regional": 700, "national": 910}',
            'BLOCK_CENTS = {"metro": 95, "regional": 125, "national": 160}',
            "EXPRESS_BLOCKS = 20",
            'STATUS_MAP = {"label_created": "label_created", "in_transit": "in_transit", "out_for_delivery": "out_for_delivery", "delivered": "delivered", "failed": "exception", "returned": "returned"}',
        ],
        "quote": '''
        if zone not in ZONE_RATES:
            raise ValidationError("swiftlink zone %r is not on the rate card" % (zone,))
        blocks = max(1, -(-int(weight_g) // 500))
        cents = ZONE_RATES[zone] + blocks * BLOCK_CENTS[zone]
        return {
            "carrier": CARRIER,
            "zone": zone,
            "amount_cents": cents,
            "blocks": blocks,
            "service": "express" if blocks > EXPRESS_BLOCKS else "standard",
        }
        ''',
        "track": '''
        record = dict(payload or {})
        state = str(record.get("state") or "").strip().lower()
        if state.startswith("shipment."):
            state = state[len("shipment."):]
        events = []
        for step in record.get("checkpoints") or []:
            name = str(step.get("state") or "").strip().lower()
            events.append({"at": step.get("when"), "status": STATUS_MAP.get(name, "unknown")})
        return {
            "carrier": CARRIER,
            "tracking_number": str(record.get("id") or ""),
            "status": STATUS_MAP.get(state, "unknown"),
            "events": events,
        }
        ''',
        "payload": {
            "id": "SW-5510983",
            "state": "shipment.out_for_delivery",
            "checkpoints": [{"when": "2026-02-12T06:45:00Z", "state": "in_transit"}],
        },
    },
    {
        "slug": "harbor_freight",
        "carrier": "harbor-freight",
        "doc": "harbor-freight rate card.\n"
        "\n"
        "Harbor Freight moves pallets and refuses anything under two kilograms.\n"
        "Freight is priced per kilogram and then marked up by a fuel surcharge\n"
        "that the carrier republishes every quarter.",
        "quote_doc": "Price a parcel, applying the published fuel surcharge.",
        "track_doc": "Read a harbor-freight consignment, whose ids nest under reference.",
        "consts": [
            'ZONE_RATES = {"depot": 380, "regional": 660, "national": 940, "offshore": 1520}',
            "PER_KG_CENTS = 210",
            "FUEL_PERMILLE = 65",
            "MIN_WEIGHT_G = 2000",
            'STATUS_MAP = {"booked": "label_created", "loaded": "in_transit", "departed": "in_transit", "arrived": "out_for_delivery", "signed": "delivered", "damaged": "exception"}',
        ],
        "quote": '''
        if int(weight_g) < MIN_WEIGHT_G:
            raise ValidationError("harbor-freight does not carry parcels under %d g" % MIN_WEIGHT_G)
        base = ZONE_RATES.get(zone)
        if base is None:
            raise ValidationError("harbor-freight does not run to zone %r" % (zone,))
        freight = int(weight_g) * PER_KG_CENTS // 1000
        fuel = freight * FUEL_PERMILLE // 1000
        return {
            "carrier": CARRIER,
            "zone": zone,
            "amount_cents": base + freight + fuel,
            "freight_cents": freight,
            "fuel_cents": fuel,
        }
        ''',
        "track": '''
        record = dict(payload or {})
        reference = record.get("reference") or {}
        code = str(record.get("current_state") or "").strip().lower()
        events = [
            {"at": step.get("at"), "status": STATUS_MAP.get(str(step.get("state") or "").lower(), "unknown")}
            for step in record.get("events") or []
        ]
        return {
            "carrier": CARRIER,
            "tracking_number": str(reference.get("number") or ""),
            "status": STATUS_MAP.get(code, "unknown"),
            "events": events,
        }
        ''',
        "payload": {
            "reference": {"number": "HF-2291470"},
            "current_state": "arrived",
            "events": [{"at": "2026-02-12T09:20:00Z", "state": "departed"}],
        },
    },
    {
        "slug": "peak_parcel",
        "carrier": "peak-parcel",
        "doc": "peak-parcel rate card.\n"
        "\n"
        "Peak Parcel prices by weight band rather than by kilogram: a parcel pays\n"
        "its band fee on top of the zone rate. The band list is short and fixed\n"
        "because the carrier sells five box sizes and nothing else.",
        "quote_doc": "Price a parcel by looking up its weight band.",
        "track_doc": "Read a peak-parcel document, which lists milestones rather than states.",
        "consts": [
            'ZONE_RATES = {"city": 410, "suburban": 560, "national": 830}',
            'BANDS = ((1000, "light"), (5000, "standard"), (15000, "heavy"))',
            'BAND_CENTS = {"light": 180, "standard": 460, "heavy": 980, "freight": 2100}',
            'STATUS_MAP = {"picked": "in_transit", "moved": "in_transit", "arrived_at_hub": "in_transit", "on_van": "out_for_delivery", "delivered": "delivered", "lost": "exception"}',
        ],
        "helpers": '''
        def _band(weight_g: int) -> str:
            for limit, name in BANDS:
                if weight_g <= limit:
                    return name
            return "freight"
        ''',
        "quote": '''
        if zone not in ZONE_RATES:
            raise ValidationError("peak-parcel zone %r is unknown" % (zone,))
        band = _band(int(weight_g))
        return {
            "carrier": CARRIER,
            "zone": zone,
            "amount_cents": ZONE_RATES[zone] + BAND_CENTS[band],
            "band": band,
        }
        ''',
        "track": '''
        record = dict(payload or {})
        tracking = record.get("tracking") or {}
        events = [
            {"at": step.get("at"), "status": STATUS_MAP.get(str(step.get("label") or "").lower(), "unknown")}
            for step in record.get("milestones") or []
        ]
        last = events[-1]["status"] if events else "unknown"
        return {
            "carrier": CARRIER,
            "tracking_number": str(tracking.get("code") or ""),
            "status": last,
            "events": events,
        }
        ''',
        "payload": {
            "tracking": {"code": "PP-338204"},
            "milestones": [
                {"at": "2026-02-12T07:00:00Z", "label": "picked"},
                {"at": "2026-02-12T11:30:00Z", "label": "on_van"},
            ],
        },
    },
    {
        "slug": "riverdale_run",
        "carrier": "riverdale-run",
        "doc": "riverdale-run rate card.\n"
        "\n"
        "Riverdale Run is the regional co-op. Its card is a base rate per zone\n"
        "multiplied by a zone factor and by the number of whole kilograms, which\n"
        "keeps the rural routes from losing money on every parcel.",
        "quote_doc": "Price a parcel with the co-op zone multiplier.",
        "track_doc": "Read a riverdale-run scan feed, which calls stages stages.",
        "consts": [
            'ZONE_RATES = {"core": 480, "belt": 640, "rural": 990}',
            'ZONE_FACTOR = {"core": 1000, "belt": 1180, "rural": 1650}',
            "MIN_KILOS = 1",
            'STATUS_MAP = {"created": "label_created", "collected": "in_transit", "scan": "in_transit", "delivery": "out_for_delivery", "complete": "delivered", "refused": "returned"}',
        ],
        "quote": '''
        base = ZONE_RATES.get(zone)
        if base is None:
            raise ValidationError("riverdale-run does not cover zone %r" % (zone,))
        kilos = max(MIN_KILOS, int(weight_g) // 1000)
        cents = base * ZONE_FACTOR[zone] * kilos // 1000
        return {"carrier": CARRIER, "zone": zone, "amount_cents": cents, "kilos": kilos}
        ''',
        "track": '''
        record = dict(payload or {})
        stage = str(record.get("stage") or "").strip().lower()
        events = [
            {"at": scan.get("time"), "status": STATUS_MAP.get(str(scan.get("stage") or "").lower(), "unknown")}
            for scan in record.get("scans") or []
        ]
        return {
            "carrier": CARRIER,
            "tracking_number": str(record.get("barcode") or ""),
            "status": STATUS_MAP.get(stage, "unknown"),
            "events": events,
        }
        ''',
        "payload": {
            "barcode": "RR-77001",
            "stage": "delivery",
            "scans": [{"time": "2026-02-12T05:55:00Z", "stage": "scan"}],
        },
    },
    {
        "slug": "skyways",
        "carrier": "skyways",
        "doc": "skyways rate card.\n"
        "\n"
        "Skyways is the air option. Every parcel pays weight at the published\n"
        "per-kilogram rate, and two additions stack on top: a remote-address\n"
        "surcharge and a heavy-parcel surcharge.",
        "quote_doc": "Price an air parcel and list the surcharges that applied.",
        "track_doc": "Read a skyways air waybill, whose legs carry the flight state.",
        "consts": [
            'ZONE_RATES = {"domestic": 890, "islands": 1340, "continental": 1620}',
            "PER_KG_CENTS = 240",
            'REMOTE_ZONES = frozenset({"islands"})',
            "REMOTE_SURCHARGE_CENTS = 375",
            "HEAVY_G = 20000",
            "HEAVY_SURCHARGE_CENTS = 650",
            'STATUS_MAP = {"booked": "label_created", "manifested": "in_transit", "airborne": "in_transit", "landed": "out_for_delivery", "delivered": "delivered", "held": "exception"}',
        ],
        "quote": '''
        if zone not in ZONE_RATES:
            raise ValidationError("skyways does not fly to zone %r" % (zone,))
        cents = ZONE_RATES[zone] + int(weight_g) * PER_KG_CENTS // 1000
        applied = []
        if zone in REMOTE_ZONES:
            cents += REMOTE_SURCHARGE_CENTS
            applied.append("remote")
        if int(weight_g) > HEAVY_G:
            cents += HEAVY_SURCHARGE_CENTS
            applied.append("heavy")
        return {"carrier": CARRIER, "zone": zone, "amount_cents": cents, "surcharges": applied}
        ''',
        "track": '''
        record = dict(payload or {})
        code = str(record.get("flight_status") or "").strip().lower()
        events = [
            {"at": leg.get("time"), "status": STATUS_MAP.get(str(leg.get("status") or "").lower(), "unknown")}
            for leg in record.get("legs") or []
        ]
        return {
            "carrier": CARRIER,
            "tracking_number": str(record.get("awb") or ""),
            "status": STATUS_MAP.get(code, "unknown"),
            "events": events,
        }
        ''',
        "payload": {
            "awb": "012-44718293",
            "flight_status": "landed",
            "legs": [{"time": "2026-02-12T02:15:00Z", "status": "airborne"}],
        },
    },
    {
        "slug": "cartwright",
        "carrier": "cartwright",
        "doc": "cartwright rate card.\n"
        "\n"
        "Cartwright charges a flat fee for the size class a parcel falls into and\n"
        "ignores distance entirely, which is why the zone table exists only to\n"
        "reject addresses the carrier will not collect from.",
        "quote_doc": "Price a parcel from its weight class, not from its zone.",
        "track_doc": "Read a cartwright consignment, whose progress list is nested.",
        "consts": [
            'ZONE_RATES = {"anywhere": 0, "highlands": 0}',
            'TIERS = ((500, "letter"), (2000, "parcel"), (10000, "box"), (25000, "crate"))',
            'TIER_CENTS = {"letter": 190, "parcel": 540, "box": 1180, "crate": 2450, "freight": 3900}',
            'STATUS_MAP = {"received": "label_created", "in_network": "in_transit", "on_route": "out_for_delivery", "completed": "delivered", "returned_to_sender": "returned", "refused": "exception"}',
        ],
        "helpers": '''
        def _tier(weight_g: int) -> str:
            for limit, name in TIERS:
                if weight_g <= limit:
                    return name
            return "freight"
        ''',
        "quote": '''
        if zone not in ZONE_RATES:
            raise ValidationError("cartwright will not collect from zone %r" % (zone,))
        tier = _tier(int(weight_g))
        return {"carrier": CARRIER, "zone": zone, "amount_cents": TIER_CENTS[tier], "tier": tier}
        ''',
        "track": '''
        record = dict(payload or {})
        consignment = record.get("consignment") or {}
        steps = record.get("progress") or []
        events = [
            {"at": step.get("at"), "status": STATUS_MAP.get(str(step.get("state") or "").lower(), "unknown")}
            for step in steps
        ]
        status = events[-1]["status"] if events else "unknown"
        return {
            "carrier": CARRIER,
            "tracking_number": str(consignment.get("id") or ""),
            "status": status,
            "events": events,
        }
        ''',
        "payload": {
            "consignment": {"id": "CW-90114"},
            "summary": "in the network",
            "progress": [{"at": "2026-02-12T10:05:00Z", "state": "on_route"}],
        },
    },
    {
        "slug": "blue_crest",
        "carrier": "blue-crest",
        "doc": "blue-crest rate card.\n"
        "\n"
        "Blue Crest keeps a minimum charge per parcel: small, light shipments are\n"
        "lifted to the floor so the carrier never runs a parcel at a loss. The\n"
        "result says whether the floor was what the customer actually paid.",
        "quote_doc": "Price a parcel and report whether the minimum charge applied.",
        "track_doc": "Read a blue-crest parcel, whose history is the authoritative list.",
        "consts": [
            'ZONE_RATES = {"bay": 399, "valley": 585, "coast": 815, "inland": 1040}',
            'PER_KG_CENTS = {"bay": 110, "valley": 135, "coast": 175, "inland": 205}',
            "MIN_CHARGE_CENTS = 650",
            'STATUS_MAP = {"pre_transit": "label_created", "transit": "in_transit", "out_for_delivery": "out_for_delivery", "delivered": "delivered", "failure": "exception", "return_to_sender": "returned"}',
        ],
        "quote": '''
        if zone not in ZONE_RATES:
            raise ValidationError("blue-crest has no rate for zone %r" % (zone,))
        cents = ZONE_RATES[zone] + int(weight_g) * PER_KG_CENTS[zone] // 1000
        return {
            "carrier": CARRIER,
            "zone": zone,
            "amount_cents": max(cents, MIN_CHARGE_CENTS),
            "raw_cents": cents,
            "floor_applied": cents < MIN_CHARGE_CENTS,
        }
        ''',
        "track": '''
        record = dict(payload or {})
        history = record.get("status_history") or []
        events = [
            {"at": step.get("at"), "status": STATUS_MAP.get(str(step.get("status") or "").lower(), "unknown")}
            for step in history
        ]
        latest = str(record.get("latest") or "").strip().lower()
        status = STATUS_MAP.get(latest, "unknown")
        if status == "unknown" and events:
            status = events[-1]["status"]
        return {
            "carrier": CARRIER,
            "tracking_number": str(record.get("parcel_id") or ""),
            "status": status,
            "events": events,
        }
        ''',
        "payload": {
            "parcel_id": "BC-4412098",
            "latest": "out_for_delivery",
            "status_history": [{"at": "2026-02-12T04:40:00Z", "status": "transit"}],
        },
    },
    {
        "slug": "iron_bridge",
        "carrier": "iron-bridge",
        "doc": "iron-bridge rate card.\n"
        "\n"
        "Iron Bridge bills in round five-cent steps, because its invoicing system\n"
        "predates decimal currency and nobody has dared change the rounding. The\n"
        "raw figure is returned alongside the rounded one for reconciliation.",
        "quote_doc": "Price a parcel and round the result up to the next five cents.",
        "track_doc": "Read an iron-bridge shipment, whose tracking id is nested.",
        "consts": [
            'ZONE_RATES = {"yard": 425, "regional": 610, "national": 845, "territory": 1290}',
            'PER_KG_CENTS = {"yard": 105, "regional": 130, "national": 165, "territory": 250}',
            "ROUNDING_STEP = 5",
            'STATUS_MAP = {"created": "label_created", "accepted": "in_transit", "in_transit": "in_transit", "with_courier": "out_for_delivery", "delivered": "delivered", "exception": "exception"}',
        ],
        "quote": '''
        if zone not in ZONE_RATES:
            raise ValidationError("iron-bridge serves no zone named %r" % (zone,))
        raw = ZONE_RATES[zone] + int(weight_g) * PER_KG_CENTS[zone] // 1000
        rounded = -(-raw // ROUNDING_STEP) * ROUNDING_STEP
        return {"carrier": CARRIER, "zone": zone, "amount_cents": rounded, "raw_cents": raw}
        ''',
        "track": '''
        record = dict(payload or {})
        shipment = record.get("shipment") or {}
        state = str(record.get("state") or "").strip().lower()
        events = [
            {"at": entry.get("at"), "status": STATUS_MAP.get(str(entry.get("state") or "").lower(), "unknown")}
            for entry in record.get("audit") or []
        ]
        return {
            "carrier": CARRIER,
            "tracking_number": str(shipment.get("tracking_number") or ""),
            "status": STATUS_MAP.get(state, "unknown"),
            "events": events,
        }
        ''',
        "payload": {
            "shipment": {"tracking_number": "IB-6620471"},
            "state": "with_courier",
            "zone": "yard",
            "audit": [{"at": "2026-02-11T19:30:00Z", "state": "in_transit"}],
        },
    },
    {
        "slug": "meadow_mover",
        "carrier": "meadow-mover",
        "doc": "meadow-mover rate card.\n"
        "\n"
        "Meadow Mover is the rural carrier. It prices weight linearly and then\n"
        "adds one oversize fee for anything above thirty kilograms, since those\n"
        "parcels ride the flatbed rather than the van.",
        "quote_doc": "Price a parcel and flag oversize handling.",
        "track_doc": "Read a meadow-mover feed, which reports a current milestone.",
        "consts": [
            'ZONE_RATES = {"village": 395, "county": 520, "statewide": 760}',
            'PER_KG_CENTS = {"village": 100, "county": 118, "statewide": 152}',
            "OVERSIZE_G = 30000",
            "OVERSIZE_FEE_CENTS = 1200",
            'STATUS_MAP = {"booked": "label_created", "collected": "in_transit", "depot": "in_transit", "van": "out_for_delivery", "signed": "delivered", "damaged": "exception"}',
        ],
        "quote": '''
        if zone not in ZONE_RATES:
            raise ValidationError("meadow-mover does not reach zone %r" % (zone,))
        cents = ZONE_RATES[zone] + int(weight_g) * PER_KG_CENTS[zone] // 1000
        oversize = int(weight_g) > OVERSIZE_G
        if oversize:
            cents += OVERSIZE_FEE_CENTS
        return {"carrier": CARRIER, "zone": zone, "amount_cents": cents, "oversize": oversize}
        ''',
        "track": '''
        record = dict(payload or {})
        events = [
            {
                "at": step.get("at"),
                "status": STATUS_MAP.get(str(step.get("milestone") or "").lower(), "unknown"),
            }
            for step in record.get("events") or []
        ]
        current = str(record.get("currentMilestone") or "").strip().lower()
        return {
            "carrier": CARRIER,
            "tracking_number": str(record.get("trackingNumber") or ""),
            "status": STATUS_MAP.get(current, "unknown"),
            "events": events,
        }
        ''',
        "payload": {
            "trackingNumber": "MM-31228",
            "currentMilestone": "van",
            "events": [{"at": "2026-02-12T06:20:00Z", "milestone": "depot"}],
        },
    },
    {
        "slug": "granite_express",
        "carrier": "granite-express",
        "doc": "granite-express rate card.\n"
        "\n"
        "Granite Express charges by the hundred grams rather than the kilogram,\n"
        "which is expensive for documents and honest for small hardware parts.\n"
        "The unit count is returned so a dispute can be recomputed by hand.",
        "quote_doc": "Price a parcel in hundred-gram units.",
        "track_doc": "Read a granite-express record, whose status nests under detail.",
        "consts": [
            'ZONE_RATES = {"hub": 455, "district": 605, "national": 880}',
            'PER_100G_CENTS = {"hub": 12, "district": 15, "national": 19}',
            "UNIT_G = 100",
            'STATUS_MAP = {"registered": "label_created", "in_transit": "in_transit", "on_truck": "out_for_delivery", "delivered": "delivered", "exception": "exception", "returned": "returned"}',
        ],
        "quote": '''
        if zone not in ZONE_RATES:
            raise ValidationError("granite-express has no card for zone %r" % (zone,))
        units = max(1, -(-int(weight_g) // UNIT_G))
        cents = ZONE_RATES[zone] + units * PER_100G_CENTS[zone]
        return {"carrier": CARRIER, "zone": zone, "amount_cents": cents, "units": units}
        ''',
        "track": '''
        record = dict(payload or {})
        detail = record.get("detail") or {}
        code = str(detail.get("status") or "").strip().lower()
        events = [
            {"at": step.get("at"), "status": STATUS_MAP.get(str(step.get("status") or "").lower(), "unknown")}
            for step in record.get("history") or []
        ]
        return {
            "carrier": CARRIER,
            "tracking_number": str(record.get("id") or ""),
            "status": STATUS_MAP.get(code, "unknown"),
            "events": events,
        }
        ''',
        "payload": {
            "id": "GE-1002338",
            "detail": {"status": "on_truck"},
            "history": [{"at": "2026-02-12T03:10:00Z", "status": "in_transit"}],
        },
    },
    {
        "slug": "willow_post",
        "carrier": "willow-post",
        "doc": "willow-post rate card.\n"
        "\n"
        "Willow Post is the last of the residential-first carriers: addresses in\n"
        "its residential zones pay a small delivery premium, because the round\n"
        "ends at a front door rather than a loading dock.",
        "quote_doc": "Price a parcel, adding the residential premium where it applies.",
        "track_doc": "Read a willow-post document, which nests events under tracking.",
        "consts": [
            'ZONE_RATES = {"urban": 430, "suburban": 575, "rural": 825, "remote_rural": 1180}',
            'PER_KG_CENTS = {"urban": 115, "suburban": 140, "rural": 180, "remote_rural": 235}',
            'RESIDENTIAL_ZONES = frozenset({"suburban", "rural", "remote_rural"})',
            "RESIDENTIAL_CENTS = 165",
            'STATUS_MAP = {"label": "label_created", "moving": "in_transit", "final_mile": "out_for_delivery", "delivered": "delivered", "lost": "exception"}',
        ],
        "quote": '''
        if zone not in ZONE_RATES:
            raise ValidationError("willow-post does not deliver to zone %r" % (zone,))
        residential = zone in RESIDENTIAL_ZONES
        cents = ZONE_RATES[zone] + int(weight_g) * PER_KG_CENTS[zone] // 1000
        if residential:
            cents += RESIDENTIAL_CENTS
        return {
            "carrier": CARRIER,
            "zone": zone,
            "amount_cents": cents,
            "residential": residential,
        }
        ''',
        "track": '''
        record = dict(payload or {})
        tracking = record.get("tracking") or {}
        events = [
            {"at": step.get("ts"), "status": STATUS_MAP.get(str(step.get("code") or "").lower(), "unknown")}
            for step in tracking.get("events") or []
        ]
        status = STATUS_MAP.get(str(record.get("status") or "").strip().lower(), "unknown")
        return {
            "carrier": CARRIER,
            "tracking_number": str(tracking.get("number") or ""),
            "status": status,
            "events": events,
        }
        ''',
        "payload": {
            "status": "final_mile",
            "tracking": {
                "number": "WP-55809",
                "events": [{"ts": "2026-02-12T08:00:00Z", "code": "moving"}],
            },
        },
    },
]


def render_carrier(spec):
    """One carrier adapter: constants, a quote, and a tracking normaliser."""
    out = ['"""' + spec["doc"] + '"""', "", "from shop.errors import ValidationError", ""]
    out.append('CARRIER = "%s"' % spec["carrier"])
    out.extend(spec["consts"])
    if spec.get("helpers"):
        out.extend(["", "", block(spec["helpers"], indent=0)])
    out.extend(
        [
            "",
            "",
            "def quote(weight_g: int, zone: str) -> dict:",
            block('"""' + spec["quote_doc"] + '"""', indent=4),
            block(spec["quote"]),
            "",
            "",
            "def parse_tracking(payload: dict) -> dict:",
            block('"""' + spec["track_doc"] + '"""', indent=4),
            block(spec["track"]),
        ]
    )
    emit("shop/shipping/carriers/%s.py" % spec["slug"], "\n".join(out) + "\n")


def render_shipping():
    """Carrier adapters plus the comparison, tracking and package modules."""
    for spec in CARRIERS:
        render_carrier(spec)
    render(
        "shop/shipping/carriers/__init__.py",
        '''
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
        ''',
    )
    render(
        "shop/shipping/rates.py",
        '''
        """Rate comparison across every carrier adapter."""

        from shop.errors import ValidationError
        from shop.shipping.carriers import all_carriers

        __all__ = ["cheapest", "compare", "quotes_for"]


        def quotes_for(weight_g: int, zone: str) -> list:
            """Ask every carrier for a price; refusals are dropped, not raised."""
            if int(weight_g) <= 0:
                raise ValidationError("weight_g must be positive")
            quotes = []
            for module in all_carriers():
                try:
                    quote = module.quote(int(weight_g), str(zone))
                except ValidationError:
                    continue
                quotes.append(dict(quote))
            quotes.sort(key=lambda row: (row["amount_cents"], row["carrier"]))
            return quotes


        def cheapest(weight_g: int, zone: str) -> dict:
            """The winning quote, or an empty dict when no carrier serves the zone."""
            quotes = quotes_for(weight_g, zone)
            return quotes[0] if quotes else {}


        def compare(weight_g: int, zone: str) -> dict:
            """A comparison table for the checkout page's shipping step."""
            quotes = quotes_for(weight_g, zone)
            best = quotes[0]["amount_cents"] if quotes else 0
            rows = []
            for quote in quotes:
                row = dict(quote)
                row["delta_cents"] = quote["amount_cents"] - best
                rows.append(row)
            return {
                "zone": str(zone),
                "weight_g": int(weight_g),
                "quotes": rows,
                "cheapest_cents": best,
                "offered": bool(rows),
            }
        ''',
    )
    render(
        "shop/shipping/tracking.py",
        '''
        """The tracking vocabulary every carrier adapter normalises into."""

        from shop.errors import ValidationError

        __all__ = ["CANONICAL_STATUSES", "TERMINAL_STATUSES", "latest", "normalise", "timeline"]

        CANONICAL_STATUSES = (
            "label_created",
            "in_transit",
            "out_for_delivery",
            "delivered",
            "exception",
            "returned",
        )

        TERMINAL_STATUSES = frozenset({"delivered", "returned"})

        _ALIASES = {
            "in-transit": "in_transit",
            "intransit": "in_transit",
            "out-for-delivery": "out_for_delivery",
            "pre_transit": "label_created",
            "pending": "label_created",
            "failed": "exception",
            "failure": "exception",
        }


        def normalise(status: str) -> str:
            """Fold a carrier spelling onto the canonical status, or ``unknown``."""
            key = str(status or "").strip().lower()
            key = _ALIASES.get(key, key)
            return key if key in CANONICAL_STATUSES else "unknown"


        def timeline(events: list) -> list:
            """Sort tracking events by timestamp, dropping entries without one."""
            kept = [dict(event) for event in events or [] if event.get("at")]
            kept.sort(key=lambda event: str(event["at"]))
            for event in kept:
                event["status"] = normalise(event.get("status"))
            return kept


        def latest(events: list) -> str:
            """The last known status in a timeline, or ``unknown`` when empty."""
            ordered = timeline(events)
            if not ordered:
                return "unknown"
            return ordered[-1]["status"]
        ''',
    )
    render(
        "shop/shipping/__init__.py",
        '''
        """Shipping: carrier rate cards, price comparison and tracking."""

        from shop.shipping import carriers, rates, tracking

        __all__ = ["carriers", "rates", "tracking"]
        ''',
    )
    payloads = {spec["carrier"]: spec["payload"] for spec in CARRIERS}
    names = [spec["carrier"] for spec in CARRIERS]
    for start in range(0, len(names), 4):
        chunk = names[start:start + 4]
        emit(
            "fixtures/data/shipping_payloads_%d.json" % (start // 4 + 1),
            as_json({name: payloads[name] for name in chunk}),
        )


# --------------------------------------------------------------------------
# shop/inventory -- the stock ledger, reservations, network sync, warehouses.
# --------------------------------------------------------------------------

WAREHOUSES = [
    {
        "slug": "ashford",
        "warehouse": "ashford-dc",
        "doc": "Ashford distribution centre.\n"
        "\n"
        "Ashford is the oldest site and the only one that still refuses partial\n"
        "picks: a line either ships complete or it does not ship at all. The\n"
        "constraint comes from the site's pick-and-pack conveyor, not from policy.",
        "consts": ['STOCK = {"SKU-A1": 42, "SKU-B7": 12, "SKU-C3": 0, "SKU-D9": 8}'],
        "stock_doc": "Units of ``sku`` on the Ashford shelves.",
        "alloc_doc": "Reserve a line, refusing anything the site cannot fill whole.",
        "stock": "    return int(STOCK.get(str(sku), 0))",
        "alloc": '''
        have = int(STOCK.get(str(sku), 0))
        wish = max(0, int(qty))
        granted = wish if have >= wish else 0
        if granted:
            STOCK[str(sku)] = have - granted
        return {
            "warehouse": WAREHOUSE,
            "sku": str(sku),
            "requested": wish,
            "granted": granted,
            "backorder": wish - granted,
            "partial": granted != wish,
        }
        ''',
    },
    {
        "slug": "bracken",
        "warehouse": "bracken-hub",
        "doc": "Bracken cross-dock hub.\n"
        "\n"
        "Bracken fills whatever it can and hands the remainder to the backorder\n"
        "queue. It reports a fill rate so the planning team can see how badly a\n"
        "promotion drained the shelves.",
        "consts": ['STOCK = {"SKU-A1": 9, "SKU-E2": 31, "SKU-F4": 4, "SKU-G6": 0}'],
        "stock_doc": "Units of ``sku`` physically at Bracken.",
        "alloc_doc": "Fill as much of the line as the shelf allows.",
        "stock": "    return int(STOCK.get(str(sku), 0))",
        "alloc": '''
        have = max(0, int(STOCK.get(str(sku), 0)))
        wish = max(0, int(qty))
        granted = min(have, wish)
        STOCK[str(sku)] = have - granted
        return {
            "warehouse": WAREHOUSE,
            "sku": str(sku),
            "requested": wish,
            "granted": granted,
            "backorder": wish - granted,
            "fill_rate": round(granted / wish, 3) if wish else 1.0,
        }
        ''',
    },
    {
        "slug": "calder_west",
        "warehouse": "calder-west",
        "doc": "Calder West case-pack site.\n"
        "\n"
        "Calder West ships in sealed cases of six. A customer asking for seven\n"
        "units is quoted twelve, because breaking a case is a manual job that the\n"
        "site charges for by the hour.",
        "consts": [
            'STOCK = {"SKU-A1": 60, "SKU-H8": 17, "SKU-J2": 5}',
            "CASE_PACK = 6",
        ],
        "stock_doc": "Whole units left at Calder West, cases included.",
        "alloc_doc": "Round the line up to whole cases and allocate those.",
        "stock": "    return int(STOCK.get(str(sku), 0))",
        "alloc": '''
        wish = max(0, int(qty))
        cases = -(-wish // CASE_PACK) if wish else 0
        needed = cases * CASE_PACK
        have = int(STOCK.get(str(sku), 0))
        granted = needed if have >= needed else 0
        if granted:
            STOCK[str(sku)] = have - granted
        return {
            "warehouse": WAREHOUSE,
            "sku": str(sku),
            "requested": wish,
            "granted": granted,
            "backorder": wish - granted,
            "case_pack": CASE_PACK,
            "cases": cases,
        }
        ''',
    },
    {
        "slug": "dunmore",
        "warehouse": "dunmore-depot",
        "doc": "Dunmore depot.\n"
        "\n"
        "Dunmore keeps a safety buffer on every line and will never allocate into\n"
        "it. The buffer exists because the depot also serves the walk-in trade,\n"
        "and an empty shelf costs more than a late online order.",
        "consts": [
            'STOCK = {"SKU-A1": 25, "SKU-K5": 6, "SKU-L7": 40}',
            "SAFETY_BUFFER = 5",
        ],
        "stock_doc": "Units of ``sku`` on hand, buffer included.",
        "alloc_doc": "Allocate from the sellable stock, leaving the buffer alone.",
        "stock": "    return int(STOCK.get(str(sku), 0))",
        "alloc": '''
        have = int(STOCK.get(str(sku), 0))
        usable = max(0, have - SAFETY_BUFFER)
        wish = max(0, int(qty))
        granted = min(usable, wish)
        STOCK[str(sku)] = have - granted
        return {
            "warehouse": WAREHOUSE,
            "sku": str(sku),
            "requested": wish,
            "granted": granted,
            "backorder": wish - granted,
            "usable": usable,
            "buffer": SAFETY_BUFFER,
        }
        ''',
    },
    {
        "slug": "eastfield",
        "warehouse": "eastfield",
        "doc": "Eastfield pallet site.\n"
        "\n"
        "Eastfield stores everything on pallets of twenty-four and quotes in\n"
        "whole pallets. It is the cheapest site per unit and the slowest per line,\n"
        "which is why only wholesale orders route here.",
        "consts": [
            'STOCK = {"SKU-M1": 240, "SKU-N4": 71, "SKU-P6": 24}',
            "PALLET_SIZE = 24",
        ],
        "stock_doc": "Units of ``sku`` stacked at Eastfield.",
        "alloc_doc": "Quote in whole pallets and allocate the pallet count.",
        "stock": "    return int(STOCK.get(str(sku), 0))",
        "alloc": '''
        wish = max(0, int(qty))
        pallets = -(-wish // PALLET_SIZE) if wish else 0
        units = pallets * PALLET_SIZE
        have = int(STOCK.get(str(sku), 0))
        granted = units if have >= units else 0
        if granted:
            STOCK[str(sku)] = have - granted
        return {
            "warehouse": WAREHOUSE,
            "sku": str(sku),
            "requested": wish,
            "granted": granted,
            "backorder": wish - granted,
            "pallets": pallets,
        }
        ''',
    },
    {
        "slug": "fairhaven",
        "warehouse": "fairhaven",
        "doc": "Fairhaven priority site.\n"
        "\n"
        "Fairhaven holds the SKUs the marketing calendar promotes, and gives them\n"
        "first call on stock. Everything else is filled at half the requested\n"
        "quantity while a promotion is live.",
        "consts": [
            'STOCK = {"SKU-A1": 18, "SKU-Q3": 44, "SKU-R5": 7}',
            'PRIORITY_SKUS = frozenset({"SKU-A1", "SKU-Q3"})',
            "SECONDARY_DIVISOR = 2",
        ],
        "stock_doc": "Units of ``sku`` at Fairhaven.",
        "alloc_doc": "Fill priority SKUs whole and everyone else at half.",
        "stock": "    return int(STOCK.get(str(sku), 0))",
        "alloc": '''
        wish = max(0, int(qty))
        have = int(STOCK.get(str(sku), 0))
        if str(sku) in PRIORITY_SKUS:
            cap = wish
        else:
            cap = wish // SECONDARY_DIVISOR
        granted = min(have, cap)
        STOCK[str(sku)] = have - granted
        return {
            "warehouse": WAREHOUSE,
            "sku": str(sku),
            "requested": wish,
            "granted": granted,
            "backorder": wish - granted,
            "priority": str(sku) in PRIORITY_SKUS,
        }
        ''',
    },
    {
        "slug": "garrick",
        "warehouse": "garrick-point",
        "doc": "Garrick Point.\n"
        "\n"
        "Garrick Point caps every line at fifty units so one wholesale order\n"
        "cannot empty the site in a single pick. Larger orders have to be split\n"
        "across the network by the planner.",
        "consts": [
            'STOCK = {"SKU-A1": 120, "SKU-S8": 63, "SKU-T2": 9}',
            "MAX_PER_LINE = 50",
        ],
        "stock_doc": "Units of ``sku`` at Garrick Point.",
        "alloc_doc": "Allocate no more than the per-line cap.",
        "stock": "    return int(STOCK.get(str(sku), 0))",
        "alloc": '''
        wish = max(0, int(qty))
        capped = min(wish, MAX_PER_LINE)
        have = int(STOCK.get(str(sku), 0))
        granted = min(have, capped)
        STOCK[str(sku)] = have - granted
        return {
            "warehouse": WAREHOUSE,
            "sku": str(sku),
            "requested": wish,
            "granted": granted,
            "backorder": wish - granted,
            "capped": wish > MAX_PER_LINE,
        }
        ''',
    },
    {
        "slug": "holloway",
        "warehouse": "holloway",
        "doc": "Holloway multi-bin site.\n"
        "\n"
        "Holloway tracks stock per bin and drains the oldest bin first, so the\n"
        "counts here are lists rather than integers. Emptying a bin removes it,\n"
        "which keeps the site's pick lists short.",
        "consts": [
            'STOCK = {"SKU-A1": [4, 9, 2], "SKU-U6": [12], "SKU-V8": []}',
        ],
        "stock_doc": "Total units across every bin holding ``sku``.",
        "alloc_doc": "Drain the oldest bin first and report the bins used.",
        "stock": "    return sum(int(count) for count in STOCK.get(str(sku)) or [])",
        "alloc": '''
        bins = STOCK.setdefault(str(sku), [])
        wish = max(0, int(qty))
        granted = 0
        used = 0
        while bins and granted < wish:
            oldest = int(bins[0])
            take = min(oldest, wish - granted)
            granted += take
            used += 1
            if take >= oldest:
                bins.pop(0)
            else:
                bins[0] = oldest - take
        return {
            "warehouse": WAREHOUSE,
            "sku": str(sku),
            "requested": wish,
            "granted": granted,
            "backorder": wish - granted,
            "bins_used": used,
        }
        ''',
    },
    {
        "slug": "iverness",
        "warehouse": "iverness",
        "doc": "Iverness weight-limited site.\n"
        "\n"
        "Iverness sits at the end of a single-track road, so a van may only leave\n"
        "with a fixed weight aboard. Allocation counts grams, not units, and stops\n"
        "when the van is full.",
        "consts": [
            'STOCK = {"SKU-A1": 30, "SKU-W3": 12, "SKU-X5": 4}',
            'UNIT_WEIGHT_G = {"SKU-A1": 700, "SKU-W3": 2100, "SKU-X5": 5400}',
            "VAN_CAPACITY_G = 8000",
        ],
        "stock_doc": "Units of ``sku`` at Iverness.",
        "alloc_doc": "Fill the van while its weight budget allows.",
        "stock": "    return int(STOCK.get(str(sku), 0))",
        "alloc": '''
        have = int(STOCK.get(str(sku), 0))
        wish = max(0, int(qty))
        unit = int(UNIT_WEIGHT_G.get(str(sku), 500))
        capacity = VAN_CAPACITY_G // unit if unit else 0
        granted = min(have, wish, capacity)
        STOCK[str(sku)] = have - granted
        return {
            "warehouse": WAREHOUSE,
            "sku": str(sku),
            "requested": wish,
            "granted": granted,
            "backorder": wish - granted,
            "weight_g": granted * unit,
            "capacity": capacity,
        }
        ''',
    },
    {
        "slug": "juniper",
        "warehouse": "juniper",
        "doc": "Juniper hazardous store.\n"
        "\n"
        "Juniper holds the aerosol and solvent lines. Hazardous SKUs are capped\n"
        "per order regardless of what is on the shelf, because the courier will\n"
        "not carry more than that in one consignment.",
        "consts": [
            'STOCK = {"SKU-A1": 50, "SKU-Y1": 88, "SKU-Z4": 15}',
            'HAZMAT_SKUS = frozenset({"SKU-Y1", "SKU-Z4"})',
            "HAZMAT_CAP = 4",
        ],
        "stock_doc": "Units of ``sku`` at Juniper.",
        "alloc_doc": "Apply the hazardous-goods cap before allocating.",
        "stock": "    return int(STOCK.get(str(sku), 0))",
        "alloc": '''
        wish = max(0, int(qty))
        hazmat = str(sku) in HAZMAT_SKUS
        cap = min(wish, HAZMAT_CAP) if hazmat else wish
        have = int(STOCK.get(str(sku), 0))
        granted = min(have, cap)
        STOCK[str(sku)] = have - granted
        return {
            "warehouse": WAREHOUSE,
            "sku": str(sku),
            "requested": wish,
            "granted": granted,
            "backorder": wish - granted,
            "hazmat": hazmat,
        }
        ''',
    },
    {
        "slug": "kellerton",
        "warehouse": "kellerton",
        "doc": "Kellerton batch site.\n"
        "\n"
        "Kellerton will not open a batch for fewer than three units, so small\n"
        "orders are refused rather than picked. The rule keeps the batch records\n"
        "clean for the traceability audit.",
        "consts": [
            'STOCK = {"SKU-A1": 22, "SKU-BB2": 11, "SKU-CC7": 2}',
            "MIN_BATCH = 3",
        ],
        "stock_doc": "Units of ``sku`` at Kellerton.",
        "alloc_doc": "Refuse a line smaller than one whole batch.",
        "stock": "    return int(STOCK.get(str(sku), 0))",
        "alloc": '''
        wish = max(0, int(qty))
        have = int(STOCK.get(str(sku), 0))
        granted = min(have, wish) if wish >= MIN_BATCH else 0
        STOCK[str(sku)] = have - granted
        return {
            "warehouse": WAREHOUSE,
            "sku": str(sku),
            "requested": wish,
            "granted": granted,
            "backorder": wish - granted,
            "min_batch": MIN_BATCH,
        }
        ''',
    },
    {
        "slug": "lowmoor",
        "warehouse": "lowmoor",
        "doc": "Lowmoor two-bin site.\n"
        "\n"
        "Lowmoor runs two bins per SKU. When the live bin falls below its\n"
        "threshold the site swaps bins and raises a restock note, which is how the\n"
        "network notice board fills up overnight.",
        "consts": [
            'STOCK = {"SKU-A1": 14, "SKU-DD3": 9, "SKU-EE5": 31}',
            "LOW_WATER = 10",
        ],
        "stock_doc": "Units of ``sku`` across both of Lowmoor's bins.",
        "alloc_doc": "Allocate and raise a restock note when the bin runs low.",
        "stock": "    return int(STOCK.get(str(sku), 0))",
        "alloc": '''
        have = int(STOCK.get(str(sku), 0))
        wish = max(0, int(qty))
        granted = min(have, wish)
        left = have - granted
        STOCK[str(sku)] = left
        return {
            "warehouse": WAREHOUSE,
            "sku": str(sku),
            "requested": wish,
            "granted": granted,
            "backorder": wish - granted,
            "remaining": left,
            "restock": left < LOW_WATER,
        }
        ''',
    },
]


def render_warehouse(spec):
    """One warehouse adapter: its stock table, a level read, an allocation."""
    out = ['"""' + spec["doc"] + '"""', ""]
    out.append('WAREHOUSE = "%s"' % spec["warehouse"])
    out.extend(spec["consts"])
    out.extend(
        [
            "",
            "",
            "def stock_level(sku: str) -> int:",
            block('"""' + spec["stock_doc"] + '"""', indent=4),
            spec["stock"],
            "",
            "",
            "def allocate(sku: str, qty: int) -> dict:",
            block('"""' + spec["alloc_doc"] + '"""', indent=4),
            block(spec["alloc"]),
        ]
    )
    emit("shop/inventory/warehouse/%s.py" % spec["slug"], "\n".join(out) + "\n")


def render_inventory():
    """The inventory package: ledger, reservations, sync, warehouse adapters."""
    for spec in WAREHOUSES:
        render_warehouse(spec)
    render(
        "shop/inventory/warehouse/__init__.py",
        '''
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
        ''',
    )
    render(
        "shop/inventory/ledger.py",
        '''
        """The append-only stock ledger.

        Every movement is one row. Nothing is ever updated in place and nothing
        is ever deleted, so the balance of a SKU is always the sum of its rows.
        """

        from shop.errors import ValidationError

        __all__ = ["InventoryLedger", "REASONS"]

        REASONS = ("receipt", "sale", "return", "adjustment", "damage", "transfer")


        class InventoryLedger:
            """A running record of stock movements."""

            def __init__(self):
                self._rows = []

            def post(self, sku: str, delta: int, reason: str = "adjustment") -> dict:
                """Write one movement. ``delta`` is signed: positive is stock in."""
                if not str(sku or "").strip():
                    raise ValidationError("a movement needs a sku")
                if str(reason) not in REASONS:
                    raise ValidationError("unknown movement reason %r" % (reason,))
                if not isinstance(delta, int) or isinstance(delta, bool) or delta == 0:
                    raise ValidationError("delta must be a non-zero integer")
                row = {
                    "seq": len(self._rows) + 1,
                    "sku": str(sku),
                    "delta": int(delta),
                    "reason": str(reason),
                }
                self._rows.append(row)
                return dict(row)

            def balance(self, sku: str) -> int:
                """Signed sum of every movement for ``sku``."""
                return sum(row["delta"] for row in self._rows if row["sku"] == str(sku))

            def history(self, sku=None) -> list:
                """Movements in posting order, optionally for one SKU only."""
                if sku is None:
                    return [dict(row) for row in self._rows]
                return [dict(row) for row in self._rows if row["sku"] == str(sku)]

            def totals(self) -> dict:
                """Net balance per SKU, sorted by SKU."""
                totals = {}
                for row in self._rows:
                    totals[row["sku"]] = totals.get(row["sku"], 0) + row["delta"]
                return {sku: totals[sku] for sku in sorted(totals)}

            def __len__(self) -> int:
                return len(self._rows)
        ''',
    )
    render(
        "shop/inventory/reservations.py",
        '''
        """Short-lived reservations against stock that has not shipped yet."""

        from shop.errors import ValidationError

        __all__ = ["ReservationBook", "HOLD_MINUTES"]

        HOLD_MINUTES = 20


        class ReservationBook:
            """Reservations keyed by the reference checkout gave them."""

            def __init__(self, hold_minutes: int = HOLD_MINUTES):
                self._holds = {}
                self._closed = []
                self.hold_minutes = int(hold_minutes)

            def reserve(self, sku: str, qty: int, reference: str) -> dict:
                """Hold ``qty`` units for ``reference``. One hold per reference."""
                key = str(reference or "").strip()
                if not key:
                    raise ValidationError("a reservation needs a reference")
                if key in self._holds:
                    raise ValidationError("reference %r is already held" % (key,))
                if int(qty) <= 0:
                    raise ValidationError("qty must be positive")
                hold = {
                    "reference": key,
                    "sku": str(sku),
                    "qty": int(qty),
                    "minutes": self.hold_minutes,
                    "state": "held",
                }
                self._holds[key] = hold
                return dict(hold)

            def release(self, reference: str) -> dict:
                """Drop a hold without consuming it."""
                hold = self._holds.pop(str(reference), None)
                if hold is None:
                    raise ValidationError("no open reservation for %r" % (reference,))
                closed = dict(hold, state="released")
                self._closed.append(closed)
                return closed

            def commit(self, reference: str) -> dict:
                """Consume a hold: the stock has shipped."""
                hold = self._holds.pop(str(reference), None)
                if hold is None:
                    raise ValidationError("no open reservation for %r" % (reference,))
                closed = dict(hold, state="committed")
                self._closed.append(closed)
                return closed

            def open_holds(self) -> list:
                """Open holds in the order they were taken."""
                return [dict(self._holds[key]) for key in sorted(self._holds)]

            def held_for(self, sku: str) -> int:
                """Units currently held for ``sku``."""
                return sum(hold["qty"] for hold in self._holds.values() if hold["sku"] == str(sku))

            def closed(self) -> list:
                """Everything released or committed, oldest first."""
                return [dict(row) for row in self._closed]
        ''',
    )
    render(
        "shop/inventory/sync.py",
        '''
        """Reconcile a local stock snapshot against the remote site's counts."""

        from shop.errors import ValidationError

        __all__ = ["apply", "diff", "plan"]


        def _clean(counts) -> dict:
            if not isinstance(counts, dict):
                raise ValidationError("stock counts must be a mapping of sku to units")
            return {str(sku): int(units) for sku, units in counts.items()}


        def diff(local: dict, remote: dict) -> list:
            """Every SKU where the two snapshots disagree, ordered by SKU."""
            left = _clean(local)
            right = _clean(remote)
            rows = []
            for sku in sorted(set(left) | set(right)):
                here = left.get(sku, 0)
                there = right.get(sku, 0)
                if here == there:
                    continue
                rows.append(
                    {
                        "sku": sku,
                        "local": here,
                        "remote": there,
                        "delta": there - here,
                        "direction": "pull" if there > here else "push",
                    }
                )
            return rows


        def plan(local: dict, remote: dict) -> dict:
            """Split the disagreements into the two work queues."""
            rows = diff(local, remote)
            return {
                "pushes": [row for row in rows if row["direction"] == "push"],
                "pulls": [row for row in rows if row["direction"] == "pull"],
                "skus": len(rows),
            }


        def apply(local: dict, remote: dict, dry_run: bool = False) -> dict:
            """Write the remote counts into the local snapshot."""
            rows = diff(local, remote)
            if not dry_run:
                for row in rows:
                    local[row["sku"]] = row["remote"]
            return {"applied": 0 if dry_run else len(rows), "pending": len(rows), "rows": rows}
        ''',
    )
    render(
        "shop/inventory/__init__.py",
        '''
        """Inventory: the ledger, reservations, network sync and the sites."""

        from shop.inventory import ledger, reservations, sync, warehouse

        __all__ = ["ledger", "reservations", "sync", "warehouse"]
        ''',
    )


# --------------------------------------------------------------------------
# The sample orders every report, handler and fixture test works from.
# --------------------------------------------------------------------------

ORDERS_SAMPLE = [
    order(),
    order(
        order_id="ORD-1043",
        channel="mobile",
        subtotal_cents=9900,
        shipping_cents=0,
        coupon_codes=[],
        items_count=1,
        created_at="2026-02-11T14:05:00Z",
        status="shipped",
        shipped_at="2026-02-12T09:00:00Z",
        customer=dict(
            BASE_ORDER["customer"],
            id="cus-78",
            email="grace@example.com",
            tier="platinum",
            orders_count=12,
            lifetime_cents=140000,
            points=980,
        ),
        lines=[line("SKU-B7", 1, 9900, "audio", 1500)],
    ),
    order(
        order_id="ORD-1044",
        channel="partner",
        currency="EUR",
        subtotal_cents=2400,
        shipping_cents=750,
        coupon_codes=["SPRING10", "WELCOME"],
        items_count=4,
        created_at="2026-02-12T08:15:00Z",
        customer=dict(
            BASE_ORDER["customer"],
            id="cus-79",
            tier="silver",
            orders_count=1,
            lifetime_cents=2400,
            points=24,
        ),
        lines=[line("SKU-C3", 2, 700, "home", 900), line("SKU-D9", 2, 500, "home", 400)],
    ),
    order(
        order_id="ORD-1045",
        channel="web",
        subtotal_cents=12500,
        shipping_cents=0,
        coupon_codes=[],
        items_count=7,
        created_at="2026-02-12T19:40:00Z",
        status="refunded",
        refunded_cents=2500,
        shipped_at="2026-02-13T07:30:00Z",
        customer=dict(
            BASE_ORDER["customer"],
            id="cus-80",
            tier="gold",
            orders_count=7,
            lifetime_cents=62000,
            points=540,
        ),
        lines=[line("SKU-E2", 7, 1250, "tools", 350)],
    ),
    order(
        order_id="ORD-1046",
        channel="store",
        subtotal_cents=800,
        shipping_cents=0,
        coupon_codes=[],
        items_count=1,
        created_at="2026-02-13T11:00:00Z",
        status="cancelled",
        customer=dict(
            BASE_ORDER["customer"],
            id="cus-81",
            tier="silver",
            orders_count=0,
            lifetime_cents=0,
            points=0,
        ),
        lines=[line("SKU-F4", 1, 800, "garden", 2200)],
    ),
    order(
        order_id="ORD-1047",
        channel="mobile",
        subtotal_cents=42000,
        shipping_cents=1200,
        coupon_codes=["BULK25"],
        items_count=12,
        created_at="2026-02-13T16:25:00Z",
        status="shipped",
        shipped_at="2026-02-14T06:10:00Z",
        customer=dict(
            BASE_ORDER["customer"],
            id="cus-77",
            tier="platinum",
            orders_count=9,
            lifetime_cents=210000,
            points=2100,
        ),
        lines=[line("SKU-G6", 12, 3500, "office", 1100)],
    ),
]


# --------------------------------------------------------------------------
# shop/customers -- segmentation, addresses, merges and notes.
# --------------------------------------------------------------------------


def render_customers():
    """The four customer modules and their package init."""
    render(
        "shop/customers/segments.py",
        '''
        """Derived customer segments.

        A segment is computed from what the customer has done, never stored on
        the record. That keeps the marketing export honest: a customer cannot
        be left in a stale segment because a nightly job did not run.
        """

        from shop.config import TIER_BOUNDARIES

        __all__ = ["SEGMENTS", "segment_for", "summarise"]

        SEGMENTS = ("new", "dormant", "returning", "loyal", "vip")

        LOYAL_ORDERS = 5
        VIP_LIFETIME_CENTS = 100000
        DORMANT_DAYS = 180
        VIP_TIER = TIER_BOUNDARIES[-1]


        def _orders(customer: dict) -> int:
            return int(customer.get("orders_count") or 0)


        def segment_for(customer: dict) -> str:
            """One of ``SEGMENTS`` for this customer record."""
            record = customer or {}
            orders = _orders(record)
            if orders == 0:
                return "new"
            if int(record.get("days_since_order") or 0) >= DORMANT_DAYS:
                return "dormant"
            lifetime = int(record.get("lifetime_cents") or 0)
            if str(record.get("tier") or "") == VIP_TIER and lifetime >= VIP_LIFETIME_CENTS:
                return "vip"
            if orders >= LOYAL_ORDERS:
                return "loyal"
            return "returning"


        def summarise(customers: list) -> dict:
            """How many customers landed in each segment."""
            counts = {name: 0 for name in SEGMENTS}
            for customer in customers or []:
                counts[segment_for(customer)] += 1
            return {"segments": counts, "customers": sum(counts.values())}
        ''',
    )
    render(
        "shop/customers/addresses.py",
        '''
        """Address normalisation and the domestic/international split."""

        from shop.errors import ValidationError

        __all__ = ["DOMESTIC_COUNTRIES", "is_domestic", "normalize", "one_line"]

        DOMESTIC_COUNTRIES = frozenset({"US", "CA", "MX"})

        REQUIRED = ("line1", "city", "country")


        def normalize(address: dict) -> dict:
            """Upper-case the codes, tidy the text, and refuse incomplete rows."""
            row = dict(address or {})
            clean = {}
            for field in ("line1", "line2", "city", "region", "postcode"):
                value = str(row.get(field) or "").strip()
                clean[field] = value
            clean["country"] = str(row.get("country") or "").strip().upper()
            missing = [field for field in REQUIRED if not clean.get(field)]
            if missing:
                raise ValidationError("address is missing %s" % ", ".join(missing))
            clean["city"] = clean["city"].title()
            clean["region"] = clean["region"].upper()
            clean["postcode"] = clean["postcode"].upper().replace(" ", "")
            if len(clean["country"]) != 2:
                raise ValidationError("country must be a two-letter code")
            return clean


        def is_domestic(address: dict) -> bool:
            """True when the address is in a country the shop ships to itself."""
            country = str((address or {}).get("country") or "").strip().upper()
            return country in DOMESTIC_COUNTRIES


        def one_line(address: dict) -> str:
            """The address as a single line for a packing slip."""
            row = normalize(address)
            parts = [row["line1"], row["line2"], row["city"], row["region"], row["postcode"]]
            return ", ".join(part for part in parts if part)
        ''',
    )
    render(
        "shop/customers/merge.py",
        '''
        """Merge a duplicate customer record into its primary.

        The primary record always wins a field it already has. The duplicate only
        fills blanks, and the counters that describe history are added together,
        because a customer who bought five times under each of two records bought
        ten times in total.
        """

        from shop.errors import ValidationError

        __all__ = ["MERGE_KEYS", "merge"]

        MERGE_KEYS = ("email", "name", "phone", "tier", "accepts_marketing")
        SUMMED_KEYS = ("orders_count", "lifetime_cents", "points")


        def _blank(value) -> bool:
            return value is None or (isinstance(value, str) and not value.strip())


        def merge(primary: dict, duplicate: dict) -> dict:
            """Return the merged record; the inputs are left untouched."""
            left = dict(primary or {})
            right = dict(duplicate or {})
            if not left.get("id") or not right.get("id"):
                raise ValidationError("both records need an id before they can merge")
            if left["id"] == right["id"]:
                raise ValidationError("cannot merge a record into itself")
            merged = dict(left)
            for key in MERGE_KEYS:
                if _blank(merged.get(key)) and not _blank(right.get(key)):
                    merged[key] = right[key]
            for key in SUMMED_KEYS:
                merged[key] = int(left.get(key) or 0) + int(right.get(key) or 0)
            merged["merged_from"] = sorted(
                {str(value) for value in (left.get("merged_from") or []) + [right["id"]]}
            )
            return merged
        ''',
    )
    render(
        "shop/customers/notes.py",
        '''
        """Free-text customer notes, with inline ``#tags`` pulled out of the body."""

        from shop.errors import ValidationError

        __all__ = ["add", "latest", "search", "tags"]

        MAX_BODY = 500


        def tags(body: str) -> list:
            """Every ``#tag`` in ``body``, lower-cased, without the hash."""
            found = []
            for word in str(body or "").split():
                if word.startswith("#") and len(word) > 1:
                    found.append(word[1:].strip(".,;:!?").lower())
            return sorted(set(tag for tag in found if tag))


        def add(notes: list, body: str, author: str = "agent") -> list:
            """Append one note and hand back the new list."""
            text = str(body or "").strip()
            if not text:
                raise ValidationError("a note needs a body")
            if len(text) > MAX_BODY:
                raise ValidationError("a note may not exceed %d characters" % MAX_BODY)
            row = {"seq": len(notes or []) + 1, "author": str(author), "body": text, "tags": tags(text)}
            return [dict(note) for note in notes or []] + [row]


        def latest(notes: list, limit: int = 3) -> list:
            """The most recent ``limit`` notes, newest first."""
            kept = [dict(note) for note in notes or []]
            kept.sort(key=lambda note: int(note.get("seq") or 0), reverse=True)
            return kept[: max(0, int(limit))]


        def search(notes: list, term: str) -> list:
            """Notes whose body or tags mention ``term``, case-insensitively."""
            needle = str(term or "").strip().lower()
            if not needle:
                raise ValidationError("search needs a term")
            hits = []
            for note in notes or []:
                haystack = str(note.get("body") or "").lower()
                if needle in haystack or needle in (note.get("tags") or []):
                    hits.append(dict(note))
            return hits
        ''',
    )
    render(
        "shop/customers/__init__.py",
        '''
        """Customers: segments, addresses, record merges and notes."""

        from shop.customers import addresses, merge, notes, segments

        __all__ = ["addresses", "merge", "notes", "segments"]
        ''',
    )


# --------------------------------------------------------------------------
# shop/reporting -- 16 builders over the same list of order dicts.
# --------------------------------------------------------------------------

REPORTS = [
    {
        "slug": "revenue_by_channel",
        "doc": "Revenue by sales channel.\n"
        "\n"
        "The commercial team compares channels on merchandise value, so shipping\n"
        "and tax are deliberately left out of the totals here.",
        "build_doc": "Orders and merchandise cents per channel, channels sorted.",
        "body": '''
        rows = {}
        for order in orders or []:
            channel = str(order.get("channel") or "unknown")
            row = rows.setdefault(channel, {"channel": channel, "orders": 0, "cents": 0})
            row["orders"] += 1
            row["cents"] += int(order.get("subtotal_cents") or 0)
        return {
            "report": "revenue_by_channel",
            "rows": [rows[key] for key in sorted(rows)],
            "orders": len(orders or []),
        }
        ''',
    },
    {
        "slug": "revenue_by_category",
        "doc": "Merchandise revenue by product category.\n"
        "\n"
        "Category is read off the order line rather than looked up in the catalogue,\n"
        "which keeps the report usable for orders whose SKU has since been retired.",
        "build_doc": "Line revenue per category, richest category first.",
        "body": '''
        rows = {}
        for order in orders or []:
            for row in order.get("lines") or []:
                category = str(row.get("category") or "uncategorised")
                entry = rows.setdefault(category, {"category": category, "units": 0, "cents": 0})
                qty = int(row.get("qty") or 0)
                entry["units"] += qty
                entry["cents"] += qty * int(row.get("unit_cents") or 0)
        ordered = [rows[key] for key in sorted(rows)]
        ordered.sort(key=lambda row: (-row["cents"], row["category"]))
        return {"report": "revenue_by_category", "rows": ordered, "categories": len(ordered)}
        ''',
    },
    {
        "slug": "average_order_value",
        "doc": "Average order value.\n"
        "\n"
        "Cancelled and empty baskets are excluded: they drag an average down without\n"
        "describing anything a merchant can act on.",
        "build_doc": "Count, total and average merchandise value of real orders.",
        "body": '''
        kept = [order for order in orders or [] if int(order.get("subtotal_cents") or 0) > 0]
        total = sum(int(order.get("subtotal_cents") or 0) for order in kept)
        count = len(kept)
        return {
            "report": "average_order_value",
            "orders": count,
            "total_cents": total,
            "average_cents": total // count if count else 0,
        }
        ''',
    },
    {
        "slug": "top_skus",
        "doc": "Best-selling SKUs.\n"
        "\n"
        "Ranked by units rather than by revenue, because the warehouse plans pick\n"
        "faces by units and only the buyers care about value.",
        "build_doc": "Units sold per SKU, heaviest first, truncated by ``limit``.",
        "body": '''
        limit = int(kwargs.get("limit", 5))
        units = {}
        for order in orders or []:
            for row in order.get("lines") or []:
                sku = str(row.get("sku") or "")
                units[sku] = units.get(sku, 0) + int(row.get("qty") or 0)
        ranked = sorted(units.items(), key=lambda pair: (-pair[1], pair[0]))[: max(0, limit)]
        return {
            "report": "top_skus",
            "limit": limit,
            "rows": [{"sku": sku, "units": count} for sku, count in ranked],
        }
        ''',
    },
    {
        "slug": "tier_mix",
        "doc": "Order mix by loyalty tier.\n"
        "\n"
        "Answers the question the loyalty team asks every quarter: are the higher\n"
        "tiers actually carrying the revenue, or just the discount budget?",
        "build_doc": "Orders and value per tier, tiers in name order.",
        "body": '''
        counts = {}
        for order in orders or []:
            customer = order.get("customer") or {}
            tier = str(customer.get("tier") or "none")
            bucket = counts.setdefault(tier, {"tier": tier, "orders": 0, "cents": 0})
            bucket["orders"] += 1
            bucket["cents"] += int(order.get("subtotal_cents") or 0)
        rows = [counts[key] for key in sorted(counts)]
        return {"report": "tier_mix", "rows": rows, "tiers": len(rows)}
        ''',
    },
    {
        "slug": "coupon_usage",
        "doc": "Coupon redemption.\n"
        "\n"
        "An order may carry several coupons. The value column is the merchandise\n"
        "the coupon was applied to, not the coupon's own discount, because the\n"
        "discount engine owns that number.",
        "build_doc": "Redemptions and touched value per coupon code.",
        "body": '''
        usage = {}
        for order in orders or []:
            for code in order.get("coupon_codes") or []:
                entry = usage.setdefault(str(code), {"code": str(code), "orders": 0, "cents": 0})
                entry["orders"] += 1
                entry["cents"] += int(order.get("subtotal_cents") or 0)
        rows = [usage[key] for key in sorted(usage)]
        return {"report": "coupon_usage", "rows": rows, "distinct": len(rows)}
        ''',
    },
    {
        "slug": "basket_size_histogram",
        "doc": "Basket size distribution.\n"
        "\n"
        "Four buckets, fixed, because the merchandising dashboard has four slots\n"
        "and a fifth would need a design conversation nobody has had yet.",
        "build_doc": "Order counts in each basket-size bucket.",
        "body": '''
        buckets = {"1": 0, "2-3": 0, "4-6": 0, "7+": 0}
        for order in orders or []:
            count = int(order.get("items_count") or len(order.get("lines") or []))
            if count <= 1:
                key = "1"
            elif count <= 3:
                key = "2-3"
            elif count <= 6:
                key = "4-6"
            else:
                key = "7+"
            buckets[key] += 1
        return {
            "report": "basket_size_histogram",
            "buckets": buckets,
            "orders": sum(buckets.values()),
        }
        ''',
    },
    {
        "slug": "daily_volume",
        "doc": "Orders and value per calendar day.\n"
        "\n"
        "The timestamp is read from ``created_at`` with a plain string slice: the\n"
        "service stores ISO-8601 in UTC, so the first ten characters are the date.",
        "build_doc": "One row per day, oldest first.",
        "body": '''
        days = {}
        for order in orders or []:
            stamp = str(order.get("created_at") or "")
            day = stamp[:10] if len(stamp) >= 10 else "unknown"
            entry = days.setdefault(day, {"day": day, "orders": 0, "cents": 0})
            entry["orders"] += 1
            entry["cents"] += int(order.get("subtotal_cents") or 0)
        rows = [days[key] for key in sorted(days)]
        return {"report": "daily_volume", "rows": rows, "days": len(rows)}
        ''',
    },
    {
        "slug": "weekday_profile",
        "doc": "Order volume by day of week.\n"
        "\n"
        "Used to schedule the support rota. Orders with an unreadable timestamp\n"
        "are skipped rather than lumped into a fake weekday.",
        "build_doc": "Order counts and value for each weekday name.",
        "imports": "import datetime",
        "body": '''
        names = ("monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday")
        counts = {name: {"weekday": name, "orders": 0, "cents": 0} for name in names}
        skipped = 0
        for order in orders or []:
            stamp = str(order.get("created_at") or "")[:10]
            try:
                day = datetime.date.fromisoformat(stamp)
            except ValueError:
                skipped += 1
                continue
            entry = counts[names[day.weekday()]]
            entry["orders"] += 1
            entry["cents"] += int(order.get("subtotal_cents") or 0)
        return {
            "report": "weekday_profile",
            "rows": [counts[name] for name in names],
            "skipped": skipped,
        }
        ''',
    },
    {
        "slug": "shipping_recovery",
        "doc": "Shipping charged versus shipping waived.\n"
        "\n"
        "Finance watches this after a free-shipping promotion: a rise in waived\n"
        "orders with no matching rise in basket size is a promotion losing money.",
        "build_doc": "Collected shipping, waived orders and the share waived.",
        "body": '''
        orders_list = list(orders or [])
        collected = sum(int(order.get("shipping_cents") or 0) for order in orders_list)
        waived = sum(1 for order in orders_list if int(order.get("shipping_cents") or 0) == 0)
        total = len(orders_list)
        return {
            "report": "shipping_recovery",
            "orders": total,
            "collected_cents": collected,
            "waived_orders": waived,
            "waived_share": round(waived / total, 3) if total else 0.0,
        }
        ''',
    },
    {
        "slug": "customer_ltv_bands",
        "doc": "Customers bucketed by lifetime value.\n"
        "\n"
        "Bands are in whole dollars but the boundaries are stored in cents, because\n"
        "everything the service computes is in cents.",
        "build_doc": "Customer counts and value per lifetime-value band.",
        "consts": ['BANDS = ((10000, "under-100"), (50000, "100-499"), (200000, "500-1999"))'],
        "body": '''
        bands = {name: {"band": name, "customers": 0, "cents": 0} for _, name in BANDS}
        bands["2000+"] = {"band": "2000+", "customers": 0, "cents": 0}
        seen = set()
        for order in orders or []:
            customer = order.get("customer") or {}
            key = str(customer.get("id") or "")
            if not key or key in seen:
                continue
            seen.add(key)
            lifetime = int(customer.get("lifetime_cents") or 0)
            name = "2000+"
            for limit, label in BANDS:
                if lifetime < limit:
                    name = label
                    break
            bands[name]["customers"] += 1
            bands[name]["cents"] += lifetime
        return {"report": "customer_ltv_bands", "rows": list(bands.values()), "customers": len(seen)}
        ''',
    },
    {
        "slug": "currency_exposure",
        "doc": "Settlement currency exposure.\n"
        "\n"
        "The treasury team needs to know how much of the book is in each currency\n"
        "before the daily hedge, so this counts orders and value per code.",
        "build_doc": "Orders and value per settlement currency.",
        "imports": "from shop.config import DEFAULT_CURRENCY",
        "body": '''
        rows = {}
        for order in orders or []:
            code = str(order.get("currency") or DEFAULT_CURRENCY)
            entry = rows.setdefault(code, {"currency": code, "orders": 0, "cents": 0})
            entry["orders"] += 1
            entry["cents"] += int(order.get("subtotal_cents") or 0)
        home = str(kwargs.get("home_currency", DEFAULT_CURRENCY))
        ordered = [rows[key] for key in sorted(rows)]
        foreign = [row for row in ordered if row["currency"] != home]
        return {
            "report": "currency_exposure",
            "rows": ordered,
            "foreign_cents": sum(row["cents"] for row in foreign),
            "home_currency": home,
        }
        ''',
    },
    {
        "slug": "points_liability",
        "doc": "Outstanding loyalty points.\n"
        "\n"
        "Points are a liability: the shop owes merchandise for them. The report\n"
        "values the balance at the redemption rate finance set last year.",
        "build_doc": "Points outstanding and their cash value.",
        "consts": ["POINT_VALUE_CENTS = 1"],
        "body": '''
        seen = {}
        for order in orders or []:
            customer = order.get("customer") or {}
            key = str(customer.get("id") or "")
            if key:
                seen[key] = int(customer.get("points") or 0)
        points = sum(seen.values())
        return {
            "report": "points_liability",
            "customers": len(seen),
            "points": points,
            "value_cents": points * POINT_VALUE_CENTS,
        }
        ''',
    },
    {
        "slug": "refund_rate",
        "doc": "Refund rate by channel.\n"
        "\n"
        "An order counts as refunded when it carries a refunded amount, whether\n"
        "the whole order came back or only one line did.",
        "build_doc": "Refunded orders and refunded cents per channel.",
        "body": '''
        rows = {}
        for order in orders or []:
            channel = str(order.get("channel") or "unknown")
            entry = rows.setdefault(channel, {"channel": channel, "orders": 0, "refunds": 0, "cents": 0})
            entry["orders"] += 1
            refunded = int(order.get("refunded_cents") or 0)
            if refunded > 0:
                entry["refunds"] += 1
                entry["cents"] += refunded
        ordered = [rows[key] for key in sorted(rows)]
        for row in ordered:
            row["rate"] = round(row["refunds"] / row["orders"], 3) if row["orders"] else 0.0
        return {"report": "refund_rate", "rows": ordered, "refunds": sum(row["refunds"] for row in ordered)}
        ''',
    },
    {
        "slug": "fulfilment_sla",
        "doc": "Time from order to dispatch.\n"
        "\n"
        "Hours are computed on the ISO-8601 stamps the service writes, so a broken\n"
        "stamp is skipped rather than counted as an instant dispatch.",
        "build_doc": "Dispatch hours per shipped order, plus the slowest one.",
        "imports": "import datetime",
        "body": '''
        hours = []
        skipped = 0
        for order in orders or []:
            placed = str(order.get("created_at") or "")
            shipped = str(order.get("shipped_at") or "")
            if not placed or not shipped:
                skipped += 1
                continue
            try:
                start = datetime.datetime.fromisoformat(placed.replace("Z", "+00:00"))
                end = datetime.datetime.fromisoformat(shipped.replace("Z", "+00:00"))
            except ValueError:
                skipped += 1
                continue
            hours.append(round((end - start).total_seconds() / 3600, 2))
        return {
            "report": "fulfilment_sla",
            "shipped": len(hours),
            "skipped": skipped,
            "hours": hours,
            "worst_hours": max(hours) if hours else 0.0,
            "median_hours": sorted(hours)[len(hours) // 2] if hours else 0.0,
        }
        ''',
    },
    {
        "slug": "inventory_pressure",
        "doc": "Lines that will strain the warehouse.\n"
        "\n"
        "Any line of three units or more is worth a pick-face check, because three\n"
        "is where the site's case handling starts to matter.",
        "build_doc": "Large lines grouped by SKU, heaviest first.",
        "consts": ["PRESSURE_QTY = 3"],
        "body": '''
        limit = int(kwargs.get("limit", 10))
        rows = {}
        for order in orders or []:
            for row in order.get("lines") or []:
                qty = int(row.get("qty") or 0)
                if qty < PRESSURE_QTY:
                    continue
                sku = str(row.get("sku") or "")
                entry = rows.setdefault(sku, {"sku": sku, "lines": 0, "units": 0, "weight_g": 0})
                entry["lines"] += 1
                entry["units"] += qty
                entry["weight_g"] += qty * int(row.get("weight_g") or 0)
        ordered = [rows[key] for key in sorted(rows)]
        ordered.sort(key=lambda row: (-row["units"], row["sku"]))
        return {"report": "inventory_pressure", "rows": ordered[: max(0, limit)], "skus": len(ordered)}
        ''',
    },
]


def render_report(spec):
    """One report builder: docstring, optional constants, one ``build``."""
    out = ['"""' + spec["doc"] + '"""', ""]
    if spec.get("imports"):
        out.extend([spec["imports"], ""])
    out.append('__all__ = ["build"]')
    out.extend(spec.get("consts", []))
    out.extend(
        [
            "",
            "",
            "def build(orders: list[dict], **kwargs) -> dict:",
            block('"""' + spec["build_doc"] + '"""', indent=4),
            block(spec["body"]),
        ]
    )
    emit("shop/reporting/reports/%s.py" % spec["slug"], "\n".join(out) + "\n")


def render_reporting():
    """The report builders, their registry package, and the package init."""
    for spec in REPORTS:
        render_report(spec)
    render(
        "shop/reporting/reports/__init__.py",
        '''
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
        ''',
    )
    render(
        "shop/reporting/__init__.py",
        '''
        """Reporting: one builder per question the business asks of the orders."""

        from shop.reporting import reports

        __all__ = ["reports"]
        ''',
    )
    for start in range(0, len(ORDERS_SAMPLE), 2):
        chunk = ORDERS_SAMPLE[start:start + 2]
        emit(
            "fixtures/data/orders_sample_%d.json" % (start // 2 + 1),
            as_json(chunk),
        )


# --------------------------------------------------------------------------
# shop/api -- the request handlers behind the storefront routes. Every handler
# takes the decoded JSON body and either answers with a payload or raises one of
# the shop errors; the middleware turns those into HTTP-ish envelopes.
# --------------------------------------------------------------------------

HANDLERS = [
    {
        "module": "cart_add",
        "title": "Add a line to the cart",
        "route": ("POST", "/api/cart/items"),
        "story": "Add a line to the cart, merging into a line that is already there.\n\nQuantities above the per-line ceiling are clamped rather than refused, because\nthe storefront stepper can race ahead of the server on a long key-press.",
        "consts": 'MAX_LINE_QTY = 25\nMAX_CART_LINES = 40',
        "body": """
        sku = str(req.get("sku") or "").strip()
        if not sku:
            raise ValidationError("sku is required")
        qty = req.get("qty", 1)
        if not isinstance(qty, int) or isinstance(qty, bool) or qty < 1:
            raise ValidationError("qty must be a positive integer")
        cart = [dict(line) for line in req.get("cart") or []]
        for row in cart:
            if row.get("sku") == sku:
                row["qty"] = min(MAX_LINE_QTY, int(row.get("qty") or 0) + qty)
                break
        else:
            if len(cart) >= MAX_CART_LINES:
                raise ValidationError("the cart already holds %d lines" % MAX_CART_LINES)
            cart.append({"sku": sku, "qty": min(MAX_LINE_QTY, qty)})
        return {
            "cart": cart,
            "lines": len(cart),
            "units": sum(int(line["qty"]) for line in cart),
            "clamped": qty > MAX_LINE_QTY,
        }
        """,
    },
    {
        "module": "cart_remove",
        "title": "Remove a line from the cart",
        "route": ("DELETE", "/api/cart/items"),
        "story": "Drop one SKU from the cart.\n\nA line that is not in the cart is a 404 rather than a silent no-op, so a client that removed the wrong row hears about it.",
        "consts": "",
        "body": """
        sku = str(req.get("sku") or "").strip()
        if not sku:
            raise ValidationError("sku is required")
        cart = [dict(line) for line in req.get("cart") or []]
        kept = [line for line in cart if line.get("sku") != sku]
        if len(kept) == len(cart):
            raise NotFoundError("sku %s is not in the cart" % sku)
        return {"cart": kept, "removed": sku, "lines": len(kept)}
        """,
    },
    {
        "module": "cart_view",
        "title": "Price up the cart",
        "route": ("GET", "/api/cart"),
        "story": "Total the cart against the price list the client sent back with it.\n\nA line with no known price is reported in ``unpriced`` instead of being\nsilently counted at zero.",
        "consts": "",
        "body": """
        cart = [dict(line) for line in req.get("cart") or []]
        prices = req.get("prices") or {}
        subtotal = 0
        unpriced = []
        for line in cart:
            sku = str(line.get("sku"))
            if sku not in prices:
                unpriced.append(sku)
                continue
            subtotal += int(prices[sku]) * int(line.get("qty") or 0)
        return {
            "lines": len(cart),
            "units": sum(int(line.get("qty") or 0) for line in cart),
            "subtotal_cents": subtotal,
            "unpriced": unpriced,
            "empty": not cart,
        }
        """,
    },
    {
        "module": "checkout_start",
        "title": "Open a checkout draft",
        "route": ("POST", "/api/checkout"),
        "story": "Open a draft order for a cart and hand back its reference.\n\nThe reference is derived from the email and the cart size, so retrying the\nsame request in the browser produces the same draft instead of two.",
        "consts": 'import hashlib\n\nDRAFT_PREFIX = "ORD-"',
        "body": """
        email = str(req.get("email") or "").strip().lower()
        if "@" not in email:
            raise ValidationError("a valid email is required")
        cart = [dict(line) for line in req.get("cart") or []]
        if not cart:
            raise ValidationError("the cart is empty")
        seed = email + "|" + ",".join(str(line.get("sku")) for line in cart)
        digest = hashlib.sha1(seed.encode("utf-8")).hexdigest()[:6].upper()
        return {
            "order_ref": DRAFT_PREFIX + digest,
            "email": email,
            "lines": len(cart),
            "draft": True,
        }
        """,
    },
    {
        "module": "order_get",
        "title": "Fetch one order",
        "route": ("GET", "/api/orders/{order_id}"),
        "story": "Look one order up in the set the caller supplied.\n\nThe handler works on the orders it is given rather than reaching for a\nstore, which keeps the fixture runnable with nothing but the stdlib.",
        "consts": "",
        "body": """
        order_id = str(req.get("order_id") or "").strip()
        if not order_id:
            raise ValidationError("order_id is required")
        for row in req.get("orders") or []:
            if str(row.get("order_id")) == order_id:
                return {"order": dict(row)}
        raise NotFoundError("no order %s" % order_id)
        """,
    },
    {
        "module": "order_cancel",
        "title": "Cancel an order that has not shipped",
        "route": ("POST", "/api/orders/{order_id}/cancel"),
        "story": "Cancel an order, but only while it is still cancellable.\n\nOnce a parcel is with the carrier the cancellation has to go through the\nreturns flow, so those statuses are refused here.",
        "consts": 'LOCKED_STATUSES = ("shipped", "delivered", "cancelled", "refunded")',
        "body": """
        order_id = str(req.get("order_id") or "").strip()
        if not order_id:
            raise ValidationError("order_id is required")
        orders = [dict(row) for row in req.get("orders") or []]
        for row in orders:
            if str(row.get("order_id")) != order_id:
                continue
            status = str(row.get("status") or "pending")
            if status in LOCKED_STATUSES:
                raise ValidationError("a %s order cannot be cancelled" % status)
            row["status"] = "cancelled"
            return {"order": row, "previous": status}
        raise NotFoundError("no order %s" % order_id)
        """,
    },
    {
        "module": "order_list",
        "title": "List orders with a filter",
        "route": ("GET", "/api/orders"),
        "story": "List orders, newest first, with an optional status and channel filter.\n\nSorting is stable on the created-at stamp, so a page of orders placed in the\nsame second keeps the order the caller supplied.",
        "consts": "DEFAULT_LIMIT = 50",
        "body": """
        status = str(req.get("status") or "").strip()
        channel = str(req.get("channel") or "").strip()
        rows = [dict(row) for row in req.get("orders") or []]
        if status:
            rows = [row for row in rows if str(row.get("status")) == status]
        if channel:
            rows = [row for row in rows if str(row.get("channel")) == channel]
        rows.sort(key=lambda row: str(row.get("created_at") or ""), reverse=True)
        limit = int(req.get("limit") or DEFAULT_LIMIT)
        if limit < 0:
            raise ValidationError("limit must not be negative")
        return {"orders": rows[:limit], "matched": len(rows), "returned": min(limit, len(rows))}
        """,
    },
    {
        "module": "refund_request",
        "title": "Open a refund request",
        "route": ("POST", "/api/refunds"),
        "story": "Open a refund against an order, full or partial.\n\nA partial refund may not exceed what is left of the order once earlier\nrefunds are taken off.",
        "consts": "",
        "body": """
        order = req.get("order") or {}
        if not order.get("order_id"):
            raise ValidationError("an order is required")
        total = int(order.get("subtotal_cents") or 0)
        already = int(order.get("refunded_cents") or 0)
        amount = int(req.get("amount_cents") or total)
        if amount <= 0:
            raise ValidationError("a refund must be positive")
        if amount + already > total:
            raise ValidationError("refund exceeds the %d cents left on the order" % (total - already))
        return {
            "refund": {
                "order_id": str(order["order_id"]),
                "amount_cents": amount,
                "reason": str(req.get("reason") or "customer request"),
            },
            "remaining_cents": total - already - amount,
        }
        """,
    },
    {
        "module": "return_create",
        "title": "Open a return inside the return window",
        "route": ("POST", "/api/returns"),
        "story": "Accept a return while the parcel is still inside the window.\n\nThe window runs from delivery, not from the order, because a pre-order can\nbe placed months before the box lands.",
        "consts": "RETURN_WINDOW_DAYS = 30",
        "body": """
        order_id = str(req.get("order_id") or "").strip()
        if not order_id:
            raise ValidationError("order_id is required")
        items = list(req.get("items") or [])
        if not items:
            raise ValidationError("a return needs at least one line")
        days = int(req.get("days_since_delivery") or 0)
        if days < 0:
            raise ValidationError("days_since_delivery must not be negative")
        if days > RETURN_WINDOW_DAYS:
            raise ValidationError("the %d day return window has closed" % RETURN_WINDOW_DAYS)
        return {
            "return": {
                "order_id": order_id,
                "items": items,
                "reason": str(req.get("reason") or "unwanted"),
                "label": "RTN-%s" % order_id.replace("ORD-", ""),
            },
            "days_left": RETURN_WINDOW_DAYS - days,
        }
        """,
    },
    {
        "module": "address_create",
        "title": "Create a shipping address",
        "route": ("POST", "/api/addresses"),
        "story": "Store a new address for a customer.\n\nThe country is upper-cased and length-checked here rather than at the\ncarrier, because a two-letter code is the only thing the rate table reads.",
        "consts": 'REQUIRED_FIELDS = ("line1", "city", "country")',
        "body": """
        address = dict(req.get("address") or {})
        for field in REQUIRED_FIELDS:
            if not str(address.get(field) or "").strip():
                raise ValidationError("address.%s is required" % field)
        country = str(address["country"]).strip().upper()
        if len(country) != 2:
            raise ValidationError("country must be a two-letter code")
        address["country"] = country
        address["city"] = str(address["city"]).strip().title()
        return {"address": address, "customer_id": str(req.get("customer_id") or "")}
        """,
    },
    {
        "module": "address_update",
        "title": "Patch one field of an address",
        "route": ("PATCH", "/api/addresses/{address_id}"),
        "story": "Patch an address with only the fields the client changed.\n\nUnknown fields are refused instead of stored, so a typo in a client cannot\nquietly add a column to every address record.",
        "consts": 'EDITABLE = ("line1", "line2", "city", "region", "postcode", "country")',
        "body": """
        address = dict(req.get("address") or {})
        if not address:
            raise NotFoundError("no address to patch")
        patch = dict(req.get("patch") or {})
        if not patch:
            raise ValidationError("the patch is empty")
        for field in patch:
            if field not in EDITABLE:
                raise ValidationError("%s is not an editable address field" % field)
        address.update({key: str(value).strip() for key, value in patch.items()})
        address["country"] = address.get("country", "").upper()
        return {"address": address, "changed": sorted(patch)}
        """,
    },
    {
        "module": "customer_get",
        "title": "Fetch a customer record",
        "route": ("GET", "/api/customers/{customer_id}"),
        "story": "Look a customer up by id or by email.\n\nSupport works from whichever of the two the customer quoted on the phone, so\nboth are accepted and the answer is the same record.",
        "consts": "",
        "body": """
        wanted = str(req.get("customer_id") or req.get("email") or "").strip().lower()
        if not wanted:
            raise ValidationError("customer_id or email is required")
        for row in req.get("customers") or []:
            if wanted in (str(row.get("id")).lower(), str(row.get("email")).lower()):
                return {"customer": dict(row)}
        raise NotFoundError("no customer %s" % wanted)
        """,
    },
    {
        "module": "customer_update",
        "title": "Update a customer's own fields",
        "route": ("PATCH", "/api/customers/{customer_id}"),
        "story": "Update the handful of customer fields a person may edit themselves.\n\nAnything else, tier included, is refused: a customer may opt out of\nmarketing but may not promote themselves.",
        "consts": 'SELF_SERVICE_FIELDS = ("name", "phone", "accepts_marketing", "locale")',
        "body": """
        customer = dict(req.get("customer") or {})
        if not customer.get("id"):
            raise ValidationError("the customer record needs an id")
        patch = dict(req.get("patch") or {})
        refused = sorted(key for key in patch if key not in SELF_SERVICE_FIELDS)
        if refused:
            raise ValidationError("these fields are not self-service: %s" % ", ".join(refused))
        customer.update(patch)
        return {"customer": customer, "changed": sorted(patch)}
        """,
    },
    {
        "module": "coupon_validate",
        "title": "Validate a coupon code",
        "route": ("POST", "/api/coupons/validate"),
        "story": "Check a coupon against the basket before the customer commits.\n\nThe answer names the reason a code does not apply, because 'invalid coupon'\nis the single most common support ticket the store gets.",
        "consts": "",
        "body": """
        code = str(req.get("code") or "").strip().upper()
        if not code:
            raise ValidationError("a coupon code is required")
        table = req.get("coupons") or {}
        coupon = table.get(code)
        if coupon is None:
            return {"code": code, "valid": False, "reason": "unknown code"}
        subtotal = int(req.get("subtotal_cents") or 0)
        minimum = int(coupon.get("min_subtotal_cents") or 0)
        if subtotal < minimum:
            return {"code": code, "valid": False, "reason": "needs %d more cents" % (minimum - subtotal)}
        if not coupon.get("active", True):
            return {"code": code, "valid": False, "reason": "retired"}
        return {
            "code": code,
            "valid": True,
            "amount_cents": int(coupon.get("amount_cents") or 0),
            "percent_off": int(coupon.get("percent_off") or 0),
        }
        """,
    },
    {
        "module": "coupon_redeem",
        "title": "Redeem one use of a coupon",
        "route": ("POST", "/api/coupons/redeem"),
        "story": "Spend one use of a limited coupon.\n\nThe use is counted down before the discount is returned, so two concurrent\ncheckouts cannot both take the last redemption.",
        "consts": "",
        "body": """
        code = str(req.get("code") or "").strip().upper()
        if not code:
            raise ValidationError("a coupon code is required")
        coupons = {key: dict(value) for key, value in (req.get("coupons") or {}).items()}
        coupon = coupons.get(code)
        if coupon is None:
            raise NotFoundError("unknown coupon %s" % code)
        left = int(coupon.get("uses_left", 0))
        if left <= 0:
            raise ValidationError("coupon %s has been fully redeemed" % code)
        coupon["uses_left"] = left - 1
        return {"coupons": coupons, "used": code, "uses_left": left - 1}
        """,
    },
    {
        "module": "inventory_check",
        "title": "Report availability for a basket",
        "route": ("POST", "/api/inventory/check"),
        "story": "Answer, per SKU, whether the basket can ship today.\n\nThe threshold comes from the shop settings, so the same handler keeps the\nstorefront honest when a merchant raises it before a sale.",
        "consts": 'from shop.config import settings\n\nDEFAULT_WANTED = 1',
        "body": """
        wanted = dict(req.get("wanted") or {})
        if not wanted:
            raise ValidationError("wanted is required, as a sku to quantity map")
        stock = req.get("stock") or {}
        floor = int(getattr(settings, "low_stock_threshold", 0) or 0)
        lines = []
        for sku, qty in sorted(wanted.items()):
            have = int(stock.get(sku, 0))
            need = int(qty or DEFAULT_WANTED)
            lines.append(
                {
                    "sku": sku,
                    "wanted": need,
                    "available": have,
                    "short": max(0, need - have),
                    "low": have <= floor,
                }
            )
        return {"lines": lines, "all_available": all(line["short"] == 0 for line in lines)}
        """,
    },
    {
        "module": "inventory_reserve",
        "title": "Reserve stock for a cart",
        "route": ("POST", "/api/inventory/reserve"),
        "story": "Hold stock while the customer is in checkout.\n\nThe hold is all-or-nothing: a basket that cannot be filled in full is\nrefused here and the shopper is told which line is short.",
        "consts": "HOLD_MINUTES = 20",
        "body": """
        reference = str(req.get("reference") or "").strip()
        if not reference:
            raise ValidationError("a reservation needs a reference")
        wanted = dict(req.get("wanted") or {})
        if not wanted:
            raise ValidationError("nothing to reserve")
        stock = req.get("stock") or {}
        short = {}
        for sku, qty in sorted(wanted.items()):
            if int(stock.get(sku, 0)) < int(qty):
                short[sku] = int(qty) - int(stock.get(sku, 0))
        if short:
            raise ValidationError("short on stock: %s" % ", ".join(sorted(short)))
        return {"reservation": {"reference": reference, "items": wanted, "minutes": HOLD_MINUTES}}
        """,
    },
    {
        "module": "shipping_quote",
        "title": "Quote a shipping price",
        "route": ("POST", "/api/shipping/quote"),
        "story": "Quote carriage for a weight and zone, with the free-shipping floor.\n\nThe floor is read from the shop settings so the storefront banner and the\ncheckout total can never disagree.",
        "consts": 'from shop.config import settings\n\nZONE_TABLE = {"domestic": 499, "north": 749, "overseas": 1499}',
        "body": """
        zone = str(req.get("zone") or "domestic").strip().lower()
        if zone not in ZONE_TABLE:
            raise ValidationError("unknown zone %s" % zone)
        weight = int(req.get("weight_g") or 0)
        if weight <= 0:
            raise ValidationError("weight_g must be positive")
        bands = 1 + (weight - 1) // 1000
        base = ZONE_TABLE[zone]
        amount = base * bands
        subtotal = int(req.get("subtotal_cents") or 0)
        free_at = int(getattr(settings, "free_shipping_cents", 0) or 0)
        return {
            "zone": zone,
            "weight_g": weight,
            "bands": bands,
            "amount_cents": 0 if subtotal >= free_at else amount,
            "base_cents": amount,
        }
        """,
    },
    {
        "module": "shipping_label",
        "title": "Mint a carrier label",
        "route": ("POST", "/api/shipping/labels"),
        "story": "Mint a label and tracking number for an order.\n\nThe tracking number is derived from the order and the carrier so a repeated\nrequest reprints the same label instead of buying a second one.",
        "consts": 'import hashlib\n\nCARRIERS = {"northpost": "NP", "swiftlink": "SW", "harbor_freight": "HF"}',
        "body": """
        order_id = str(req.get("order_id") or "").strip()
        carrier = str(req.get("carrier") or "").strip().lower()
        if not order_id:
            raise ValidationError("order_id is required")
        if carrier not in CARRIERS:
            raise ValidationError("unknown carrier %s" % carrier)
        seed = order_id + "|" + carrier
        tail = hashlib.sha1(seed.encode("utf-8")).hexdigest()[:10].upper()
        return {
            "label": {
                "order_id": order_id,
                "carrier": carrier,
                "tracking_number": CARRIERS[carrier] + tail,
                "format": str(req.get("format") or "pdf"),
            }
        }
        """,
    },
    {
        "module": "payment_authorize",
        "title": "Authorize a card payment",
        "route": ("POST", "/api/payments/authorize"),
        "story": "Authorize an amount against a payment method.\n\nThe authorization code is deterministic on the reference, which is what lets\nthe retry queue re-send an authorization without double-charging.",
        "consts": 'import hashlib\n\nSUPPORTED_CURRENCIES = ("USD", "EUR", "GBP", "CAD")',
        "body": """
        reference = str(req.get("reference") or "").strip()
        if not reference:
            raise ValidationError("reference is required")
        amount = int(req.get("amount_cents") or 0)
        if amount <= 0:
            raise ValidationError("amount_cents must be positive")
        currency = str(req.get("currency") or "USD").upper()
        if currency not in SUPPORTED_CURRENCIES:
            raise ValidationError("currency %s is not supported" % currency)
        code = hashlib.sha1((reference + currency).encode("utf-8")).hexdigest()[:8].upper()
        return {
            "authorization": {
                "reference": reference,
                "code": "AUTH-" + code,
                "amount_cents": amount,
                "currency": currency,
                "state": "authorized",
            }
        }
        """,
    },
    {
        "module": "payment_capture",
        "title": "Capture an authorization",
        "route": ("POST", "/api/payments/capture"),
        "story": "Capture all or part of an authorization.\n\nPartial capture is normal for a split shipment; capturing more than was\nauthorized is never allowed.",
        "consts": "",
        "body": """
        auth = req.get("authorization") or {}
        code = str(auth.get("code") or "").strip()
        if not code:
            raise ValidationError("an authorization code is required")
        authorized = int(auth.get("amount_cents") or 0)
        amount = int(req.get("amount_cents") or authorized)
        if amount <= 0:
            raise ValidationError("amount_cents must be positive")
        if amount > authorized:
            raise ValidationError("cannot capture %d of a %d authorization" % (amount, authorized))
        return {
            "capture": {"authorization": code, "amount_cents": amount},
            "uncaptured_cents": authorized - amount,
        }
        """,
    },
    {
        "module": "payment_refund",
        "title": "Refund a captured payment",
        "route": ("POST", "/api/payments/refund"),
        "story": "Refund against a capture, never against more than was captured.\n\nThe answer says how much is still refundable, which is the number support\nquotes back to the customer.",
        "consts": "",
        "body": """
        capture = req.get("capture") or {}
        if not capture.get("authorization"):
            raise NotFoundError("no capture to refund")
        captured = int(capture.get("amount_cents") or 0)
        already = int(capture.get("refunded_cents") or 0)
        amount = int(req.get("amount_cents") or (captured - already))
        if amount <= 0:
            raise ValidationError("nothing left to refund")
        if amount + already > captured:
            raise ValidationError("refund exceeds the %d cents captured" % captured)
        return {
            "refund": {"authorization": str(capture["authorization"]), "amount_cents": amount},
            "refundable_cents": captured - already - amount,
        }
        """,
    },
    {
        "module": "wishlist_add",
        "title": "Add a SKU to a wishlist",
        "route": ("POST", "/api/wishlists/items"),
        "story": "Add a SKU to a customer's wishlist without duplicating it.\n\nThe wishlist has a ceiling because the storefront renders all of it in one\nscroll, and an unbounded list was the slowest page on the site.",
        "consts": "MAX_WISHLIST = 100",
        "body": """
        customer_id = str(req.get("customer_id") or "").strip()
        sku = str(req.get("sku") or "").strip()
        if not customer_id or not sku:
            raise ValidationError("customer_id and sku are both required")
        items = [str(item) for item in req.get("items") or []]
        if sku in items:
            return {"items": items, "added": False, "reason": "already saved"}
        if len(items) >= MAX_WISHLIST:
            raise ValidationError("the wishlist already holds %d items" % MAX_WISHLIST)
        items.append(sku)
        return {"items": items, "added": True, "size": len(items)}
        """,
    },
    {
        "module": "review_submit",
        "title": "Accept a product review",
        "route": ("POST", "/api/reviews"),
        "story": "Accept a review with a star rating and a body.\n\nA review is stored unapproved: the store moderates before anything appears\non the product page, so this handler never publishes.",
        "consts": "MIN_BODY = 20",
        "body": """
        sku = str(req.get("sku") or "").strip()
        if not sku:
            raise ValidationError("sku is required")
        rating = req.get("rating")
        if not isinstance(rating, int) or isinstance(rating, bool) or not 1 <= rating <= 5:
            raise ValidationError("rating must be an integer from 1 to 5")
        body = str(req.get("body") or "").strip()
        if len(body) < MIN_BODY:
            raise ValidationError("a review needs at least %d characters" % MIN_BODY)
        return {
            "review": {
                "sku": sku,
                "rating": rating,
                "body": body,
                "author": str(req.get("author") or "anonymous"),
                "approved": False,
            }
        }
        """,
    },
    {
        "module": "search_products",
        "title": "Search the product list",
        "route": ("GET", "/api/products/search"),
        "story": "Rank the catalogue against a free-text query.\n\nA name match outranks a description match and a tag match outranks both\nslightly, which is enough to keep the obvious product on top.",
        "consts": "DEFAULT_LIMIT = 20",
        "body": """
        query = str(req.get("q") or "").strip().lower()
        if not query:
            raise ValidationError("q is required")
        terms = [term for term in query.split() if term]
        hits = []
        for product in req.get("products") or []:
            name = str(product.get("name") or "").lower()
            description = str(product.get("description") or "").lower()
            tags = " ".join(str(tag).lower() for tag in product.get("tags") or [])
            score = 0
            for term in terms:
                if term in name:
                    score += 5
                if term in tags:
                    score += 3
                if term in description:
                    score += 1
            if score:
                hits.append({"sku": str(product.get("sku")), "score": score})
        hits.sort(key=lambda hit: (-hit["score"], hit["sku"]))
        limit = int(req.get("limit") or DEFAULT_LIMIT)
        return {"query": query, "hits": hits[:limit], "total": len(hits)}
        """,
    },
    {
        "module": "product_get",
        "title": "Fetch one product",
        "route": ("GET", "/api/products/{sku}"),
        "story": "Fetch a product by SKU.\n\nRetired products stay reachable so order history can still render them, but\nthey are marked unavailable so the buy button can be withdrawn.",
        "consts": "",
        "body": """
        sku = str(req.get("sku") or "").strip().upper()
        if not sku:
            raise ValidationError("sku is required")
        for product in req.get("products") or []:
            if str(product.get("sku") or "").upper() == sku:
                row = dict(product)
                row["purchasable"] = not row.get("retired", False) and int(row.get("stock") or 0) > 0
                return {"product": row}
        raise NotFoundError("no product %s" % sku)
        """,
    },
    {
        "module": "product_variants",
        "title": "List the variants of a product",
        "route": ("GET", "/api/products/{sku}/variants"),
        "story": "List a product's variants, optionally filtered by one attribute.\n\nVariants with no stock are kept in the answer but flagged, because the\nsize selector greys them out rather than hiding them.",
        "consts": "",
        "body": """
        parent = str(req.get("sku") or "").strip()
        if not parent:
            raise ValidationError("sku is required")
        wanted = req.get("attributes") or {}
        rows = []
        for variant in req.get("variants") or []:
            if str(variant.get("parent") or "") != parent:
                continue
            attributes = dict(variant.get("attributes") or {})
            if any(str(attributes.get(key)) != str(value) for key, value in wanted.items()):
                continue
            rows.append(
                {
                    "sku": str(variant.get("sku")),
                    "attributes": attributes,
                    "in_stock": int(variant.get("stock") or 0) > 0,
                }
            )
        if not rows:
            raise NotFoundError("no variants for %s" % parent)
        rows.sort(key=lambda row: row["sku"])
        return {"variants": rows, "count": len(rows)}
        """,
    },
    {
        "module": "subscription_pause",
        "title": "Pause a subscription",
        "route": ("POST", "/api/subscriptions/{id}/pause"),
        "story": "Pause a subscription for a bounded number of days.\n\nPausing forever is deliberately not offered: a customer who wants to stop\nshould cancel, which keeps the churn number honest.",
        "consts": "MAX_PAUSE_DAYS = 90",
        "body": """
        subscription = dict(req.get("subscription") or {})
        if not subscription.get("id"):
            raise ValidationError("a subscription is required")
        if str(subscription.get("state")) == "cancelled":
            raise ValidationError("a cancelled subscription cannot be paused")
        days = int(req.get("days") or 30)
        if not 1 <= days <= MAX_PAUSE_DAYS:
            raise ValidationError("a pause is between 1 and %d days" % MAX_PAUSE_DAYS)
        subscription["state"] = "paused"
        subscription["paused_days"] = days
        return {"subscription": subscription, "resumes_in_days": days}
        """,
    },
    {
        "module": "giftcard_redeem",
        "title": "Spend a gift card",
        "route": ("POST", "/api/giftcards/redeem"),
        "story": "Draw down a gift card balance against an amount due.\n\nThe card is spent only up to what is owed, and whatever is left stays on the\ncard for next time.",
        "consts": "",
        "body": """
        card = dict(req.get("card") or {})
        code = str(card.get("code") or "").strip().upper()
        if not code:
            raise ValidationError("a gift card code is required")
        balance = int(card.get("balance_cents") or 0)
        if balance <= 0:
            raise ValidationError("gift card %s has no balance" % code)
        due = int(req.get("amount_cents") or balance)
        if due <= 0:
            raise ValidationError("amount_cents must be positive")
        applied = min(balance, due)
        card["balance_cents"] = balance - applied
        return {
            "card": card,
            "applied_cents": applied,
            "outstanding_cents": due - applied,
        }
        """,
    },
    {
        "module": "tax_quote",
        "title": "Quote sales tax for a region",
        "route": ("POST", "/api/tax/quote"),
        "story": "Quote tax for a subtotal in a region, with the shop default as fallback.\n\nRates are stored in basis points so a 7.25% rate is an integer 725 and the\narithmetic never touches a float.",
        "consts": 'from shop.config import settings\n\nREGION_RATES_BPS = {"US-CA": 725, "US-NY": 400, "GB": 2000, "DE": 1900}',
        "body": """
        subtotal = int(req.get("subtotal_cents") or 0)
        if subtotal < 0:
            raise ValidationError("subtotal_cents must not be negative")
        region = str(req.get("region") or "").strip().upper()
        fallback = int(getattr(settings, "tax_rate_bps", 0) or 0)
        rate = REGION_RATES_BPS.get(region, fallback)
        tax = (subtotal * rate + 5000) // 10000
        return {
            "region": region or "default",
            "rate_bps": rate,
            "subtotal_cents": subtotal,
            "tax_cents": tax,
            "total_cents": subtotal + tax,
        }
        """,
    },
]


def render_handler(spec):
    render(
        "shop/api/handlers/%s.py" % spec["module"],
        '''
        """@@STORY@@"""

        from shop.errors import NotFoundError, ValidationError

        __all__ = ["HANDLE", "ROUTE", "handle"]

        HANDLE = "@@MODULE@@"
        ROUTE = @@ROUTE@@
        @@CONSTS@@


        def handle(request: dict) -> dict:
            """@@TITLE@@."""
            req = dict(request or {})
        @@BODY@@
        ''',
        STORY=spec["story"],
        MODULE=spec["module"],
        ROUTE=repr(spec["route"]),
        CONSTS=spec["consts"],
        TITLE=spec["title"],
        BODY=block(spec["body"], 4),
    )


def render_api():
    """shop/api: handlers, the route table and the middleware chain."""
    for spec in HANDLERS:
        render_handler(spec)
    render(
        "shop/api/handlers/__init__.py",
        '''
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
        ''',
    )
    render(
        "shop/api/routes.py",
        '''
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
        ''',
    )
    render(
        "shop/api/middleware.py",
        '''
        """The small middleware chain every request runs through.

        Order matters: a request id is minted first so a failure deep in a handler
        can still be logged against something, and the error envelope is applied
        last so it always sees the id.
        """

        import hashlib

        from shop.errors import NotFoundError, ShopError, ValidationError

        __all__ = ["ERROR_STATUS", "authenticate", "envelope", "request_id", "timed"]

        ERROR_STATUS = {ValidationError: 422, NotFoundError: 404, ShopError: 400}
        PUBLIC_PREFIXES = ("/api/products", "/api/shipping/quote")


        def request_id(method: str, path: str, body: dict) -> str:
            """A stable request id, derived so a replay produces the same id."""
            seed = "%s %s %s" % (str(method).upper(), path, sorted(body or {}))
            return "req-" + hashlib.sha1(seed.encode("utf-8")).hexdigest()[:12]


        def authenticate(path: str, headers: dict) -> dict:
            """Public routes pass through; everything else needs a bearer token."""
            headers = dict(headers or {})
            if str(path).startswith(PUBLIC_PREFIXES):
                return {"actor": "anonymous", "public": True}
            token = str(headers.get("authorization") or "")
            if not token.startswith("Bearer ") or len(token) <= 7:
                raise ValidationError("a bearer token is required")
            return {"actor": token[7:].strip(), "public": False}


        def status_for(error: Exception) -> int:
            """The HTTP status an exception maps to."""
            for kind, status in ERROR_STATUS.items():
                if isinstance(error, kind):
                    return status
            return 500


        def envelope(error: Exception, rid: str) -> dict:
            """The JSON body an error is answered with."""
            return {
                "error": {
                    "type": type(error).__name__,
                    "message": str(error),
                    "status": status_for(error),
                    "request_id": rid,
                }
            }


        def timed(start_ns: int, end_ns: int) -> dict:
            """A timing record for the access log, in whole microseconds."""
            elapsed = max(0, int(end_ns) - int(start_ns))
            return {"micros": elapsed // 1000, "slow": elapsed > 250000000}
        ''',
    )
    render(
        "shop/api/__init__.py",
        '''
        """The JSON API: handlers, the route table and the middleware chain."""

        from shop.api import handlers, middleware, routes

        __all__ = ["handlers", "middleware", "routes"]
        ''',
    )


# --------------------------------------------------------------------------
# shop/integrations/vendors -- 40 marketplace adapters over an in-memory queue.
# --------------------------------------------------------------------------

CANONICAL_STATUSES = (
    "new",
    "pending",
    "paid",
    "fulfilling",
    "shipped",
    "delivered",
    "cancelled",
    "refunded",
)

OPEN_STATUSES = ("new", "pending", "paid", "fulfilling", "shipped")

VENDORS = [
    ("shopify", "Shopify", ("pending", "paid", "fulfilled", "cancelled", "refunded"), ("new", "paid", "shipped", "cancelled", "refunded")),
    ("woocommerce", "WooCommerce", ("pending", "processing", "completed", "cancelled", "refunded"), ("new", "paid", "delivered", "cancelled", "refunded")),
    ("bigcommerce", "BigCommerce", ("pending", "awaiting_payment", "shipped", "cancelled"), ("new", "pending", "shipped", "cancelled")),
    ("magento", "Magento", ("pending", "processing", "complete", "canceled", "closed"), ("new", "paid", "delivered", "cancelled", "refunded")),
    ("squarespace", "Squarespace", ("pending", "fulfilled", "canceled"), ("new", "shipped", "cancelled")),
    ("wix", "Wix", ("pending", "paid", "fulfilled", "canceled", "refunded"), ("new", "paid", "shipped", "cancelled", "refunded")),
    ("salesforce_cc", "Salesforce CC", ("created", "exported", "shipped", "cancelled"), ("new", "paid", "shipped", "cancelled")),
    ("adobe_commerce", "Adobe Commerce", ("pending", "processing", "complete", "canceled"), ("new", "paid", "delivered", "cancelled")),
    ("shopware", "Shopware", ("open", "in_progress", "completed", "cancelled"), ("new", "fulfilling", "delivered", "cancelled")),
    ("prestashop", "PrestaShop", ("awaiting_check", "payment_accepted", "shipped", "canceled"), ("new", "paid", "shipped", "cancelled")),
    ("opencart", "OpenCart", ("pending", "processing", "shipped", "canceled"), ("new", "paid", "shipped", "cancelled")),
    ("volusion", "Volusion", ("new", "processing", "shipped", "voided"), ("new", "fulfilling", "shipped", "cancelled")),
    ("big_cartel", "Big Cartel", ("pending", "processed", "shipped", "cancelled"), ("new", "paid", "shipped", "cancelled")),
    ("ecwid", "Ecwid", ("pending", "paid", "shipped", "cancelled", "refunded"), ("new", "paid", "shipped", "cancelled", "refunded")),
    ("shift4shop", "Shift4Shop", ("new", "processing", "shipped", "cancelled"), ("new", "fulfilling", "shipped", "cancelled")),
    ("nopcommerce", "nopCommerce", ("pending", "processing", "complete", "cancelled"), ("new", "paid", "delivered", "cancelled")),
    ("sylius", "Sylius", ("cart", "new", "fulfilled", "cancelled"), ("pending", "new", "delivered", "cancelled")),
    ("spree", "Spree", ("cart", "complete", "canceled", "returned"), ("pending", "paid", "cancelled", "refunded")),
    ("medusa", "Medusa", ("pending", "completed", "canceled", "requires_action"), ("new", "paid", "cancelled", "pending")),
    ("vendure", "Vendure", ("adding_items", "arranging_payment", "delivered", "cancelled"), ("pending", "paid", "delivered", "cancelled")),
    ("reaction", "Reaction Commerce", ("new", "core_order_approved", "shipped", "canceled"), ("new", "paid", "shipped", "cancelled")),
    ("saleor", "Saleor", ("unfulfilled", "partially_fulfilled", "fulfilled", "canceled"), ("paid", "fulfilling", "delivered", "cancelled")),
    ("oscommerce", "osCommerce", ("pending", "preparing", "delivered", "canceled"), ("new", "fulfilling", "delivered", "cancelled")),
    ("zen_cart", "Zen Cart", ("pending", "processing", "shipped", "cancelled"), ("new", "paid", "shipped", "cancelled")),
    ("cs_cart", "CS-Cart", ("open", "processed", "shipped", "declined"), ("new", "paid", "shipped", "cancelled")),
    ("thirty_bees", "Thirty Bees", ("waiting", "paid", "sent", "canceled"), ("new", "paid", "shipped", "cancelled")),
    ("lightspeed", "Lightspeed", ("quoted", "invoiced", "delivered", "voided"), ("new", "paid", "delivered", "cancelled")),
    ("linnworks", "Linnworks", ("pending", "processing", "shipped", "cancelled"), ("new", "fulfilling", "shipped", "cancelled")),
    ("brightpearl", "Brightpearl", ("open", "paid", "shipped", "closed"), ("new", "paid", "shipped", "refunded")),
    ("netsuite", "NetSuite", ("pending_approval", "pending_fulfillment", "billed", "closed"), ("new", "fulfilling", "paid", "refunded")),
    ("odoo", "Odoo", ("draft", "sale", "done", "cancel"), ("new", "paid", "delivered", "cancelled")),
    ("erply", "Erply", ("draft", "confirmed", "fulfilled", "voided"), ("new", "paid", "delivered", "cancelled")),
    ("cin7", "Cin7", ("draft", "authorised", "shipped", "voided"), ("new", "paid", "shipped", "cancelled")),
    ("dear_systems", "DEAR Systems", ("draft", "authorised", "packed", "voided"), ("new", "paid", "fulfilling", "cancelled")),
    ("katana", "Katana", ("draft", "in_progress", "delivered", "cancelled"), ("new", "fulfilling", "delivered", "cancelled")),
    ("tradegecko", "TradeGecko", ("draft", "active", "fulfilled", "void"), ("new", "paid", "delivered", "cancelled")),
    ("skubana", "Skubana", ("received", "allocated", "shipped", "cancelled"), ("new", "fulfilling", "shipped", "cancelled")),
    ("extensiv", "Extensiv", ("draft", "approved", "shipped", "cancelled"), ("new", "paid", "shipped", "cancelled")),
    ("channel_advisor", "ChannelAdvisor", ("pending", "processing", "shipped", "cancelled"), ("new", "fulfilling", "shipped", "cancelled")),
    ("sellercloud", "SellerCloud", ("new", "processing", "shipped", "cancelled"), ("new", "fulfilling", "shipped", "cancelled")),
]

# Ten different ways to hand a queue back, and four different ways to accept a
# status write. Every vendor pairs one of each, so no two adapters share a body.
FETCH_STYLES = [
    (
        "Newest first, cut to one page.",
        '''
        rows = sorted(self._orders, key=lambda row: row["created_at"], reverse=True)
        return [dict(row) for row in rows[:PAGE_SIZE]]
        ''',
    ),
    (
        "One row per order id, the last write winning.",
        '''
        latest = {}
        for row in self._orders:
            latest[row["order_id"]] = row
        return [dict(latest[order_id]) for order_id in sorted(latest)]
        ''',
    ),
    (
        "Only orders that have not settled yet.",
        '''
        return [dict(row) for row in self._orders if row["status"] in ACTIVE_STATUSES]
        ''',
    ),
    (
        "Every row, with the native status mapped to ours.",
        '''
        out = []
        for row in self._orders:
            record = dict(row)
            record["status"] = STATUS_ALIASES.get(row["status"], "unknown")
            out.append(record)
        return out
        ''',
    ),
    (
        "One page per call; the cursor wraps around at the end.",
        '''
        start = self._cursor
        page = self._orders[start:start + PAGE_SIZE]
        self._cursor = 0 if start + PAGE_SIZE >= len(self._orders) else start + PAGE_SIZE
        return [dict(row) for row in page]
        ''',
    ),
    (
        "Only orders worth syncing, above the vendor minimum.",
        '''
        return [dict(row) for row in self._orders if int(row["total_cents"]) >= MIN_TOTAL_CENTS]
        ''',
    ),
    (
        "Read through the id index, ordered by order id.",
        '''
        self._by_id = {row["order_id"]: dict(row) for row in self._orders}
        return [self._by_id[order_id] for order_id in sorted(self._by_id)]
        ''',
    ),
    (
        "Every row, with the unit count lifted out of the vendor shape.",
        '''
        out = []
        for row in self._orders:
            record = dict(row)
            record["unit_count"] = int(record.pop("units", 0))
            out.append(record)
        return out
        ''',
    ),
    (
        "One sales channel only, set on the client.",
        '''
        return [dict(row) for row in self._orders if row["channel"] == self.channel]
        ''',
    ),
    (
        "The queue in arrival order, each row numbered.",
        '''
        return [dict(row, position=index) for index, row in enumerate(self._orders, start=1)]
        ''',
    ),
]

PUSH_STYLES = [
    (
        "Write the status back into the queue after a linear scan.",
        '''
        for row in self._orders:
            if row["order_id"] == order_id:
                row["status"] = status
                return {"order_id": order_id, "status": status, "vendor": VENDOR}
        raise ValidationError("%s does not know order %r" % (VENDOR, order_id))
        ''',
    ),
    (
        "Write through the id index, failing on an unknown id first.",
        '''
        if order_id not in self._by_id:
            raise ValidationError("%s does not know order %r" % (VENDOR, order_id))
        self._by_id[order_id]["status"] = status
        return {"order_id": order_id, "status": status, "indexed": True}
        ''',
    ),
    (
        "Translate to the canonical status before writing it back.",
        '''
        canonical = STATUS_ALIASES.get(status, status)
        if canonical not in CANONICAL_STATUSES:
            raise ValidationError("%s cannot represent status %r" % (VENDOR, status))
        for row in self._orders:
            if row["order_id"] == order_id:
                row["status"] = status
                return {"order_id": order_id, "status": canonical, "native": status}
        raise ValidationError("%s does not know order %r" % (VENDOR, order_id))
        ''',
    ),
    (
        "Write the status and remember the move for the audit trail.",
        '''
        previous = None
        for row in self._orders:
            if row["order_id"] == order_id:
                previous = row["status"]
                row["status"] = status
                break
        else:
            raise ValidationError("%s does not know order %r" % (VENDOR, order_id))
        self.history.append({"order_id": order_id, "from": previous, "to": status})
        return {"order_id": order_id, "status": status, "previous": previous, "moves": len(self.history)}
        ''',
    ),
]


def vendor_seed(index, slug, native):
    """Four deterministic orders for one vendor, in the vendor's own vocabulary."""
    prefix = "".join(part[0] for part in slug.split("_")).upper()[:3] or "VEN"
    rows = []
    for step in range(4):
        rows.append(
            {
                "order_id": "%s-%d" % (prefix, 7000 + index * 10 + step),
                "status": native[(index + step) % len(native)],
                "total_cents": 1500 + ((index * 7 + step * 13) % 40) * 125,
                "units": 1 + (index + step) % 4,
                "channel": ("web", "mobile", "marketplace")[(index + step) % 3],
                "created_at": "2026-02-%02dT%02d:%02d:00Z"
                % (2 + step, 8 + (index + step) % 10, (index * 3) % 60),
            }
        )
    return rows


def render_vendor(index, spec):
    """One marketplace adapter: constants, its seed queue, and a client class."""
    slug, label, native, canonical = spec
    aliases = dict(zip(native, canonical))
    active = tuple(status for status in canonical if status in OPEN_STATUSES) or ("new",)
    fetch_doc, fetch_code = FETCH_STYLES[index % len(FETCH_STYLES)]
    push_doc, push_code = PUSH_STYLES[index // len(FETCH_STYLES)]
    page_size = 10 + (index % 5) * 10
    min_total = 1000 + (index % 7) * 250
    doc = (
        "%s order sync.\n\nOrders leave %s in its own vocabulary -- %s -- so each row keeps\n"
        "the native status while ``STATUS_ALIASES`` publishes ours. The queue\nlives in memory: no credentials, no network, and a test can drive a full\nsync in a few milliseconds."
        % (label, label, ", ".join(native))
    )
    out = ['"""' + doc + '"""', "", "from shop.errors import ValidationError", ""]
    out.append('VENDOR = "%s"' % slug)
    out.append('LABEL = "%s"' % label)
    out.append('AUTH_HEADER = "X-%s-Token"' % label)
    out.append("PAGE_SIZE = %d" % page_size)
    out.append("MIN_TOTAL_CENTS = %d" % min_total)
    out.append("CANONICAL_STATUSES = %s" % as_literal(CANONICAL_STATUSES))
    out.append("ACTIVE_STATUSES = %s" % as_literal(active))
    out.append("STATUS_ALIASES = %s" % as_literal(aliases))
    out.append("")
    out.append("SEED = %s" % as_literal(vendor_seed(index, slug, native)))
    out.extend(
        [
            "",
            "",
            "class Client:",
            '    """Talk to a %s store without leaving the process."""' % label,
            "",
            '    def __init__(self, token: str = "", channel: str = "web"):',
            '        """Seed the queue, the id index and the write history."""',
            "        self.token = token",
            "        self.channel = channel",
            "        self._orders = [dict(row) for row in SEED]",
            '        self._by_id = {row["order_id"]: row for row in self._orders}',
            "        self._cursor = 0",
            "        self.history = []",
            "",
            "    def fetch_orders(self) -> list:",
            block('"""' + fetch_doc + '"""', indent=8),
            block(fetch_code, indent=8),
            "",
            "    def push_status(self, order_id: str, status: str) -> dict:",
            block('"""' + push_doc + '"""', indent=8),
            block(push_code, indent=8),
        ]
    )
    emit("shop/integrations/vendors/%s.py" % slug, "\n".join(out) + "\n")


def render_integrations():
    """shop/integrations: 40 marketplace adapters and their registry."""
    for index, spec in enumerate(VENDORS):
        render_vendor(index, spec)
    render(
        "shop/integrations/vendors/__init__.py",
        '''
        """Marketplace adapters.

        Each module exposes the same small surface, so the sync worker never has to
        know which marketplace it is talking to::

            VENDOR                       the slug we key the feed on
            Client().fetch_orders()      the queue as this vendor sends it
            Client().push_status(id, s)  a status write, answered in canonical form

        A vendor that does not know an order id raises ``ValidationError``; the
        worker treats that as "skip and retry later", never as a hard failure.
        """

        import importlib
        import pkgutil

        __all__ = ["all_vendors", "client_for", "vendor_names"]


        def vendor_names() -> list:
            """Adapter module names in this package."""
            found = [info.name for info in pkgutil.iter_modules(__path__)]
            return sorted(name for name in found if not name.startswith("_"))


        def all_vendors() -> list:
            """Every adapter, ordered by vendor slug."""
            modules = [importlib.import_module(__name__ + "." + name) for name in vendor_names()]
            return sorted(modules, key=lambda module: module.VENDOR)


        def client_for(name: str):
            """A fresh client for a vendor slug, or ``None`` when unregistered."""
            for module in all_vendors():
                if module.VENDOR == name:
                    return module.Client()
            return None
        ''',
    )
    render(
        "shop/integrations/__init__.py",
        '''
        """Feeds from the marketplaces this store sells through."""

        from shop.integrations import vendors

        __all__ = ["vendors"]
        ''',
    )


# --------------------------------------------------------------------------
# shop/notifications -- text templates and the channels that carry them.
# --------------------------------------------------------------------------

NOTIFICATION_DEFAULTS = {
    "address_line": "12 Alder Way, Springfield",
    "amount": "$12.00",
    "carrier": "Northpost",
    "coupon_code": "WELCOME10",
    "discount": "10%",
    "eta": "Thursday",
    "expires_on": "2026-03-31",
    "first_name": "Ada",
    "invoice_number": "INV-88121",
    "item_name": "torque wrench",
    "order_id": "ORD-1042",
    "points": "320",
    "quantity": "3",
    "reset_url": "https://example.com/reset/abc",
    "review_url": "https://example.com/review/abc",
    "store_name": "Ogcode Supply",
    "support_email": "help@example.com",
    "total": "$36.00",
    "tracking_number": "NP-55809",
    "unsubscribe_url": "https://example.com/unsubscribe",
}

TEMPLATES = [
    (
        "order-confirmation",
        '''
        Hi {first_name},

        Thanks for order {order_id}. We charged {total} and will ship to:

            {address_line}

        You get a tracking number as soon as {carrier} picks the parcel up.

        Questions? Write to {support_email}.

        {store_name}
        ''',
    ),
    (
        "order-shipped",
        '''
        Hi {first_name},

        Order {order_id} left the warehouse with {carrier}.

        Tracking number: {tracking_number}
        Estimated arrival: {eta}

        {store_name}
        ''',
    ),
    (
        "order-delivered",
        '''
        Hi {first_name},

        Order {order_id} was delivered. We hope the {item_name} is what you wanted.

        Tell us how it went: {review_url}

        {store_name}
        ''',
    ),
    (
        "order-cancelled",
        '''
        Hi {first_name},

        Order {order_id} is cancelled. A refund of {amount} is on its way back to
        the card you paid with.

        {store_name}
        ''',
    ),
    (
        "payment-failed",
        '''
        Hi {first_name},

        We could not charge the card on order {order_id} for {total}. The items stay
        in your cart for another 24 hours.

        Update the card, or reply to {support_email} and we will help.

        {store_name}
        ''',
    ),
    (
        "payment-receipt",
        '''
        Hi {first_name},

        Payment received. Invoice {invoice_number} covers {total} on order {order_id}.

        {store_name}
        ''',
    ),
    (
        "refund-issued",
        '''
        Hi {first_name},

        We refunded {amount} for order {order_id}. Banks usually take five business
        days to show it.

        {store_name}
        ''',
    ),
    (
        "return-approved",
        '''
        Hi {first_name},

        The return for order {order_id} is approved. Pack the {item_name} and use the
        label from the earlier email.

        {store_name}
        ''',
    ),
    (
        "return-received",
        '''
        Hi {first_name},

        The {item_name} from order {order_id} is back at the warehouse. Your refund of
        {amount} is queued.

        {store_name}
        ''',
    ),
    (
        "password-reset",
        '''
        Hi {first_name},

        Use this link to set a new password: {reset_url}

        The link expires in one hour. If you did not ask for this, ignore this email.

        {store_name}
        ''',
    ),
    (
        "welcome",
        '''
        Welcome, {first_name}.

        Your account at {store_name} is ready. Use {coupon_code} for {discount} off the
        first order, before {expires_on}.

        Unsubscribe: {unsubscribe_url}
        ''',
    ),
    (
        "cart-abandoned",
        '''
        Hi {first_name},

        The {item_name} in your cart is still waiting. Finish the order and {discount}
        comes off the total.

        {store_name}
        ''',
    ),
    (
        "back-in-stock",
        '''
        Hi {first_name},

        {item_name} is back in stock and we held your size for 48 hours.

        {store_name}
        ''',
    ),
    (
        "review-request",
        '''
        Hi {first_name},

        How did the {item_name} work out? A short review helps the next customer
        decide.

        {review_url}

        {store_name}
        ''',
    ),
    (
        "loyalty-points",
        '''
        Hi {first_name},

        You have {points} points waiting, worth {discount} off the next order.

        {store_name}
        ''',
    ),
    (
        "tier-upgrade",
        '''
        Hi {first_name},

        You moved up a tier. Shipping is free from now on and every order earns
        double points.

        {store_name}
        ''',
    ),
    (
        "gift-card",
        '''
        Hi {first_name},

        A gift card worth {amount} is ready against order {order_id}. Use {coupon_code}
        at checkout before {expires_on}.

        {store_name}
        ''',
    ),
    (
        "subscription-renewed",
        '''
        Hi {first_name},

        The subscription for {item_name} renewed. We charged {total} and the next box
        ships to {address_line}.

        {store_name}
        ''',
    ),
    (
        "subscription-paused",
        '''
        Hi {first_name},

        The subscription for {item_name} is paused. Resume whenever you like and we
        ship within two days.

        {store_name}
        ''',
    ),
    (
        "winback-offer",
        '''
        Hi {first_name},

        We saved {coupon_code} for you: {discount} off any order placed before
        {expires_on}. {quantity} new arrivals landed this week.

        {store_name}
        ''',
    ),
]

SENDERS = [
    (
        "email",
        "Transactional email through the receipt provider.",
        '''
        import hashlib

        from shop.errors import ValidationError

        __all__ = ["CHANNEL", "REQUIRED", "send"]

        CHANNEL = "email"
        REQUIRED = ("to", "subject", "body")


        def send(message: dict) -> dict:
            """Accept a message and answer with the provider's receipt."""
            msg = dict(message or {})
            missing = [key for key in REQUIRED if not msg.get(key)]
            if missing:
                raise ValidationError("email needs %s" % ", ".join(missing))
            recipient = str(msg["to"])
            if "@" not in recipient:
                raise ValidationError("not an email address: %r" % (recipient,))
            seed = "%s|%s" % (recipient, msg["subject"])
            digest = hashlib.sha1(seed.encode("utf-8")).hexdigest()
            return {
                "channel": CHANNEL,
                "to": recipient,
                "accepted": True,
                "message_id": "em-" + digest[:10],
            }
        ''',
    ),
    (
        "sms",
        "Short message delivery, split into 160-character parts.",
        '''
        from shop.errors import ValidationError

        __all__ = ["CHANNEL", "PART_CHARS", "send"]

        CHANNEL = "sms"
        PART_CHARS = 160


        def send(message: dict) -> dict:
            """Send a text, reporting how many parts it needed."""
            msg = dict(message or {})
            recipient = str(msg.get("to") or "")
            digits = "".join(char for char in recipient if char.isdigit())
            if len(digits) < 7:
                raise ValidationError("not a phone number: %r" % (recipient,))
            body = str(msg.get("body") or msg.get("subject") or "")
            if not body:
                raise ValidationError("sms needs a body")
            parts = max(1, -(-len(body) // PART_CHARS))
            return {
                "channel": CHANNEL,
                "to": "+" + digits,
                "accepted": True,
                "parts": parts,
                "truncated": len(body) > PART_CHARS,
            }
        ''',
    ),
    (
        "push",
        "Mobile push notification, title and body only.",
        '''
        from shop.errors import ValidationError

        __all__ = ["CHANNEL", "TITLE_CHARS", "send"]

        CHANNEL = "push"
        TITLE_CHARS = 60


        def send(message: dict) -> dict:
            """Cut the title to what a lock screen shows."""
            msg = dict(message or {})
            title = str(msg.get("subject") or msg.get("title") or "").strip()
            if not title:
                raise ValidationError("push needs a title")
            body = str(msg.get("body") or "")
            return {
                "channel": CHANNEL,
                "title": title[:TITLE_CHARS],
                "preview": body[:120],
                "accepted": True,
                "collapsed": len(title) > TITLE_CHARS,
            }
        ''',
    ),
    (
        "slack",
        "Channel post, rendered as Block Kit blocks.",
        '''
        from shop.errors import ValidationError

        __all__ = ["CHANNEL", "send"]

        CHANNEL = "slack"


        def send(message: dict) -> dict:
            """Turn the message into the two blocks a channel post needs."""
            msg = dict(message or {})
            body = str(msg.get("body") or "").strip()
            if not body:
                raise ValidationError("slack needs a body")
            blocks = [
                {"type": "header", "text": {"type": "plain_text", "text": str(msg.get("subject") or CHANNEL)}},
                {"type": "section", "text": {"type": "mrkdwn", "text": body[:3000]}},
            ]
            return {
                "channel": CHANNEL,
                "target": str(msg.get("to") or "#operations"),
                "blocks": blocks,
                "accepted": True,
            }
        ''',
    ),
    (
        "webhook",
        "Signed POST to a partner endpoint, queued not delivered.",
        '''
        import hashlib
        import json

        from shop.errors import ValidationError

        __all__ = ["CHANNEL", "send"]

        CHANNEL = "webhook"


        def send(message: dict) -> dict:
            """Sign the payload and report the key ordering used."""
            msg = dict(message or {})
            if not msg:
                raise ValidationError("webhook needs a payload")
            payload = json.dumps(msg, sort_keys=True, separators=(",", ":"))
            signature = hashlib.sha256(payload.encode("utf-8")).hexdigest()
            return {
                "channel": CHANNEL,
                "accepted": True,
                "signature": signature[:16],
                "bytes": len(payload.encode("utf-8")),
                "keys": sorted(msg),
            }
        ''',
    ),
    (
        "digest",
        "One daily summary standing in for several notifications.",
        '''
        from shop.errors import ValidationError

        __all__ = ["CHANNEL", "send"]

        CHANNEL = "digest"


        def send(message: dict) -> dict:
            """Collapse a message, or a list of items, into one summary."""
            msg = dict(message or {})
            items = list(msg.get("items") or [])
            if not items:
                subject = str(msg.get("subject") or "").strip()
                if not subject:
                    raise ValidationError("digest needs items or a subject")
                items = [subject]
            joined = "; ".join(str(item) for item in items)
            return {
                "channel": CHANNEL,
                "count": len(items),
                "summary": joined[:200],
                "accepted": True,
            }
        ''',
    ),
]

USED_PLACEHOLDERS = set()
for _name, _text in TEMPLATES:
    found = set(re.findall(r"\{([a-z_]+)\}", _text))
    missing = found - set(NOTIFICATION_DEFAULTS)
    if missing:
        raise SystemExit("notification template %s needs defaults for %s" % (_name, sorted(missing)))
    USED_PLACEHOLDERS |= found


def render_notifications():
    """shop/notifications: the text templates, the renderer and the channels."""
    for name, text in TEMPLATES:
        body = textwrap.dedent(text).strip("\n") + "\n"
        emit("shop/notifications/templates/%s.txt" % name, body)
    defaults = {key: NOTIFICATION_DEFAULTS[key] for key in sorted(USED_PLACEHOLDERS)}
    render(
        "shop/notifications/render.py",
        '''
        """Fill a notification template from a context dict.

        Templates are plain text with ``{placeholder}`` holes. Every hole has a
        default, so a sender can render one with a partial context instead of
        failing halfway through a send.
        """

        from pathlib import Path

        from shop.errors import NotFoundError, ValidationError

        __all__ = ["DEFAULTS", "TEMPLATE_DIR", "list_templates", "render", "template_path"]

        TEMPLATE_DIR = Path(__file__).resolve().parent / "templates"

        DEFAULTS = @@DEFAULTS@@


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
        ''',
        DEFAULTS=as_literal(defaults, width=100),
    )
    for channel, doc, code in SENDERS:
        emit("shop/notifications/senders/%s.py" % channel, '"""' + doc + '"""\n\n' + textwrap.dedent(code).strip("\n") + "\n")
    render(
        "shop/notifications/senders/__init__.py",
        '''
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
        ''',
    )
    render(
        "shop/notifications/__init__.py",
        '''
        """Customer notifications: text templates and the channels that ship them."""

        from shop.notifications import render, senders

        __all__ = ["render", "senders"]
        ''',
    )


def render_search():
    """shop/search: tokenizing, indexing, scoring, facets and suggestions."""
    render(
        "shop/search/tokenizer.py",
        '''
        """Split user text into search tokens.

        The indexer and the query parser both run through here, so a document and
        the search that should find it agree on what a word is: lower case,
        letters and digits only, apostrophes removed (``don't`` becomes ``dont``)
        and hyphens split (``t-shirt`` finds ``shirt``).
        """

        import re

        __all__ = ["MAX_TOKEN", "TOKEN_RE", "token_set", "tokenize"]

        TOKEN_RE = re.compile(r"[a-z0-9]+")

        MAX_TOKEN = 48


        def tokenize(text: str) -> list:
            """Lower-case tokens in the order they appear, each capped in length."""
            if not text:
                return []
            cleaned = str(text).lower().replace("'", "")
            return [token[:MAX_TOKEN] for token in TOKEN_RE.findall(cleaned)]


        def token_set(text: str) -> set:
            """The distinct tokens of ``text``, for set-based scoring."""
            return set(tokenize(text))
        ''',
    )
    render(
        "shop/search/stopwords.py",
        '''
        """Words too common to carry meaning in a product search.

        Stop words are dropped when a document is indexed and again when a query is
        parsed, so ``the red shoes`` and ``red shoes`` are the same search. The set
        is intentionally small: dropping an adjective that shoppers actually use
        loses more recall than keeping a filler word costs precision.
        """

        __all__ = ["STOPWORDS", "content_words", "is_stopword"]

        STOPWORDS = frozenset(
            (
                "a", "about", "after", "all", "also", "an", "and", "any", "are",
                "as", "at", "be", "been", "but", "by", "can", "do", "does", "for",
                "from", "had", "has", "have", "how", "if", "in", "into", "is", "it",
                "its", "just", "more", "most", "my", "no", "not", "of", "on", "or",
                "our", "out", "over", "same", "she", "so", "some", "than", "that",
                "the", "their", "them", "then", "there", "these", "they", "this",
                "those", "to", "too", "up", "us", "very", "was", "we", "were",
                "what", "when", "where", "which", "who", "why", "will", "with", "you",
                "your",
            )
        )


        def is_stopword(token: str) -> bool:
            """True for a word the search engine ignores."""
            return token in STOPWORDS


        def content_words(tokens) -> list:
            """The tokens that survive the stop list, order preserved."""
            return [token for token in tokens if token not in STOPWORDS]
        ''',
    )
    render(
        "shop/search/normalizer.py",
        '''
        """Fold text down to the terms the index holds.

        Accents are folded (``café`` -> ``cafe``), stop words disappear and a crude
        English singulariser folds plurals, so ``shoes`` and ``shoe`` hit the same
        postings. The rules are deliberately conservative: a word that does not
        look plural is left exactly as the shopper typed it.
        """

        import unicodedata

        from shop.search.stopwords import content_words
        from shop.search.tokenizer import tokenize

        __all__ = ["fold", "normalize", "singular"]

        PLURALS = (
            ("ies", "y"),
            ("ches", "ch"),
            ("shes", "sh"),
            ("xes", "x"),
            ("zes", "z"),
            ("ses", "s"),
        )


        def fold(text: str) -> str:
            """Strip combining marks so accented text compares equal to plain text."""
            decomposed = unicodedata.normalize("NFKD", str(text or ""))
            return "".join(ch for ch in decomposed if not unicodedata.combining(ch))


        def singular(token: str) -> str:
            """A best-effort singular form, returned unchanged when unsure."""
            word = str(token)
            if len(word) <= 3 or not word.endswith("s") or word.endswith("ss"):
                return word
            for suffix, replacement in PLURALS:
                if word.endswith(suffix):
                    return word[: -len(suffix)] + replacement
            return word[:-1]


        def normalize(text: str) -> list:
            """The indexable terms of ``text``, in order, duplicates kept."""
            return [singular(token) for token in content_words(tokenize(fold(text)))]
        ''',
    )
    render(
        "shop/search/synonyms.py",
        '''
        """Merchandising synonyms.

        A shopper types ``sneakers``; the catalogue says ``trainers``. Each group
        below has a canonical head and lookups go through that head, so either
        spelling reaches the same postings. Heads are stored in the singular form
        the normaliser produces, which is why they all end without an ``s``.
        """

        __all__ = ["GROUPS", "HEAD", "canonical", "expand", "known"]

        GROUPS = (
            ("trainer", ("sneaker", "running shoe", "athletic shoe")),
            ("jumper", ("sweater", "pullover", "knitwear")),
            ("trouser", ("pant", "slack", "jean")),
            ("bag", ("backpack", "rucksack", "daypack")),
            ("mug", ("cup", "tumbler", "beaker")),
            ("lamp", ("light", "lantern", "sconce")),
            ("charger", ("adapter", "power supply", "psu")),
            ("headphone", ("earphone", "earbud", "headset")),
            ("notebook", ("journal", "pad", "notepad")),
            ("wallet", ("purse", "billfold", "cardholder")),
        )

        HEAD = {}
        for _head, _members in GROUPS:
            HEAD[_head] = _head
            for _member in _members:
                HEAD[_member] = _head


        def canonical(term: str) -> str:
            """The group head for a term, or the term itself when it has no group."""
            word = str(term).strip().lower()
            return HEAD.get(word, word)


        def expand(term: str) -> frozenset:
            """Every spelling that should match ``term``, canonical included."""
            head = canonical(term)
            for name, members in GROUPS:
                if name == head:
                    return frozenset((head,) + tuple(members))
            return frozenset((head,))


        def known() -> list:
            """Every group head, sorted, for the admin synonym screen."""
            return sorted(name for name, _ in GROUPS)
        ''',
    )
    render(
        "shop/search/index.py",
        '''
        """An in-memory inverted index over product text.

        Documents are added by id, so re-indexing a product replaces its postings
        instead of doubling them. Term frequencies are kept per document because
        the BM25 ranker needs them; nothing here touches the disk.
        """

        from shop.search.normalizer import normalize

        __all__ = ["InvertedIndex"]


        class InvertedIndex:
            """Term to ``{document id: term frequency}`` plus per-document lengths."""

            def __init__(self):
                self._postings = {}
                self._lengths = {}

            def add(self, doc_id: str, text: str) -> int:
                """Index one document, replacing any earlier version of it."""
                self.remove(doc_id)
                tokens = normalize(text)
                self._lengths[doc_id] = len(tokens)
                for token in tokens:
                    bucket = self._postings.setdefault(token, {})
                    bucket[doc_id] = bucket.get(doc_id, 0) + 1
                return len(tokens)

            def remove(self, doc_id: str) -> bool:
                """Drop a document; ``False`` when it was never indexed."""
                if self._lengths.pop(doc_id, None) is None:
                    return False
                for term in list(self._postings):
                    bucket = self._postings[term]
                    bucket.pop(doc_id, None)
                    if not bucket:
                        del self._postings[term]
                return True

            def postings(self, term: str) -> dict:
                """A copy of the term's document frequencies, never the live dict."""
                return dict(self._postings.get(term, {}))

            def doc_length(self, doc_id: str) -> int:
                """How many tokens the document carried when it was indexed."""
                return int(self._lengths.get(doc_id, 0))

            def doc_freq(self, term: str) -> int:
                """How many documents carry the term."""
                return len(self._postings.get(term, {}))

            @property
            def doc_count(self) -> int:
                """How many documents the index holds."""
                return len(self._lengths)

            def avg_length(self) -> float:
                """Mean document length in tokens, zero for an empty index."""
                if not self._lengths:
                    return 0.0
                return sum(self._lengths.values()) / len(self._lengths)

            def terms(self) -> list:
                """Every indexed term, sorted."""
                return sorted(self._postings)

            def doc_ids(self) -> list:
                """Every indexed document id, sorted."""
                return sorted(self._lengths)
        ''',
    )
    render(
        "shop/search/fuzzy.py",
        '''
        """Edit-distance helpers behind the did-you-mean suggestion.

        The distance is the classic Levenshtein edit count computed with two rows
        instead of a full matrix, so suggesting a correction for a 30 character
        query stays cheap enough to run inside a request.
        """

        __all__ = ["closest", "distance", "within"]


        def distance(left: str, right: str) -> int:
            """The number of inserts, deletes and substitutions between two words."""
            first = str(left or "")
            second = str(right or "")
            if first == second:
                return 0
            if not first:
                return len(second)
            if not second:
                return len(first)
            previous = list(range(len(second) + 1))
            for row, char in enumerate(first, start=1):
                current = [row]
                for column, other in enumerate(second, start=1):
                    cost = 0 if char == other else 1
                    current.append(
                        min(previous[column] + 1, current[column - 1] + 1, previous[column - 1] + cost)
                    )
                previous = current
            return previous[-1]


        def within(left: str, right: str, max_distance: int = 2) -> bool:
            """Whether two words are close enough to be the same misspelling."""
            first = str(left or "").lower()
            second = str(right or "").lower()
            if abs(len(first) - len(second)) > abs(int(max_distance)):
                return False
            return distance(first, second) <= abs(int(max_distance))


        def closest(term: str, vocabulary, max_distance: int = 2, limit: int = 5) -> list:
            """Vocabulary entries near ``term``, nearest first, alphabetically on a tie."""
            word = str(term or "").strip().lower()
            if not word:
                return []
            scored = []
            for candidate in vocabulary:
                other = str(candidate).strip().lower()
                steps = distance(word, other)
                if steps <= abs(int(max_distance)):
                    scored.append((steps, len(other), other))
            scored.sort()
            return [other for _steps, _length, other in scored[: max(0, int(limit))]]
        ''',
    )
    render(
        "shop/search/matcher.py",
        '''
        """Score a document against a query, synonyms included.

        The score is the share of the query's terms the document carries, after
        both sides are folded and expanded through the synonym groups. It answers
        the question the storefront asks first (``should this product appear?'') and
        leaves BM25 ordering to :mod:`shop.search.ranker`.
        """

        from shop.search.normalizer import normalize
        from shop.search.synonyms import expand

        __all__ = ["expand_terms", "missing", "score"]


        def expand_terms(terms) -> set:
            """Every spelling accepted for the given terms."""
            wanted = set()
            for term in terms:
                wanted.update(expand(term))
            return wanted


        def score(query: str, text: str) -> float:
            """Share of the query's terms the document carries, 0.0 to 1.0."""
            wanted = expand_terms(normalize(query))
            if not wanted:
                return 0.0
            present = set(normalize(text))
            if not present:
                return 0.0
            return round(len(wanted & present) / len(wanted), 4)


        def missing(query: str, text: str) -> list:
            """The query's terms the document does not carry, sorted."""
            wanted = expand_terms(normalize(query))
            present = set(normalize(text))
            return sorted(wanted - present)
        ''',
    )
    render(
        "shop/search/ranker.py",
        '''
        """BM25 ordering over the inverted index.

        BM25 rewards a document that repeats a query term and penalises one that is
        long enough to mention everything, which is exactly the behaviour the
        catalogue needs: a short, specific product page outranks a category blurb
        that happens to contain the same word.
        """

        import math

        from shop.search.normalizer import normalize

        __all__ = ["B", "K1", "bm25"]

        K1 = 1.5
        B = 0.75


        def bm25(index, query: str, limit: int = 10) -> list:
            """``(doc_id, score)`` pairs, best first, ties broken by document id."""
            terms = normalize(query)
            if not terms or not index.doc_count:
                return []
            total = index.doc_count
            average = index.avg_length() or 1.0
            scores = {}
            for term in terms:
                postings = index.postings(term)
                if not postings:
                    continue
                idf = math.log(1.0 + (total - len(postings) + 0.5) / (len(postings) + 0.5))
                for doc_id, frequency in postings.items():
                    length = index.doc_length(doc_id) or 1
                    norm = K1 * (1 - B + B * length / average)
                    weight = idf * frequency * (K1 + 1) / (frequency + norm)
                    scores[doc_id] = scores.get(doc_id, 0.0) + weight
            ranked = sorted(scores.items(), key=lambda row: (-row[1], str(row[0])))
            return [(doc_id, round(score, 6)) for doc_id, score in ranked[: max(0, int(limit))]]
        ''',
    )
    render(
        "shop/search/facets.py",
        '''
        """Facet counting and filtering for a result page.

        The search box hands back raw result dicts; the sidebar needs counts per
        value and the query string needs the filters applied. Both live here so the
        web layer never counts anything itself.
        """

        __all__ = ["apply_filters", "facet_counts", "top_values"]


        def facet_counts(values) -> dict:
            """How often each value appears, as a plain dict."""
            counts = {}
            for value in values:
                key = str(value)
                counts[key] = counts.get(key, 0) + 1
            return counts


        def top_values(values, limit: int = 5) -> list:
            """``(value, count)`` pairs, most common first, ties alphabetical."""
            ranked = sorted(facet_counts(values).items(), key=lambda row: (-row[1], row[0]))
            return ranked[: max(0, int(limit))]


        def apply_filters(items, filters) -> list:
            """Keep the items whose named fields hold any of the allowed values."""
            wanted = {
                str(key): {str(value) for value in values}
                for key, values in dict(filters or {}).items()
            }
            kept = []
            for item in items:
                if all(str(item.get(key)) in allowed for key, allowed in wanted.items()):
                    kept.append(item)
            return kept
        ''',
    )
    render(
        "shop/search/suggestions.py",
        '''
        """Type-ahead suggestions and a cheap did-you-mean.

        Prefix matches are ordered shortest first, so typing ``cha`` offers ``charger``
        before ``charging cable``. Did-you-mean only speaks up when a vocabulary word
        is within a couple of edits; silence is better than a wrong guess.
        """

        from shop.search.fuzzy import closest

        __all__ = ["MAX_SUGGESTIONS", "did_you_mean", "prefix_matches"]

        MAX_SUGGESTIONS = 8


        def prefix_matches(prefix: str, vocabulary, limit: int = MAX_SUGGESTIONS) -> list:
            """Vocabulary entries starting with ``prefix``, shortest first."""
            stem = str(prefix or "").strip().lower()
            if not stem:
                return []
            hits = [str(word) for word in vocabulary if str(word).lower().startswith(stem)]
            hits.sort(key=lambda word: (len(word), word))
            return hits[: max(0, int(limit))]


        def did_you_mean(term: str, vocabulary, max_distance: int = 2) -> str:
            """The nearest vocabulary entry, or an empty string when nothing is close."""
            hits = closest(term, vocabulary, max_distance=max_distance, limit=1)
            return hits[0] if hits else ""
        ''',
    )
    render(
        "shop/search/query.py",
        '''
        """Parse a shopper query into terms, phrases, exclusions and filters.

        The grammar is the one the storefront search box accepts::

            red shoes -blue brand:acme "wide fit"

        Words are free terms, a quoted run is a phrase that must appear whole, a
        leading ``-`` excludes a term and ``field:value`` becomes a facet filter.
        Parsing never raises on user input: an unbalanced quote is closed for you
        and an empty query parses to an empty search.
        """

        import shlex

        from shop.search.normalizer import normalize

        __all__ = ["FIELDS", "parse"]

        FIELDS = ("brand", "category", "color", "size", "tier", "channel")


        def parse(text: str) -> dict:
            """The structured form of a query string."""
            parsed = {"terms": [], "phrases": [], "negated": [], "filters": {}}
            raw = str(text or "")
            try:
                words = shlex.split(raw)
            except ValueError:
                words = raw.replace('"', " ").split()
            for word in words:
                if ":" in word and not word.startswith("-"):
                    field, _, value = word.partition(":")
                    if field.lower() in FIELDS and value.strip():
                        parsed["filters"][field.lower()] = value.strip().lower()
                        continue
                if word.startswith("-") and len(word) > 1:
                    parsed["negated"].extend(normalize(word[1:]))
                elif len(word.split()) > 1:
                    phrase = " ".join(normalize(word))
                    if phrase:
                        parsed["phrases"].append(phrase)
                else:
                    parsed["terms"].extend(normalize(word))
            parsed["terms"] = [term for term in parsed["terms"] if term]
            return parsed
        ''',
    )
    render(
        "shop/search/__init__.py",
        '''
        """Product search: tokenizing, an inverted index, scoring and suggestions.

        The pieces are small on purpose so a caller can take one without the rest:
        a facet sidebar needs only :mod:`shop.search.facets`, while the storefront
        search route wires the tokenizer, index, matcher and ranker together.
        """

        from shop.search.fuzzy import closest, distance
        from shop.search.index import InvertedIndex
        from shop.search.matcher import score
        from shop.search.query import parse
        from shop.search.ranker import bm25
        from shop.search.tokenizer import tokenize

        __all__ = [
            "InvertedIndex",
            "bm25",
            "closest",
            "distance",
            "parse",
            "score",
            "tokenize",
        ]
        ''',
    )


def render_ops():
    """shop/ops: runbooks, retention, backups, flags and the health report."""
    render(
        "shop/ops/runbook.py",
        '''
        """Operational runbooks as ordered, checkable steps.

        A runbook is a tuple of ``(title, owner, minutes)`` triples. Nothing here
        touches a server: the helpers only turn a runbook into the checklist an
        on-call engineer works down and the estimate a status page quotes.
        """

        from shop.errors import NotFoundError

        __all__ = ["RUNBOOKS", "checklist", "next_step", "steps_for", "total_minutes"]

        RUNBOOKS = {
            "deploy": (
                ("Freeze the release branch", "release-captain", 5),
                ("Run the smoke suite against staging", "on-call", 10),
                ("Drain traffic from the old pool", "platform", 3),
                ("Promote the new pool and watch error rate", "on-call", 15),
                ("Unfreeze the branch and announce the version", "release-captain", 2),
            ),
            "rollback": (
                ("Declare the rollback in the incident channel", "on-call", 2),
                ("Point the load balancer back at the previous pool", "platform", 5),
                ("Replay orders captured during the bad window", "data", 20),
                ("Confirm the checkout probe is green", "on-call", 5),
            ),
            "incident": (
                ("Open the incident and assign a commander", "on-call", 3),
                ("Post the first customer-facing status", "comms", 5),
                ("Identify the failing dependency", "on-call", 15),
                ("Apply the mitigation and watch for ten minutes", "platform", 15),
                ("Write the timeline before standing down", "commander", 25),
            ),
            "key-rotation": (
                ("Create the new signing key", "security", 5),
                ("Publish it to the gateway allow-list", "platform", 5),
                ("Switch the issuer and re-sign webhooks", "security", 10),
                ("Retire the old key after the grace window", "security", 2),
            ),
            "db-restore": (
                ("Snapshot the current volume before touching it", "data", 10),
                ("Restore the chosen backup into a scratch instance", "data", 30),
                ("Verify the order and ledger row counts", "data", 10),
                ("Swap the connection string and restart the workers", "platform", 8),
            ),
        }


        def names() -> list:
            """Every runbook name, sorted."""
            return sorted(RUNBOOKS)


        def steps_for(name: str) -> list:
            """The runbook's steps as dicts numbered from one."""
            if name not in RUNBOOKS:
                raise NotFoundError("no runbook named %r" % (name,))
            return [
                {"index": position, "title": title, "owner": owner, "minutes": minutes}
                for position, (title, owner, minutes) in enumerate(RUNBOOKS[name], start=1)
            ]


        def next_step(name: str, done: int) -> dict:
            """The step after ``done`` completed ones, or an empty dict at the end."""
            steps = steps_for(name)
            position = int(done)
            if position < 0:
                position = 0
            if position >= len(steps):
                return {}
            return steps[position]


        def total_minutes(name: str) -> int:
            """The runbook's time estimate, added up."""
            return sum(step["minutes"] for step in steps_for(name))


        def checklist(name: str) -> list:
            """One printable line per step, ready for the incident channel."""
            return [
                "[ ] %d. %s (%s, %dm)" % (step["index"], step["title"], step["owner"], step["minutes"])
                for step in steps_for(name)
            ]
        ''',
    )
    render(
        "shop/ops/retention.py",
        '''
        """How long each table's rows survive.

        Retention is policy rather than code: the nightly job asks this module
        which tables it may prune and how old a row has to be before it goes.
        """

        from datetime import date, timedelta

        from shop.errors import ValidationError

        __all__ = ["POLICIES", "purge_before", "retention_days", "should_purge"]

        POLICIES = {
            "webhook_events": {"days": 14, "key": "received_at"},
            "search_queries": {"days": 45, "key": "asked_at"},
            "cart_snapshots": {"days": 60, "key": "captured_at"},
            "payment_attempts": {"days": 400, "key": "attempted_at"},
            "audit_entries": {"days": 730, "key": "recorded_at"},
        }


        def retention_days(table: str) -> int:
            """The window for a table, refusing one nobody set a policy for."""
            if table not in POLICIES:
                raise ValidationError("no retention policy for table %r" % (table,))
            return int(POLICIES[table]["days"])


        def should_purge(table: str, age_days: int) -> bool:
            """Whether a row that old is past the window and may go."""
            return int(age_days) >= retention_days(table)


        def purge_before(table: str, today: str, days=None) -> str:
            """The ISO date before which rows of ``table`` may be removed."""
            window = retention_days(table) if days is None else int(days)
            if window <= 0:
                raise ValidationError("retention window for %r must be positive" % (table,))
            start = date.fromisoformat(str(today)[:10])
            return (start - timedelta(days=window)).isoformat()
        ''',
    )
    render(
        "shop/ops/backup.py",
        '''
        """Nightly backup plans and the manifest check that follows them.

        The plan is pure data: one entry per target with the dated filename the
        uploader will use. ``verify`` then reads a finished manifest and reports
        every problem in one pass, so a half-finished upload is never silent.
        """

        __all__ = ["REQUIRED", "TARGETS", "manifest", "plan", "verify"]

        TARGETS = (
            {"name": "orders-db", "path": "var/backups/orders", "compress": True},
            {"name": "ledger-db", "path": "var/backups/ledger", "compress": True},
            {"name": "catalog-db", "path": "var/backups/catalog", "compress": False},
            {"name": "media", "path": "var/backups/media", "compress": True},
        )

        REQUIRED = ("name", "path", "bytes")


        def plan(today: str, targets=TARGETS) -> dict:
            """One dated entry per backup target."""
            stamp = str(today)[:10]
            entries = [
                {
                    "name": target["name"],
                    "path": "%s/%s-%s.sqlite" % (target["path"], target["name"], stamp),
                    "compress": bool(target.get("compress")),
                }
                for target in targets
            ]
            return {"date": stamp, "entries": entries, "count": len(entries)}


        def manifest(entries) -> dict:
            """Freeze a list of finished entries into the shape ``verify`` reads."""
            frozen = [dict(entry) for entry in entries]
            return {"files": frozen, "count": len(frozen)}


        def verify(entries) -> list:
            """Every problem with a manifest, as human-readable lines."""
            problems = []
            seen = set()
            for index, entry in enumerate(entries):
                missing = [key for key in REQUIRED if key not in entry]
                if missing:
                    problems.append("entry %d missing %s" % (index, ", ".join(sorted(missing))))
                    continue
                name = str(entry["name"])
                if name in seen:
                    problems.append("duplicate target %r" % (name,))
                seen.add(name)
                if int(entry.get("bytes", 0)) <= 0:
                    problems.append("target %r is empty" % (name,))
            return problems
        ''',
    )
    render(
        "shop/ops/rotation.py",
        '''
        """Credential rotation schedule.

        Every secret has an interval and a last rotation date; the status call
        answers the only question the on-call calendar cares about, which is how
        many days are left and whether the grace window has already passed.
        """

        from datetime import date, timedelta

        from shop.errors import ValidationError

        __all__ = ["ROTATION", "WARN_DAYS", "due_on", "interval_days", "status"]

        ROTATION = {
            "api-token": 90,
            "webhook-secret": 180,
            "signing-key": 365,
            "database-password": 60,
            "smtp-password": 120,
        }

        WARN_DAYS = 14


        def interval_days(key: str) -> int:
            """The rotation interval for a secret, refusing an unknown one."""
            if key not in ROTATION:
                raise ValidationError("no rotation interval for %r" % (key,))
            return int(ROTATION[key])


        def due_on(key: str, last_rotated: str) -> str:
            """The ISO date the secret comes due."""
            start = date.fromisoformat(str(last_rotated)[:10])
            return (start + timedelta(days=interval_days(key))).isoformat()


        def status(key: str, last_rotated: str, today: str) -> dict:
            """Due date, days left and a state of ``ok``, ``due`` or ``overdue``."""
            due = date.fromisoformat(due_on(key, last_rotated))
            now = date.fromisoformat(str(today)[:10])
            left = (due - now).days
            if left < 0:
                state = "overdue"
            elif left <= WARN_DAYS:
                state = "due"
            else:
                state = "ok"
            return {"key": key, "due_on": due.isoformat(), "days_left": left, "state": state}
        ''',
    )
    render(
        "shop/ops/audit.py",
        '''
        """An append-only audit trail for the ops console.

        Entries are plain dicts, so the same shape lands in the database, in the
        NDJSON export and in the console table without a translation layer. Every
        entry must name who did what to which target; a blank field is refused
        rather than stored as an anonymous row.
        """

        from shop.errors import ValidationError

        __all__ = ["FIELDS", "record", "summarise", "trail"]

        FIELDS = ("actor", "action", "target")


        def record(actor: str, action: str, target: str, at: str) -> dict:
            """One audit entry, refusing a blank field or timestamp."""
            entry = {"actor": str(actor).strip(), "action": str(action).strip(), "target": str(target).strip()}
            blank = [key for key in FIELDS if not entry[key]]
            if blank:
                raise ValidationError("audit entry needs %s" % (", ".join(blank),))
            if not str(at).strip():
                raise ValidationError("audit entry needs a timestamp")
            entry["at"] = str(at).strip()
            return entry


        def trail(entries, actor=None, action=None) -> list:
            """The entries matching both filters, in the order they arrived."""
            kept = []
            for entry in entries:
                if actor is not None and entry.get("actor") != actor:
                    continue
                if action is not None and entry.get("action") != action:
                    continue
                kept.append(dict(entry))
            return kept


        def summarise(entries) -> dict:
            """Totals by actor and by action, each sorted alphabetically."""
            by_actor = {}
            by_action = {}
            total = 0
            for entry in entries:
                total += 1
                actor = str(entry.get("actor", ""))
                action = str(entry.get("action", ""))
                by_actor[actor] = by_actor.get(actor, 0) + 1
                by_action[action] = by_action.get(action, 0) + 1
            return {"total": total, "by_actor": dict(sorted(by_actor.items())), "by_action": dict(sorted(by_action.items()))}
        ''',
    )
    render(
        "shop/ops/quota.py",
        '''
        """Per-tenant quotas for the public API.

        The limits are the ones the plan page advertises. ``check`` reports every
        breach at once instead of stopping at the first, so a support reply can
        list everything a tenant has to trim.
        """

        __all__ = ["LIMITS", "check", "headroom", "within"]

        LIMITS = {
            "orders_per_day": 5000,
            "api_calls_per_minute": 600,
            "uploads_per_hour": 120,
            "webhooks_per_minute": 300,
            "exports_per_day": 20,
        }


        def check(counts) -> list:
            """Every quota the counts exceed, worst overage first."""
            breaches = []
            for name, used in dict(counts or {}).items():
                limit = LIMITS.get(name)
                if limit is None or int(used) <= limit:
                    continue
                breaches.append({"quota": name, "limit": limit, "used": int(used), "over": int(used) - limit})
            breaches.sort(key=lambda row: (-row["over"], row["quota"]))
            return breaches


        def within(counts) -> bool:
            """True when every named quota is respected."""
            return not check(counts)


        def headroom(counts) -> dict:
            """Units left per quota, floored at zero for an exceeded limit."""
            remaining = {}
            for name, limit in LIMITS.items():
                used = int(dict(counts or {}).get(name, 0))
                remaining[name] = max(0, limit - used)
            return remaining
        ''',
    )
    render(
        "shop/ops/replay.py",
        '''
        """Replaying captured events: normalise, dedupe by id, slice by time.

        The event log stores whatever each producer sent, so replay works on a
        canonical shape: an id, a known kind, an ISO timestamp and a payload dict.
        Deduplication keeps the last version of an id, because a producer retry
        carries the corrected payload.
        """

        from shop.errors import ValidationError

        __all__ = ["KINDS", "dedupe", "normalise", "summary", "window"]

        KINDS = ("order.created", "order.paid", "order.shipped", "order.refunded", "stock.adjusted")


        def normalise(event) -> dict:
            """A canonical event, refusing a missing id or an unknown kind."""
            source = dict(event or {})
            identifier = str(source.get("id", "")).strip()
            if not identifier:
                raise ValidationError("event needs an id")
            kind = str(source.get("kind", "")).strip()
            if kind not in KINDS:
                raise ValidationError("unknown event kind %r" % (kind,))
            at = str(source.get("at", "")).strip()
            if not at:
                raise ValidationError("event %r needs a timestamp" % (identifier,))
            return {"id": identifier, "kind": kind, "at": at, "payload": dict(source.get("payload") or {})}


        def dedupe(events) -> list:
            """One event per id, the last one seen, first-seen order preserved."""
            order = []
            latest = {}
            for event in events:
                clean = normalise(event)
                if clean["id"] not in latest:
                    order.append(clean["id"])
                latest[clean["id"]] = clean
            return [latest[identifier] for identifier in order]


        def window(events, start: str, end: str) -> list:
            """Events whose timestamp falls inside the inclusive ISO window."""
            low = str(start)
            high = str(end)
            return [dict(event) for event in events if low <= str(event.get("at", "")) <= high]


        def summary(events) -> dict:
            """Totals and per-kind counts for a replay run."""
            counts = {}
            total = 0
            for event in events:
                total += 1
                kind = str(event.get("kind", ""))
                counts[kind] = counts.get(kind, 0) + 1
            return {"total": total, "by_kind": dict(sorted(counts.items()))}
        ''',
    )
    render(
        "shop/ops/flags.py",
        '''
        """Feature flags resolved per tenant.

        A flag is a rollout percentage plus an explicit tenant allow-list; a tenant
        in the list always gets the flag and everyone else gets it when their stable
        bucket falls under the percentage. The bucket is a hash of flag name and
        tenant, never a random number, so a tenant never flickers between builds.
        """

        from shop.errors import ValidationError

        __all__ = ["DEFAULTS", "bucket", "enabled", "listing"]

        DEFAULTS = {
            "new-checkout": {"percent": 25, "tenants": ("acme", "globex")},
            "one-page-cart": {"percent": 50, "tenants": ()},
            "async-webhooks": {"percent": 100, "tenants": ()},
            "search-synonyms": {"percent": 10, "tenants": ("initech",)},
            "split-shipments": {"percent": 0, "tenants": ("umbrella",)},
        }


        def bucket(name: str, tenant: str) -> int:
            """A stable 0-99 bucket for the flag and tenant pair."""
            value = 2166136261
            for byte in (str(name) + ":" + str(tenant)).encode("utf-8"):
                value = (value ^ byte) * 16777619 & 0xFFFFFFFF
            return value % 100


        def enabled(name: str, tenant: str = "") -> bool:
            """Whether the tenant sees the flag; an unknown flag is a bug."""
            if name not in DEFAULTS:
                raise ValidationError("unknown feature flag %r" % (name,))
            spec = DEFAULTS[name]
            if str(tenant) in spec["tenants"]:
                return True
            return bucket(name, tenant) < int(spec["percent"])


        def listing() -> list:
            """Every flag with its rollout, sorted by name."""
            return [
                {"name": name, "percent": int(DEFAULTS[name]["percent"]), "tenants": list(DEFAULTS[name]["tenants"])}
                for name in sorted(DEFAULTS)
            ]
        ''',
    )
    render(
        "shop/ops/incidents.py",
        '''
        """Incident severity and the escalation ladder.

        Severity follows outage length, not the noise level of the report: a
        checkout that has been refusing payments for two hours is a sev1 whatever
        channel it was reported in. Escalation hands the incident to the next
        responder once it has outlived its severity's window.
        """

        from shop.errors import ValidationError

        __all__ = ["ESCALATE_AFTER", "SERVICES", "SEVERITIES", "escalate", "open_incident", "severity_for"]

        SEVERITIES = ("sev1", "sev2", "sev3", "sev4")

        ESCALATE_AFTER = {"sev1": 15, "sev2": 30, "sev3": 120, "sev4": 480}

        SERVICES = ("checkout", "catalog", "payments", "inventory", "search", "notifications")

        LADDER = {"sev4": "sev3", "sev3": "sev2", "sev2": "sev1", "sev1": "sev1"}


        def severity_for(minutes: int) -> str:
            """The severity an outage of that many minutes has earned."""
            elapsed = int(minutes)
            if elapsed >= 120:
                return "sev1"
            if elapsed >= 45:
                return "sev2"
            if elapsed >= 15:
                return "sev3"
            return "sev4"


        def open_incident(title: str, service: str, minutes: int = 0, opened_at: str = "2026-02-11T09:30:00Z") -> dict:
            """A new incident dict, refusing a blank title or unknown service."""
            if not str(title).strip():
                raise ValidationError("an incident needs a title")
            if service not in SERVICES:
                raise ValidationError("unknown service %r" % (service,))
            return {
                "title": str(title).strip(),
                "service": service,
                "minutes": int(minutes),
                "opened_at": str(opened_at),
                "severity": severity_for(minutes),
                "page": 1,
            }


        def escalate(incident: dict) -> dict:
            """The same incident one rung up, with its page count raised."""
            raised = dict(incident)
            current = str(raised.get("severity", "sev4"))
            raised["severity"] = LADDER.get(current, "sev1")
            raised["page"] = int(raised.get("page", 1)) + 1
            return raised
        ''',
    )
    render(
        "shop/ops/health.py",
        '''
        """The component health report behind ``GET /health``.

        Every probe returns the same small dict, and ``overall`` folds them into the
        one status the load balancer reads. A failing critical component takes the
        whole service down; anything else only degrades it.
        """

        __all__ = ["CRITICAL", "component", "merge", "overall"]

        CRITICAL = ("database", "payments")


        def component(name: str, ok: bool, detail: str = "", latency_ms: int = 0) -> dict:
            """One probe result in the shape the health page renders."""
            return {
                "name": str(name),
                "ok": bool(ok),
                "detail": str(detail),
                "latency_ms": int(latency_ms),
            }


        def merge(*groups) -> list:
            """Flatten several probe lists into one, keeping their order."""
            merged = []
            for group in groups:
                for item in group:
                    merged.append(dict(item))
            return merged


        def overall(components) -> dict:
            """``ok``, ``degraded`` or ``down``, plus the names that failed."""
            checks = [dict(item) for item in components]
            failed = [str(item.get("name", "")) for item in checks if not item.get("ok")]
            if not checks:
                status = "unknown"
            elif not failed:
                status = "ok"
            elif any(name in CRITICAL for name in failed):
                status = "down"
            else:
                status = "degraded"
            return {"status": status, "checked": len(checks), "failed": sorted(failed)}
        ''',
    )
    render(
        "shop/ops/smoke.py",
        '''
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
        ''',
    )
    render(
        "shop/ops/__init__.py",
        '''
        """Operational helpers: runbooks, retention, backups, flags and health.

        These modules are deliberately dependency-free so the deploy pipeline can
        import one without booting the service. Every call takes its dates and
        counts as arguments instead of reading the clock, which keeps the tests
        deterministic.
        """

        __all__ = [
            "audit",
            "backup",
            "flags",
            "health",
            "incidents",
            "quota",
            "replay",
            "retention",
            "rotation",
            "runbook",
            "smoke",
        ]
        ''',
    )


# SENTINEL
