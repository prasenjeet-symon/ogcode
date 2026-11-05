# shop

Order pricing for the storefront: carts, coupons, spend tiers, tax and
shipping, in integer cents.

The service is small but the pricing path is spread across it on purpose, the
way it grew: `shop/money.py` holds the arithmetic, `shop/discounts/` the coupon
and tier rules, `shop/catalog/` the products and quantity breaks, and
`shop/orders/` the cart and the checkout that ties the discounts, tax and
shipping together into an `Order`.

## Pricing rules

These are the rules merchandising signed off on. They are also written into the
docstrings of the functions that implement them, and the test suite is the
contract.

1. Percentage coupons come off the subtotal first.
2. The spend tier comes off whatever the coupon left, but the tier itself is
   earned by the subtotal before any coupon.
3. A fixed coupon is taken off last and can never make the payable amount
   negative.
4. Tax is charged on the discounted subtotal, after every discount.
5. Shipping is flat below the free-shipping threshold and free at or above it;
   it is added after tax.
6. Every cent is accounted for: the per-line discount shares add back up to the
   order's discount total.

## Running the tests

```sh
python3 -m unittest discover -q -s tests -t .
```

Pure standard library, no install step.
