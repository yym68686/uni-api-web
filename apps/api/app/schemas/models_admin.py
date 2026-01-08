from __future__ import annotations

from pydantic import BaseModel, Field


class AdminModelItem(BaseModel):
    model: str
    enabled: bool
    input_usd_per_m: str | None = Field(default=None, alias="inputUsdPerM")
    output_usd_per_m: str | None = Field(default=None, alias="outputUsdPerM")
    sources: int = 0
    available: bool = True


class AdminModelsListResponse(BaseModel):
    items: list[AdminModelItem]


class AdminModelUpdateRequest(BaseModel):
    enabled: bool | None = None
    input_usd_per_m: str | None = Field(default=None, alias="inputUsdPerM")
    output_usd_per_m: str | None = Field(default=None, alias="outputUsdPerM")


class AdminModelUpdateResponse(BaseModel):
    item: AdminModelItem
