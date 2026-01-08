from __future__ import annotations

import datetime as dt
import uuid
from urllib.parse import urlparse

from sqlalchemy import delete, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.models.llm_channel import LlmChannel
from app.models.llm_channel_group import LlmChannelGroup
from app.schemas.channels import (
    LlmChannelCreateRequest,
    LlmChannelCreateResponse,
    LlmChannelDeleteResponse,
    LlmChannelItem,
    LlmChannelsListResponse,
    LlmChannelUpdateRequest,
    LlmChannelUpdateResponse,
)

ALLOWED_SCHEMES: set[str] = {"http", "https"}
WILDCARD_GROUPS: set[str] = {"*", "all"}


def _dt_iso(value: dt.datetime) -> str:
    return value.astimezone(dt.timezone.utc).isoformat()


def _mask_api_key(value: str) -> str:
    raw = value.strip()
    if len(raw) <= 10:
        return "********"
    return f"{raw[:6]}…{raw[-4:]}"


def _normalize_base_url(value: str) -> str:
    raw = value.strip()
    parsed = urlparse(raw)
    if parsed.scheme not in ALLOWED_SCHEMES:
        raise ValueError("invalid base url scheme")
    if not parsed.netloc:
        raise ValueError("invalid base url")
    normalized = raw.rstrip("/")
    if len(normalized) > 400:
        raise ValueError("base url too large (max 400)")
    return normalized


def _normalize_name(value: str) -> str:
    name = value.strip()
    if len(name) < 2:
        raise ValueError("name too small (min 2)")
    if len(name) > 64:
        raise ValueError("name too large (max 64)")
    return name


def _normalize_group(value: str) -> str:
    group = value.strip()
    if group == "":
        raise ValueError("invalid group")
    if "\n" in group or "\r" in group:
        raise ValueError("invalid group")
    if len(group) > 64:
        raise ValueError("group too large (max 64)")
    return group


async def _get_groups(session: AsyncSession, channel_id: uuid.UUID) -> list[str]:
    rows = (
        await session.execute(
            select(LlmChannelGroup.group_name).where(LlmChannelGroup.channel_id == channel_id)
        )
    ).scalars().all()
    return [str(r) for r in rows]


def _to_item(row: LlmChannel, groups: list[str]) -> LlmChannelItem:
    return LlmChannelItem(
        id=str(row.id),
        name=row.name,
        baseUrl=row.base_url,
        apiKeyMasked=_mask_api_key(row.api_key),
        allowGroups=sorted(set(groups)),
        createdAt=_dt_iso(row.created_at),
        updatedAt=_dt_iso(row.updated_at),
    )


async def list_channels(session: AsyncSession, *, org_id: uuid.UUID) -> LlmChannelsListResponse:
    rows = (
        await session.execute(
            select(LlmChannel).where(LlmChannel.org_id == org_id).order_by(LlmChannel.created_at.desc())
        )
    ).scalars().all()

    items: list[LlmChannelItem] = []
    for row in rows:
        groups = await _get_groups(session, row.id)
        items.append(_to_item(row, groups))
    return LlmChannelsListResponse(items=items)


async def pick_channel_for_group(
    session: AsyncSession, *, org_id: uuid.UUID, group_name: str
) -> LlmChannel | None:
    group = group_name.strip() or "default"
    channels = (
        await session.execute(
            select(LlmChannel)
            .where(LlmChannel.org_id == org_id)
            .order_by(LlmChannel.created_at.asc())
        )
    ).scalars().all()
    if not channels:
        return None

    ids = [c.id for c in channels]
    group_rows = (
        await session.execute(
            select(LlmChannelGroup.channel_id, LlmChannelGroup.group_name).where(
                LlmChannelGroup.channel_id.in_(ids)
            )
        )
    ).all()
    allow_map: dict[uuid.UUID, set[str]] = {}
    for channel_id, group_name_value in group_rows:
        allow_map.setdefault(channel_id, set()).add(str(group_name_value))

    for c in channels:
        allow = allow_map.get(c.id, set())
        if not allow:
            return c
        if group in allow:
            return c
        if allow.intersection(WILDCARD_GROUPS):
            return c
    return None


async def create_channel(
    session: AsyncSession, *, org_id: uuid.UUID, input: LlmChannelCreateRequest
) -> LlmChannelCreateResponse:
    name = _normalize_name(input.name)
    base_url = _normalize_base_url(input.base_url)
    api_key = str(input.api_key).strip()
    if len(api_key) < 8:
        raise ValueError("api key too small (min 8)")

    row = LlmChannel(org_id=org_id, name=name, base_url=base_url, api_key=api_key)
    session.add(row)
    await session.commit()
    await session.refresh(row)

    groups = []
    for g in input.allow_groups:
        groups.append(_normalize_group(g))
    for g in sorted(set(groups)):
        session.add(LlmChannelGroup(channel_id=row.id, group_name=g))
    await session.commit()

    return LlmChannelCreateResponse(item=_to_item(row, sorted(set(groups))))


async def update_channel(
    session: AsyncSession, *, org_id: uuid.UUID, channel_id: uuid.UUID, input: LlmChannelUpdateRequest
) -> LlmChannelUpdateResponse | None:
    row = await session.get(LlmChannel, channel_id)
    if not row or row.org_id != org_id:
        return None

    if input.name is not None:
        row.name = _normalize_name(input.name)
    if input.base_url is not None:
        row.base_url = _normalize_base_url(input.base_url)
    if input.api_key is not None:
        api_key = str(input.api_key).strip()
        if len(api_key) < 8:
            raise ValueError("api key too small (min 8)")
        row.api_key = api_key

    if input.allow_groups is not None:
        normalized: list[str] = []
        for g in input.allow_groups:
            normalized.append(_normalize_group(g))
        normalized = sorted(set(normalized))
        await session.execute(delete(LlmChannelGroup).where(LlmChannelGroup.channel_id == channel_id))
        for g in normalized:
            session.add(LlmChannelGroup(channel_id=channel_id, group_name=g))

    await session.commit()
    await session.refresh(row)
    groups = await _get_groups(session, row.id)
    return LlmChannelUpdateResponse(item=_to_item(row, groups))


async def delete_channel(
    session: AsyncSession, *, org_id: uuid.UUID, channel_id: uuid.UUID
) -> LlmChannelDeleteResponse | None:
    row = await session.get(LlmChannel, channel_id)
    if not row or row.org_id != org_id:
        return None
    await session.delete(row)
    await session.commit()
    return LlmChannelDeleteResponse(ok=True, id=str(channel_id))
