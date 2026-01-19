from __future__ import annotations

import uuid
import time

from fastapi import APIRouter, Depends, HTTPException, Request
from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession
from starlette.responses import Response, StreamingResponse

import httpx
import json

from app.auth import get_current_membership, get_current_user, require_admin
from app.models.api_key import ApiKey
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
from app.schemas.account import AccountDeleteResponse
from app.schemas.admin_settings import AdminSettingsResponse, AdminSettingsUpdateRequest
from app.schemas.auth import AuthResponse, GoogleOAuthExchangeRequest, LoginRequest, RegisterRequest, UserPublic
from app.schemas.email_verification import EmailCodeRequest, EmailCodeResponse, RegisterVerifyRequest
from app.schemas.keys import (
    ApiKeyCreateRequest,
    ApiKeyCreateResponse,
    ApiKeyDeleteResponse,
    ApiKeysListResponse,
    ApiKeyRevealResponse,
    ApiKeyUpdateRequest,
    ApiKeyUpdateResponse,
)
from app.schemas.usage import UsageResponse
from app.schemas.channels import (
    LlmChannelCreateRequest,
    LlmChannelCreateResponse,
    LlmChannelDeleteResponse,
    LlmChannelsListResponse,
    LlmChannelUpdateRequest,
    LlmChannelUpdateResponse,
)
from app.schemas.billing import BillingLedgerListResponse
from app.schemas.models import ModelsListResponse, OpenAIModelsListResponse, OpenAIModelItem
from app.schemas.models_admin import AdminModelsListResponse, AdminModelUpdateRequest, AdminModelUpdateResponse
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
from app.storage.auth_db import get_user_by_token
from app.storage.auth_db import login as auth_login
from app.storage.auth_db import register_and_login, revoke_session
from app.storage.api_key_auth import authenticate_api_key
from app.storage.channels_db import (
    create_channel,
    delete_channel,
    list_channels,
    pick_channel_for_group,
    update_channel,
)
from app.storage.models_db import UNSET, get_model_config, list_admin_models, list_user_models, upsert_model_config
from app.storage.usage_db import list_usage_events, record_usage_event
from app.storage.keys_db import (
    create_api_key,
    delete_api_key,
    list_api_keys,
    update_api_key,
)
from app.storage.billing_db import list_balance_ledger
from app.storage.oauth_google import login_with_google
from app.schemas.logs import LogsListResponse
from app.db import SessionLocal
from app.storage.email_verification_db import request_email_code, verify_email_code
from app.models.organization import Organization

router = APIRouter()


@router.get("/health")
async def health() -> dict[str, str]:
    return {"status": "ok"}


@router.get("/admin/settings", response_model=AdminSettingsResponse)
async def admin_settings(
    session: AsyncSession = Depends(get_db_session),
    admin_user=Depends(require_admin),
    membership=Depends(get_current_membership),
) -> dict:
    _ = admin_user
    org = await session.get(Organization, membership.org_id)
    if not org:
        raise HTTPException(status_code=404, detail="not found")
    return {"registrationEnabled": bool(org.registration_enabled)}


@router.patch("/admin/settings", response_model=AdminSettingsResponse)
async def admin_update_settings(
    payload: AdminSettingsUpdateRequest,
    session: AsyncSession = Depends(get_db_session),
    admin_user=Depends(require_admin),
    membership=Depends(get_current_membership),
) -> dict:
    _ = admin_user
    org = await session.get(Organization, membership.org_id)
    if not org:
        raise HTTPException(status_code=404, detail="not found")
    if payload.registration_enabled is not None:
        org.registration_enabled = bool(payload.registration_enabled)
        await session.commit()
        await session.refresh(org)
    return {"registrationEnabled": bool(org.registration_enabled)}


@router.post("/auth/register", response_model=AuthResponse)
async def register(
    payload: RegisterRequest, session: AsyncSession = Depends(get_db_session)
) -> AuthResponse:
    from app.core.config import settings

    org = await ensure_default_org(session)
    if not bool(getattr(org, "registration_enabled", True)):
        raise HTTPException(status_code=403, detail="registration disabled")

    if settings.email_verification_required:
        raise HTTPException(status_code=400, detail="email verification required")
    try:
        return await register_and_login(session, payload)
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e)) from e


