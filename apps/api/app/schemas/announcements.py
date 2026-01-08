from __future__ import annotations

from pydantic import BaseModel, Field


class AnnouncementItem(BaseModel):
    id: str
    title: str
    meta: str
    level: str
    created_at: str = Field(alias="createdAt")


class AnnouncementsListResponse(BaseModel):
    items: list[AnnouncementItem]


class AnnouncementCreateRequest(BaseModel):
    title: str
    meta: str
    level: str = "warning"


class AnnouncementCreateResponse(BaseModel):
    item: AnnouncementItem


class AnnouncementUpdateRequest(BaseModel):
    title: str
    meta: str
    level: str = "warning"


class AnnouncementUpdateResponse(BaseModel):
    item: AnnouncementItem


class AnnouncementDeleteResponse(BaseModel):
    ok: bool
    id: str
