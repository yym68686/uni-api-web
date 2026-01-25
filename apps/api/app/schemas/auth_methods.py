from __future__ import annotations

from pydantic import BaseModel, EmailStr, Field


class OAuthProviderItem(BaseModel):
    id: str
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


class EmailChangeRequestCodeRequest(BaseModel):
    new_email: EmailStr = Field(alias="newEmail")


class EmailChangeConfirmRequest(BaseModel):
    new_email: EmailStr = Field(alias="newEmail")
    new_email_code: str = Field(alias="newEmailCode")
    current_password: str | None = Field(default=None, alias="currentPassword")
    current_email_code: str | None = Field(default=None, alias="currentEmailCode")


class EmailChangeConfirmResponse(BaseModel):
    ok: bool = True
    email: EmailStr