@router.post("/auth/email/request", response_model=EmailCodeResponse)
async def email_request_code(
    payload: EmailCodeRequest,
    request: Request,
    session: AsyncSession = Depends(get_db_session),
) -> EmailCodeResponse:
    org = await ensure_default_org(session)
    if str(payload.purpose or "").strip() == "register" and not bool(getattr(org, "registration_enabled", True)):
        raise HTTPException(status_code=403, detail="registration disabled")

    xff = request.headers.get("x-forwarded-for")
    client_ip = xff.split(",")[0].strip()[:64] if xff and xff.strip() else (request.client.host if request.client else None)
    try:
        expires = await request_email_code(
            session,
            email=str(payload.email),
            purpose=str(payload.purpose),
            client_ip=client_ip,
        )
    except ValueError as e:
        msg = str(e)
        if msg in {"too many requests", "please wait"}:
            raise HTTPException(status_code=429, detail=msg) from e
        raise HTTPException(status_code=400, detail=msg) from e
    return EmailCodeResponse(ok=True, expiresInSeconds=expires)


@router.post("/auth/register/verify", response_model=AuthResponse)
async def register_verify(
    payload: RegisterVerifyRequest,
    request: Request,
    session: AsyncSession = Depends(get_db_session),
) -> AuthResponse:
    _ = request
    org = await ensure_default_org(session)
    if not bool(getattr(org, "registration_enabled", True)):
        raise HTTPException(status_code=403, detail="registration disabled")
    try:
        await verify_email_code(
            session,
            email=str(payload.email),
            purpose="register",
            code=str(payload.code),
        )
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e)) from e

    try:
        return await register_and_login(
            session,
            RegisterRequest(
                email=payload.email,
                password=payload.password,
                adminBootstrapToken=payload.admin_bootstrap_token,
            ),
        )
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


@router.delete("/account", response_model=AccountDeleteResponse)
async def delete_account(
    session: AsyncSession = Depends(get_db_session),
    current_user=Depends(get_current_user),
    membership=Depends(get_current_membership),
) -> AccountDeleteResponse:  # type: ignore[no-untyped-def]
    if membership.role == "owner":
        owners_count = (
            await session.execute(
                select(func.count())
                .select_from(Membership)
                .where(Membership.org_id == membership.org_id, Membership.role == "owner")
            )
        ).scalar_one()
        if int(owners_count) <= 1:
            raise HTTPException(status_code=400, detail="cannot delete last owner")

    deleted = await delete_admin_user(session, current_user.id)
    if not deleted:
        raise HTTPException(status_code=404, detail="not found")
    return AccountDeleteResponse(ok=True, id=str(current_user.id))


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
        group=current_user.group_name,
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
        if str(e) == "registration disabled":
            raise HTTPException(status_code=403, detail="registration disabled") from e
        raise HTTPException(status_code=400, detail=str(e)) from e


def _extract_bearer_token(value: str | None) -> str | None:
    if not value:
        return None
    prefix = "bearer "
    if value.lower().startswith(prefix):
        token = value[len(prefix) :].strip()
        return token or None
    return None


async def _require_user_for_models(request: Request, session: AsyncSession):
    from app.constants import SESSION_COOKIE_NAME

    auth = request.headers.get("authorization")
    bearer = _extract_bearer_token(auth)

    user = None
    if bearer and bearer.startswith("sk-"):
        try:
            _api_key, user = await authenticate_api_key(session, authorization=auth)
        except ValueError as e:
            detail = str(e) or "unauthorized"
            status = 403 if detail == "banned" else 401
            raise HTTPException(status_code=status, detail=detail) from e
    else:
        token = bearer or request.cookies.get(SESSION_COOKIE_NAME)
        if not token:
            raise HTTPException(status_code=401, detail="unauthorized")
        user = await get_user_by_token(session, token)
        if not user:
            raise HTTPException(status_code=401, detail="unauthorized")
        if user.banned_at is not None:
            raise HTTPException(status_code=403, detail="banned")

    membership = (
        await session.execute(
            select(Membership).where(Membership.user_id == user.id).order_by(Membership.created_at.asc())
        )
    ).scalars().first()
    if not membership:
        raise HTTPException(status_code=403, detail="missing membership")
    return user, membership


@router.get("/console/models", response_model=ModelsListResponse)
async def console_list_models(request: Request, session: AsyncSession = Depends(get_db_session)) -> ModelsListResponse:
    user, membership = await _require_user_for_models(request, session)
    items = await list_user_models(session, org_id=membership.org_id, group_name=user.group_name)
    return ModelsListResponse(items=items)  # type: ignore[arg-type]


