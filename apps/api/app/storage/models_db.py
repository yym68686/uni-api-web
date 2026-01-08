from __future__ import annotations

import asyncio
import datetime as dt
import uuid
from decimal import Decimal, InvalidOperation

import httpx
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.models.llm_channel import LlmChannel
from app.models.llm_model_config import LlmModelConfig


USD_MICROS = Decimal("1000000")
UNSET = object()

DEFAULT_USD_PER_M_BY_PREFIX: dict[str, tuple[str | None, str | None]] = {
    # NOTE: Prices are "$/M tokens" as strings, e.g. "0.15" => $0.15 per 1M tokens.
    # Longest-prefix match (more specific prefixes override shorter ones).
    "claude-3-7-sonnet": ("3", "15"),
    "claude-opus-4": ("3", "15"),
    "gemini-2.5-pro": ("3", "15"),
    "gemini-3-pro": ("3", "15"),
    "claude-3-7-sonnet-think": ("4", "20"),
    "gemini-2.5-flash": ("0.15", "0.60"),
    "gemini-3-flash": ("0.15", "0.60"),
    "deepseek-chat": ("0.14", None),
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
    for prefix in sorted(DEFAULT_USD_PER_M_BY_PREFIX.keys(), key=len, reverse=True):
        if model_id.startswith(prefix):
            input_usd, output_usd = DEFAULT_USD_PER_M_BY_PREFIX.get(prefix, (None, None))
            return (_parse_usd_per_m(input_usd), _parse_usd_per_m(output_usd))
    return (None, None)


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
    if not channels:
        return {}

    results = await asyncio.gather(
        *(fetch_models_for_channel(c) for c in channels), return_exceptions=True
    )
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
        default_in, default_out = default_price_for_model(mid)
        input_micros = cfg.input_usd_micros_per_m if (cfg and cfg.input_usd_micros_per_m is not None) else default_in
        output_micros = cfg.output_usd_micros_per_m if (cfg and cfg.output_usd_micros_per_m is not None) else default_out
        items.append(
            {
                "model": mid,
                "enabled": enabled,
                "inputUsdPerM": _micros_to_str(input_micros),
                "outputUsdPerM": _micros_to_str(output_micros),
                "sources": int(available_counts.get(mid, 0)),
                "available": mid in available_counts,
            }
        )
    return items
