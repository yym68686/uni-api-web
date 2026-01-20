from __future__ import annotations

from pydantic import BaseModel, ConfigDict, Field


class AnnouncementItem(BaseModel):
    model_config = ConfigDict(populate_by_name=True)

    id: str
    title: str
    title_zh: str | None = Field(default=None, alias="titleZh")
    title_en: str | None = Field(default=None, alias="titleEn")
    content: str
    content_zh: str | None = Field(default=None, alias="contentZh")
    content_en: str | None = Field(default=None, alias="contentEn")
    level: str
    created_at: str = Field(alias="createdAt")


class AnnouncementsListResponse(BaseModel):
    model_config = ConfigDict(populate_by_name=True)

    items: list[AnnouncementItem]


class AnnouncementCreateRequest(BaseModel):
    model_config = ConfigDict(populate_by_name=True)

    title: str | None = None
    title_zh: str | None = Field(default=None, alias="titleZh")
    title_en: str | None = Field(default=None, alias="titleEn")
    content: str | None = None
    content_zh: str | None = Field(default=None, alias="contentZh")
    content_en: str | None = Field(default=None, alias="contentEn")
    level: str = "warning"


class AnnouncementCreateResponse(BaseModel):
    model_config = ConfigDict(populate_by_name=True)

    item: AnnouncementItem


class AnnouncementUpdateRequest(BaseModel):
    model_config = ConfigDict(populate_by_name=True)

    title: str | None = None
    title_zh: str | None = Field(default=None, alias="titleZh")
    title_en: str | None = Field(default=None, alias="titleEn")
    content: str | None = None
    content_zh: str | None = Field(default=None, alias="contentZh")
    content_en: str | None = Field(default=None, alias="contentEn")
    level: str = "warning"


class AnnouncementUpdateResponse(BaseModel):
    model_config = ConfigDict(populate_by_name=True)

    item: AnnouncementItem


class AnnouncementDeleteResponse(BaseModel):
    model_config = ConfigDict(populate_by_name=True)

    ok: bool
    id: str
