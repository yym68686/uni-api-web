from __future__ import annotations

import uuid

from fastapi import APIRouter, Depends, HTTPException, Request
from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth import get_current_membership, get_current_user, require_admin
from app.models.membership import Membership
from app.schemas.announcements import (
    AnnouncementCreateRequest,
    AnnouncementCreateResponse,
    AnnouncementDeleteResponse,
    AnnouncementsListResponse,
    AnnouncementUpdateRequest,
    AnnouncementUpdateResponse,
)
from app.schemas.admin_users import (
    AdminUserDeleteResponse,
    AdminUsersListResponse,
    AdminUserUpdateRequest,
    AdminUserUpdateResponse,
)
from app.schemas.auth import AuthResponse, GoogleOAuthExchangeRequest, LoginRequest, RegisterRequest, UserPublic
from app.schemas.keys import ApiKeyCreateRequest, ApiKeyCreateResponse, ApiKeysListResponse
from app.schemas.usage import UsageResponse
from app.db import get_db_session
from app.storage.announcements_db import (
    create_announcement,
    delete_announcement,
    list_announcements,
    update_announcement,
)
from app.storage.admin_users_db import delete_admin_user, list_admin_users, update_admin_user
from app.storage.orgs_db import ensure_default_org
from app.storage.auth_db import grant_admin_role
from app.storage.auth_db import login as auth_login
from app.storage.auth_db import register_and_login, revoke_session
from app.storage.keys_db import create_api_key, list_api_keys, revoke_api_key
from app.storage.oauth_google import login_with_google

router = APIRouter()


@router.get("/health")
async def health() -> dict[str, str]:
    return {"status": "ok"}


@router.post("/auth/register", response_model=AuthResponse)
async def register(
    payload: RegisterRequest, session: AsyncSession = Depends(get_db_session)
) -> AuthResponse:
    try:
        return await register_and_login(session, payload)
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e)) from e


@router.post("/auth/login", response_model=AuthResponse)
async def login(payload: LoginRequest, session: AsyncSession = Depends(get_db_session)) -> AuthResponse:
    try:
        res = await auth_login(session, payload)
    except ValueError as e:
        if str(e) == "banned":
            raise HTTPException(status_code=403, detail="banned") from e
        raise HTTPException(status_code=400, detail=str(e)) from e
    if not res:
        raise HTTPException(status_code=401, detail="invalid credentials")
    return res


@router.post("/auth/logout")
async def logout(
    request: Request,
    session: AsyncSession = Depends(get_db_session),
    current_user=Depends(get_current_user),
) -> dict:
    # NOTE: token can be provided via Authorization Bearer or cookie; revoke when present.
    from app.constants import SESSION_COOKIE_NAME

    _ = current_user
    auth = request.headers.get("authorization")
    token = auth[7:].strip() if auth and auth.lower().startswith("bearer ") else None
    if not token:
        token = request.cookies.get(SESSION_COOKIE_NAME)
    if token:
        await revoke_session(session, token)
    return {"ok": True}


@router.get("/auth/me", response_model=UserPublic)
async def me(
    current_user=Depends(get_current_user),
    membership=Depends(get_current_membership),
) -> UserPublic:  # type: ignore[no-untyped-def]
    import datetime as dt

    def _dt_iso(value: dt.datetime | None) -> str | None:
        if not value:
            return None
        return value.astimezone(dt.timezone.utc).isoformat()

    return UserPublic(
        id=str(current_user.id),
        email=current_user.email,
        role=membership.role,
        balance=int(current_user.balance),
        orgId=str(membership.org_id),
        createdAt=_dt_iso(current_user.created_at) or dt.datetime.now(dt.timezone.utc).isoformat(),
        lastLoginAt=_dt_iso(current_user.last_login_at),
    )


@router.post("/auth/admin/claim")
async def claim_admin(
    payload: dict,
    session: AsyncSession = Depends(get_db_session),
    current_user=Depends(get_current_user),
) -> dict:
    token = payload.get("token")
    if not isinstance(token, str) or not token:
        raise HTTPException(status_code=400, detail="missing token")
    try:
        user = await grant_admin_role(session, current_user, token)
        return {"ok": True, "role": user.role}
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e)) from e


@router.post("/auth/oauth/google", response_model=AuthResponse)
async def oauth_google(
    payload: GoogleOAuthExchangeRequest, session: AsyncSession = Depends(get_db_session)
) -> AuthResponse:
    from app.core.config import settings

    if payload.redirect_uri != settings.google_redirect_uri:
        raise HTTPException(status_code=400, detail="invalid redirect_uri")
    try:
        return await login_with_google(
            session,
            code=payload.code,
            code_verifier=payload.code_verifier,
            redirect_uri=payload.redirect_uri,
        )
    except ValueError as e:
        if str(e) == "banned":
            raise HTTPException(status_code=403, detail="banned") from e
        raise HTTPException(status_code=400, detail=str(e)) from e


@router.get("/announcements", response_model=AnnouncementsListResponse)
async def announcements(
    session: AsyncSession = Depends(get_db_session), current_user=Depends(get_current_user)
) -> AnnouncementsListResponse:
    return await list_announcements(session)


@router.post("/admin/announcements", response_model=AnnouncementCreateResponse)
async def admin_create_announcement(
    payload: AnnouncementCreateRequest,
    session: AsyncSession = Depends(get_db_session),
    admin_user=Depends(require_admin),
) -> AnnouncementCreateResponse:
    _ = admin_user
    try:
        return await create_announcement(session, payload)
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e)) from e


