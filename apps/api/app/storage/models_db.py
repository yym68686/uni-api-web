from __future__ import annotations

import asyncio
import datetime as dt
import json
import uuid
from decimal import Decimal, InvalidOperation, ROUND_DOWN
from pathlib import Path
from typing import Literal, TypedDict

import httpx
from sqlalchemy import Integer, case, cast, func, select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import AsyncSession

from app.models.llm_channel import LlmChannel
from app.models.llm_model_config import LlmModelConfig
from app.models.llm_model_pricing_rule import LlmModelPricingRule
from app.models.llm_usage_event import LlmUsageEvent
from app.models.organization import Organization
from app.storage.channels_db import list_channels_for_group


USD_MICROS = Decimal("1000000")
UNSET = object()

DefaultPriceEntry = tuple[str | None, str | None] | tuple[str | None, str | None, float]

DEFAULT_MODEL_PRICING_PATH = Path(__file__).resolve().parent.parent / "data" / "default-model-pricing.json"


def _load_default_prices() -> dict[str, DefaultPriceEntry]:
    raw = json.loads(DEFAULT_MODEL_PRICING_PATH.read_text(encoding="utf-8"))
    if not isinstance(raw, dict):
        raise ValueError("invalid default pricing file")

    prices: dict[str, DefaultPriceEntry] = {}
    for prefix, entry in raw.items():
        if not isinstance(prefix, str) or not isinstance(entry, dict):
            raise ValueError("invalid default pricing entry")

        input_usd = entry.get("inputUsdPerM")
        output_usd = entry.get("outputUsdPerM")
        discount_raw = entry.get("discount")

        if input_usd is not None and not isinstance(input_usd, str):
            raise ValueError("invalid default input price")
        if output_usd is not None and not isinstance(output_usd, str):
            raise ValueError("invalid default output price")
        if discount_raw is None:
            prices[prefix] = (input_usd, output_usd)
            continue
        if not isinstance(discount_raw, str):
            raise ValueError("invalid default discount")

        prices[prefix] = (input_usd, output_usd, float(discount_raw))
    return prices


# NOTE:
# - Prices are "$/M tokens" as strings, e.g. "2.5" => $2.5 per 1M tokens.
# - Optional 3rd value is a discount multiplier: 0.1 => 10% of original (10x cheaper).
# - Longest-prefix match (more specific prefixes override shorter ones).
DEFAULT_USD_PER_M_BY_PREFIX: dict[str, DefaultPriceEntry] = _load_default_prices()


def _dt_iso(value: dt.datetime) -> str:
    return value.astimezone(dt.timezone.utc).isoformat()


def _normalize_model_id(value: str) -> str:
    v = value.strip()
    if v == "":
        raise ValueError("invalid model")
    if "\n" in v or "\r" in v:
        raise ValueError("invalid model")
    if len(v) > 200:
        raise ValueError("model too large (max 200)")
    return v


def default_price_for_model(model_id: str) -> tuple[int | None, int | None]:
    eff_in, eff_out, _, _, _ = default_price_detail_for_model(model_id)
    return eff_in, eff_out


def default_price_detail_for_model(
    model_id: str,
) -> tuple[int | None, int | None, int | None, int | None, float | None]:
    for prefix in sorted(DEFAULT_USD_PER_M_BY_PREFIX.keys(), key=len, reverse=True):
        if not model_id.startswith(prefix):
            continue
        entry = DEFAULT_USD_PER_M_BY_PREFIX.get(prefix)
        if not entry:
            return (None, None, None, None, None)

        input_usd: str | None
        output_usd: str | None
        discount: float | None
        if len(entry) == 2:
            input_usd, output_usd = entry
            discount = None
        else:
            input_usd, output_usd, discount_raw = entry
            discount = float(discount_raw)
            if discount <= 0 or discount > 1:
                raise ValueError("invalid discount")

        original_in = _parse_usd_per_m(input_usd)
        original_out = _parse_usd_per_m(output_usd)

        if discount is None or discount >= 1:
            return (original_in, original_out, original_in, original_out, None)

        eff_in = _apply_discount(original_in, discount)
        eff_out = _apply_discount(original_out, discount)
        return (eff_in, eff_out, original_in, original_out, discount)

    return (None, None, None, None, None)


