from __future__ import annotations

import datetime as dt
import secrets
import uuid

from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.models.session import Session
from app.models.user import User
from app.schemas.auth import AuthResponse, LoginRequest, RegisterRequest, UserPublic
from app.security import sha256_hex
from app.security_password import hash_password, verify_password


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


def _normalize_email(email: str) -> str:
    return email.strip().lower()


def _validate_password(password: str) -> None:
    if len(password) < 6:
        raise ValueError("password too small (min 6)")
    if len(password) > 128:
        raise ValueError("password too large (max 128)")


async def _is_first_user(session: AsyncSession) -> bool:
    count = (await session.execute(select(func.count()).select_from(User))).scalar_one()
    return int(count) == 0


def _should_grant_admin(requested_token: str | None) -> bool:
    from app.core.config import settings

    if not requested_token:
        return False
    if not settings.admin_bootstrap_token:
        return False
    return secrets.compare_digest(requested_token, settings.admin_bootstrap_token)


async def create_user(session: AsyncSession, input: RegisterRequest) -> User:
    email = _normalize_email(str(input.email))
    _validate_password(input.password)

    existing = (await session.execute(select(User).where(User.email == email))).scalar_one_or_none()
    if existing:
        raise ValueError("email already registered")

    is_first = await _is_first_user(session)
    role = "admin" if is_first or _should_grant_admin(input.admin_bootstrap_token) else "user"
    row = User(email=email, password_hash=hash_password(input.password), role=role)
    session.add(row)
    await session.commit()
    await session.refresh(row)
    return row


async def authenticate_user(session: AsyncSession, input: LoginRequest) -> User | None:
    email = _normalize_email(str(input.email))
    row = (await session.execute(select(User).where(User.email == email))).scalar_one_or_none()
    if not row:
        return None
    if not verify_password(input.password, row.password_hash):
        return None

    row.last_login_at = dt.datetime.now(dt.timezone.utc)
    await session.commit()
    await session.refresh(row)
    return row


async def grant_admin_role(session: AsyncSession, user: User, token: str) -> User:
    if not _should_grant_admin(token):
        raise ValueError("invalid bootstrap token")
    if user.role != "admin":
        user.role = "admin"
        await session.commit()
        await session.refresh(user)
    return user


async def create_session(session: AsyncSession, user_id: uuid.UUID, ttl_days: int = 7) -> str:
    token = secrets.token_urlsafe(48)
    token_hash = sha256_hex(token)
    now = dt.datetime.now(dt.timezone.utc)
    expires_at = now + dt.timedelta(days=ttl_days)
    row = Session(user_id=user_id, token_hash=token_hash, expires_at=expires_at, last_used_at=now)
    session.add(row)
    await session.commit()
    return token


async def revoke_session(session: AsyncSession, token: str) -> None:
    token_hash = sha256_hex(token)
    row = (await session.execute(select(Session).where(Session.token_hash == token_hash))).scalar_one_or_none()
    if not row:
        return
    if row.revoked_at is None:
        row.revoked_at = dt.datetime.now(dt.timezone.utc)
        await session.commit()


async def get_user_by_token(session: AsyncSession, token: str) -> User | None:
    now = dt.datetime.now(dt.timezone.utc)
    token_hash = sha256_hex(token)
    sess = (
        await session.execute(
            select(Session).where(
                Session.token_hash == token_hash,
                Session.revoked_at.is_(None),
                Session.expires_at > now,
            )
        )
    ).scalar_one_or_none()
    if not sess:
        return None
    sess.last_used_at = now
    await session.commit()

    user = await session.get(User, sess.user_id)
    return user


async def register_and_login(session: AsyncSession, input: RegisterRequest) -> AuthResponse:
    user = await create_user(session, input)
    from app.core.config import settings

    token = await create_session(session, user.id, ttl_days=settings.session_ttl_days)
    return AuthResponse(token=token, user=_to_user_public(user))


async def login(session: AsyncSession, input: LoginRequest) -> AuthResponse | None:
    user = await authenticate_user(session, input)
    if not user:
        return None
    from app.core.config import settings

    token = await create_session(session, user.id, ttl_days=settings.session_ttl_days)
    return AuthResponse(token=token, user=_to_user_public(user))
