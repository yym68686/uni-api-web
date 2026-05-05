from __future__ import annotations

import datetime as dt
import uuid
from decimal import Decimal
from typing import Any

from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.models.balance_ledger_entry import BalanceLedgerEntry
from app.models.llm_usage_event import LlmUsageEvent
from app.storage.balance_math import remaining_usd_2_from_micros


USD_MICROS = Decimal("1000000")
USD_MICROS_PER_CENT = 10_000


def _dt_iso(value: dt.datetime) -> str:
    return value.astimezone(dt.timezone.utc).isoformat()


def _micros_to_usd_2(value: int) -> float:
    return float((Decimal(int(value)) / USD_MICROS).quantize(Decimal("0.01")))


def ledger_spend_micros_at_entry_expr():
    return (
        select(func.coalesce(func.sum(LlmUsageEvent.cost_usd_micros), 0))
        .where(
            LlmUsageEvent.org_id == BalanceLedgerEntry.org_id,
            LlmUsageEvent.user_id == BalanceLedgerEntry.user_id,
            LlmUsageEvent.created_at <= BalanceLedgerEntry.created_at,
        )
        .correlate(BalanceLedgerEntry)
        .scalar_subquery()
    )


def stage_balance_adjustment_ledger_entry(
    session: AsyncSession,
    *,
    org_id: uuid.UUID,
    user_id: uuid.UUID,
    actor_user_id: uuid.UUID | None,
    balance_before: int,
    balance_after: int,
    entry_type: str = "adjustment",
) -> None:
    delta = int(balance_after) - int(balance_before)
    if delta == 0:
        return
    row = BalanceLedgerEntry(
        org_id=org_id,
        user_id=user_id,
        actor_user_id=actor_user_id,
        entry_type=str(entry_type),
        delta_usd_micros=int(delta) * USD_MICROS_PER_CENT,
        balance_usd_micros=int(balance_after) * USD_MICROS_PER_CENT,
    )
    session.add(row)


async def list_balance_ledger(
    session: AsyncSession,
    *,
    org_id: uuid.UUID,
    user_id: uuid.UUID,
    limit: int = 50,
    offset: int = 0,
) -> list[dict[str, Any]]:
    safe_limit = min(max(int(limit), 1), 200)
    safe_offset = max(int(offset), 0)
    spend_micros_at_entry = ledger_spend_micros_at_entry_expr().label("spend_micros_at_entry")

    rows = (
        await session.execute(
            select(BalanceLedgerEntry, spend_micros_at_entry)
            .where(BalanceLedgerEntry.org_id == org_id, BalanceLedgerEntry.user_id == user_id)
            .order_by(BalanceLedgerEntry.created_at.desc())
            .limit(safe_limit)
            .offset(safe_offset)
        )
    ).all()

    items: list[dict[str, Any]] = []
    for r, spend_micros in rows:
        items.append(
            {
                "id": str(r.id),
                "type": str(r.entry_type),
                "deltaUsd": _micros_to_usd_2(int(r.delta_usd_micros)),
                "balanceUsd": remaining_usd_2_from_micros(
                    credits_usd_micros=int(r.balance_usd_micros),
                    spend_usd_micros_total=int(spend_micros or 0),
                ),
                "createdAt": _dt_iso(r.created_at),
            }
        )
    return items
