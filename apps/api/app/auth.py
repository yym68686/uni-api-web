from __future__ import annotations

from fastapi import Depends, HTTPException, Request
from sqlalchemy.ext.asyncio import AsyncSession

from app.constants import SESSION_COOKIE_NAME
from app.db import get_db_session
from app.models.user import User
from app.storage.auth_db import get_user_by_token


def _extract_bearer_token(value: str | None) -> str | None:
    if not value:
        return None
    prefix = "bearer "
    if value.lower().startswith(prefix):
        token = value[len(prefix) :].strip()
        return token or None
    return None


async def get_current_user(
    request: Request, session: AsyncSession = Depends(get_db_session)
) -> User:
    auth = request.headers.get("authorization")
    token = _extract_bearer_token(auth) or request.cookies.get(SESSION_COOKIE_NAME)
    if not token:
        raise HTTPException(status_code=401, detail="unauthorized")

    user = await get_user_by_token(session, token)
    if not user:
        raise HTTPException(status_code=401, detail="unauthorized")
    return user

