from __future__ import annotations

import datetime as dt
import uuid

from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.models.announcement import Announcement
from app.schemas.announcements import (
    AnnouncementCreateRequest,
    AnnouncementCreateResponse,
    AnnouncementDeleteResponse,
    AnnouncementItem,
    AnnouncementsListResponse,
    AnnouncementUpdateRequest,
    AnnouncementUpdateResponse,
)


def _dt_iso(value: dt.datetime) -> str:
    return value.astimezone(dt.timezone.utc).isoformat()


def _to_item(row: Announcement) -> AnnouncementItem:
    return AnnouncementItem(
        id=str(row.id),
        title=row.title,
        meta=row.meta,
        level=row.level,
        createdAt=_dt_iso(row.created_at),
    )


async def list_announcements(session: AsyncSession, limit: int = 6) -> AnnouncementsListResponse:
    rows = (
        await session.execute(
            select(Announcement).order_by(Announcement.created_at.desc()).limit(limit)
        )
    ).scalars().all()
    return AnnouncementsListResponse(items=[_to_item(r) for r in rows])


async def create_announcement(
    session: AsyncSession, input: AnnouncementCreateRequest
) -> AnnouncementCreateResponse:
    title = input.title.strip()
    meta = input.meta.strip()
    level = input.level.strip() or "warning"

    if len(title) < 2:
        raise ValueError("title too small (min 2)")
    if len(title) > 180:
        raise ValueError("title too large (max 180)")
    if len(meta) < 2:
        raise ValueError("meta too small (min 2)")
    if len(meta) > 120:
        raise ValueError("meta too large (max 120)")
    if level not in {"info", "warning", "success", "destructive"}:
        raise ValueError("invalid level")

    row = Announcement(title=title, meta=meta, level=level)
    session.add(row)
    await session.commit()
    await session.refresh(row)
    return AnnouncementCreateResponse(item=_to_item(row))


async def update_announcement(
    session: AsyncSession, announcement_id: str, input: AnnouncementUpdateRequest
) -> AnnouncementUpdateResponse | None:
    try:
        announcement_uuid = uuid.UUID(announcement_id)
    except ValueError:
        raise ValueError("invalid announcement id") from None

    row = (
        await session.execute(select(Announcement).where(Announcement.id == announcement_uuid))
    ).scalar_one_or_none()
    if not row:
        return None

    title = input.title.strip()
    meta = input.meta.strip()
    level = input.level.strip() or "warning"

    if len(title) < 2:
        raise ValueError("title too small (min 2)")
    if len(title) > 180:
        raise ValueError("title too large (max 180)")
    if len(meta) < 2:
        raise ValueError("meta too small (min 2)")
    if len(meta) > 120:
        raise ValueError("meta too large (max 120)")
    if level not in {"info", "warning", "success", "destructive"}:
        raise ValueError("invalid level")

    row.title = title
    row.meta = meta
    row.level = level
    await session.commit()
    await session.refresh(row)
    return AnnouncementUpdateResponse(item=_to_item(row))


async def delete_announcement(
    session: AsyncSession, announcement_id: str
) -> AnnouncementDeleteResponse | None:
    try:
        announcement_uuid = uuid.UUID(announcement_id)
    except ValueError:
        raise ValueError("invalid announcement id") from None

    row = (
        await session.execute(select(Announcement).where(Announcement.id == announcement_uuid))
    ).scalar_one_or_none()
    if not row:
        return None

    await session.delete(row)
    await session.commit()
    return AnnouncementDeleteResponse(ok=True, id=announcement_id)


async def ensure_seed_announcements(session: AsyncSession) -> None:
    count = (await session.execute(select(func.count()).select_from(Announcement))).scalar_one()
    if count > 0:
        return

    now = dt.datetime.now(dt.timezone.utc)
    seeds = [
        Announcement(
            title="新增：API Keys 支持一键撤销",
            meta="Today · Security",
            level="warning",
            created_at=now - dt.timedelta(hours=2),
        ),
        Announcement(
            title="计费：本月成本统计将接入真实账单",
            meta="This week · Billing",
            level="info",
            created_at=now - dt.timedelta(days=2),
        ),
        Announcement(
            title="模型路由：支持按 Workspace 配置默认模型",
            meta="Soon · Routing",
            level="info",
            created_at=now - dt.timedelta(days=6),
        ),
    ]
    session.add_all(seeds)
    await session.commit()
