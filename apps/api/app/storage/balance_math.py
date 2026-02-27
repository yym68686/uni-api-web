from __future__ import annotations

from decimal import Decimal
from typing import Final


USD_MICROS_PER_CENT: Final[int] = 10_000


def remaining_usd_micros(*, credits_usd_cents: int, spend_usd_micros_total: int) -> int:
    credits_micros = int(credits_usd_cents) * USD_MICROS_PER_CENT
    spend_micros = int(spend_usd_micros_total)
    return int(max(credits_micros - spend_micros, 0))


def remaining_usd_cents_rounded(*, credits_usd_cents: int, spend_usd_micros_total: int) -> int:
    remaining_micros = remaining_usd_micros(
        credits_usd_cents=int(credits_usd_cents),
        spend_usd_micros_total=int(spend_usd_micros_total),
    )
    if remaining_micros <= 0:
        return 0
    return int((remaining_micros + (USD_MICROS_PER_CENT // 2)) // USD_MICROS_PER_CENT)


def remaining_usd_2(*, credits_usd_cents: int, spend_usd_micros_total: int) -> float:
    cents = remaining_usd_cents_rounded(
        credits_usd_cents=int(credits_usd_cents),
        spend_usd_micros_total=int(spend_usd_micros_total),
    )
    return float(Decimal(int(cents)) / Decimal("100"))


def credits_usd_cents_for_desired_remaining(
    *,
    desired_remaining_usd_cents: int,
    spend_usd_micros_total: int,
) -> int:
    safe_remaining = int(max(int(desired_remaining_usd_cents), 0))
    spend_micros = int(max(int(spend_usd_micros_total), 0))
    spend_cents, spend_remainder = divmod(spend_micros, USD_MICROS_PER_CENT)
    bump = 1 if spend_remainder > (USD_MICROS_PER_CENT // 2) else 0
    return int(safe_remaining + int(spend_cents) + bump)

