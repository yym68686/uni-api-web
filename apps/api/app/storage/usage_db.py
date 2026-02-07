from __future__ import annotations

import datetime as dt
import uuid
from decimal import Decimal
from typing import Any

from sqlalchemy import case, func, select, update
from sqlalchemy.ext.asyncio import AsyncSession

from app.models.api_key import ApiKey
from app.models.llm_usage_event import LlmUsageEvent


USD_MICROS = Decimal("1000000")


def _iso_day(value: dt.datetime) -> str:
    return value.date().isoformat()


async def record_usage_event(
    session: AsyncSession,
    *,
    org_id: uuid.UUID,
    user_id: uuid.UUID,
    api_key_id: uuid.UUID | None = None,
    model_id: str,
    ok: bool,
    status_code: int,
    input_tokens: int = 0,
    cached_tokens: int = 0,
    output_tokens: int = 0,
    total_tokens: int = 0,
    cost_usd_micros: int = 0,
    total_duration_ms: int = 0,
    ttft_ms: int = 0,
    source_ip: str | None = None,
) -> None:
    computed_cost = int(max(0, cost_usd_micros))
    row = LlmUsageEvent(
        org_id=org_id,
        user_id=user_id,
        api_key_id=api_key_id,
        model_id=model_id,
        ok=bool(ok),
        status_code=int(status_code),
        input_tokens=int(max(0, input_tokens)),
        cached_tokens=int(max(0, cached_tokens)),
        output_tokens=int(max(0, output_tokens)),
        total_tokens=int(max(0, total_tokens)),
        cost_usd_micros=computed_cost,
        total_duration_ms=int(max(0, total_duration_ms)),
        ttft_ms=int(max(0, ttft_ms)),
        source_ip=(source_ip.strip()[:64] if isinstance(source_ip, str) and source_ip.strip() else None),
    )
    session.add(row)

    if api_key_id is not None and computed_cost > 0:
        await session.execute(
            update(ApiKey)
            .where(ApiKey.id == api_key_id)
            .values(spend_usd_micros_total=ApiKey.spend_usd_micros_total + computed_cost)
        )
    await session.commit()


def _micros_to_usd(value: int) -> float:
    return float((Decimal(int(value)) / USD_MICROS).quantize(Decimal("0.0000001")))


def _dt_iso(value: dt.datetime) -> str:
    return value.astimezone(dt.timezone.utc).isoformat()


async def list_usage_events(
    session: AsyncSession,
    *,
    org_id: uuid.UUID,
    user_id: uuid.UUID,
    limit: int = 50,
    offset: int = 0,
) -> list[dict[str, Any]]:
    safe_limit = min(max(int(limit), 1), 200)
    safe_offset = max(int(offset), 0)
    rows = (
        await session.execute(
            select(LlmUsageEvent)
            .where(LlmUsageEvent.org_id == org_id, LlmUsageEvent.user_id == user_id)
            .order_by(LlmUsageEvent.created_at.desc())
            .limit(safe_limit)
            .offset(safe_offset)
        )
    ).scalars().all()

    items: list[dict[str, Any]] = []
    for r in rows:
        total_ms = int(getattr(r, "total_duration_ms", 0) or 0)
        ttft_ms = int(getattr(r, "ttft_ms", 0) or 0)
        denom_ms = max(total_ms - ttft_ms, 0)
        tps = None
        if int(r.output_tokens) > 0 and denom_ms > 0:
            tps = float(Decimal(int(r.output_tokens)) / (Decimal(int(denom_ms)) / Decimal("1000")))
        items.append(
            {
                "id": str(r.id),
                "model": str(r.model_id),
                "createdAt": _dt_iso(r.created_at),
                "ok": bool(r.ok),
                "statusCode": int(r.status_code),
                "inputTokens": int(r.input_tokens),
                "cachedTokens": int(getattr(r, "cached_tokens", 0) or 0),
                "outputTokens": int(r.output_tokens),
                "totalDurationMs": total_ms,
                "ttftMs": ttft_ms,
                "tps": tps,
                "costUsd": _micros_to_usd(int(r.cost_usd_micros)),
                "sourceIp": (str(r.source_ip) if r.source_ip else None),
            }
        )
    return items


