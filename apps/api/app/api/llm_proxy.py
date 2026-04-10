from __future__ import annotations

from dataclasses import dataclass
import uuid


@dataclass(frozen=True)
class UsagePricing:
    input_usd_micros_per_m: int | None
    output_usd_micros_per_m: int | None


@dataclass(frozen=True)
class LlmProxyContext:
    api_key_id: uuid.UUID
    user_id: uuid.UUID
    org_id: uuid.UUID
    model_id: str
    source_ip: str | None
    upstream_base_url: str
    upstream_api_key: str
    pricing: UsagePricing


def estimate_cost_usd_micros(
    *,
    pricing: UsagePricing,
    input_tokens: int,
    cached_tokens: int,
    output_tokens: int,
) -> int:
    cost_micros = 0

    if pricing.input_usd_micros_per_m is not None and input_tokens > 0:
        cached = min(max(int(cached_tokens), 0), int(input_tokens))
        uncached = max(int(input_tokens) - cached, 0)
        if uncached > 0:
            cost_micros += int((uncached * int(pricing.input_usd_micros_per_m) + 999_999) // 1_000_000)
        if cached > 0:
            cost_micros += int((cached * int(pricing.input_usd_micros_per_m) + 9_999_999) // 10_000_000)

    if pricing.output_usd_micros_per_m is not None and output_tokens > 0:
        cost_micros += int((int(output_tokens) * int(pricing.output_usd_micros_per_m) + 999_999) // 1_000_000)

    return int(max(cost_micros, 0))
