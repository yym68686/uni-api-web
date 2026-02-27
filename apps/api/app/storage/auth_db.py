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
from app.storage.balance_math import remaining_usd_2
from app.storage.invites_db import find_user_by_invite_code, generate_unique_invite_code
from app.storage.orgs_db import ADMIN_LIKE_ROLES, ensure_default_org, ensure_membership, get_membership


def _dt_iso(value: dt.datetime | None) -> str | None:
    if not value:
        return None
    return value.astimezone(dt.timezone.utc).isoformat()


def _to_user_public(row: User) -> UserPublic:
    credits_cents = int(getattr(row, "balance", 0) or 0)
    spend_micros_total = int(getattr(row, "spend_usd_micros_total", 0) or 0)
    return UserPublic(
        id=str(row.id),
        email=row.email,
        role=row.role,
        group=row.group_name,
        balance=remaining_usd_2(credits_usd_cents=credits_cents, spend_usd_micros_total=spend_micros_total),
        orgId=None,
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


async def create_user(
    session: AsyncSession,
    input: RegisterRequest,
    *,
    signup_ip: str | None = None,
    signup_device_id: str | None = None,
    signup_user_agent: str | None = None,
) -> User:
    email = _normalize_email(str(input.email))
    _validate_password(input.password)

    existing = (await session.execute(select(User).where(User.email == email))).scalar_one_or_none()
    if existing:
        raise ValueError("email already registered")

    is_first = await _is_first_user(session)
    requested_admin = _should_grant_admin(input.admin_bootstrap_token)

    now = dt.datetime.now(dt.timezone.utc)
    inviter_user_id = None
    raw_invite_code = getattr(input, "invite_code", None)
    if isinstance(raw_invite_code, str) and raw_invite_code.strip():
        inviter = await find_user_by_invite_code(session, raw_invite_code)
        if not inviter:
            raise ValueError("invalid invite code")
        inviter_user_id = inviter.id

    row = User(
        email=email,
        password_hash=hash_password(input.password),
        password_set_at=now,
        role="user",
        invite_code=await generate_unique_invite_code(session),
        invited_by_user_id=inviter_user_id,
        invited_at=(now if inviter_user_id else None),
        signup_ip=(signup_ip.strip()[:64] if isinstance(signup_ip, str) and signup_ip.strip() else None),
        signup_device_id=(
            signup_device_id.strip()[:64] if isinstance(signup_device_id, str) and signup_device_id.strip() else None
        ),
        signup_user_agent=(
            signup_user_agent.strip()[:255] if isinstance(signup_user_agent, str) and signup_user_agent.strip() else None
        ),
    )
    session.add(row)
    await session.commit()
    await session.refresh(row)

    org = await ensure_default_org(session)
    membership_role = "owner" if is_first else ("admin" if requested_admin else "developer")
    await ensure_membership(session, org_id=org.id, user_id=row.id, role=membership_role)
    row.role = "admin" if membership_role in ADMIN_LIKE_ROLES else "user"
    await session.commit()
    await session.refresh(row)
    return row


async def authenticate_user(session: AsyncSession, input: LoginRequest) -> User | None:
    email = _normalize_email(str(input.email))
    row = (await session.execute(select(User).where(User.email == email))).scalar_one_or_none()
    if not row:
        return None
    if row.banned_at is not None:
        raise ValueError("banned")
    if not verify_password(input.password, row.password_hash):
        return None

    now = dt.datetime.now(dt.timezone.utc)
    row.last_login_at = now
    if row.password_set_at is None:
        row.password_set_at = now
    await session.commit()
    await session.refresh(row)
    return row


async def grant_admin_role(session: AsyncSession, user: User, token: str) -> User:
    if not _should_grant_admin(token):
        raise ValueError("invalid bootstrap token")
    org = await ensure_default_org(session)
    membership = await get_membership(session, org_id=org.id, user_id=user.id)
    if not membership:
        membership = await ensure_membership(session, org_id=org.id, user_id=user.id, role="admin")
    if membership.role not in ADMIN_LIKE_ROLES:
        membership.role = "admin"
        await session.commit()
        await session.refresh(membership)
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


async def revoke_other_sessions(session: AsyncSession, *, user_id: uuid.UUID, current_token: str) -> None:
    from sqlalchemy import update

    now = dt.datetime.now(dt.timezone.utc)
    current_hash = sha256_hex(current_token)
    await session.execute(
        update(Session)
        .where(
            Session.user_id == user_id,
            Session.revoked_at.is_(None),
            Session.expires_at > now,
            Session.token_hash != current_hash,
        )
        .values(revoked_at=now)
    )
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


async def register_and_login(
    session: AsyncSession,
    input: RegisterRequest,
    *,
    signup_ip: str | None = None,
    signup_device_id: str | None = None,
    signup_user_agent: str | None = None,
) -> AuthResponse:
    user = await create_user(
        session,
        input,
        signup_ip=signup_ip,
        signup_device_id=signup_device_id,
        signup_user_agent=signup_user_agent,
    )
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