async def get_usage_response(
    session: AsyncSession, *, org_id: uuid.UUID, user_id: uuid.UUID, days: int = 7
) -> dict:
    now = dt.datetime.now(dt.timezone.utc)
    start_24h = now - dt.timedelta(hours=24)
    start_days = now - dt.timedelta(days=days)

    errors_expr = case((LlmUsageEvent.ok.is_(False), 1), else_=0)

    summary_row = (
        await session.execute(
            select(
                func.count(LlmUsageEvent.id),
                func.coalesce(func.sum(LlmUsageEvent.total_tokens), 0),
                func.coalesce(func.sum(errors_expr), 0),
                func.coalesce(func.sum(LlmUsageEvent.cost_usd_micros), 0),
            ).where(
                LlmUsageEvent.org_id == org_id,
                LlmUsageEvent.user_id == user_id,
                LlmUsageEvent.created_at >= start_24h,
            )
        )
    ).one()

    requests_24h = int(summary_row[0])
    tokens_24h = int(summary_row[1] or 0)
    errors_24h = int(summary_row[2] or 0)
    spend_24h_micros = int(summary_row[3] or 0)
    error_rate_24h = float(errors_24h / requests_24h) if requests_24h > 0 else 0.0
    spend_24h_usd = float((Decimal(spend_24h_micros) / USD_MICROS).quantize(Decimal("0.0000001")))

    # Daily points
    daily_rows = (
        await session.execute(
            select(
                func.date_trunc("day", LlmUsageEvent.created_at).label("day"),
                func.count(LlmUsageEvent.id),
                func.coalesce(func.sum(LlmUsageEvent.input_tokens), 0),
                func.coalesce(func.sum(LlmUsageEvent.output_tokens), 0),
                func.coalesce(func.sum(LlmUsageEvent.total_tokens), 0),
                func.coalesce(func.sum(errors_expr), 0),
            )
            .where(
                LlmUsageEvent.org_id == org_id,
                LlmUsageEvent.user_id == user_id,
                LlmUsageEvent.created_at >= start_days,
            )
            .group_by("day")
            .order_by("day")
        )
    ).all()

    points_map: dict[str, dict] = {}
    for day_dt, reqs, in_t, out_t, total_t, errs in daily_rows:
        day_key = _iso_day(day_dt)
        reqs_i = int(reqs)
        errs_i = int(errs or 0)
        points_map[day_key] = {
            "date": day_key,
            "requests": reqs_i,
            "inputTokens": int(in_t or 0),
            "outputTokens": int(out_t or 0),
            "totalTokens": int(total_t or 0),
            "errorRate": float(errs_i / reqs_i) if reqs_i > 0 else 0.0,
        }

    # Ensure contiguous last N days.
    daily: list[dict] = []
    today = dt.datetime.now(dt.timezone.utc).date()
    for i in range(days - 1, -1, -1):
        day = (today - dt.timedelta(days=i)).isoformat()
        daily.append(
            points_map.get(
                day,
                {
                    "date": day,
                    "requests": 0,
                    "inputTokens": 0,
                    "outputTokens": 0,
                    "totalTokens": 0,
                    "errorRate": 0.0,
                },
            )
        )

    # Top models (24h)
    top_rows = (
        await session.execute(
            select(
                LlmUsageEvent.model_id,
                func.count(LlmUsageEvent.id),
                func.coalesce(func.sum(LlmUsageEvent.total_tokens), 0),
            )
            .where(
                LlmUsageEvent.org_id == org_id,
                LlmUsageEvent.user_id == user_id,
                LlmUsageEvent.created_at >= start_24h,
            )
            .group_by(LlmUsageEvent.model_id)
            .order_by(func.count(LlmUsageEvent.id).desc())
            .limit(8)
        )
    ).all()
    top_models = [
        {"model": str(model), "requests": int(reqs), "tokens": int(tokens or 0)}
        for model, reqs, tokens in top_rows
    ]

    return {
        "summary": {
            "requests24h": requests_24h,
            "tokens24h": tokens_24h,
            "errorRate24h": error_rate_24h,
            "spend24hUsd": spend_24h_usd,
        },
        "daily": daily,
        "topModels": top_models,
    }