@router.patch("/admin/announcements/{announcement_id}", response_model=AnnouncementUpdateResponse)
async def admin_update_announcement(
    announcement_id: str,
    payload: AnnouncementUpdateRequest,
    session: AsyncSession = Depends(get_db_session),
    admin_user=Depends(require_admin),
) -> AnnouncementUpdateResponse:
    _ = admin_user
    try:
        updated = await update_announcement(session, announcement_id, payload)
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e)) from e
    if not updated:
        raise HTTPException(status_code=404, detail="not found")
    return updated


@router.delete("/admin/announcements/{announcement_id}", response_model=AnnouncementDeleteResponse)
async def admin_delete_announcement(
    announcement_id: str,
    session: AsyncSession = Depends(get_db_session),
    admin_user=Depends(require_admin),
) -> AnnouncementDeleteResponse:
    _ = admin_user
    try:
        deleted = await delete_announcement(session, announcement_id)
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e)) from e
    if not deleted:
        raise HTTPException(status_code=404, detail="not found")
    return deleted


@router.get("/admin/users", response_model=AdminUsersListResponse)
async def admin_list_users(
    session: AsyncSession = Depends(get_db_session),
    admin_user=Depends(require_admin),
    membership=Depends(get_current_membership),
) -> AdminUsersListResponse:
    _ = admin_user
    return await list_admin_users(session, org_id=membership.org_id)


@router.patch("/admin/users/{user_id}", response_model=AdminUserUpdateResponse)
async def admin_update_user(
    user_id: str,
    payload: AdminUserUpdateRequest,
    session: AsyncSession = Depends(get_db_session),
    admin_user=Depends(require_admin),
    membership=Depends(get_current_membership),
) -> AdminUserUpdateResponse:
    try:
        parsed = uuid.UUID(user_id)
    except ValueError:
        raise HTTPException(status_code=400, detail="invalid user id") from None
    if parsed == admin_user.id and payload.banned is True:
        raise HTTPException(status_code=400, detail="cannot ban self")

    try:
        updated = await update_admin_user(
            session,
            org_id=membership.org_id,
            actor_role=membership.role,
            user_id=parsed,
            input=payload,
        )
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e)) from e
    if not updated:
        raise HTTPException(status_code=404, detail="not found")
    return updated


@router.delete("/admin/users/{user_id}", response_model=AdminUserDeleteResponse)
async def admin_delete_user(
    user_id: str,
    session: AsyncSession = Depends(get_db_session),
    admin_user=Depends(require_admin),
    membership=Depends(get_current_membership),
) -> AdminUserDeleteResponse:
    if str(admin_user.id) == user_id:
        raise HTTPException(status_code=400, detail="cannot delete self")
    try:
        parsed = uuid.UUID(user_id)
    except ValueError:
        raise HTTPException(status_code=400, detail="invalid user id") from None

    target_role = (
        await session.execute(
            select(Membership.role).where(
                Membership.org_id == membership.org_id, Membership.user_id == parsed
            )
        )
    ).scalar_one_or_none()
    if target_role == "owner" and membership.role != "owner":
        raise HTTPException(status_code=403, detail="cannot delete owner")
    if target_role == "owner":
        owners_count = (
            await session.execute(
                select(func.count())
                .select_from(Membership)
                .where(Membership.org_id == membership.org_id, Membership.role == "owner")
            )
        ).scalar_one()
        if int(owners_count) <= 1:
            raise HTTPException(status_code=400, detail="cannot delete last owner")

    deleted = await delete_admin_user(session, parsed)
    if not deleted:
        raise HTTPException(status_code=404, detail="not found")
    return deleted


@router.get("/usage", response_model=UsageResponse)
async def usage(current_user=Depends(get_current_user)) -> dict:
    # TODO: Replace with real Postgres aggregation.
    # Keep response shape consistent with the console's current expectations.
    return {
        "summary": {"requests24h": 0, "tokens24h": 0, "errorRate24h": 0, "spend24hUsd": 0},
        "daily": [],
        "topModels": [],
    }


@router.get("/keys", response_model=ApiKeysListResponse)
async def list_keys(
    session: AsyncSession = Depends(get_db_session), current_user=Depends(get_current_user)
) -> ApiKeysListResponse:
    return await list_api_keys(session, current_user.id)


@router.post("/keys", response_model=ApiKeyCreateResponse)
async def create_key(
    payload: ApiKeyCreateRequest,
    session: AsyncSession = Depends(get_db_session),
    current_user=Depends(get_current_user),
) -> ApiKeyCreateResponse:
    name = payload.name.strip()
    if len(name) < 2:
        raise HTTPException(status_code=400, detail="name too small (min 2)")
    if len(name) > 64:
        raise HTTPException(status_code=400, detail="name too large (max 64)")
    return await create_api_key(session, current_user.id, ApiKeyCreateRequest(name=name))


@router.delete("/keys/{key_id}")
async def revoke_key(
    key_id: str,
    session: AsyncSession = Depends(get_db_session),
    current_user=Depends(get_current_user),
) -> dict:
    item = await revoke_api_key(
        session, key_id, user_id=None if current_user.role == "admin" else current_user.id
    )
    if not item:
        raise HTTPException(status_code=404, detail="not found")
    return {"item": item}
