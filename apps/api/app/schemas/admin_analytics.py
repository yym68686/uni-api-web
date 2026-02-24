from __future__ import annotations

from pydantic import BaseModel, Field


class AdminAnalyticsRange(BaseModel):
    from_: str = Field(alias="from")
    to: str
    tz: str
    granularity: str


class AdminAnalyticsKpis(BaseModel):
    spend_usd: float = Field(alias="spendUsd")
    calls: int
    errors: int
    active_users: int = Field(alias="activeUsers")
    input_tokens: int = Field(alias="inputTokens")
    output_tokens: int = Field(alias="outputTokens")
    cached_tokens: int = Field(alias="cachedTokens")
    p95_latency_ms: float | None = Field(default=None, alias="p95LatencyMs")
    p95_ttft_ms: float | None = Field(default=None, alias="p95TtftMs")


class AdminAnalyticsSeriesPoint(BaseModel):
    ts: str
    spend_usd: float = Field(alias="spendUsd")
    calls: int
    errors: int
    input_tokens: int = Field(alias="inputTokens")
    output_tokens: int = Field(alias="outputTokens")
    cached_tokens: int = Field(alias="cachedTokens")
    p95_latency_ms: float | None = Field(default=None, alias="p95LatencyMs")


class AdminAnalyticsUserLeader(BaseModel):
    user_id: str = Field(alias="userId")
    email: str | None = None
    spend_usd: float = Field(alias="spendUsd")
    calls: int
    errors: int
    total_tokens: int = Field(alias="totalTokens")


class AdminAnalyticsModelLeader(BaseModel):
    model: str
    spend_usd: float = Field(alias="spendUsd")
    calls: int
    errors: int
    total_tokens: int = Field(alias="totalTokens")


class AdminAnalyticsErrorLeader(BaseModel):
    key: str
    count: int
    share: float | None = None


class AdminAnalyticsLeaders(BaseModel):
    users: list[AdminAnalyticsUserLeader]
    models: list[AdminAnalyticsModelLeader]
    errors: list[AdminAnalyticsErrorLeader]


class AdminAnalyticsResponse(BaseModel):
    range: AdminAnalyticsRange
    kpis: AdminAnalyticsKpis
    series: list[AdminAnalyticsSeriesPoint]
    leaders: AdminAnalyticsLeaders

