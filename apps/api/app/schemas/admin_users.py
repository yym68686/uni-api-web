from __future__ import annotations

from pydantic import BaseModel, EmailStr, Field


class AdminUserItem(BaseModel):
    id: str
    email: EmailStr
    role: str
    group: str = "default"
    balance: float
    banned_at: str | None = Field(default=None, alias="bannedAt")
    created_at: str = Field(alias="createdAt")
    last_login_at: str | None = Field(default=None, alias="lastLoginAt")

    api_keys_total: int = Field(alias="apiKeysTotal")
    api_keys_active: int = Field(alias="apiKeysActive")
    sessions_active: int = Field(alias="sessionsActive")


class AdminUsersListResponse(BaseModel):
    items: list[AdminUserItem]


class AdminUserUpdateRequest(BaseModel):
    balance: float | None = None
    banned: bool | None = None
    group: str | None = None
    role: str | None = None


class AdminUserUpdateResponse(BaseModel):
    item: AdminUserItem


class AdminUserDeleteResponse(BaseModel):
    ok: bool
    id: str
