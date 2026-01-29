from __future__ import annotations

import datetime as dt
import uuid
from decimal import Decimal, ROUND_HALF_UP

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.models.billing_topup import BillingTopup
from app.models.referral_bonus_event import ReferralBonusEvent
from app.models.user import User
from app.storage.billing_db import stage_balance_adjustment_ledger_entry


REFERRAL_RATE = Decimal("0.25")
REFERRAL_CAP_USD_CENTS = 10_000
REFERRAL_PENDING_HOURS = 72


def _normalize_email(value: str | None) -> str | None:
    if not isinstance(value, str):
        return None
    v = value.strip().lower()
    return v or None


def _normalize_token(value: str | None, *, max_len: int) -> str | None:
    if not isinstance(value, str):
        return None
    v = value.strip()
    if v == "":
        return None
    return v[:max_len]


def compute_referral_bonus_usd_cents(units: int) -> int:
    safe_units = int(max(int(units), 0))
    # units are integer USD credits; compute 25% with 2-decimal precision (cents).
    bonus = Decimal(safe_units) * REFERRAL_RATE
    bonus_usd = bonus.quantize(Decimal("0.01"), rounding=ROUND_HALF_UP)
    cents = int((bonus_usd * Decimal("100")).to_integral_value(rounding=ROUND_HALF_UP))
    return int(min(max(cents, 0), REFERRAL_CAP_USD_CENTS))


def detect_referral_block_reason(*, inviter: User, invitee: User, topup: BillingTopup) -> str | None:
    if inviter.id == invitee.id:
        return "self_invite"

    payer_email = _normalize_email(getattr(topup, "payer_email", None))
    inviter_email = _normalize_email(getattr(inviter, "email", None))
    inviter_payment_email = _normalize_email(getattr(inviter, "first_payment_email", None))
    if payer_email and ((inviter_email and payer_email == inviter_email) or (inviter_payment_email and payer_email == inviter_payment_email)):
        return "same_payment_email"

    invitee_device_ids = {
        _normalize_token(getattr(invitee, "signup_device_id", None), max_len=64),
        _normalize_token(getattr(topup, "client_device_id", None), max_len=64),
    }
    inviter_device_ids = {
        _normalize_token(getattr(inviter, "signup_device_id", None), max_len=64),
        _normalize_token(getattr(inviter, "first_payment_device_id", None), max_len=64),
    }
    invitee_device_ids.discard(None)
    inviter_device_ids.discard(None)
    if invitee_device_ids and inviter_device_ids and invitee_device_ids.intersection(inviter_device_ids):
        return "same_device"

    invitee_ips = {
        _normalize_token(getattr(invitee, "signup_ip", None), max_len=64),
        _normalize_token(getattr(topup, "client_ip", None), max_len=64),
    }
    inviter_ips = {
        _normalize_token(getattr(inviter, "signup_ip", None), max_len=64),
        _normalize_token(getattr(inviter, "first_payment_ip", None), max_len=64),
    }
    invitee_ips.discard(None)
    inviter_ips.discard(None)
    if invitee_ips and inviter_ips and invitee_ips.intersection(inviter_ips):
        return "same_ip"

    return None


async def maybe_create_referral_bonus_event(
    session: AsyncSession,
    *,
    org_id: uuid.UUID,
    topup: BillingTopup,
    invitee: User,
    now: dt.datetime | None = None,
) -> ReferralBonusEvent | None:
    inviter_user_id = getattr(invitee, "invited_by_user_id", None)
    if not inviter_user_id:
        return None

    existing_active = (
        await session.execute(
            select(ReferralBonusEvent.id)
            .where(
                ReferralBonusEvent.invitee_user_id == invitee.id,
                ReferralBonusEvent.status.in_(["pending", "confirmed"]),
            )
            .limit(1)
        )
    ).scalar_one_or_none()
    if existing_active is not None:
        return None

    inviter = await session.get(User, inviter_user_id)
    if not inviter:
        return None

    created_at = now or dt.datetime.now(dt.timezone.utc)
    bonus_cents = compute_referral_bonus_usd_cents(int(getattr(topup, "units", 0) or 0))
    reason = detect_referral_block_reason(inviter=inviter, invitee=invitee, topup=topup)
    status = "blocked" if reason else "pending"

    row = ReferralBonusEvent(
        org_id=org_id,
        inviter_user_id=inviter.id,
        invitee_user_id=invitee.id,
        topup_id=topup.id,
        status=status,
        bonus_usd_cents=bonus_cents,
        blocked_reason=reason,
        created_at=created_at,
    )
    session.add(row)
    await session.flush()
    return row