@router.get("/models", response_model=OpenAIModelsListResponse)
async def list_models(request: Request, session: AsyncSession = Depends(get_db_session)) -> OpenAIModelsListResponse:
    user, membership = await _require_user_for_models(request, session)

    items = await list_user_models(session, org_id=membership.org_id, group_name=user.group_name)
    def _model_id(value: object) -> str:
        if isinstance(value, dict):
            raw = value.get("model")
            return str(raw) if raw is not None else ""
        return str(getattr(value, "model", "") or "")

    data = [OpenAIModelItem(id=_model_id(item)) for item in sorted(items, key=_model_id) if _model_id(item)]
    return OpenAIModelsListResponse(data=data)


@router.get("/logs", response_model=LogsListResponse)
async def logs(
    limit: int = 50,
    offset: int = 0,
    session: AsyncSession = Depends(get_db_session),
    current_user=Depends(get_current_user),
    membership=Depends(get_current_membership),
) -> LogsListResponse:  # type: ignore[no-untyped-def]
    items = await list_usage_events(
        session,
        org_id=membership.org_id,
        user_id=current_user.id,
        limit=limit,
        offset=offset,
    )
    return LogsListResponse(items=items)  # type: ignore[arg-type]


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
            actor_user_id=admin_user.id,
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


@router.get("/admin/channels", response_model=LlmChannelsListResponse)
async def admin_list_channels(
    session: AsyncSession = Depends(get_db_session),
    admin_user=Depends(require_admin),
    membership=Depends(get_current_membership),
) -> LlmChannelsListResponse:
    _ = admin_user
    return await list_channels(session, org_id=membership.org_id)


@router.post("/admin/channels", response_model=LlmChannelCreateResponse)
async def admin_create_channel(
    payload: LlmChannelCreateRequest,
    session: AsyncSession = Depends(get_db_session),
    admin_user=Depends(require_admin),
    membership=Depends(get_current_membership),
) -> LlmChannelCreateResponse:
    _ = admin_user
    try:
        return await create_channel(session, org_id=membership.org_id, input=payload)
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e)) from e


@router.patch("/admin/channels/{channel_id}", response_model=LlmChannelUpdateResponse)
async def admin_update_channel(
    channel_id: str,
    payload: LlmChannelUpdateRequest,
    session: AsyncSession = Depends(get_db_session),
    admin_user=Depends(require_admin),
    membership=Depends(get_current_membership),
) -> LlmChannelUpdateResponse:
    _ = admin_user
    try:
        parsed = uuid.UUID(channel_id)
    except ValueError:
        raise HTTPException(status_code=400, detail="invalid channel id") from None
    try:
        updated = await update_channel(session, org_id=membership.org_id, channel_id=parsed, input=payload)
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e)) from e
    if not updated:
        raise HTTPException(status_code=404, detail="not found")
    return updated


@router.delete("/admin/channels/{channel_id}", response_model=LlmChannelDeleteResponse)
async def admin_delete_channel(
    channel_id: str,
    session: AsyncSession = Depends(get_db_session),
    admin_user=Depends(require_admin),
    membership=Depends(get_current_membership),
) -> LlmChannelDeleteResponse:
    _ = admin_user
    try:
        parsed = uuid.UUID(channel_id)
    except ValueError:
        raise HTTPException(status_code=400, detail="invalid channel id") from None
    deleted = await delete_channel(session, org_id=membership.org_id, channel_id=parsed)
    if not deleted:
        raise HTTPException(status_code=404, detail="not found")
    return deleted


