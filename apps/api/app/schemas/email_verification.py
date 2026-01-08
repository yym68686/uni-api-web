from __future__ import annotations

from pydantic import BaseModel, EmailStr, Field


class EmailCodeRequest(BaseModel):
    email: EmailStr
    purpose: str = "register"


class EmailCodeResponse(BaseModel):
    ok: bool = True
    expires_in_seconds: int = Field(alias="expiresInSeconds")


class RegisterVerifyRequest(BaseModel):
    email: EmailStr
    password: str
    code: str
    admin_bootstrap_token: str | None = Field(default=None, alias="adminBootstrapToken")

