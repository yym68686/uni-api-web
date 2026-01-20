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

_VALID_LEVELS = {"info", "warning", "success", "destructive"}


def _dt_iso(value: dt.datetime) -> str:
    return value.astimezone(dt.timezone.utc).isoformat()


def _normalize_optional_text(value: str | None) -> str | None:
    if value is None:
        return None
    normalized = value.strip()
    return normalized if normalized else None


def _validate_optional_text(value: str | None, *, field: str, min_len: int, max_len: int) -> None:
    if value is None:
        return
    if len(value) < min_len:
        raise ValueError(f"{field} too small (min {min_len})")
    if len(value) > max_len:
        raise ValueError(f"{field} too large (max {max_len})")


def _to_item(row: Announcement) -> AnnouncementItem:
    return AnnouncementItem(
        id=str(row.id),
        title=row.title,
        title_zh=row.title_zh,
        title_en=row.title_en,
        meta=row.meta,
        meta_zh=row.meta_zh,
        meta_en=row.meta_en,
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
    title_zh = _normalize_optional_text(input.title_zh)
    title_en = _normalize_optional_text(input.title_en)
    title_legacy = _normalize_optional_text(input.title)

    meta_zh = _normalize_optional_text(input.meta_zh)
    meta_en = _normalize_optional_text(input.meta_en)
    meta_legacy = _normalize_optional_text(input.meta)

    level = input.level.strip() or "warning"

    if title_legacy and not title_zh and not title_en:
        title_zh = title_legacy
    if meta_legacy and not meta_zh and not meta_en:
        meta_zh = meta_legacy

    _validate_optional_text(title_zh, field="titleZh", min_len=2, max_len=180)
    _validate_optional_text(title_en, field="titleEn", min_len=2, max_len=180)
    _validate_optional_text(title_legacy, field="title", min_len=2, max_len=180)
    _validate_optional_text(meta_zh, field="metaZh", min_len=2, max_len=120)
    _validate_optional_text(meta_en, field="metaEn", min_len=2, max_len=120)
    _validate_optional_text(meta_legacy, field="meta", min_len=2, max_len=120)

    if level not in _VALID_LEVELS:
        raise ValueError("invalid level")

    fallback_title = title_zh or title_en or title_legacy
    fallback_meta = meta_zh or meta_en or meta_legacy
    if not fallback_title:
        raise ValueError("missing title")
    if not fallback_meta:
        raise ValueError("missing meta")

    row = Announcement(
        title=fallback_title,
        title_zh=title_zh,
        title_en=title_en,
        meta=fallback_meta,
        meta_zh=meta_zh,
        meta_en=meta_en,
        level=level,
    )
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

    fields_set = input.model_fields_set

    title_zh = row.title_zh
    title_en = row.title_en
    meta_zh = row.meta_zh
    meta_en = row.meta_en

    if "title_zh" in fields_set:
        title_zh = _normalize_optional_text(input.title_zh)
        _validate_optional_text(title_zh, field="titleZh", min_len=2, max_len=180)
    if "title_en" in fields_set:
        title_en = _normalize_optional_text(input.title_en)
        _validate_optional_text(title_en, field="titleEn", min_len=2, max_len=180)
    if "meta_zh" in fields_set:
        meta_zh = _normalize_optional_text(input.meta_zh)
        _validate_optional_text(meta_zh, field="metaZh", min_len=2, max_len=120)
    if "meta_en" in fields_set:
        meta_en = _normalize_optional_text(input.meta_en)
        _validate_optional_text(meta_en, field="metaEn", min_len=2, max_len=120)

    title_legacy = _normalize_optional_text(input.title) if "title" in fields_set else None
    meta_legacy = _normalize_optional_text(input.meta) if "meta" in fields_set else None
    if title_legacy is not None:
        _validate_optional_text(title_legacy, field="title", min_len=2, max_len=180)
    if meta_legacy is not None:
        _validate_optional_text(meta_legacy, field="meta", min_len=2, max_len=120)

    level = row.level
    if "level" in fields_set:
        next_level = input.level.strip() or "warning"
        if next_level not in _VALID_LEVELS:
            raise ValueError("invalid level")
        level = next_level

    if title_legacy and "title_zh" not in fields_set and "title_en" not in fields_set:
        title_zh = title_legacy
    if meta_legacy and "meta_zh" not in fields_set and "meta_en" not in fields_set:
        meta_zh = meta_legacy

    fallback_title = title_zh or title_en or title_legacy
    fallback_meta = meta_zh or meta_en or meta_legacy
    if not fallback_title:
        raise ValueError("missing title")
    if not fallback_meta:
        raise ValueError("missing meta")

    row.title_zh = title_zh
    row.title_en = title_en
    row.meta_zh = meta_zh
    row.meta_en = meta_en
    row.title = fallback_title
    row.meta = fallback_meta
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
            title_zh="新增：API Keys 支持一键撤销",
            title_en="New: API keys support one-click revoke",
            meta="Today · Security",
            meta_zh="今天 · 安全",
            meta_en="Today · Security",
            level="warning",
            created_at=now - dt.timedelta(hours=2),
        ),
        Announcement(
            title="计费：本月成本统计将接入真实账单",
            title_zh="计费：本月成本统计将接入真实账单",
            title_en="Billing: monthly cost will reflect real invoices",
            meta="This week · Billing",
            meta_zh="本周 · 计费",
            meta_en="This week · Billing",
            level="info",
            created_at=now - dt.timedelta(days=2),
        ),
        Announcement(
            title="模型路由：支持按 Workspace 配置默认模型",
            title_zh="模型路由：支持按 Workspace 配置默认模型",
            title_en="Model routing: default model by workspace",
            meta="Soon · Routing",
            meta_zh="即将 · 路由",
            meta_en="Soon · Routing",
            level="info",
            created_at=now - dt.timedelta(days=6),
        ),
    ]
    session.add_all(seeds)
    await session.commit()
