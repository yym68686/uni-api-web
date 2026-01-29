from __future__ import annotations

import datetime as dt
import uuid
from decimal import Decimal, ROUND_HALF_UP

from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.models.api_key import ApiKey
from app.models.membership import Membership
from app.models.session import Session
from app.models.user import User
from app.schemas.admin_users import (
    AdminUserDeleteResponse,
    AdminUserItem,
    AdminUsersListResponse,
    AdminUserUpdateRequest,
    AdminUserUpdateResponse,
)
from app.storage.billing_db import stage_balance_adjustment_ledger_entry

ALLOWED_MEMBERSHIP_ROLES: set[str] = {"owner", "admin", "billing", "developer", "viewer"}
USD_CENTS = Decimal("100")


def _dt_iso(value: dt.datetime | None) -> str | None:
    if not value:
        return None
    return value.astimezone(dt.timezone.utc).isoformat()


def _cents_to_usd_2(value: int) -> float:
    return float((Decimal(int(value)) / USD_CENTS).quantize(Decimal("0.01")))


def _usd_to_cents_2(value: float) -> int:
    dec = Decimal(str(value)).quantize(Decimal("0.01"), rounding=ROUND_HALF_UP)
    return int((dec * USD_CENTS).to_integral_value(rounding=ROUND_HALF_UP))


def _to_item(
    row: User,
    *,
    role: str,
    keys_total: int,
    keys_active: int,
    sessions_active: int,
) -> AdminUserItem:
    return AdminUserItem(
        id=str(row.id),
        email=row.email,
        role=role,
        group=row.group_name,
        balance=_cents_to_usd_2(int(row.balance)),
        bannedAt=_dt_iso(row.banned_at),
        createdAt=_dt_iso(row.created_at) or dt.datetime.now(dt.timezone.utc).isoformat(),
        lastLoginAt=_dt_iso(row.last_login_at),
        apiKeysTotal=int(keys_total),
        apiKeysActive=int(keys_active),
        sessionsActive=int(sessions_active),
    )


def _with_counts_query(now: dt.datetime, org_id: uuid.UUID):
    keys_counts = (
        select(
            ApiKey.user_id.label("user_id"),
            func.count(ApiKey.id).label("keys_total"),
            func.count(ApiKey.id).filter(ApiKey.revoked_at.is_(None)).label("keys_active"),
        )
        .group_by(ApiKey.user_id)
        .subquery()
    )

    sessions_counts = (
        select(
            Session.user_id.label("user_id"),
            func.count(Session.id)
            .filter(Session.revoked_at.is_(None), Session.expires_at > now)
            .label("sessions_active"),
        )
        .group_by(Session.user_id)
        .subquery()
    )

    q = (
        select(
            User,
            func.coalesce(Membership.role, "developer").label("role"),
            func.coalesce(keys_counts.c.keys_total, 0),
            func.coalesce(keys_counts.c.keys_active, 0),
            func.coalesce(sessions_counts.c.sessions_active, 0),
        )
        .outerjoin(
            Membership,
            (Membership.user_id == User.id) & (Membership.org_id == org_id),
        )
        .outerjoin(keys_counts, keys_counts.c.user_id == User.id)
        .outerjoin(sessions_counts, sessions_counts.c.user_id == User.id)
    )
    return q


async def list_admin_users(
    session: AsyncSession, *, org_id: uuid.UUID, limit: int = 50, offset: int = 0
) -> AdminUsersListResponse:
    now = dt.datetime.now(dt.timezone.utc)
    q = (
        _with_counts_query(now, org_id)
        .order_by(User.created_at.desc())
        .limit(limit)
        .offset(offset)
    )
    rows = (await session.execute(q)).all()
    items: list[AdminUserItem] = []
    for user, role, keys_total, keys_active, sessions_active in rows:
        items.append(
            _to_item(
                user,
                role=str(role),
                keys_total=int(keys_total),
                keys_active=int(keys_active),
                sessions_active=int(sessions_active),
            )
        )
    return AdminUsersListResponse(items=items)


