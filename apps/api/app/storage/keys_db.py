from __future__ import annotations

import datetime as dt
import uuid
from decimal import Decimal

from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.models.api_key import ApiKey
from app.models.llm_usage_event import LlmUsageEvent
from app.schemas.keys import (
    ApiKeyCreateRequest,
    ApiKeyCreateResponse,
    ApiKeyItem,
    ApiKeysListResponse,
)
from app.security import generate_api_key, key_prefix, sha256_hex


USD_MICROS = Decimal("1000000")


def _dt_iso(value: dt.datetime | None) -> str | None:
    if not value:
        return None
    return value.astimezone(dt.timezone.utc).isoformat()


def _micros_to_usd(value: int) -> float:
    return float((Decimal(int(value)) / USD_MICROS).quantize(Decimal("0.0000001")))


def _to_item(row: ApiKey, *, spend_usd: float = 0.0) -> ApiKeyItem:
    return ApiKeyItem(
        id=str(row.id),
        name=row.name,
        prefix=row.prefix,
        createdAt=_dt_iso(row.created_at) or dt.datetime.now(dt.timezone.utc).isoformat(),
        lastUsedAt=_dt_iso(row.last_used_at),
        revokedAt=_dt_iso(row.revoked_at),
        spendUsd=float(spend_usd),
    )


async def list_api_keys(session: AsyncSession, user_id: uuid.UUID) -> ApiKeysListResponse:
    spend_sub = (
        select(
            LlmUsageEvent.api_key_id.label("api_key_id"),
            func.coalesce(func.sum(LlmUsageEvent.cost_usd_micros), 0).label("cost_micros"),
        )
        .where(LlmUsageEvent.user_id == user_id)
        .group_by(LlmUsageEvent.api_key_id)
        .subquery()
    )

    rows = (
        await session.execute(
            select(ApiKey, func.coalesce(spend_sub.c.cost_micros, 0))
            .outerjoin(spend_sub, spend_sub.c.api_key_id == ApiKey.id)
            .where(ApiKey.user_id == user_id)
            .order_by(ApiKey.created_at.desc())
        )
    ).all()

    items: list[ApiKeyItem] = []
    for key, cost_micros in rows:
        items.append(_to_item(key, spend_usd=_micros_to_usd(int(cost_micros or 0))))
    return ApiKeysListResponse(items=items)


async def create_api_key(
    session: AsyncSession, user_id: uuid.UUID, input: ApiKeyCreateRequest
) -> ApiKeyCreateResponse:
    full_key = generate_api_key()
    row = ApiKey(
        user_id=user_id,
        name=input.name.strip(),
        key_hash=sha256_hex(full_key),
        prefix=key_prefix(full_key),
        key_plaintext=full_key,
        created_at=dt.datetime.now(dt.timezone.utc),
    )
    session.add(row)
    await session.commit()
    await session.refresh(row)
    return ApiKeyCreateResponse(item=_to_item(row, spend_usd=0.0), key=full_key)


async def reveal_api_key(
    session: AsyncSession, key_id: str, *, user_id: uuid.UUID
) -> str | None:
    try:
        parsed = uuid.UUID(key_id)
    except ValueError:
        return None

    row = await session.get(ApiKey, parsed)
    if not row:
        return None
    if row.user_id != user_id:
        return None
    secret = getattr(row, "key_plaintext", None)
    if not isinstance(secret, str) or secret.strip() == "":
        return None
    return secret.strip()


async def revoke_api_key(
    session: AsyncSession, key_id: str, *, user_id: uuid.UUID | None = None
) -> ApiKeyItem | None:
    try:
        parsed = uuid.UUID(key_id)
    except ValueError:
        return None

    row = await session.get(ApiKey, parsed)
    if not row:
        return None
    if user_id is not None and row.user_id != user_id:
        return None
    if row.revoked_at is None:
        row.revoked_at = dt.datetime.now(dt.timezone.utc)
        await session.commit()
        await session.refresh(row)
    return _to_item(row)


async def set_api_key_revoked(
    session: AsyncSession, key_id: str, *, revoked: bool, user_id: uuid.UUID | None = None
) -> ApiKeyItem | None:
    try:
        parsed = uuid.UUID(key_id)
    except ValueError:
        return None

    row = await session.get(ApiKey, parsed)
    if not row:
        return None
    if user_id is not None and row.user_id != user_id:
        return None

    if revoked:
        if row.revoked_at is None:
            row.revoked_at = dt.datetime.now(dt.timezone.utc)
    else:
        if row.revoked_at is not None:
            row.revoked_at = None
    await session.commit()
    await session.refresh(row)
    return _to_item(row)


async def delete_api_key(
    session: AsyncSession, key_id: str, *, user_id: uuid.UUID | None = None
) -> bool:
    try:
        parsed = uuid.UUID(key_id)
    except ValueError:
        return False

    row = await session.get(ApiKey, parsed)
    if not row:
        return False
    if user_id is not None and row.user_id != user_id:
        return False
    await session.delete(row)
    await session.commit()
    return True
