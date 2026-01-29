from __future__ import annotations

import datetime as dt

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.models.invite_visit import InviteVisit
from app.storage.invites_db import find_user_by_invite_code, normalize_invite_code


VISIT_DEDUP_HOURS = 24


def _normalize_token(value: str | None, *, max_len: int) -> str | None:
    if not isinstance(value, str):
        return None
    v = value.strip()
    if v == "":
        return None
    return v[:max_len]


async def record_invite_visit(
    session: AsyncSession,
    *,
    invite_code: str,
    visitor_device_id: str | None,
    visitor_ip: str | None,
    visitor_user_agent: str | None,
    now: dt.datetime | None = None,
) -> bool:
    raw = str(invite_code or "").strip()
    if raw == "":
        return False

    inviter = await find_user_by_invite_code(session, raw)
    if not inviter:
        return False

    ts = now or dt.datetime.now(dt.timezone.utc)
    cutoff = ts - dt.timedelta(hours=VISIT_DEDUP_HOURS)

    normalized_code = normalize_invite_code(raw)
    device_id = _normalize_token(visitor_device_id, max_len=64)
    ip = _normalize_token(visitor_ip, max_len=64)
    user_agent = _normalize_token(visitor_user_agent, max_len=255)

    if device_id:
        existing = (
            await session.execute(
                select(InviteVisit.id)
                .where(
                    InviteVisit.invite_code == normalized_code,
                    InviteVisit.visitor_device_id == device_id,
                    InviteVisit.created_at >= cutoff,
                )
                .limit(1)
            )
        ).scalar_one_or_none()
        if existing is not None:
            return False
    elif ip and user_agent:
        existing = (
            await session.execute(
                select(InviteVisit.id)
                .where(
                    InviteVisit.invite_code == normalized_code,
                    InviteVisit.visitor_ip == ip,
                    InviteVisit.visitor_user_agent == user_agent,
                    InviteVisit.created_at >= cutoff,
                )
                .limit(1)
            )
        ).scalar_one_or_none()
        if existing is not None:
            return False

    row = InviteVisit(
        inviter_user_id=inviter.id,
        invite_code=normalized_code,
        visitor_device_id=device_id,
        visitor_ip=ip,
        visitor_user_agent=user_agent,
        created_at=ts,
    )
    session.add(row)
    await session.commit()
    return True

