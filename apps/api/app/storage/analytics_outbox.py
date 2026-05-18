from __future__ import annotations

import datetime as dt
import logging
import uuid
import asyncio
from typing import Any

import httpx
from sqlalchemy import case, func, select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import AsyncSession

from app.core.config import settings
from app.db import SessionLocal
from app.models.analytics_outbox_event import AnalyticsOutboxEvent

logger = logging.getLogger(__name__)


def _now() -> dt.datetime:
    return dt.datetime.now(dt.timezone.utc)


def _clean(value: object, max_len: int = 120) -> str | None:
    if not isinstance(value, str):
        return None
    cleaned = value.strip()
    return cleaned[:max_len] if cleaned else None


def _project_id() -> str:
    return settings.dataocean_project_id.strip() or "uni-api-web"


def dataocean_enabled() -> bool:
    return bool(
        settings.dataocean_outbox_enabled
        and settings.dataocean_collect_url.strip()
        and settings.dataocean_server_key.strip()
    )


async def enqueue_analytics_event(
    session: AsyncSession,
    *,
    name: str,
    user_id: object | None = None,
    anonymous_id: str | None = None,
    session_id: str | None = None,
    occurred_at: dt.datetime | None = None,
    properties: dict[str, Any] | None = None,
    context: dict[str, Any] | None = None,
    event_id: str | None = None,
    commit: bool = True,
) -> AnalyticsOutboxEvent | None:
    event_name = _clean(name, 96)
    if not event_name:
        return None
    if not dataocean_enabled():
        return None

    ts = occurred_at or _now()
    if ts.tzinfo is None:
        ts = ts.replace(tzinfo=dt.timezone.utc)
    else:
        ts = ts.astimezone(dt.timezone.utc)

    row = AnalyticsOutboxEvent(
        event_id=_clean(event_id, 120) or f"uni:{event_name}:{uuid.uuid4()}",
        event_name=event_name,
        project_id=_project_id(),
        user_id=_clean(str(user_id), 120) if user_id is not None else None,
        anonymous_id=_clean(anonymous_id, 120),
        session_id=_clean(session_id, 120),
        occurred_at=ts,
        payload={
            "properties": properties or {},
            "context": context or {},
        },
        status="pending",
        next_attempt_at=ts,
    )
    session.add(row)
    if commit:
        try:
            await session.commit()
        except IntegrityError:
            await session.rollback()
            existing = (
                await session.execute(
                    select(AnalyticsOutboxEvent).where(AnalyticsOutboxEvent.event_id == row.event_id)
                )
            ).scalar_one_or_none()
            return existing
        await session.refresh(row)
    return row


async def enqueue_analytics_event_best_effort(**kwargs: Any) -> None:
    try:
        async with SessionLocal() as session:
            await enqueue_analytics_event(session, **kwargs)
    except Exception:
        logger.exception("dataocean: enqueue failed")


async def process_pending_analytics_events(*, limit: int = 50) -> int:
    if not dataocean_enabled():
        return 0

    async with SessionLocal() as session:
        rows = (
            await session.execute(
                select(AnalyticsOutboxEvent)
                .where(
                    AnalyticsOutboxEvent.status.in_(["pending", "failed"]),
                    (AnalyticsOutboxEvent.next_attempt_at.is_(None) | (AnalyticsOutboxEvent.next_attempt_at <= _now())),
                )
                .order_by(AnalyticsOutboxEvent.created_at.asc())
                .limit(max(1, min(int(limit), 100)))
                .with_for_update(skip_locked=True)
            )
        ).scalars().all()

        if not rows:
            return 0

        events = [_to_dataocean_event(row) for row in rows]
        try:
            await _post_batch(events)
        except Exception as exc:
            retry_at = _now() + dt.timedelta(seconds=min(300, 10 * max(1, max((row.attempts for row in rows), default=0) + 1)))
            message = str(exc)[:500]
            for row in rows:
                row.status = "failed"
                row.attempts = int(row.attempts or 0) + 1
                row.next_attempt_at = retry_at
                row.last_error = message
            await session.commit()
            logger.warning("dataocean: batch push failed: %s", message)
            return 0

        sent_at = _now()
        for row in rows:
            row.status = "sent"
            row.attempts = int(row.attempts or 0) + 1
            row.sent_at = sent_at
            row.next_attempt_at = None
            row.last_error = None
        await session.commit()
        return len(rows)