def _apply_discount(value: int | None, discount: float) -> int | None:
    if value is None:
        return None
    dec = (Decimal(int(value)) * Decimal(str(discount))).to_integral_value(rounding=ROUND_DOWN)
    return int(dec)


def _micros_to_str(value: int | None) -> str | None:
    if value is None:
        return None
    dec = (Decimal(int(value)) / USD_MICROS).normalize()
    s = format(dec, "f")
    if "." in s:
        s = s.rstrip("0").rstrip(".")
    return s


def _parse_usd_per_m(value: str | None) -> int | None:
    if value is None:
        return None
    raw = value.strip()
    if raw == "":
        return None
    try:
        dec = Decimal(raw)
    except InvalidOperation as e:
        raise ValueError("invalid price") from e
    if not dec.is_finite() or dec < 0:
        raise ValueError("invalid price")
    micros = int((dec * USD_MICROS).to_integral_value())
    if micros > 10_000_000_000:
        raise ValueError("price too large")
    return micros


def _parse_discount(value: float | str | None) -> float | None:
    if value is None:
        return None
    try:
        dec = Decimal(str(value).strip())
    except InvalidOperation as e:
        raise ValueError("invalid discount") from e
    if not dec.is_finite() or dec <= 0 or dec > 1:
        raise ValueError("invalid discount")
    if dec >= 1:
        return None
    return float(dec)


def _normalize_price_fields(input_micros: int | None, output_micros: int | None) -> None:
    if input_micros is None and output_micros is None:
        raise ValueError("at least one price is required")


def default_model_pricing_seed_rows() -> list[tuple[str, int | None, int | None, float | None]]:
    rows: list[tuple[str, int | None, int | None, float | None]] = []
    for prefix, entry in DEFAULT_USD_PER_M_BY_PREFIX.items():
        if len(entry) == 2:
            input_usd, output_usd = entry
            discount = None
        else:
            input_usd, output_usd, discount_raw = entry
            discount = _parse_discount(discount_raw)

        input_micros = _parse_usd_per_m(input_usd)
        output_micros = _parse_usd_per_m(output_usd)
        _normalize_price_fields(input_micros, output_micros)
        rows.append((prefix, input_micros, output_micros, discount))
    return rows


def _pricing_rule_detail(
    input_original: int | None,
    output_original: int | None,
    discount: float | None,
) -> tuple[int | None, int | None, int | None, int | None, float | None]:
    if discount is None or discount >= 1:
        return (input_original, output_original, input_original, output_original, None)

    if discount <= 0:
        raise ValueError("invalid discount")
    return (
        _apply_discount(input_original, discount),
        _apply_discount(output_original, discount),
        input_original,
        output_original,
        float(discount),
    )


def model_pricing_rule_to_item(row: LlmModelPricingRule) -> dict:
    eff_in, eff_out, orig_in, orig_out, discount = _pricing_rule_detail(
        row.input_usd_micros_per_m_original,
        row.output_usd_micros_per_m_original,
        row.discount,
    )
    return {
        "prefix": row.prefix,
        "inputUsdPerM": _micros_to_str(eff_in),
        "outputUsdPerM": _micros_to_str(eff_out),
        "inputUsdPerMOriginal": _micros_to_str(orig_in),
        "outputUsdPerMOriginal": _micros_to_str(orig_out),
        "discount": discount,
    }


def price_detail_for_model_from_rules(
    model_id: str, rules: list[LlmModelPricingRule]
) -> tuple[int | None, int | None, int | None, int | None, float | None]:
    model = model_id.strip()
    best: LlmModelPricingRule | None = None
    for rule in rules:
        if not model.startswith(rule.prefix):
            continue
        if best is None or len(rule.prefix) > len(best.prefix):
            best = rule

    if best is None:
        return (None, None, None, None, None)
    return _pricing_rule_detail(
        best.input_usd_micros_per_m_original,
        best.output_usd_micros_per_m_original,
        best.discount,
    )


