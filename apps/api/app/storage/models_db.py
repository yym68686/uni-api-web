from __future__ import annotations

import asyncio
import datetime as dt
import uuid
from decimal import Decimal, InvalidOperation, ROUND_HALF_UP

import httpx
from sqlalchemy import Integer, cast, func, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.models.llm_channel import LlmChannel
from app.models.llm_model_config import LlmModelConfig
from app.models.llm_usage_event import LlmUsageEvent
from app.storage.channels_db import list_channels_for_group


USD_MICROS = Decimal("1000000")
UNSET = object()

DefaultPriceEntry = tuple[str | None, str | None] | tuple[str | None, str | None, float]

DEFAULT_USD_PER_M_BY_PREFIX: dict[str, DefaultPriceEntry] = {
    # NOTE:
    # - Prices are "$/M tokens" as strings, e.g. "2.5" => $2.5 per 1M tokens.
    # - Optional 3rd value is a discount multiplier: 0.1 => 10% of original (10x cheaper).
    # - Longest-prefix match (more specific prefixes override shorter ones).
    "claude-3-5-sonnet": ("3", "15", 0.7),
    "claude-3-7-sonnet": ("3", "15", 0.7),
    "claude-opus-4": ("15", "75", 0.7),
    "claude-sonnet-4": ("3", "15", 0.7),
    "claude-haiku-4-5": ("0.5", "2.5", 0.6),
    "gemini-2.5-pro": ("2.5", "20", 0.1),
    "gemini-2.5-flash": ("0.6", "5", 0.1),
    "gemini-3-pro": ("6", "36", 0.1),
    "gemini-3-flash": ("1.5", "6", 0.1),
    "gemini-embedding-001": ("3", "15", 0.1),
    "gpt-5": ("3.75", "30", 0.1),
    "gpt-5.1": ("3.75", "30", 0.05),
    "gpt-5.2": ("3.75", "30", 0.05),
    "text-embedding-004": ("3", "15", 0.1),
    "deepseek-chat": ("0.14", "0.60"),
}


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
    dec = (Decimal(int(value)) * Decimal(str(discount))).to_integral_value(rounding=ROUND_HALF_UP)
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
    if dec < 0:
        raise ValueError("invalid price")
    micros = int((dec * USD_MICROS).to_integral_value())
    if micros > 10_000_000_000:
        raise ValueError("price too large")
    return micros


AVAILABILITY_24H_BUCKETS = 48
AVAILABILITY_24H_BUCKET_SECONDS = 30 * 60


async def fetch_model_availability_24h(
    session: AsyncSession, *, org_id: uuid.UUID, model_ids: list[str]
) -> dict[str, list[int]]:
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

    down_filter = (LlmUsageEvent.status_code >= 500) | (
        LlmUsageEvent.status_code.in_([401, 403, 408, 429])
    )

    rows = (
        await session.execute(
            select(LlmUsageEvent.model_id, bucket_expr)
            .where(
                LlmUsageEvent.org_id == org_id,
                LlmUsageEvent.created_at >= start,
                LlmUsageEvent.model_id.in_(model_ids),
                down_filter,
            )
            .group_by(LlmUsageEvent.model_id, bucket_expr)
        )
    ).all()

    availability: dict[str, list[int]] = {mid: [0] * AVAILABILITY_24H_BUCKETS for mid in model_ids}
    for mid, bucket in rows:
        model = str(mid)
        idx = int(bucket) if bucket is not None else None
        if idx is None or idx < 0:
            continue
        if idx >= AVAILABILITY_24H_BUCKETS:
            idx = AVAILABILITY_24H_BUCKETS - 1
        slots = availability.get(model)
        if slots is None:
            slots = [0] * AVAILABILITY_24H_BUCKETS
            availability[model] = slots
        slots[idx] = 1
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

    all_ids = set(available_counts.keys()) | set(config_map.keys())
    items: list[dict] = []
    for mid in sorted(all_ids):
        cfg = config_map.get(mid)
        enabled = True if not cfg else bool(cfg.enabled)
        eff_in_default, eff_out_default, orig_in_default, orig_out_default, discount = default_price_detail_for_model(
            mid
        )

        input_from_cfg = bool(cfg and cfg.input_usd_micros_per_m is not None)
        output_from_cfg = bool(cfg and cfg.output_usd_micros_per_m is not None)

        input_micros = cfg.input_usd_micros_per_m if input_from_cfg else eff_in_default
        output_micros = cfg.output_usd_micros_per_m if output_from_cfg else eff_out_default

        show_discount = bool(discount is not None and discount < 1)
        input_orig = orig_in_default if (show_discount and not input_from_cfg) else None
        output_orig = orig_out_default if (show_discount and not output_from_cfg) else None
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

    items: list[dict] = []
    for mid in model_ids:
        cfg = config_map.get(mid)
        eff_in_default, eff_out_default, orig_in_default, orig_out_default, discount = default_price_detail_for_model(
            mid
        )

        input_from_cfg = bool(cfg and cfg.input_usd_micros_per_m is not None)
        output_from_cfg = bool(cfg and cfg.output_usd_micros_per_m is not None)

        input_micros = cfg.input_usd_micros_per_m if input_from_cfg else eff_in_default
        output_micros = cfg.output_usd_micros_per_m if output_from_cfg else eff_out_default

        show_discount = bool(discount is not None and discount < 1)
        input_orig = orig_in_default if (show_discount and not input_from_cfg) else None
        output_orig = orig_out_default if (show_discount and not output_from_cfg) else None
        discount_out = float(discount) if (show_discount and (input_orig is not None or output_orig is not None)) else None

        items.append(
            {
                "model": mid,
                "inputUsdPerM": _micros_to_str(input_micros),
                "outputUsdPerM": _micros_to_str(output_micros),
                "inputUsdPerMOriginal": _micros_to_str(input_orig),
                "outputUsdPerMOriginal": _micros_to_str(output_orig),
                "discount": discount_out,
                "availability24h": availability_map.get(mid, [0] * AVAILABILITY_24H_BUCKETS),
                "sources": int(available_counts.get(mid, 0)),
            }
        )
    return items