async def get_dataocean_status(session: AsyncSession) -> dict[str, Any]:
    status_expr = AnalyticsOutboxEvent.status
    row = (
        await session.execute(
            select(
                func.count(AnalyticsOutboxEvent.id).label("total"),
                func.coalesce(func.sum(case((status_expr == "pending", 1), else_=0)), 0).label("pending"),
                func.coalesce(func.sum(case((status_expr == "failed", 1), else_=0)), 0).label("failed"),
                func.coalesce(func.sum(case((status_expr == "sent", 1), else_=0)), 0).label("sent"),
                func.max(AnalyticsOutboxEvent.sent_at).label("last_sent_at"),
                func.max(AnalyticsOutboxEvent.created_at).label("last_queued_at"),
            )
        )
    ).first()
    last_error_row = (
        await session.execute(
            select(AnalyticsOutboxEvent.last_error)
            .where(AnalyticsOutboxEvent.last_error.is_not(None))
            .order_by(AnalyticsOutboxEvent.updated_at.desc())
            .limit(1)
        )
    ).first()
    return {
        "enabled": dataocean_enabled(),
        "collectUrl": settings.dataocean_collect_url.strip() or None,
        "projectId": _project_id(),
        "dashboardUrl": settings.dataocean_dashboard_url.strip() or settings.dataocean_collect_url.strip() or None,
        "publicKeyConfigured": bool(settings.dataocean_public_write_key.strip() or settings.dataocean_server_key.strip()),
        "serverKeyConfigured": bool(settings.dataocean_server_key.strip()),
        "total": int(getattr(row, "total", 0) or 0) if row else 0,
        "pending": int(getattr(row, "pending", 0) or 0) if row else 0,
        "failed": int(getattr(row, "failed", 0) or 0) if row else 0,
        "sent": int(getattr(row, "sent", 0) or 0) if row else 0,
        "lastSentAt": _dt_iso(getattr(row, "last_sent_at", None)) if row else None,
        "lastQueuedAt": _dt_iso(getattr(row, "last_queued_at", None)) if row else None,
        "lastError": str(getattr(last_error_row, "last_error", "") or "")[:500] if last_error_row else None,
    }


async def run_dataocean_outbox_worker(stop_event) -> None:
    interval = max(5, int(settings.dataocean_flush_interval_seconds or 30))
    while not stop_event.is_set():
        try:
            await process_pending_analytics_events()
        except Exception:
            logger.exception("dataocean: worker failed")
        try:
            await asyncio.wait_for(stop_event.wait(), timeout=interval)
        except asyncio.TimeoutError:
            pass


def _to_dataocean_event(row: AnalyticsOutboxEvent) -> dict[str, Any]:
    payload = row.payload if isinstance(row.payload, dict) else {}
    properties = payload.get("properties") if isinstance(payload.get("properties"), dict) else {}
    context = payload.get("context") if isinstance(payload.get("context"), dict) else {}
    return {
        "eventId": row.event_id,
        "name": row.event_name,
        "timestamp": row.occurred_at.astimezone(dt.timezone.utc).isoformat(),
        "anonymousId": row.anonymous_id,
        "sessionId": row.session_id,
        "userId": row.user_id,
        "properties": properties,
        "context": context,
        **{key: value for key, value in payload.items() if key not in {"properties", "context"}},
    }


async def _post_batch(events: list[dict[str, Any]]) -> None:
    base = settings.dataocean_collect_url.strip().rstrip("/")
    if not base:
        raise RuntimeError("DATAOCEAN_COLLECT_URL is not configured")
    async with httpx.AsyncClient(timeout=8.0) as client:
        res = await client.post(
            f"{base}/api/collect/batch",
            headers={"X-DataOcean-Key": settings.dataocean_server_key.strip()},
            json={"projectId": _project_id(), "events": events},
        )
        res.raise_for_status()


def _dt_iso(value: dt.datetime | None) -> str | None:
    if not value:
        return None
    return value.astimezone(dt.timezone.utc).isoformat()
