from __future__ import annotations

import time

from pydantic import BaseModel, Field

from app.core.config import settings


class ModelCatalogItem(BaseModel):
    model: str
    input_usd_per_m: str | None = Field(default=None, alias="inputUsdPerM")
    output_usd_per_m: str | None = Field(default=None, alias="outputUsdPerM")
    sources: int = 0


class ModelsListResponse(BaseModel):
    items: list[ModelCatalogItem]


class OpenAIModelItem(BaseModel):
    id: str
    object: str = "model"
    # OpenAI uses unix timestamp; many clients accept either seconds or ms.
    # Use ms to match common OpenAI-compatible gateways.
    created: int = Field(default_factory=lambda: int(time.time() * 1000))
    owned_by: str = Field(default_factory=lambda: settings.app_name)


class OpenAIModelsListResponse(BaseModel):
    object: str = "list"
    data: list[OpenAIModelItem]
