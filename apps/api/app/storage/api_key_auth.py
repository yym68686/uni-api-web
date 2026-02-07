from __future__ import annotations

import datetime as dt

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.models.api_key import ApiKey
from app.models.user import User
from app.security import sha256_hex
from app.storage.orgs_db import ensure_default_org, ensure_membership


def _extract_bearer_token(value: str | None) -> str | None:
    if not value:
        return None
    prefix = "bearer "
    if value.lower().startswith(prefix):
        token = value[len(prefix) :].strip()
        return token or None
    return None


async def authenticate_api_key(
    session: AsyncSession, *, authorization: str | None
) -> tuple[ApiKey, User]:
    token = _extract_bearer_token(authorization)
    if not token:
        raise ValueError("missing api key")
    if not token.startswith("sk-"):
        raise ValueError("invalid api key")

    token_hash = sha256_hex(token)
    row = (
        await session.execute(
            select(ApiKey).where(ApiKey.key_hash == token_hash, ApiKey.revoked_at.is_(None))
        )
    ).scalar_one_or_none()
    if not row:
        raise ValueError("invalid api key")

    user = await session.get(User, row.user_id)
    if not user:
        raise ValueError("invalid api key")
    if user.banned_at is not None:
        raise ValueError("banned")

    limit = getattr(row, "spend_limit_usd_micros", None)
    spend_total = int(getattr(row, "spend_usd_micros_total", 0) or 0)
    if limit is not None and spend_total >= int(limit):
        raise ValueError("api_key_spend_limit_exceeded")

    row.last_used_at = dt.datetime.now(dt.timezone.utc)
    await session.commit()
    await session.refresh(row)

    org = await ensure_default_org(session)
    await ensure_membership(session, org_id=org.id, user_id=user.id, role="developer")
    return row, user
