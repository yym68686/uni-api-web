from __future__ import annotations

from pydantic import BaseModel, EmailStr, Field


class UserPublic(BaseModel):
    id: str
    email: EmailStr
    role: str = "user"
    created_at: str = Field(alias="createdAt")
    last_login_at: str | None = Field(default=None, alias="lastLoginAt")


class RegisterRequest(BaseModel):
    email: EmailStr
    password: str
    admin_bootstrap_token: str | None = Field(default=None, alias="adminBootstrapToken")


class LoginRequest(BaseModel):
    email: EmailStr
    password: str


class AuthResponse(BaseModel):
    token: str
    user: UserPublic


class GoogleOAuthExchangeRequest(BaseModel):
    code: str
    code_verifier: str = Field(alias="codeVerifier")
    redirect_uri: str = Field(alias="redirectUri")
