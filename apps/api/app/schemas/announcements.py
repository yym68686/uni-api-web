from __future__ import annotations

from pydantic import BaseModel, Field


class AnnouncementItem(BaseModel):
    id: str
    title: str
    title_zh: str | None = Field(default=None, alias="titleZh")
    title_en: str | None = Field(default=None, alias="titleEn")
    meta: str
    meta_zh: str | None = Field(default=None, alias="metaZh")
    meta_en: str | None = Field(default=None, alias="metaEn")
    level: str
    created_at: str = Field(alias="createdAt")


class AnnouncementsListResponse(BaseModel):
    items: list[AnnouncementItem]


class AnnouncementCreateRequest(BaseModel):
    title: str | None = None
    title_zh: str | None = Field(default=None, alias="titleZh")
    title_en: str | None = Field(default=None, alias="titleEn")
    meta: str | None = None
    meta_zh: str | None = Field(default=None, alias="metaZh")
    meta_en: str | None = Field(default=None, alias="metaEn")
    level: str = "warning"


class AnnouncementCreateResponse(BaseModel):
    item: AnnouncementItem


class AnnouncementUpdateRequest(BaseModel):
    title: str | None = None
    title_zh: str | None = Field(default=None, alias="titleZh")
    title_en: str | None = Field(default=None, alias="titleEn")
    meta: str | None = None
    meta_zh: str | None = Field(default=None, alias="metaZh")
    meta_en: str | None = Field(default=None, alias="metaEn")
    level: str = "warning"


class AnnouncementUpdateResponse(BaseModel):
    item: AnnouncementItem


class AnnouncementDeleteResponse(BaseModel):
    ok: bool
    id: str
