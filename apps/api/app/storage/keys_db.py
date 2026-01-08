from __future__ import annotations

import datetime as dt
import uuid
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.models.api_key import ApiKey
from app.schemas.keys import ApiKeyCreateRequest, ApiKeyCreateResponse, ApiKeyItem, ApiKeysListResponse
from app.security import generate_api_key, key_prefix, sha256_hex


def _dt_iso(value: dt.datetime | None) -> str | None:
    if not value:
        return None
    return value.astimezone(dt.timezone.utc).isoformat()


def _to_item(row: ApiKey) -> ApiKeyItem:
    return ApiKeyItem(
        id=str(row.id),
        name=row.name,
        prefix=row.prefix,
        createdAt=_dt_iso(row.created_at) or dt.datetime.now(dt.timezone.utc).isoformat(),
        lastUsedAt=_dt_iso(row.last_used_at),
        revokedAt=_dt_iso(row.revoked_at),
    )


async def list_api_keys(session: AsyncSession, user_id: uuid.UUID) -> ApiKeysListResponse:
    rows = (
        await session.execute(
            select(ApiKey)
            .where(ApiKey.user_id == user_id)
            .order_by(ApiKey.created_at.desc())
        )
    ).scalars().all()
    return ApiKeysListResponse(items=[_to_item(r) for r in rows])


async def create_api_key(
    session: AsyncSession, user_id: uuid.UUID, input: ApiKeyCreateRequest
) -> ApiKeyCreateResponse:
    full_key = generate_api_key()
    row = ApiKey(
        user_id=user_id,
        name=input.name.strip(),
        key_hash=sha256_hex(full_key),
        prefix=key_prefix(full_key),
        created_at=dt.datetime.now(dt.timezone.utc),
    )
    session.add(row)
    await session.commit()
    await session.refresh(row)
    return ApiKeyCreateResponse(item=_to_item(row), key=full_key)


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
