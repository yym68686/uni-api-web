from __future__ import annotations

import datetime as dt
import uuid
from decimal import ROUND_HALF_UP, Decimal

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.models.api_key import ApiKey
from app.schemas.keys import (
    ApiKeyCreateRequest,
    ApiKeyCreateResponse,
    ApiKeyItem,
    ApiKeysListResponse,
    ApiKeyUpdateRequest,
)
from app.security import generate_api_key, key_prefix, sha256_hex


USD_MICROS = Decimal("1000000")
USD_DECIMAL_2DP = Decimal("0.01")


def _dt_iso(value: dt.datetime | None) -> str | None:
    if not value:
        return None
    return value.astimezone(dt.timezone.utc).isoformat()


def _micros_to_usd(value: int) -> float:
    return float((Decimal(int(value)) / USD_MICROS).quantize(Decimal("0.0000001")))

def _micros_to_usd_2(value: int) -> float:
    return float((Decimal(int(value)) / USD_MICROS).quantize(USD_DECIMAL_2DP))

def _to_item(row: ApiKey) -> ApiKeyItem:
    spend_micros = int(getattr(row, "spend_usd_micros_total", 0) or 0)
    limit_micros = getattr(row, "spend_limit_usd_micros", None)
    limit_usd = _micros_to_usd_2(int(limit_micros)) if limit_micros is not None else None
    return ApiKeyItem(
        id=str(row.id),
        name=row.name,
        prefix=row.prefix,
        createdAt=_dt_iso(row.created_at) or dt.datetime.now(dt.timezone.utc).isoformat(),
        lastUsedAt=_dt_iso(row.last_used_at),
        revokedAt=_dt_iso(row.revoked_at),
        spendUsd=_micros_to_usd(spend_micros),
        spendLimitUsd=limit_usd,
    )


async def list_api_keys(session: AsyncSession, user_id: uuid.UUID) -> ApiKeysListResponse:
    rows = (
        await session.execute(
            select(ApiKey)
            .where(ApiKey.user_id == user_id)
            .order_by(ApiKey.created_at.desc())
        )
    ).scalars().all()

    return ApiKeysListResponse(items=[_to_item(row) for row in rows])


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
    return ApiKeyCreateResponse(item=_to_item(row), key=full_key)


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

async def update_api_key(
    session: AsyncSession, key_id: str, *, input: ApiKeyUpdateRequest, user_id: uuid.UUID | None = None
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

    fields_set = getattr(input, "model_fields_set", None)
    if fields_set is None:
        fields_set = getattr(input, "__fields_set__", set())

    if "name" in fields_set:
        raw = input.name
        name = (raw or "").strip()
        if len(name) < 2:
            raise ValueError("name too small (min 2)")
        if len(name) > 64:
            raise ValueError("name too large (max 64)")
        if "\n" in name or "\r" in name:
            raise ValueError("invalid name")
        row.name = name

    if "revoked" in fields_set and input.revoked is not None:
        if bool(input.revoked):
            if row.revoked_at is None:
                row.revoked_at = dt.datetime.now(dt.timezone.utc)
        else:
            if row.revoked_at is not None:
                row.revoked_at = None

    if "spend_limit_usd" in fields_set:
        raw_limit = input.spend_limit_usd
        if raw_limit is None:
            row.spend_limit_usd_micros = None
        else:
            limit = Decimal(raw_limit)
            if limit <= 0:
                raise ValueError("invalid spend limit")
            quantized = limit.quantize(USD_DECIMAL_2DP, rounding=ROUND_HALF_UP)
            if quantized != limit:
                raise ValueError("spend limit must have at most 2 decimals")
            cents = int((quantized * Decimal("100")).to_integral_value(rounding=ROUND_HALF_UP))
            row.spend_limit_usd_micros = int(max(cents, 0)) * 10_000

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
