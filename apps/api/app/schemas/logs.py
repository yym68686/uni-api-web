from __future__ import annotations

from pydantic import BaseModel, Field


class LogItem(BaseModel):
    id: str
    model: str
    created_at: str = Field(alias="createdAt")
    ok: bool
    status_code: int = Field(alias="statusCode")
    input_tokens: int = Field(alias="inputTokens")
    output_tokens: int = Field(alias="outputTokens")
    total_duration_ms: int = Field(alias="totalDurationMs")
    ttft_ms: int = Field(alias="ttftMs")
    tps: float | None = None
    cost_usd: float = Field(alias="costUsd")
    source_ip: str | None = Field(default=None, alias="sourceIp")


class LogsListResponse(BaseModel):
    items: list[LogItem]

