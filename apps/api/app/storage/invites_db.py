from __future__ import annotations

import secrets

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.models.user import User


INVITE_CODE_ALPHABET = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
INVITE_CODE_LENGTH = 8


def normalize_invite_code(value: str) -> str:
    return value.strip().lower()


async def generate_unique_invite_code(session: AsyncSession) -> str:
    for _ in range(40):
        code = "".join(secrets.choice(INVITE_CODE_ALPHABET) for _ in range(INVITE_CODE_LENGTH))
        normalized = normalize_invite_code(code)
        existing = (
            await session.execute(select(User.id).where(User.invite_code == normalized).limit(1))
        ).scalar_one_or_none()
        if existing is None:
            return normalized
    raise ValueError("failed to generate invite code")


async def find_user_by_invite_code(session: AsyncSession, raw_code: str) -> User | None:
    normalized = normalize_invite_code(raw_code)
    return (await session.execute(select(User).where(User.invite_code == normalized))).scalar_one_or_none()


async def ensure_user_invite_code(session: AsyncSession, user: User) -> str:
    existing = str(user.invite_code or "").strip()
    if existing != "":
        user.invite_code = normalize_invite_code(existing)
        return str(user.invite_code)

    user.invite_code = await generate_unique_invite_code(session)
    await session.commit()
    await session.refresh(user)
    return str(user.invite_code or "")