async def get_admin_user(
    session: AsyncSession, *, org_id: uuid.UUID, user_id: uuid.UUID
) -> AdminUserItem | None:
    now = dt.datetime.now(dt.timezone.utc)
    q = _with_counts_query(now, org_id).where(User.id == user_id)
    row = (await session.execute(q)).first()
    if not row:
        return None
    user, role, keys_total, keys_active, sessions_active = row
    return _to_item(
        user,
        role=str(role),
        keys_total=int(keys_total),
        keys_active=int(keys_active),
        sessions_active=int(sessions_active),
    )


async def update_admin_user(
    session: AsyncSession,
    *,
    org_id: uuid.UUID,
    actor_role: str,
    actor_user_id: uuid.UUID | None,
    user_id: uuid.UUID,
    input: AdminUserUpdateRequest,
) -> AdminUserUpdateResponse | None:
    user = await session.get(User, user_id)
    if not user:
        return None

    membership = (
        await session.execute(
            select(Membership).where(Membership.org_id == org_id, Membership.user_id == user_id)
        )
    ).scalars().first()
    if not membership:
        membership = Membership(org_id=org_id, user_id=user_id, role="developer")
        session.add(membership)
        await session.commit()
        await session.refresh(membership)

    if membership.role == "owner" and actor_role != "owner":
        raise ValueError("cannot modify owner")

    balance_before = int(user.balance)
    balance_after: int | None = None
    if input.balance is not None:
        if not isinstance(input.balance, (int, float)):
            raise ValueError("balance must be a number")
        if input.balance < 0:
            raise ValueError("balance must be >= 0")
        if input.balance > 1_000_000_000:
            raise ValueError("balance too large")
        balance_after = _usd_to_cents_2(float(input.balance))
        user.balance = balance_after

    if input.banned is not None:
        user.banned_at = dt.datetime.now(dt.timezone.utc) if input.banned else None
        if input.banned:
            user.group_name = user.group_name or "default"

    fields_set = getattr(input, "model_fields_set", None)
    if fields_set is None:
        fields_set = getattr(input, "__fields_set__", set())

    if "group" in fields_set:
        raw = input.group
        group = (raw or "").strip()
        if group == "":
            group = "default"
        if "\n" in group or "\r" in group:
            raise ValueError("invalid group")
        if len(group) > 64:
            raise ValueError("group too large (max 64)")
        user.group_name = group

    if "role" in fields_set:
        raw_role = (input.role or "").strip().lower()
        if raw_role == "":
            raise ValueError("missing role")
        if raw_role not in ALLOWED_MEMBERSHIP_ROLES:
            raise ValueError("invalid role")

        if raw_role == "owner" or membership.role == "owner":
            if actor_role != "owner":
                raise ValueError("only owner can modify owner role")

        if membership.role == "owner" and raw_role != "owner":
            owners_count = (
                await session.execute(
                    select(func.count())
                    .select_from(Membership)
                    .where(Membership.org_id == org_id, Membership.role == "owner")
                )
            ).scalar_one()
            if int(owners_count) <= 1:
                raise ValueError("cannot demote last owner")

        membership.role = raw_role
        user.role = "admin" if raw_role in {"owner", "admin"} else "user"

    if balance_after is not None:
        stage_balance_adjustment_ledger_entry(
            session,
            org_id=org_id,
            user_id=user.id,
            actor_user_id=actor_user_id,
            balance_before=balance_before,
            balance_after=int(balance_after),
        )

    await session.commit()
    await session.refresh(user)
    item = await get_admin_user(session, org_id=org_id, user_id=user.id)
    if not item:
        return None
    return AdminUserUpdateResponse(item=item)


async def delete_admin_user(
    session: AsyncSession, user_id: uuid.UUID
) -> AdminUserDeleteResponse | None:
    user = await session.get(User, user_id)
    if not user:
        return None
    await session.delete(user)
    await session.commit()
    return AdminUserDeleteResponse(ok=True, id=str(user_id))