@router.post("/chat/completions")
async def chat_completions(request: Request, session: AsyncSession = Depends(get_db_session)):
    auth = request.headers.get("authorization")
    try:
        api_key, user = await authenticate_api_key(session, authorization=auth)
    except ValueError as e:
        detail = str(e) or "unauthorized"
        status = 403 if detail == "banned" else 401
        raise HTTPException(status_code=status, detail=detail) from e

    raw = await request.body()
    try:
        payload = json.loads(raw.decode("utf-8"))
    except Exception as e:  # noqa: BLE001
        raise HTTPException(status_code=400, detail="invalid json") from e
    if not isinstance(payload, dict):
        raise HTTPException(status_code=400, detail="invalid json")

    model_id = payload.get("model")
    if not isinstance(model_id, str) or not model_id.strip():
        raise HTTPException(status_code=400, detail="missing model")

    # Basic credit gate (balance is maintained by admin for now).
    if int(getattr(user, "balance", 0)) <= 0:
        raise HTTPException(status_code=402, detail="insufficient balance")

    membership = (
        await session.execute(
            select(Membership).where(Membership.user_id == user.id).order_by(Membership.created_at.asc())
        )
    ).scalars().first()
    if not membership:
        raise HTTPException(status_code=403, detail="missing membership")

    cfg = await get_model_config(session, org_id=membership.org_id, model_id=model_id.strip())
    if cfg and not cfg.enabled:
        raise HTTPException(status_code=403, detail="model disabled")

    channel = await pick_channel_for_group(session, org_id=membership.org_id, group_name=user.group_name)
    if not channel:
        raise HTTPException(status_code=503, detail="no channel configured")

    upstream_url = f"{channel.base_url.rstrip('/')}/chat/completions"
    headers: dict[str, str] = {
        "authorization": f"Bearer {channel.api_key}",
        "content-type": "application/json",
    }
    # Forward optional OpenAI compatibility headers if present.
    for name in ("openai-organization", "openai-project", "anthropic-version"):
        value = request.headers.get(name)
        if value:
            headers[name] = value

    stream = bool(payload.get("stream"))
    timeout = httpx.Timeout(60.0, connect=10.0)

    forwarded_for = request.headers.get("x-forwarded-for") or ""
    source_ip = forwarded_for.split(",")[0].strip() if forwarded_for.strip() else None
    if not source_ip and request.client:
        source_ip = request.client.host

    started = time.perf_counter()
    ttft_ms = 0
    total_ms = 0

    if stream:
        # Important: do NOT use `async with client.stream(...)` here, otherwise the upstream
        # stream is closed immediately when the request handler returns. Keep the stream open
        # and close it inside the generator's `finally`.
        client = httpx.AsyncClient(timeout=timeout)
        req_up = client.build_request("POST", upstream_url, headers=headers, content=raw)
        res = await client.send(req_up, stream=True)
        content_type = res.headers.get("content-type") or "application/json"
        ok = res.status_code < 400
        input_tokens = 0
        output_tokens = 0
        total_tokens = 0

        async def iterator():
            nonlocal ttft_ms, total_ms, input_tokens, output_tokens, total_tokens
            first = None
            sse_buf = bytearray()
            try:
                async for chunk in res.aiter_bytes():
                    if first is None:
                        first = time.perf_counter()
                        ttft_ms = int((first - started) * 1000)
                    if ok and content_type.startswith("text/event-stream"):
                        sse_buf.extend(chunk)
                        while True:
                            idx = sse_buf.find(b"\n")
                            if idx < 0:
                                break
                            raw_line = bytes(sse_buf[:idx]).rstrip(b"\r")
                            del sse_buf[: idx + 1]
                            line = raw_line.strip()
                            if not line.startswith(b"data:"):
                                continue
                            data = line[len(b"data:") :].strip()
                            if not data or data == b"[DONE]":
                                continue
                            try:
                                obj = json.loads(data.decode("utf-8"))
                            except Exception:
                                continue
                            if not isinstance(obj, dict):
                                continue
                            usage = obj.get("usage")
                            if not isinstance(usage, dict):
                                continue
                            try:
                                prompt = int(usage.get("prompt_tokens") or 0)
                                completion = int(usage.get("completion_tokens") or 0)
                                total_raw = usage.get("total_tokens")
                                total = int(total_raw) if total_raw is not None else 0
                                output = max(total - prompt, 0) if total > 0 else completion
                                input_tokens = prompt
                                output_tokens = output
                                total_tokens = total if total > 0 else (input_tokens + output_tokens)
                            except Exception:
                                continue
                    yield chunk
            except (httpx.ReadError, httpx.StreamError):
                # Treat upstream disconnects/cancellation as a normal stream termination.
                pass
            finally:
                total_ms = int((time.perf_counter() - started) * 1000)
                try:
                    await res.aclose()
                except Exception:
                    pass
                try:
                    await client.aclose()
                except Exception:
                    pass
                try:
                    async with SessionLocal() as s:
                        cost_micros = 0
                        if ok and total_tokens > 0:
                            cfg = await get_model_config(s, org_id=membership.org_id, model_id=model_id.strip())
                            from app.storage.models_db import default_price_for_model

                            default_in, default_out = default_price_for_model(model_id.strip())
                            in_price = (
                                cfg.input_usd_micros_per_m
                                if (cfg and cfg.input_usd_micros_per_m is not None)
                                else default_in
                            )
                            out_price = (
                                cfg.output_usd_micros_per_m
                                if (cfg and cfg.output_usd_micros_per_m is not None)
                                else default_out
                            )
                            if in_price is not None and input_tokens > 0:
                                cost_micros += int((input_tokens * int(in_price) + 999_999) // 1_000_000)
                            if out_price is not None and output_tokens > 0:
                                cost_micros += int((output_tokens * int(out_price) + 999_999) // 1_000_000)

                        await record_usage_event(
                            s,
                            org_id=membership.org_id,
                            user_id=user.id,
                            api_key_id=api_key.id,
                            model_id=model_id.strip(),
                            ok=ok,
                            status_code=int(res.status_code),
                            input_tokens=input_tokens,
                            output_tokens=output_tokens,
                            total_tokens=total_tokens,
                            cost_usd_micros=cost_micros,
                            total_duration_ms=total_ms,
                            ttft_ms=ttft_ms,
                            source_ip=source_ip,
                        )
                except Exception:
                    pass

        return StreamingResponse(
            iterator(),
            status_code=int(res.status_code),
            media_type=content_type,
            headers={"cache-control": "no-cache", "x-accel-buffering": "no"},
        )

    # Non-stream response: read full body then close the upstream response/client.
    async with httpx.AsyncClient(timeout=timeout) as client:
        async with client.stream("POST", upstream_url, headers=headers, content=raw) as res:
            content_type = res.headers.get("content-type") or "application/json"
            ok = res.status_code < 400

            body_bytes = bytearray()
            first = None
            async for chunk in res.aiter_bytes():
                if first is None:
                    first = time.perf_counter()
                    ttft_ms = int((first - started) * 1000)
                body_bytes.extend(chunk)
            total_ms = int((time.perf_counter() - started) * 1000)

    # Record usage/spend for dashboard and logs.
    input_tokens = 0
    output_tokens = 0
    total_tokens = 0
    cost_micros = 0
    if ok and content_type.startswith("application/json"):
        try:
            body = json.loads(body_bytes.decode("utf-8"))
            usage = body.get("usage") if isinstance(body, dict) else None
            if isinstance(usage, dict):
                input_tokens = int(usage.get("prompt_tokens") or 0)
                completion = int(usage.get("completion_tokens") or 0)
                total_raw = usage.get("total_tokens")
                total_tokens = int(total_raw) if total_raw is not None else 0
                output_tokens = max(total_tokens - input_tokens, 0) if total_tokens > 0 else completion
                if total_tokens <= 0:
                    total_tokens = input_tokens + output_tokens
        except Exception:
            pass

    # Estimate cost using configured/default pricing when usage is present.
    if ok and total_tokens > 0:
        cfg = await get_model_config(session, org_id=membership.org_id, model_id=model_id.strip())
        from app.storage.models_db import default_price_for_model

        default_in, default_out = default_price_for_model(model_id.strip())
        in_price = cfg.input_usd_micros_per_m if (cfg and cfg.input_usd_micros_per_m is not None) else default_in
        out_price = cfg.output_usd_micros_per_m if (cfg and cfg.output_usd_micros_per_m is not None) else default_out
        if in_price is not None and input_tokens > 0:
            cost_micros += int((input_tokens * int(in_price) + 999_999) // 1_000_000)
        if out_price is not None and output_tokens > 0:
            cost_micros += int((output_tokens * int(out_price) + 999_999) // 1_000_000)

    try:
        await record_usage_event(
            session,
            org_id=membership.org_id,
            user_id=user.id,
            api_key_id=api_key.id,
            model_id=model_id.strip(),
            ok=ok,
            status_code=int(res.status_code),
            input_tokens=input_tokens,
            output_tokens=output_tokens,
            total_tokens=total_tokens,
            cost_usd_micros=cost_micros,
            total_duration_ms=total_ms,
            ttft_ms=ttft_ms,
            source_ip=source_ip,
        )
    except Exception:
        # Accounting should not break the request path.
        pass

    return Response(content=bytes(body_bytes), status_code=int(res.status_code), media_type=content_type)


@router.get("/admin/models", response_model=AdminModelsListResponse)
async def admin_list_models(
    session: AsyncSession = Depends(get_db_session),
    admin_user=Depends(require_admin),
    membership=Depends(get_current_membership),
) -> AdminModelsListResponse:
    _ = admin_user
    items = await list_admin_models(session, org_id=membership.org_id)
    return AdminModelsListResponse(items=items)  # type: ignore[arg-type]


@router.patch("/admin/models/{model_id:path}", response_model=AdminModelUpdateResponse)
async def admin_update_model(
    model_id: str,
    payload: AdminModelUpdateRequest,
    session: AsyncSession = Depends(get_db_session),
    admin_user=Depends(require_admin),
    membership=Depends(get_current_membership),
) -> AdminModelUpdateResponse:
    _ = admin_user
    fields_set = getattr(payload, "model_fields_set", None)
    if fields_set is None:
        fields_set = getattr(payload, "__fields_set__", set())

    enabled = payload.enabled if "enabled" in fields_set else None
    input_price = payload.input_usd_per_m if "input_usd_per_m" in fields_set else UNSET
    output_price = payload.output_usd_per_m if "output_usd_per_m" in fields_set else UNSET

    try:
        row = await upsert_model_config(
            session,
            org_id=membership.org_id,
            model_id=model_id,
            enabled=enabled,
            input_usd_per_m=input_price,
            output_usd_per_m=output_price,
        )
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e)) from e

    items = await list_admin_models(session, org_id=membership.org_id)
    item = next((x for x in items if x.get("model") == row.model_id), None)
    if not item:
        item = {"model": row.model_id, "enabled": bool(row.enabled), "sources": 0, "available": False}
    return AdminModelUpdateResponse(item=item)  # type: ignore[arg-type]


@router.get("/usage", response_model=UsageResponse)
async def usage(
    session: AsyncSession = Depends(get_db_session),
    current_user=Depends(get_current_user),
    membership=Depends(get_current_membership),
) -> dict:
    from app.storage.usage_db import get_usage_response

    return await get_usage_response(session, org_id=membership.org_id, user_id=current_user.id)


@router.get("/billing/ledger", response_model=BillingLedgerListResponse)
async def billing_ledger(
    limit: int = 50,
    offset: int = 0,
    session: AsyncSession = Depends(get_db_session),
    current_user=Depends(get_current_user),
    membership=Depends(get_current_membership),
) -> BillingLedgerListResponse:  # type: ignore[no-untyped-def]
    items = await list_balance_ledger(
        session,
        org_id=membership.org_id,
        user_id=current_user.id,
        limit=int(limit),
        offset=int(offset),
    )
    return BillingLedgerListResponse(items=items)  # type: ignore[arg-type]


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


@router.patch("/keys/{key_id}", response_model=ApiKeyUpdateResponse)
async def update_key(
    key_id: str,
    payload: ApiKeyUpdateRequest,
    session: AsyncSession = Depends(get_db_session),
    current_user=Depends(get_current_user),
) -> ApiKeyUpdateResponse:
    try:
        item = await update_api_key(
            session,
            key_id,
            input=payload,
            user_id=None if current_user.role in {"admin", "owner"} else current_user.id,
        )
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e)) from e
    if not item:
        raise HTTPException(status_code=404, detail="not found")
    return ApiKeyUpdateResponse(item=item)