async def confirm_due_referral_bonuses(session: AsyncSession, *, now: dt.datetime | None = None, limit: int = 200) -> int:
    safe_limit = min(max(int(limit), 1), 500)
    ts = now or dt.datetime.now(dt.timezone.utc)
    cutoff = ts - dt.timedelta(hours=REFERRAL_PENDING_HOURS)

    rows = (
        await session.execute(
            select(ReferralBonusEvent)
            .where(ReferralBonusEvent.status == "pending", ReferralBonusEvent.created_at <= cutoff)
            .with_for_update(skip_locked=True)
            .limit(safe_limit)
        )
    ).scalars().all()

    confirmed = 0
    for event in rows:
        topup = await session.get(BillingTopup, event.topup_id)
        if not topup or getattr(topup, "refunded_at", None) is not None:
            event.status = "blocked"
            event.blocked_reason = event.blocked_reason or "refunded"
            event.reversed_at = ts
            continue

        inviter = (
            (
                await session.execute(
                    select(User).where(User.id == event.inviter_user_id).with_for_update()
                )
            )
            .scalars()
            .first()
        )
        if not inviter:
            event.status = "blocked"
            event.blocked_reason = event.blocked_reason or "missing_inviter"
            event.reversed_at = ts
            continue

        balance_before = int(inviter.balance)
        delta_cents = int(max(int(event.bonus_usd_cents), 0))
        inviter.balance = balance_before + delta_cents
        stage_balance_adjustment_ledger_entry(
            session,
            org_id=event.org_id,
            user_id=inviter.id,
            actor_user_id=None,
            balance_before=balance_before,
            balance_after=int(inviter.balance),
            entry_type="referral_bonus",
        )
        event.status = "confirmed"
        event.confirmed_at = ts
        confirmed += 1

    if confirmed > 0 or len(rows) > 0:
        await session.commit()
    return confirmed


async def process_referral_refund(session: AsyncSession, *, topup: BillingTopup, now: dt.datetime | None = None) -> None:
    ts = now or dt.datetime.now(dt.timezone.utc)
    if getattr(topup, "refunded_at", None) is None:
        topup.refunded_at = ts

    event = (
        (
            await session.execute(
                select(ReferralBonusEvent).where(ReferralBonusEvent.topup_id == topup.id).with_for_update()
            )
        )
        .scalars()
        .first()
    )
    if not event:
        await session.commit()
        return

    if event.status == "pending":
        event.status = "blocked"
        event.blocked_reason = event.blocked_reason or "refunded"
        event.reversed_at = ts
        await session.commit()
        return

    if event.status != "confirmed":
        await session.commit()
        return

    inviter = (
        (
            await session.execute(select(User).where(User.id == event.inviter_user_id).with_for_update())
        )
        .scalars()
        .first()
    )
    if not inviter:
        event.status = "reversed"
        event.reversed_at = ts
        await session.commit()
        return

    balance_before = int(inviter.balance)
    delta_cents = int(max(int(event.bonus_usd_cents), 0))
    inviter.balance = balance_before - delta_cents
    stage_balance_adjustment_ledger_entry(
        session,
        org_id=event.org_id,
        user_id=inviter.id,
        actor_user_id=None,
        balance_before=balance_before,
        balance_after=int(inviter.balance),
        entry_type="referral_bonus",
    )
    event.status = "reversed"
    event.reversed_at = ts
    await session.commit()

