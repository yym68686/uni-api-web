from __future__ import annotations

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.models.membership import Membership
from app.models.organization import Organization


ADMIN_LIKE_ROLES: set[str] = {"owner", "admin"}


async def ensure_default_org(session: AsyncSession) -> Organization:
    org = (await session.execute(select(Organization).order_by(Organization.created_at.asc()))).scalars().first()
    if org:
        return org
    org = Organization(name="Default")
    session.add(org)
    await session.commit()
    await session.refresh(org)
    return org


async def get_membership(
    session: AsyncSession, *, org_id, user_id
) -> Membership | None:
    return (
        await session.execute(
            select(Membership).where(Membership.org_id == org_id, Membership.user_id == user_id)
        )
    ).scalars().first()


async def ensure_membership(
    session: AsyncSession, *, org_id, user_id, role: str
) -> Membership:
    existing = await get_membership(session, org_id=org_id, user_id=user_id)
    if existing:
        return existing
    row = Membership(org_id=org_id, user_id=user_id, role=role)
    session.add(row)
    await session.commit()
    await session.refresh(row)
    return row

