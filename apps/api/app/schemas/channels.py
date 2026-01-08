from __future__ import annotations

from pydantic import BaseModel, Field


class LlmChannelItem(BaseModel):
    id: str
    name: str
    base_url: str = Field(alias="baseUrl")
    api_key_masked: str = Field(alias="apiKeyMasked")
    allow_groups: list[str] = Field(alias="allowGroups")
    created_at: str = Field(alias="createdAt")
    updated_at: str = Field(alias="updatedAt")


class LlmChannelsListResponse(BaseModel):
    items: list[LlmChannelItem]


class LlmChannelCreateRequest(BaseModel):
    name: str
    base_url: str = Field(alias="baseUrl")
    api_key: str = Field(alias="apiKey")
    allow_groups: list[str] = Field(default_factory=list, alias="allowGroups")


class LlmChannelCreateResponse(BaseModel):
    item: LlmChannelItem


class LlmChannelUpdateRequest(BaseModel):
    name: str | None = None
    base_url: str | None = Field(default=None, alias="baseUrl")
    api_key: str | None = Field(default=None, alias="apiKey")
    allow_groups: list[str] | None = Field(default=None, alias="allowGroups")


class LlmChannelUpdateResponse(BaseModel):
    item: LlmChannelItem


class LlmChannelDeleteResponse(BaseModel):
    ok: bool
    id: str