AVAILABILITY_24H_BUCKETS = 48
AVAILABILITY_24H_BUCKET_SECONDS = 30 * 60
MODEL_AVAILABILITY_FAILURE_STATUS_MIN = 500
MODEL_AVAILABILITY_MIN_BUCKET_REQUESTS = 5
MODEL_AVAILABILITY_DEGRADED_RATE = 0.05
MODEL_AVAILABILITY_DOWN_RATE = 0.2

ModelAvailabilityStatus = Literal["healthy", "degraded", "down", "unknown"]


class ModelAvailabilityBucket(TypedDict):
    total: int
    failed: int
    status: ModelAvailabilityStatus


def _model_availability_relevant_filter():
    return (LlmUsageEvent.status_code < 400) | (
        LlmUsageEvent.status_code >= MODEL_AVAILABILITY_FAILURE_STATUS_MIN
    )


def model_availability_bucket(total: int, failed: int) -> ModelAvailabilityBucket:
    total_count = max(0, int(total))
    failed_count = min(max(0, int(failed)), total_count)
    if total_count < MODEL_AVAILABILITY_MIN_BUCKET_REQUESTS:
        status: ModelAvailabilityStatus = "unknown"
    else:
        failure_rate = failed_count / total_count
        if failure_rate >= MODEL_AVAILABILITY_DOWN_RATE:
            status = "down"
        elif failure_rate >= MODEL_AVAILABILITY_DEGRADED_RATE:
            status = "degraded"
        else:
            status = "healthy"
    return {"total": total_count, "failed": failed_count, "status": status}


def _empty_availability_24h_buckets() -> list[ModelAvailabilityBucket]:
    return [model_availability_bucket(0, 0) for _ in range(AVAILABILITY_24H_BUCKETS)]


def _availability_legacy_slots(buckets: list[ModelAvailabilityBucket]) -> list[int]:
    return [1 if bucket["status"] == "down" else 0 for bucket in buckets]


async def fetch_model_availability_24h(
    session: AsyncSession, *, org_id: uuid.UUID, model_ids: list[str]
) -> dict[str, list[ModelAvailabilityBucket]]:
    if not model_ids:
        return {}
    now = dt.datetime.now(dt.timezone.utc)
    start = now - dt.timedelta(hours=24)
    start_epoch = start.timestamp()

    bucket_expr = cast(
        func.floor(
            (func.extract("epoch", LlmUsageEvent.created_at) - start_epoch) / AVAILABILITY_24H_BUCKET_SECONDS
        ),
        Integer,
    )

    total_count_expr = func.count().label("total_count")
    failed_count_expr = func.coalesce(
        func.sum(
            case(
                (LlmUsageEvent.status_code >= MODEL_AVAILABILITY_FAILURE_STATUS_MIN, 1),
                else_=0,
            )
        ),
        0,
    ).label("failed_count")

    rows = (
        await session.execute(
            select(LlmUsageEvent.model_id, bucket_expr, total_count_expr, failed_count_expr)
            .where(
                LlmUsageEvent.org_id == org_id,
                LlmUsageEvent.created_at >= start,
                LlmUsageEvent.model_id.in_(model_ids),
                _model_availability_relevant_filter(),
            )
            .group_by(LlmUsageEvent.model_id, bucket_expr)
        )
    ).all()

    availability: dict[str, list[ModelAvailabilityBucket]] = {
        mid: _empty_availability_24h_buckets() for mid in model_ids
    }
    for mid, bucket, total_count, failed_count in rows:
        model = str(mid)
        idx = int(bucket) if bucket is not None else None
        if idx is None or idx < 0:
            continue
        if idx >= AVAILABILITY_24H_BUCKETS:
            idx = AVAILABILITY_24H_BUCKETS - 1
        slots = availability.get(model)
        if slots is None:
            slots = _empty_availability_24h_buckets()
            availability[model] = slots
        slots[idx] = model_availability_bucket(int(total_count or 0), int(failed_count or 0))
    return availability


