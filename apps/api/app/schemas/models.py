from __future__ import annotations

from pydantic import BaseModel, Field


class ModelCatalogItem(BaseModel):
    model: str
    input_usd_per_m: str | None = Field(default=None, alias="inputUsdPerM")
    output_usd_per_m: str | None = Field(default=None, alias="outputUsdPerM")
    sources: int = 0


class ModelsListResponse(BaseModel):
    items: list[ModelCatalogItem]

