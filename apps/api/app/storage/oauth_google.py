from __future__ import annotations

import datetime as dt
import secrets

import httpx
from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.core.config import settings
from app.models.oauth_identity import OAuthIdentity
from app.models.user import User
from app.schemas.auth import AuthResponse, UserPublic
from app.security_password import hash_password
from app.storage.auth_db import create_session


class GoogleProfile:
    def __init__(self, sub: str, email: str, email_verified: bool) -> None:
        self.sub = sub
        self.email = email
        self.email_verified = email_verified


def _dt_iso(value: dt.datetime | None) -> str | None:
    if not value:
        return None
    return value.astimezone(dt.timezone.utc).isoformat()


def _to_user_public(row: User) -> UserPublic:
    return UserPublic(
        id=str(row.id),
        email=row.email,
        role=row.role,
        createdAt=_dt_iso(row.created_at) or dt.datetime.now(dt.timezone.utc).isoformat(),
        lastLoginAt=_dt_iso(row.last_login_at),
    )


async def _exchange_google_code(code: str, code_verifier: str, redirect_uri: str) -> str:
    if not settings.google_client_id or not settings.google_client_secret:
        raise ValueError("google oauth not configured")

    async with httpx.AsyncClient(timeout=12) as client:
        token_res = await client.post(
            "https://oauth2.googleapis.com/token",
            data={
                "client_id": settings.google_client_id,
                "client_secret": settings.google_client_secret,
                "code": code,
                "code_verifier": code_verifier,
                "redirect_uri": redirect_uri,
                "grant_type": "authorization_code",
            },
            headers={"content-type": "application/x-www-form-urlencoded"},
        )
        if token_res.status_code != 200:
            raise ValueError("google token exchange failed")
        token_json = token_res.json()
        access_token = token_json.get("access_token")
        if not isinstance(access_token, str) or not access_token:
            raise ValueError("missing access token")
        return access_token


async def _fetch_google_profile(access_token: str) -> GoogleProfile:
    async with httpx.AsyncClient(timeout=12) as client:
        res = await client.get(
            "https://openidconnect.googleapis.com/v1/userinfo",
            headers={"authorization": f"Bearer {access_token}"},
        )
        if res.status_code != 200:
            raise ValueError("google userinfo failed")
        data = res.json()
        sub = data.get("sub")
        email = data.get("email")
        verified = data.get("email_verified")
        if not isinstance(sub, str) or not sub:
            raise ValueError("missing sub")
        if not isinstance(email, str) or not email:
            raise ValueError("missing email")
        return GoogleProfile(sub=sub, email=email.lower(), email_verified=bool(verified))


async def login_with_google(
    session: AsyncSession, *, code: str, code_verifier: str, redirect_uri: str
) -> AuthResponse:
    access_token = await _exchange_google_code(code, code_verifier, redirect_uri)
    profile = await _fetch_google_profile(access_token)

    if not profile.email_verified:
        raise ValueError("email not verified")

    identity = (
        await session.execute(
            select(OAuthIdentity).where(
                OAuthIdentity.provider == "google", OAuthIdentity.subject == profile.sub
            )
        )
    ).scalar_one_or_none()

    user: User | None = None
    if identity:
        user = await session.get(User, identity.user_id)

    if not user:
        user = (
            await session.execute(select(User).where(User.email == profile.email))
        ).scalar_one_or_none()

    if not user:
        user = User(email=profile.email, password_hash=hash_password(secrets.token_urlsafe(32)))
        count = (await session.execute(select(func.count()).select_from(User))).scalar_one()
        if int(count) == 0:
            user.role = "admin"
        session.add(user)
        await session.commit()
        await session.refresh(user)

    if not identity:
        identity = OAuthIdentity(
            provider="google", subject=profile.sub, user_id=user.id, email=profile.email
        )
        session.add(identity)
        await session.commit()

    user.last_login_at = dt.datetime.now(dt.timezone.utc)
    await session.commit()
    await session.refresh(user)

    token = await create_session(session, user.id, ttl_days=settings.session_ttl_days)
    return AuthResponse(token=token, user=_to_user_public(user))