async def get_model_config(
    session: AsyncSession, *, org_id: uuid.UUID, model_id: str
) -> LlmModelConfig | None:
    return (
        await session.execute(
            select(LlmModelConfig).where(LlmModelConfig.org_id == org_id, LlmModelConfig.model_id == model_id)
        )
    ).scalars().first()


async def upsert_model_config(
    session: AsyncSession,
    *,
    org_id: uuid.UUID,
    model_id: str,
    enabled: bool | None,
    input_usd_per_m: str | None | object,
    output_usd_per_m: str | None | object,
) -> LlmModelConfig:
    model = _normalize_model_id(model_id)
    row = await get_model_config(session, org_id=org_id, model_id=model)
    if not row:
        row = LlmModelConfig(org_id=org_id, model_id=model, enabled=True)
        session.add(row)
        await session.commit()
        await session.refresh(row)

    if enabled is not None:
        row.enabled = bool(enabled)

    if input_usd_per_m is not UNSET:
        row.input_usd_micros_per_m = _parse_usd_per_m(input_usd_per_m)  # type: ignore[arg-type]
    if output_usd_per_m is not UNSET:
        row.output_usd_micros_per_m = _parse_usd_per_m(output_usd_per_m)  # type: ignore[arg-type]

    await session.commit()
    await session.refresh(row)
    return row


async def list_model_pricing_rules(session: AsyncSession, *, org_id: uuid.UUID) -> list[LlmModelPricingRule]:
    return (
        await session.execute(
            select(LlmModelPricingRule)
            .where(LlmModelPricingRule.org_id == org_id)
            .order_by(func.length(LlmModelPricingRule.prefix).desc(), LlmModelPricingRule.prefix.asc())
        )
    ).scalars().all()


async def list_admin_model_pricing(session: AsyncSession, *, org_id: uuid.UUID) -> list[dict]:
    return [model_pricing_rule_to_item(row) for row in await list_model_pricing_rules(session, org_id=org_id)]


async def ensure_default_model_pricing_rules(session: AsyncSession, *, org_id: uuid.UUID) -> None:
    org = await session.get(Organization, org_id)
    if org is not None and bool(getattr(org, "model_pricing_initialized", False)):
        return

    existing = (
        await session.execute(
            select(func.count())
            .select_from(LlmModelPricingRule)
            .where(LlmModelPricingRule.org_id == org_id)
        )
    ).scalar_one()
    if int(existing or 0) == 0:
        for prefix, input_micros, output_micros, discount in default_model_pricing_seed_rows():
            session.add(
                LlmModelPricingRule(
                    org_id=org_id,
                    prefix=prefix,
                    input_usd_micros_per_m_original=input_micros,
                    output_usd_micros_per_m_original=output_micros,
                    discount=discount,
                )
            )
    if org is not None:
        org.model_pricing_initialized = True
    await session.commit()
    if org is not None:
        await session.refresh(org)


async def get_model_pricing_rule(
    session: AsyncSession, *, org_id: uuid.UUID, prefix: str
) -> LlmModelPricingRule | None:
    normalized = _normalize_model_id(prefix)
    return (
        await session.execute(
            select(LlmModelPricingRule).where(
                LlmModelPricingRule.org_id == org_id,
                LlmModelPricingRule.prefix == normalized,
            )
        )
    ).scalars().first()


async def create_model_pricing_rule(
    session: AsyncSession,
    *,
    org_id: uuid.UUID,
    prefix: str,
    input_usd_per_m_original: str | None,
    output_usd_per_m_original: str | None,
    discount: float | str | None,
) -> LlmModelPricingRule:
    normalized = _normalize_model_id(prefix)
    input_micros = _parse_usd_per_m(input_usd_per_m_original)
    output_micros = _parse_usd_per_m(output_usd_per_m_original)
    _normalize_price_fields(input_micros, output_micros)

    row = LlmModelPricingRule(
        org_id=org_id,
        prefix=normalized,
        input_usd_micros_per_m_original=input_micros,
        output_usd_micros_per_m_original=output_micros,
        discount=_parse_discount(discount),
    )
    session.add(row)
    try:
        await session.commit()
    except IntegrityError as e:
        await session.rollback()
        raise ValueError("pricing prefix already exists") from e
    await session.refresh(row)
    return row


