from __future__ import annotations

import datetime as dt
import uuid
from decimal import Decimal
from typing import Any

from sqlalchemy import text
from sqlalchemy.ext.asyncio import AsyncSession


USD_MICROS = Decimal("1000000")


_ADMIN_ANALYTICS_SQL = text(
    """
    WITH filtered AS MATERIALIZED (
      SELECT
        user_id,
        model_id,
        ok,
        status_code,
        input_tokens,
        output_tokens,
        cached_tokens,
        total_tokens,
        cost_usd_micros,
        total_duration_ms,
        ttft_ms,
        created_at
      FROM llm_usage_events
      WHERE org_id = :org_id
        AND created_at >= :start_utc
        AND created_at <= :end_utc
    ),
    kpi AS (
      SELECT
        COUNT(*) AS calls,
        COALESCE(SUM(CASE WHEN ok IS FALSE THEN 1 ELSE 0 END), 0) AS errors,
        COALESCE(SUM(input_tokens), 0) AS input_tokens,
        COALESCE(SUM(output_tokens), 0) AS output_tokens,
        COALESCE(SUM(cached_tokens), 0) AS cached_tokens,
        COALESCE(SUM(cost_usd_micros), 0) AS spend_micros,
        percentile_cont(0.95) WITHIN GROUP (ORDER BY total_duration_ms) AS p95_latency_ms,
        percentile_cont(0.95) WITHIN GROUP (ORDER BY ttft_ms) AS p95_ttft_ms
      FROM filtered
    ),
    series AS (
      SELECT
        date_trunc(:granularity, created_at) AS bucket,
        COUNT(*) AS calls,
        COALESCE(SUM(CASE WHEN ok IS FALSE THEN 1 ELSE 0 END), 0) AS errors,
        COALESCE(SUM(input_tokens), 0) AS input_tokens,
        COALESCE(SUM(output_tokens), 0) AS output_tokens,
        COALESCE(SUM(cached_tokens), 0) AS cached_tokens,
        COALESCE(SUM(cost_usd_micros), 0) AS spend_micros,
        percentile_cont(0.95) WITHIN GROUP (ORDER BY total_duration_ms) AS p95_latency_ms
      FROM filtered
      GROUP BY bucket
    ),
    users_grouped AS (
      SELECT
        filtered.user_id AS user_id,
        users.email AS email,
        COALESCE(SUM(filtered.cost_usd_micros), 0) AS spend_micros,
        COUNT(*) AS calls,
        COALESCE(SUM(CASE WHEN filtered.ok IS FALSE THEN 1 ELSE 0 END), 0) AS errors,
        COALESCE(SUM(filtered.total_tokens), 0) AS total_tokens
      FROM filtered
      JOIN users ON users.id = filtered.user_id
      GROUP BY filtered.user_id, users.email
    ),
    user_count AS (
      SELECT COUNT(*) AS active_users
      FROM users_grouped
    ),
    top_users AS (
      SELECT
        user_id,
        email,
        spend_micros,
        calls,
        errors,
        total_tokens,
        ROW_NUMBER() OVER (ORDER BY spend_micros DESC, user_id ASC) AS sort_rank
      FROM users_grouped
      ORDER BY spend_micros DESC, user_id ASC
      LIMIT :limit
    ),
    models_grouped AS (
      SELECT
        model_id AS model,
        COALESCE(SUM(cost_usd_micros), 0) AS spend_micros,
        COUNT(*) AS calls,
        COALESCE(SUM(CASE WHEN ok IS FALSE THEN 1 ELSE 0 END), 0) AS errors,
        COALESCE(SUM(total_tokens), 0) AS total_tokens
      FROM filtered
      WHERE BTRIM(model_id) <> ''
      GROUP BY model_id
    ),
    top_models AS (
      SELECT
        model,
        spend_micros,
        calls,
        errors,
        total_tokens,
        ROW_NUMBER() OVER (ORDER BY total_tokens DESC, model ASC) AS sort_rank
      FROM models_grouped
      ORDER BY total_tokens DESC, model ASC
      LIMIT :limit
    ),
    errors_grouped AS (
      SELECT
        status_code,
        COUNT(*) AS count
      FROM filtered
      WHERE ok IS FALSE
      GROUP BY status_code
    ),
    top_errors AS (
      SELECT
        status_code,
        count,
        ROW_NUMBER() OVER (ORDER BY count DESC, status_code ASC) AS sort_rank
      FROM errors_grouped
      ORDER BY count DESC, status_code ASC
      LIMIT :limit
    )
    SELECT
      'kpi' AS row_kind,
      CAST(NULL AS TIMESTAMP WITH TIME ZONE) AS bucket,
      CAST(NULL AS uuid) AS user_id,
      CAST(NULL AS varchar) AS email,
      CAST(NULL AS varchar) AS model,
      CAST(NULL AS integer) AS status_code,
      kpi.calls AS calls,
      kpi.errors AS errors,
      kpi.input_tokens AS input_tokens,
      kpi.output_tokens AS output_tokens,
      kpi.cached_tokens AS cached_tokens,
      CAST(NULL AS bigint) AS total_tokens,
      user_count.active_users AS active_users,
      kpi.spend_micros AS spend_micros,
      kpi.p95_latency_ms AS p95_latency_ms,
      kpi.p95_ttft_ms AS p95_ttft_ms,
      CAST(NULL AS bigint) AS sort_rank,
      0 AS sort_group
    FROM kpi
    CROSS JOIN user_count
    UNION ALL
    SELECT
      'series' AS row_kind,
      series.bucket AS bucket,
      CAST(NULL AS uuid) AS user_id,
      CAST(NULL AS varchar) AS email,
      CAST(NULL AS varchar) AS model,
      CAST(NULL AS integer) AS status_code,
      series.calls AS calls,
      series.errors AS errors,
      series.input_tokens AS input_tokens,
      series.output_tokens AS output_tokens,
      series.cached_tokens AS cached_tokens,
      CAST(NULL AS bigint) AS total_tokens,
      CAST(NULL AS bigint) AS active_users,
      series.spend_micros AS spend_micros,
      series.p95_latency_ms AS p95_latency_ms,
      CAST(NULL AS double precision) AS p95_ttft_ms,
      CAST(NULL AS bigint) AS sort_rank,
      1 AS sort_group
    FROM series
    UNION ALL
    SELECT
      'user' AS row_kind,
      CAST(NULL AS TIMESTAMP WITH TIME ZONE) AS bucket,
      top_users.user_id AS user_id,
      top_users.email AS email,
      CAST(NULL AS varchar) AS model,
      CAST(NULL AS integer) AS status_code,
      top_users.calls AS calls,
      top_users.errors AS errors,
      CAST(NULL AS bigint) AS input_tokens,
      CAST(NULL AS bigint) AS output_tokens,
      CAST(NULL AS bigint) AS cached_tokens,
      top_users.total_tokens AS total_tokens,
      CAST(NULL AS bigint) AS active_users,
      top_users.spend_micros AS spend_micros,
      CAST(NULL AS double precision) AS p95_latency_ms,
      CAST(NULL AS double precision) AS p95_ttft_ms,
      top_users.sort_rank AS sort_rank,
      2 AS sort_group
    FROM top_users
    UNION ALL
    SELECT
      'model' AS row_kind,
      CAST(NULL AS TIMESTAMP WITH TIME ZONE) AS bucket,
      CAST(NULL AS uuid) AS user_id,
      CAST(NULL AS varchar) AS email,
      top_models.model AS model,
      CAST(NULL AS integer) AS status_code,
      top_models.calls AS calls,
      top_models.errors AS errors,
      CAST(NULL AS bigint) AS input_tokens,
      CAST(NULL AS bigint) AS output_tokens,
      CAST(NULL AS bigint) AS cached_tokens,
      top_models.total_tokens AS total_tokens,
      CAST(NULL AS bigint) AS active_users,
      top_models.spend_micros AS spend_micros,
      CAST(NULL AS double precision) AS p95_latency_ms,
      CAST(NULL AS double precision) AS p95_ttft_ms,
      top_models.sort_rank AS sort_rank,
      3 AS sort_group
    FROM top_models
    UNION ALL
    SELECT
      'error' AS row_kind,
      CAST(NULL AS TIMESTAMP WITH TIME ZONE) AS bucket,
      CAST(NULL AS uuid) AS user_id,
      CAST(NULL AS varchar) AS email,
      CAST(NULL AS varchar) AS model,
      top_errors.status_code AS status_code,
      top_errors.count AS calls,
      top_errors.count AS errors,
      CAST(NULL AS bigint) AS input_tokens,
      CAST(NULL AS bigint) AS output_tokens,
      CAST(NULL AS bigint) AS cached_tokens,
      CAST(NULL AS bigint) AS total_tokens,
      CAST(NULL AS bigint) AS active_users,
      CAST(NULL AS bigint) AS spend_micros,
      CAST(NULL AS double precision) AS p95_latency_ms,
      CAST(NULL AS double precision) AS p95_ttft_ms,
      top_errors.sort_rank AS sort_rank,
      4 AS sort_group
    FROM top_errors
    ORDER BY sort_group, bucket, sort_rank
    """
)


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

    rows = (
        await session.execute(
            _ADMIN_ANALYTICS_SQL,
            {
                "org_id": org_id,
                "start_utc": start_utc,
                "end_utc": end_utc,
                "granularity": safe_granularity,
                "limit": safe_limit,
            },
        )
    ).mappings().all()

    calls = 0
    errors = 0
    active_users = 0
    input_tokens = 0
    output_tokens = 0
    cached_tokens = 0
    spend_micros = 0
    p95_latency = None
    p95_ttft = None
    series: list[dict[str, Any]] = []
    users: list[dict[str, Any]] = []
    models: list[dict[str, Any]] = []
    errors_leaders: list[dict[str, Any]] = []

    for row in rows:
        row_kind = row["row_kind"]
        if row_kind == "kpi":
            calls = int(row["calls"] or 0)
            errors = int(row["errors"] or 0)
            active_users = int(row["active_users"] or 0)
            input_tokens = int(row["input_tokens"] or 0)
            output_tokens = int(row["output_tokens"] or 0)
            cached_tokens = int(row["cached_tokens"] or 0)
            spend_micros = int(row["spend_micros"] or 0)
            p95_latency = row["p95_latency_ms"]
            p95_ttft = row["p95_ttft_ms"]
            continue

        if row_kind == "series":
            ts_dt = row["bucket"]
            if not isinstance(ts_dt, dt.datetime):
                continue
            series.append(
                {
                    "ts": _dt_iso(ts_dt),
                    "spendUsd": _micros_to_usd(int(row["spend_micros"] or 0)),
                    "calls": int(row["calls"] or 0),
                    "errors": int(row["errors"] or 0),
                    "inputTokens": int(row["input_tokens"] or 0),
                    "outputTokens": int(row["output_tokens"] or 0),
                    "cachedTokens": int(row["cached_tokens"] or 0),
                    "p95LatencyMs": float(row["p95_latency_ms"]) if row["p95_latency_ms"] is not None else None,
                }
            )
            continue

        if row_kind == "user":
            user_id = row["user_id"]
            if not isinstance(user_id, uuid.UUID):
                continue
            users.append(
                {
                    "userId": str(user_id),
                    "email": str(row["email"]) if row["email"] is not None else None,
                    "spendUsd": _micros_to_usd(int(row["spend_micros"] or 0)),
                    "calls": int(row["calls"] or 0),
                    "errors": int(row["errors"] or 0),
                    "totalTokens": int(row["total_tokens"] or 0),
                }
            )
            continue

        if row_kind == "model":
            model = str(row["model"] or "").strip()
            if model == "":
                continue
            models.append(
                {
                    "model": model,
                    "spendUsd": _micros_to_usd(int(row["spend_micros"] or 0)),
                    "calls": int(row["calls"] or 0),
                    "errors": int(row["errors"] or 0),
                    "totalTokens": int(row["total_tokens"] or 0),
                }
            )
            continue

        if row_kind == "error":
            errors_total = max(errors, 0)
            code = int(row["status_code"] or 0)
            count = int(row["errors"] or 0)
            share = float(count / errors_total) if errors_total > 0 else None
            errors_leaders.append({"key": str(code), "count": count, "share": share})

    spend_usd = _micros_to_usd(spend_micros)

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
