from __future__ import annotations

import datetime as dt
import uuid
from typing import Any

from sqlalchemy import select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import AsyncSession

from app.models.billing_topup import BillingTopup
from app.models.creem_event import CreemEvent
from app.models.user import User
from app.storage.billing_db import stage_balance_adjustment_ledger_entry


MAX_BALANCE_USD = 1_000_000_000


def generate_topup_request_id() -> str:
    return f"topup_{uuid.uuid4().hex}"


async def create_billing_topup(
    session: AsyncSession,
    *,
    org_id: uuid.UUID,
    user_id: uuid.UUID,
    request_id: str,
    units: int,
) -> BillingTopup:
    row = BillingTopup(
        org_id=org_id,
        user_id=user_id,
        request_id=request_id,
        units=int(max(units, 0)),
        status="pending",
    )
    session.add(row)
    await session.commit()
    await session.refresh(row)
    return row


async def get_billing_topup_for_user(
    session: AsyncSession,
    *,
    org_id: uuid.UUID,
    user_id: uuid.UUID,
    request_id: str,
) -> BillingTopup | None:
    return (
        (
            await session.execute(
                select(BillingTopup).where(
                    BillingTopup.org_id == org_id,
                    BillingTopup.user_id == user_id,
                    BillingTopup.request_id == request_id,
                )
            )
        )
        .scalars()
        .first()
    )


async def get_billing_topup_by_request_id(
    session: AsyncSession, *, request_id: str
) -> BillingTopup | None:
    return (
        (await session.execute(select(BillingTopup).where(BillingTopup.request_id == request_id)))
        .scalars()
        .first()
    )


async def mark_billing_topup_failed(
    session: AsyncSession,
    *,
    request_id: str,
    checkout_id: str | None,
    order_id: str | None,
    currency: str | None,
    amount_total_cents: int | None,
) -> BillingTopup | None:
    topup = await get_billing_topup_by_request_id(session, request_id=request_id)
    if not topup:
        return None
    if topup.status == "completed":
        return topup
    topup.status = "failed"
    if checkout_id:
        topup.checkout_id = checkout_id
    if order_id:
        topup.order_id = order_id
    if currency:
        topup.currency = currency[:8]
    if amount_total_cents is not None:
        topup.amount_total_cents = int(max(amount_total_cents, 0))
    await session.commit()
    await session.refresh(topup)
    return topup


async def set_billing_topup_status(
    session: AsyncSession,
    *,
    request_id: str,
    status: str,
    checkout_id: str | None,
    order_id: str | None,
    currency: str | None,
    amount_total_cents: int | None,
) -> BillingTopup | None:
    topup = await get_billing_topup_by_request_id(session, request_id=request_id)
    if not topup:
        return None

    normalized_status = str(status or "").strip().lower()
    if normalized_status == "":
        normalized_status = "pending"

    topup.status = normalized_status[:24]
    if checkout_id:
        topup.checkout_id = checkout_id
    if order_id:
        topup.order_id = order_id
    if currency:
        topup.currency = currency[:8]
    if amount_total_cents is not None:
        topup.amount_total_cents = int(max(amount_total_cents, 0))

    await session.commit()
    await session.refresh(topup)
    return topup


async def complete_billing_topup(
    session: AsyncSession,
    *,
    request_id: str,
    checkout_id: str | None,
    order_id: str | None,
    currency: str | None,
    amount_total_cents: int | None,
) -> BillingTopup | None:
    now = dt.datetime.now(dt.timezone.utc)
    topup = (
        (
            await session.execute(
                select(BillingTopup)
                .where(BillingTopup.request_id == request_id)
                .with_for_update()
            )
        )
        .scalars()
        .first()
    )
    if not topup:
        return None
    if topup.status == "completed":
        return topup

    user = (
        (await session.execute(select(User).where(User.id == topup.user_id).with_for_update()))
        .scalars()
        .first()
    )
    if not user:
        return None

    balance_before = int(user.balance)
    delta = int(max(topup.units, 0))
    balance_after = balance_before + delta
    if balance_after > MAX_BALANCE_USD:
        raise ValueError("balance too large")

    user.balance = balance_after
    stage_balance_adjustment_ledger_entry(
        session,
        org_id=topup.org_id,
        user_id=user.id,
        actor_user_id=None,
        balance_before=balance_before,
        balance_after=balance_after,
        entry_type="top_up",
    )

    topup.status = "completed"
    topup.completed_at = now
    if checkout_id:
        topup.checkout_id = checkout_id
    if order_id:
        topup.order_id = order_id
    if currency:
        topup.currency = currency[:8]
    if amount_total_cents is not None:
        topup.amount_total_cents = int(max(amount_total_cents, 0))

    await session.commit()
    await session.refresh(topup)
    return topup


async def creem_event_exists(session: AsyncSession, *, creem_event_id: str) -> bool:
    existing = (
        await session.execute(select(CreemEvent.id).where(CreemEvent.creem_event_id == creem_event_id))
    ).scalar_one_or_none()
    return existing is not None


async def record_creem_event(
    session: AsyncSession,
    *,
    creem_event_id: str,
    event_type: str,
    status: str,
    raw_payload: dict[str, Any],
    org_id: uuid.UUID | None = None,
    user_id: uuid.UUID | None = None,
    amount_units: int = 0,
    amount_total_cents: int | None = None,
    currency: str | None = None,
) -> bool:
    row = CreemEvent(
        creem_event_id=str(creem_event_id)[:64],
        event_type=str(event_type)[:64],
        status=str(status)[:24],
        raw_payload=raw_payload,
        org_id=org_id,
        user_id=user_id,
        amount_units=int(max(amount_units, 0)),
        amount_total_cents=(int(max(amount_total_cents, 0)) if amount_total_cents is not None else None),
        currency=(currency[:8] if isinstance(currency, str) and currency else None),
    )
    session.add(row)
    try:
        await session.commit()
    except IntegrityError:
        await session.rollback()
        return False
    return True