async def update_model_pricing_rule(
    session: AsyncSession,
    *,
    org_id: uuid.UUID,
    current_prefix: str,
    prefix: str,
    input_usd_per_m_original: str | None,
    output_usd_per_m_original: str | None,
    discount: float | str | None,
) -> LlmModelPricingRule | None:
    row = await get_model_pricing_rule(session, org_id=org_id, prefix=current_prefix)
    if row is None:
        return None

    normalized = _normalize_model_id(prefix)
    input_micros = _parse_usd_per_m(input_usd_per_m_original)
    output_micros = _parse_usd_per_m(output_usd_per_m_original)
    _normalize_price_fields(input_micros, output_micros)

    row.prefix = normalized
    row.input_usd_micros_per_m_original = input_micros
    row.output_usd_micros_per_m_original = output_micros
    row.discount = _parse_discount(discount)

    try:
        await session.commit()
    except IntegrityError as e:
        await session.rollback()
        raise ValueError("pricing prefix already exists") from e
    await session.refresh(row)
    return row


async def delete_model_pricing_rule(session: AsyncSession, *, org_id: uuid.UUID, prefix: str) -> bool:
    row = await get_model_pricing_rule(session, org_id=org_id, prefix=prefix)
    if row is None:
        return False
    await session.delete(row)
    await session.commit()
    return True


async def get_price_detail_for_model(
    session: AsyncSession, *, org_id: uuid.UUID, model_id: str
) -> tuple[int | None, int | None, int | None, int | None, float | None]:
    return price_detail_for_model_from_rules(
        model_id,
        await list_model_pricing_rules(session, org_id=org_id),
    )


async def fetch_models_for_channel(channel: LlmChannel) -> set[str]:
    url = f"{channel.base_url.rstrip('/')}/models"
    headers = {"authorization": f"Bearer {channel.api_key}"}
    timeout = httpx.Timeout(20.0, connect=8.0)
    async with httpx.AsyncClient(timeout=timeout) as client:
        res = await client.get(url, headers=headers)
    if res.status_code != 200:
        return set()
    data = res.json()
    models: set[str] = set()

    if isinstance(data, dict):
        if isinstance(data.get("data"), list):
            for item in data["data"]:
                if isinstance(item, dict):
                    mid = item.get("id")
                    if isinstance(mid, str) and mid.strip():
                        models.add(mid.strip())
        elif isinstance(data.get("models"), list):
            for item in data["models"]:
                if isinstance(item, str) and item.strip():
                    models.add(item.strip())
                if isinstance(item, dict):
                    mid = item.get("id")
                    if isinstance(mid, str) and mid.strip():
                        models.add(mid.strip())

    return models


async def fetch_available_models(
    session: AsyncSession, *, org_id: uuid.UUID
) -> dict[str, int]:
    channels = (
        await session.execute(select(LlmChannel).where(LlmChannel.org_id == org_id))
    ).scalars().all()
    return await fetch_available_models_for_channels(channels)


async def fetch_available_models_for_channels(channels: list[LlmChannel]) -> dict[str, int]:
    if not channels:
        return {}
    results = await asyncio.gather(*(fetch_models_for_channel(c) for c in channels), return_exceptions=True)
    counts: dict[str, int] = {}
    for res in results:
        if isinstance(res, Exception):
            continue
        for mid in res:
            counts[mid] = counts.get(mid, 0) + 1
    return counts


