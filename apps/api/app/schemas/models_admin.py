from __future__ import annotations

from pydantic import BaseModel, Field


class AdminModelItem(BaseModel):
    model: str
    enabled: bool
    input_usd_per_m: str | None = Field(default=None, alias="inputUsdPerM")
    output_usd_per_m: str | None = Field(default=None, alias="outputUsdPerM")
    discount: float | None = Field(default=None, alias="discount")
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


class AdminModelPricingItem(BaseModel):
    prefix: str
    input_usd_per_m: str | None = Field(default=None, alias="inputUsdPerM")
    output_usd_per_m: str | None = Field(default=None, alias="outputUsdPerM")
    input_usd_per_m_original: str | None = Field(default=None, alias="inputUsdPerMOriginal")
    output_usd_per_m_original: str | None = Field(default=None, alias="outputUsdPerMOriginal")
    discount: float | None = None


class AdminModelPricingListResponse(BaseModel):
    items: list[AdminModelPricingItem]


class AdminModelPricingUpsertRequest(BaseModel):
    prefix: str
    input_usd_per_m_original: str | None = Field(default=None, alias="inputUsdPerMOriginal")
    output_usd_per_m_original: str | None = Field(default=None, alias="outputUsdPerMOriginal")
    discount: float | None = None


class AdminModelPricingUpdateResponse(BaseModel):
    item: AdminModelPricingItem


class AdminModelPricingDeleteResponse(BaseModel):
    ok: bool
    prefix: str
