from __future__ import annotations

from decimal import Decimal

from pydantic import BaseModel, Field


class ApiKeyItem(BaseModel):
    id: str
    name: str
    prefix: str
    created_at: str = Field(alias="createdAt")
    last_used_at: str | None = Field(default=None, alias="lastUsedAt")
    revoked_at: str | None = Field(default=None, alias="revokedAt")
    spend_usd: float = Field(default=0.0, alias="spendUsd")
    spend_limit_usd: float | None = Field(default=None, alias="spendLimitUsd")


class ApiKeysListResponse(BaseModel):
    items: list[ApiKeyItem]


class ApiKeyCreateRequest(BaseModel):
    name: str


class ApiKeyCreateResponse(BaseModel):
    item: ApiKeyItem
    key: str


class ApiKeyUpdateRequest(BaseModel):
    revoked: bool | None = None
    name: str | None = None
    spend_limit_usd: Decimal | None = Field(default=None, alias="spendLimitUsd")


class ApiKeyUpdateResponse(BaseModel):
    item: ApiKeyItem


class ApiKeyDeleteResponse(BaseModel):
    ok: bool
    id: str


class ApiKeyRevealResponse(BaseModel):
    id: str
    key: str
