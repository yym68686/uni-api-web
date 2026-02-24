from __future__ import annotations

import datetime as dt
import uuid
from decimal import Decimal
from typing import Any

from sqlalchemy import case, func, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.models.llm_usage_event import LlmUsageEvent
from app.models.user import User


USD_MICROS = Decimal("1000000")


def _dt_iso(value: dt.datetime) -> str:
    return value.astimezone(dt.timezone.utc).isoformat()


def _ensure_aware_utc(value: dt.datetime) -> dt.datetime:
    if value.tzinfo is None:
        return value.replace(tzinfo=dt.timezone.utc)
    return value.astimezone(dt.timezone.utc)


def _micros_to_usd(value: int) -> float:
    return float((Decimal(int(value)) / USD_MICROS).quantize(Decimal("0.0000001")))


def _coerce_granularity(value: str) -> str:
    raw = str(value or "").strip().lower()
    return "hour" if raw == "hour" else "day"


def _coerce_limit(value: int) -> int:
    try:
        n = int(value)
    except Exception:
        return 10
    return min(max(n, 1), 100)


async def get_admin_analytics(
    session: AsyncSession,
    *,
    org_id: uuid.UUID,
    start: dt.datetime,
    end: dt.datetime,
    tz: str,
    granularity: str,
    limit: int,
) -> dict[str, Any]:
    start_utc = _ensure_aware_utc(start)
    end_utc = _ensure_aware_utc(end)
    if start_utc > end_utc:
        raise ValueError("invalid range")

    safe_granularity = _coerce_granularity(granularity)
    safe_limit = _coerce_limit(limit)
    tz_name = str(tz or "UTC").strip() or "UTC"

    where = (
        (LlmUsageEvent.org_id == org_id)
        & (LlmUsageEvent.created_at >= start_utc)
        & (LlmUsageEvent.created_at <= end_utc)
    )

    errors_expr = case((LlmUsageEvent.ok.is_(False), 1), else_=0)

    p95_latency_expr = func.percentile_cont(0.95).within_group(LlmUsageEvent.total_duration_ms)
    p95_ttft_expr = func.percentile_cont(0.95).within_group(LlmUsageEvent.ttft_ms)

    # KPIs across the range
    kpi_row = (
        await session.execute(
            select(
                func.count(LlmUsageEvent.id).label("calls"),
                func.coalesce(func.sum(errors_expr), 0).label("errors"),
                func.count(func.distinct(LlmUsageEvent.user_id)).label("active_users"),
                func.coalesce(func.sum(LlmUsageEvent.input_tokens), 0).label("input_tokens"),
                func.coalesce(func.sum(LlmUsageEvent.output_tokens), 0).label("output_tokens"),
                func.coalesce(func.sum(LlmUsageEvent.cached_tokens), 0).label("cached_tokens"),
                func.coalesce(func.sum(LlmUsageEvent.cost_usd_micros), 0).label("spend_micros"),
                p95_latency_expr.label("p95_latency_ms"),
                p95_ttft_expr.label("p95_ttft_ms"),
            ).where(where)
        )
    ).first()

    calls = int(getattr(kpi_row, "calls", 0) or 0) if kpi_row else 0
    errors = int(getattr(kpi_row, "errors", 0) or 0) if kpi_row else 0
    active_users = int(getattr(kpi_row, "active_users", 0) or 0) if kpi_row else 0
    input_tokens = int(getattr(kpi_row, "input_tokens", 0) or 0) if kpi_row else 0
    output_tokens = int(getattr(kpi_row, "output_tokens", 0) or 0) if kpi_row else 0
    cached_tokens = int(getattr(kpi_row, "cached_tokens", 0) or 0) if kpi_row else 0
    spend_micros = int(getattr(kpi_row, "spend_micros", 0) or 0) if kpi_row else 0
    p95_latency = getattr(kpi_row, "p95_latency_ms", None) if kpi_row else None
    p95_ttft = getattr(kpi_row, "p95_ttft_ms", None) if kpi_row else None

    spend_usd = _micros_to_usd(spend_micros)

    # Timeseries
    bucket = func.date_trunc(safe_granularity, LlmUsageEvent.created_at).label("bucket")
    series_rows = (
        await session.execute(
            select(
                bucket,
                func.count(LlmUsageEvent.id).label("calls"),
                func.coalesce(func.sum(errors_expr), 0).label("errors"),
                func.coalesce(func.sum(LlmUsageEvent.input_tokens), 0).label("input_tokens"),
                func.coalesce(func.sum(LlmUsageEvent.output_tokens), 0).label("output_tokens"),
                func.coalesce(func.sum(LlmUsageEvent.cached_tokens), 0).label("cached_tokens"),
                func.coalesce(func.sum(LlmUsageEvent.cost_usd_micros), 0).label("spend_micros"),
                p95_latency_expr.label("p95_latency_ms"),
            )
            .where(where)
            .group_by(bucket)
            .order_by(bucket)
        )
    ).all()

    series: list[dict[str, Any]] = []
    for row in series_rows:
        ts_dt = getattr(row, "bucket", None)
        if not isinstance(ts_dt, dt.datetime):
            continue
        series.append(
            {
                "ts": _dt_iso(ts_dt),
                "spendUsd": _micros_to_usd(int(getattr(row, "spend_micros", 0) or 0)),
                "calls": int(getattr(row, "calls", 0) or 0),
                "errors": int(getattr(row, "errors", 0) or 0),
                "inputTokens": int(getattr(row, "input_tokens", 0) or 0),
                "outputTokens": int(getattr(row, "output_tokens", 0) or 0),
                "cachedTokens": int(getattr(row, "cached_tokens", 0) or 0),
                "p95LatencyMs": float(getattr(row, "p95_latency_ms", 0) or 0) if getattr(row, "p95_latency_ms", None) is not None else None,
            }
        )

    # Leaders: users (spend)
    users_rows = (
        await session.execute(
            select(
                LlmUsageEvent.user_id.label("user_id"),
                User.email.label("email"),
                func.coalesce(func.sum(LlmUsageEvent.cost_usd_micros), 0).label("spend_micros"),
                func.count(LlmUsageEvent.id).label("calls"),
                func.coalesce(func.sum(errors_expr), 0).label("errors"),
                func.coalesce(func.sum(LlmUsageEvent.total_tokens), 0).label("total_tokens"),
            )
            .join(User, User.id == LlmUsageEvent.user_id)
            .where(where)
            .group_by(LlmUsageEvent.user_id, User.email)
            .order_by(func.coalesce(func.sum(LlmUsageEvent.cost_usd_micros), 0).desc())
            .limit(safe_limit)
        )
    ).all()

    users: list[dict[str, Any]] = []
    for row in users_rows:
        user_id = getattr(row, "user_id", None)
        if not isinstance(user_id, uuid.UUID):
            continue
        users.append(
            {
                "userId": str(user_id),
                "email": (str(getattr(row, "email")) if getattr(row, "email", None) is not None else None),
                "spendUsd": _micros_to_usd(int(getattr(row, "spend_micros", 0) or 0)),
                "calls": int(getattr(row, "calls", 0) or 0),
                "errors": int(getattr(row, "errors", 0) or 0),
                "totalTokens": int(getattr(row, "total_tokens", 0) or 0),
            }
        )

    # Leaders: models (tokens)
    models_rows = (
        await session.execute(
            select(
                LlmUsageEvent.model_id.label("model"),
                func.coalesce(func.sum(LlmUsageEvent.cost_usd_micros), 0).label("spend_micros"),
                func.count(LlmUsageEvent.id).label("calls"),
                func.coalesce(func.sum(errors_expr), 0).label("errors"),
                func.coalesce(func.sum(LlmUsageEvent.total_tokens), 0).label("total_tokens"),
            )
            .where(where)
            .group_by(LlmUsageEvent.model_id)
            .order_by(func.coalesce(func.sum(LlmUsageEvent.total_tokens), 0).desc())
            .limit(safe_limit)
        )
    ).all()

    models: list[dict[str, Any]] = []
    for row in models_rows:
        model = str(getattr(row, "model", "") or "").strip()
        if model == "":
            continue
        models.append(
            {
                "model": model,
                "spendUsd": _micros_to_usd(int(getattr(row, "spend_micros", 0) or 0)),
                "calls": int(getattr(row, "calls", 0) or 0),
                "errors": int(getattr(row, "errors", 0) or 0),
                "totalTokens": int(getattr(row, "total_tokens", 0) or 0),
            }
        )

    # Leaders: errors by status code
    errors_rows = (
        await session.execute(
            select(
                LlmUsageEvent.status_code.label("status_code"),
                func.count(LlmUsageEvent.id).label("count"),
            )
            .where(where, LlmUsageEvent.ok.is_(False))
            .group_by(LlmUsageEvent.status_code)
            .order_by(func.count(LlmUsageEvent.id).desc())
            .limit(safe_limit)
        )
    ).all()

    errors_total = max(errors, 0)
    errors_leaders: list[dict[str, Any]] = []
    for row in errors_rows:
        code = int(getattr(row, "status_code", 0) or 0)
        count = int(getattr(row, "count", 0) or 0)
        share = float(count / errors_total) if errors_total > 0 else None
        errors_leaders.append({"key": str(code), "count": count, "share": share})

    return {
        "range": {
            "from": _dt_iso(start_utc),
            "to": _dt_iso(end_utc),
            "tz": tz_name,
            "granularity": safe_granularity,
        },
        "kpis": {
            "spendUsd": spend_usd,
            "calls": calls,
            "errors": errors,
            "activeUsers": active_users,
            "inputTokens": input_tokens,
            "outputTokens": output_tokens,
            "cachedTokens": cached_tokens,
            "p95LatencyMs": float(p95_latency) if p95_latency is not None else None,
            "p95TtftMs": float(p95_ttft) if p95_ttft is not None else None,
        },
        "series": series,
        "leaders": {"users": users, "models": models, "errors": errors_leaders},
    }