async def list_admin_models(
    session: AsyncSession, *, org_id: uuid.UUID
) -> list[dict]:
    available_counts = await fetch_available_models(session, org_id=org_id)
    configs = (
        await session.execute(select(LlmModelConfig).where(LlmModelConfig.org_id == org_id))
    ).scalars().all()
    config_map = {c.model_id: c for c in configs}
    pricing_rules = await list_model_pricing_rules(session, org_id=org_id)

    all_ids = set(available_counts.keys()) | set(config_map.keys())
    items: list[dict] = []
    for mid in sorted(all_ids):
        cfg = config_map.get(mid)
        enabled = True if not cfg else bool(cfg.enabled)
        eff_in_rule, eff_out_rule, orig_in_rule, orig_out_rule, discount = price_detail_for_model_from_rules(
            mid, pricing_rules
        )

        input_from_cfg = bool(cfg and cfg.input_usd_micros_per_m is not None)
        output_from_cfg = bool(cfg and cfg.output_usd_micros_per_m is not None)

        input_micros = cfg.input_usd_micros_per_m if input_from_cfg else eff_in_rule
        output_micros = cfg.output_usd_micros_per_m if output_from_cfg else eff_out_rule

        show_discount = bool(discount is not None and discount < 1)
        input_orig = orig_in_rule if (show_discount and not input_from_cfg) else None
        output_orig = orig_out_rule if (show_discount and not output_from_cfg) else None
        discount_out = float(discount) if (show_discount and (input_orig is not None or output_orig is not None)) else None
        items.append(
            {
                "model": mid,
                "enabled": enabled,
                "inputUsdPerM": _micros_to_str(input_micros),
                "outputUsdPerM": _micros_to_str(output_micros),
                "discount": discount_out,
                "sources": int(available_counts.get(mid, 0)),
                "available": mid in available_counts,
            }
        )
    return items


async def list_user_models(
    session: AsyncSession, *, org_id: uuid.UUID, group_name: str, include_availability: bool = False
) -> list[dict]:
    channels = await list_channels_for_group(session, org_id=org_id, group_name=group_name)
    available_counts = await fetch_available_models_for_channels(channels)
    if not available_counts:
        return []

    configs = (
        await session.execute(
            select(LlmModelConfig).where(
                LlmModelConfig.org_id == org_id,
                LlmModelConfig.model_id.in_(sorted(available_counts.keys())),
            )
        )
    ).scalars().all()
    config_map = {c.model_id: c for c in configs}

    model_ids = [
        mid for mid in sorted(available_counts.keys()) if not (config_map.get(mid) and not config_map[mid].enabled)
    ]
    availability_map = (
        await fetch_model_availability_24h(session, org_id=org_id, model_ids=model_ids)
        if include_availability
        else {}
    )
    pricing_rules = await list_model_pricing_rules(session, org_id=org_id)

    items: list[dict] = []
    for mid in model_ids:
        cfg = config_map.get(mid)
        eff_in_rule, eff_out_rule, orig_in_rule, orig_out_rule, discount = price_detail_for_model_from_rules(
            mid, pricing_rules
        )

        input_from_cfg = bool(cfg and cfg.input_usd_micros_per_m is not None)
        output_from_cfg = bool(cfg and cfg.output_usd_micros_per_m is not None)

        input_micros = cfg.input_usd_micros_per_m if input_from_cfg else eff_in_rule
        output_micros = cfg.output_usd_micros_per_m if output_from_cfg else eff_out_rule

        show_discount = bool(discount is not None and discount < 1)
        input_orig = orig_in_rule if (show_discount and not input_from_cfg) else None
        output_orig = orig_out_rule if (show_discount and not output_from_cfg) else None
        discount_out = float(discount) if (show_discount and (input_orig is not None or output_orig is not None)) else None
        availability_buckets = availability_map.get(mid, _empty_availability_24h_buckets())

        items.append(
            {
                "model": mid,
                "inputUsdPerM": _micros_to_str(input_micros),
                "outputUsdPerM": _micros_to_str(output_micros),
                "inputUsdPerMOriginal": _micros_to_str(input_orig),
                "outputUsdPerMOriginal": _micros_to_str(output_orig),
                "discount": discount_out,
                "availability24h": _availability_legacy_slots(availability_buckets),
                "availability24hBuckets": availability_buckets,
                "sources": int(available_counts.get(mid, 0)),
            }
        )
    return items
