from __future__ import annotations

import datetime as dt
import uuid
from decimal import Decimal
from typing import Any

from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.models.announcement import Announcement
from app.models.balance_ledger_entry import BalanceLedgerEntry
from app.models.llm_channel import LlmChannel
from app.models.llm_model_config import LlmModelConfig
from app.models.llm_usage_event import LlmUsageEvent
from app.models.membership import Membership
from app.models.organization import Organization
from app.models.user import User


USD_MICROS = Decimal("1000000")


def _dt_iso(value: dt.datetime) -> str:
    return value.astimezone(dt.timezone.utc).isoformat()


def _micros_to_usd_2(value: int) -> float:
    return float((Decimal(int(value)) / USD_MICROS).quantize(Decimal("0.01")))


async def get_admin_overview(session: AsyncSession, *, org_id: uuid.UUID) -> dict[str, Any]:
    now = dt.datetime.now(dt.timezone.utc)
    start_24h = now - dt.timedelta(hours=24)

    org = await session.get(Organization, org_id)
    registration_enabled = bool(getattr(org, "registration_enabled", True)) if org else True

    users_total = (
        await session.execute(select(func.count()).select_from(Membership).where(Membership.org_id == org_id))
    ).scalar_one()
    users_banned = (
        await session.execute(
            select(func.count())
            .select_from(Membership)
            .join(User, User.id == Membership.user_id)
            .where(Membership.org_id == org_id, User.banned_at.is_not(None))
        )
    ).scalar_one()
    channels_total = (
        await session.execute(select(func.count()).select_from(LlmChannel).where(LlmChannel.org_id == org_id))
    ).scalar_one()
    models_config_total = (
        await session.execute(select(func.count()).select_from(LlmModelConfig).where(LlmModelConfig.org_id == org_id))
    ).scalar_one()
    models_disabled = (
        await session.execute(
            select(func.count())
            .select_from(LlmModelConfig)
            .where(LlmModelConfig.org_id == org_id, LlmModelConfig.enabled.is_(False))
        )
    ).scalar_one()
    announcements_total = (
        await session.execute(select(func.count()).select_from(Announcement))
    ).scalar_one()

    usage_row = (
        await session.execute(
            select(
                func.count(LlmUsageEvent.id).label("calls"),
                func.count(LlmUsageEvent.id).filter(LlmUsageEvent.ok.is_(False)).label("errors"),
                func.count(func.distinct(LlmUsageEvent.user_id)).label("active_users"),
                func.count(func.distinct(LlmUsageEvent.api_key_id))
                .filter(LlmUsageEvent.api_key_id.is_not(None))
                .label("active_keys"),
                func.coalesce(func.sum(LlmUsageEvent.cost_usd_micros), 0).label("spend_micros"),
            ).where(LlmUsageEvent.org_id == org_id, LlmUsageEvent.created_at >= start_24h)
        )
    ).first()

    calls_24h = int(getattr(usage_row, "calls", 0) or 0) if usage_row else 0
    errors_24h = int(getattr(usage_row, "errors", 0) or 0) if usage_row else 0
    active_users_24h = int(getattr(usage_row, "active_users", 0) or 0) if usage_row else 0
    active_keys_24h = int(getattr(usage_row, "active_keys", 0) or 0) if usage_row else 0
    spend_micros_24h = int(getattr(usage_row, "spend_micros", 0) or 0) if usage_row else 0
    spend_usd_24h = _micros_to_usd_2(spend_micros_24h)

    health: list[dict[str, Any]] = []
    if channels_total <= 0:
        health.append({"id": "no_channels", "level": "destructive"})
    if not registration_enabled:
        health.append({"id": "registration_disabled", "level": "info"})
    if models_disabled > 0:
        health.append({"id": "models_disabled", "level": "warning", "value": int(models_disabled)})
    if users_banned > 0:
        health.append({"id": "users_banned", "level": "info", "value": int(users_banned)})
    if errors_24h > 0:
        health.append({"id": "errors_24h", "level": "warning", "value": int(errors_24h)})

    user_rows = (
        await session.execute(
            select(User)
            .join(Membership, Membership.user_id == User.id)
            .where(Membership.org_id == org_id)
            .order_by(User.created_at.desc())
            .limit(10)
        )
    ).scalars().all()

    ledger_rows = (
        await session.execute(
            select(BalanceLedgerEntry, User.email)
            .join(User, User.id == BalanceLedgerEntry.user_id)
            .where(BalanceLedgerEntry.org_id == org_id)
            .order_by(BalanceLedgerEntry.created_at.desc())
            .limit(10)
        )
    ).all()

    ann_rows = (
        await session.execute(select(Announcement).order_by(Announcement.created_at.desc()).limit(10))
    ).scalars().all()

    channel_rows = (
        await session.execute(
            select(LlmChannel).where(LlmChannel.org_id == org_id).order_by(LlmChannel.updated_at.desc()).limit(10)
        )
    ).scalars().all()

    events: list[dict[str, Any]] = []
    for u in user_rows:
        events.append(
            {
                "id": f"user:{u.id}",
                "type": "user_created",
                "createdAt": _dt_iso(u.created_at),
                "href": "/admin/users",
                "email": u.email,
            }
        )

    for row, email in ledger_rows:
        events.append(
            {
                "id": f"ledger:{row.id}",
                "type": "balance_adjusted",
                "createdAt": _dt_iso(row.created_at),
                "href": "/admin/users",
                "email": str(email),
                "deltaUsd": _micros_to_usd_2(int(row.delta_usd_micros)),
                "balanceUsd": _micros_to_usd_2(int(row.balance_usd_micros)),
            }
        )

    for a in ann_rows:
        events.append(
            {
                "id": f"announcement:{a.id}",
                "type": "announcement_published",
                "createdAt": _dt_iso(a.created_at),
                "href": "/admin/announcements",
                "title": a.title,
                "titleZh": a.title_zh,
                "titleEn": a.title_en,
            }
        )

    for c in channel_rows:
        events.append(
            {
                "id": f"channel:{c.id}",
                "type": "channel_updated",
                "createdAt": _dt_iso(c.updated_at),
                "href": "/admin/channels",
                "channelName": c.name,
            }
        )

    def _created_at_iso(e: dict[str, Any]) -> str:
        raw = e.get("createdAt")
        return str(raw) if isinstance(raw, str) else ""

    events_sorted = sorted(events, key=_created_at_iso, reverse=True)[:15]

    return {
        "registrationEnabled": registration_enabled,
        "kpis": {
            "calls24h": calls_24h,
            "errors24h": errors_24h,
            "spendUsd24h": spend_usd_24h,
            "activeUsers24h": active_users_24h,
            "activeKeys24h": active_keys_24h,
        },
        "counts": {
            "usersTotal": int(users_total),
            "usersBanned": int(users_banned),
            "channelsTotal": int(channels_total),
            "modelsConfigTotal": int(models_config_total),
            "modelsDisabled": int(models_disabled),
            "announcementsTotal": int(announcements_total),
        },
        "health": health,
        "events": events_sorted,
    }

