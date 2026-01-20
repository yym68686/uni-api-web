from __future__ import annotations

from pydantic import BaseModel, Field


class OAuthProviderItem(BaseModel):
    provider: str
    email: str
    created_at: str = Field(alias="createdAt")


class AuthMethodsResponse(BaseModel):
    password_set: bool = Field(alias="passwordSet")
    oauth: list[OAuthProviderItem]


class PasswordRequestCodeResponse(BaseModel):
    ok: bool = True
    expires_in_seconds: int = Field(alias="expiresInSeconds")


class PasswordSetRequest(BaseModel):
    password: str
    code: str


class PasswordChangeRequest(BaseModel):
    current_password: str = Field(alias="currentPassword")
    new_password: str = Field(alias="newPassword")


class PasswordRemoveRequest(BaseModel):
    code: str

