from __future__ import annotations

import datetime as dt
import uuid
from decimal import Decimal
from typing import Any

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.models.balance_ledger_entry import BalanceLedgerEntry


USD_MICROS = Decimal("1000000")


def _dt_iso(value: dt.datetime) -> str:
    return value.astimezone(dt.timezone.utc).isoformat()


def _micros_to_usd_2(value: int) -> float:
    return float((Decimal(int(value)) / USD_MICROS).quantize(Decimal("0.01")))


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
        delta_usd_micros=int(delta) * 1_000_000,
        balance_usd_micros=int(balance_after) * 1_000_000,
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

    rows = (
        await session.execute(
            select(BalanceLedgerEntry)
            .where(BalanceLedgerEntry.org_id == org_id, BalanceLedgerEntry.user_id == user_id)
            .order_by(BalanceLedgerEntry.created_at.desc())
            .limit(safe_limit)
            .offset(safe_offset)
        )
    ).scalars().all()

    items: list[dict[str, Any]] = []
    for r in rows:
        items.append(
            {
                "id": str(r.id),
                "type": str(r.entry_type),
                "deltaUsd": _micros_to_usd_2(int(r.delta_usd_micros)),
                "balanceUsd": _micros_to_usd_2(int(r.balance_usd_micros)),
                "createdAt": _dt_iso(r.created_at),
            }
        )
    return items
