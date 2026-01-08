from __future__ import annotations

from pydantic import BaseModel, Field


class DailyUsagePoint(BaseModel):
    date: str
    requests: int
    input_tokens: int = Field(alias="inputTokens")
    output_tokens: int = Field(alias="outputTokens")
    total_tokens: int = Field(alias="totalTokens")
    error_rate: float = Field(alias="errorRate")


class UsageSummary(BaseModel):
    requests_24h: int = Field(alias="requests24h")
    tokens_24h: int = Field(alias="tokens24h")
    error_rate_24h: float = Field(alias="errorRate24h")
    spend_24h_usd: float = Field(alias="spend24hUsd")


class TopModel(BaseModel):
    model: str
    requests: int
    tokens: int


class UsageResponse(BaseModel):
    summary: UsageSummary
    daily: list[DailyUsagePoint]
    top_models: list[TopModel] = Field(alias="topModels")

