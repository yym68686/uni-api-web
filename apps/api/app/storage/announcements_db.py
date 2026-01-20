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
        content=row.content,
        content_zh=row.content_zh,
        content_en=row.content_en,
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

    content_zh = _normalize_optional_text(input.content_zh)
    content_en = _normalize_optional_text(input.content_en)
    content_legacy = _normalize_optional_text(input.content)

    level = input.level.strip() or "warning"

    if title_legacy and not title_zh and not title_en:
        title_zh = title_legacy
    if content_legacy and not content_zh and not content_en:
        content_zh = content_legacy

    _validate_optional_text(title_zh, field="titleZh", min_len=2, max_len=180)
    _validate_optional_text(title_en, field="titleEn", min_len=2, max_len=180)
    _validate_optional_text(title_legacy, field="title", min_len=2, max_len=180)
    _validate_optional_text(content_zh, field="contentZh", min_len=2, max_len=2000)
    _validate_optional_text(content_en, field="contentEn", min_len=2, max_len=2000)
    _validate_optional_text(content_legacy, field="content", min_len=2, max_len=2000)

    if level not in _VALID_LEVELS:
        raise ValueError("invalid level")

    fallback_title = title_zh or title_en or title_legacy
    fallback_content = content_zh or content_en or content_legacy
    if not fallback_title:
        raise ValueError("missing title")
    if not fallback_content:
        raise ValueError("missing content")

    row = Announcement(
        title=fallback_title,
        title_zh=title_zh,
        title_en=title_en,
        content=fallback_content,
        content_zh=content_zh,
        content_en=content_en,
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
    content_zh = row.content_zh
    content_en = row.content_en

    if "title_zh" in fields_set:
        title_zh = _normalize_optional_text(input.title_zh)
        _validate_optional_text(title_zh, field="titleZh", min_len=2, max_len=180)
    if "title_en" in fields_set:
        title_en = _normalize_optional_text(input.title_en)
        _validate_optional_text(title_en, field="titleEn", min_len=2, max_len=180)
    if "content_zh" in fields_set:
        content_zh = _normalize_optional_text(input.content_zh)
        _validate_optional_text(content_zh, field="contentZh", min_len=2, max_len=2000)
    if "content_en" in fields_set:
        content_en = _normalize_optional_text(input.content_en)
        _validate_optional_text(content_en, field="contentEn", min_len=2, max_len=2000)

    title_legacy = _normalize_optional_text(input.title) if "title" in fields_set else None
    content_legacy = _normalize_optional_text(input.content) if "content" in fields_set else None
    if title_legacy is not None:
        _validate_optional_text(title_legacy, field="title", min_len=2, max_len=180)
    if content_legacy is not None:
        _validate_optional_text(content_legacy, field="content", min_len=2, max_len=2000)

    level = row.level
    if "level" in fields_set:
        next_level = input.level.strip() or "warning"
        if next_level not in _VALID_LEVELS:
            raise ValueError("invalid level")
        level = next_level

    if title_legacy and "title_zh" not in fields_set and "title_en" not in fields_set:
        title_zh = title_legacy
    if content_legacy and "content_zh" not in fields_set and "content_en" not in fields_set:
        content_zh = content_legacy

    fallback_title = title_zh or title_en or title_legacy
    fallback_content = content_zh or content_en or content_legacy
    if not fallback_title:
        raise ValueError("missing title")
    if not fallback_content:
        raise ValueError("missing content")

    row.title_zh = title_zh
    row.title_en = title_en
    row.content_zh = content_zh
    row.content_en = content_en
    row.title = fallback_title
    row.content = fallback_content
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
            content="API keys can now be revoked (and restored) at any time in the console.",
            content_zh="API Keys 现在支持随时撤销与恢复。",
            content_en="API keys can now be revoked (and restored) at any time in the console.",
            level="warning",
            created_at=now - dt.timedelta(hours=2),
        ),
        Announcement(
            title="计费：本月成本统计将接入真实账单",
            title_zh="计费：本月成本统计将接入真实账单",
            title_en="Billing: monthly cost will reflect real invoices",
            content="Billing will show a clearer breakdown of balance adjustments and model spend.",
            content_zh="账单页将展示更清晰的余额变更记录与模型消费统计。",
            content_en="Billing will show a clearer breakdown of balance adjustments and model spend.",
            level="info",
            created_at=now - dt.timedelta(days=2),
        ),
        Announcement(
            title="模型路由：支持按 Workspace 配置默认模型",
            title_zh="模型路由：支持按 Workspace 配置默认模型",
            title_en="Model routing: default model by workspace",
            content="You will be able to set default models per workspace to reduce misconfiguration.",
            content_zh="即将支持按工作区设置默认模型，减少误配与来回沟通。",
            content_en="You will be able to set default models per workspace to reduce misconfiguration.",
            level="info",
            created_at=now - dt.timedelta(days=6),
        ),
    ]
    session.add_all(seeds)
    await session.commit()
