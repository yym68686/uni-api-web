from __future__ import annotations

import uuid
from decimal import Decimal, InvalidOperation, ROUND_HALF_UP

from sqlalchemy.ext.asyncio import AsyncSession

from app.models.organization import Organization
from app.models.user import User
from app.storage.billing_db import stage_balance_adjustment_ledger_entry

USD_CENTS = Decimal("100")
USD_DECIMAL_2DP = Decimal("0.01")
MAX_NEW_USER_TRIAL_USD = Decimal("1000000")


def usd_to_cents_2(value: float | int | str) -> int:
    try:
        dec = Decimal(str(value)).quantize(USD_DECIMAL_2DP, rounding=ROUND_HALF_UP)
    except (InvalidOperation, ValueError) as exc:
        raise ValueError("trial balance must be a number") from exc
    if not dec.is_finite():
        raise ValueError("trial balance must be a number")
    if dec < 0:
        raise ValueError("trial balance must be >= 0")
    if dec > MAX_NEW_USER_TRIAL_USD:
        raise ValueError("trial balance too large")
    return int((dec * USD_CENTS).to_integral_value(rounding=ROUND_HALF_UP))


def cents_to_usd_2(value: int) -> float:
    cents = max(int(value or 0), 0)
    return float((Decimal(cents) / USD_CENTS).quantize(USD_DECIMAL_2DP))


def new_user_trial_balance_cents(org: Organization) -> int:
    if not bool(getattr(org, "new_user_trial_enabled", False)):
        return 0
    return max(int(getattr(org, "new_user_trial_balance_usd_cents", 0) or 0), 0)


def stage_new_user_trial_credit(
    session: AsyncSession,
    *,
    org: Organization,
    user: User,
) -> None:
    balance_after = new_user_trial_balance_cents(org)
    if balance_after <= 0:
        return
    user.balance = balance_after
    stage_balance_adjustment_ledger_entry(
        session,
        org_id=uuid.UUID(str(org.id)),
        user_id=uuid.UUID(str(user.id)),
        actor_user_id=None,
        balance_before=0,
        balance_after=balance_after,
        entry_type="trial_credit",
    )