@router.delete("/keys/{key_id}", response_model=ApiKeyDeleteResponse)
async def delete_key(
    key_id: str,
    session: AsyncSession = Depends(get_db_session),
    current_user=Depends(get_current_user),
) -> ApiKeyDeleteResponse:
    ok = await delete_api_key(
        session, key_id, user_id=None if current_user.role in {"admin", "owner"} else current_user.id
    )
    if not ok:
        raise HTTPException(status_code=404, detail="not found")
    return ApiKeyDeleteResponse(ok=True, id=key_id)


@router.get("/keys/{key_id}/reveal", response_model=ApiKeyRevealResponse)
async def reveal_key(
    key_id: str,
    session: AsyncSession = Depends(get_db_session),
    current_user=Depends(get_current_user),
) -> ApiKeyRevealResponse:
    try:
        parsed = uuid.UUID(key_id)
    except ValueError:
        raise HTTPException(status_code=404, detail="not found") from None

    row = await session.get(ApiKey, parsed)
    if not row or row.user_id != current_user.id:
        raise HTTPException(status_code=404, detail="not found")
    key = getattr(row, "key_plaintext", None)
    if not isinstance(key, str) or key.strip() == "":
        raise HTTPException(status_code=409, detail="key not stored; rotate")
    return ApiKeyRevealResponse(id=key_id, key=key.strip())
