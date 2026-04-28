from __future__ import annotations

import asyncio
from dataclasses import dataclass
from email.parser import BytesParser
from email.policy import default as email_policy
import logging
import uuid
import time
import datetime as dt
import secrets
import hashlib
import hmac

from fastapi import APIRouter, Depends, HTTPException, Request, Query
from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession
from starlette.responses import Response, StreamingResponse

import httpx
import json

from app.api.client_ip import extract_request_client_ip, extract_request_client_ip_or_localhost
from app.api.llm_proxy import LlmProxyContext, UsagePricing, estimate_cost_usd_micros
from app.api.upstream_headers import _build_upstream_headers, _filter_upstream_response_headers
from app.auth import get_current_membership, get_current_user, require_admin
from app.constants import ACCOUNT_TEMPORARILY_LIMITED_DETAIL
from app.models.api_key import ApiKey
from app.models.membership import Membership
from app.models.oauth_identity import OAuthIdentity
from app.models.user import User
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
from app.schemas.auth_methods import (
    AuthMethodsResponse,
    EmailChangeConfirmRequest,
    EmailChangeConfirmResponse,
    EmailChangeRequestCodeRequest,
    PasswordChangeRequest,
    PasswordRemoveRequest,
    PasswordRequestCodeResponse,
    PasswordSetRequest,
)
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
from app.schemas.admin_analytics import AdminAnalyticsResponse
from app.schemas.admin_overview import AdminOverviewResponse
from app.schemas.billing import (
    BillingLedgerListResponse,
    BillingSettingsResponse,
    BillingTopupCheckoutRequest,
    BillingTopupCheckoutResponse,
    BillingTopupStatusResponse,
)
from app.schemas.invite import InviteSummaryResponse, InviteVisitRequest
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
from app.storage.orgs_db import ensure_default_org, ensure_membership
from app.storage.auth_db import grant_admin_role
from app.storage.auth_db import get_user_by_token
from app.storage.auth_db import login as auth_login
from app.storage.auth_db import register_and_login, revoke_session
from app.storage.auth_db import revoke_other_sessions
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
from app.storage.admin_analytics_db import get_admin_analytics
from app.storage.admin_overview_db import get_admin_overview
from app.storage.oauth_google import link_google_identity, login_with_google
from app.schemas.logs import LogsListResponse
from app.db import SessionLocal
from app.storage.email_verification_db import request_email_code, verify_email_code
from app.models.organization import Organization
from app.security_password import hash_password, verify_password
from app.core.config import settings
from app.core.zhupay import (
    ZhupayError,
    convert_credits_to_money,
    create_jump_order,
    is_configured as zhupay_is_configured,
    money_to_cents as zhupay_money_to_cents,
    query_order as zhupay_query_order,
    verify_payload as zhupay_verify_payload,
)
from app.storage.topups_db import (
    complete_billing_topup,
    create_billing_topup,
    creem_event_exists,
    generate_topup_request_id,
    get_billing_topup_by_checkout_id,
    get_billing_topup_by_order_id,
    get_billing_topup_by_request_id,
    get_billing_topup_for_user,
    mark_billing_topup_failed,
    record_creem_event,
    set_billing_topup_status,
)
from app.storage.referrals_db import process_referral_refund

router = APIRouter()
logger = logging.getLogger(__name__)

def _safe_int(value: object) -> int:
    try:
        return int(value)  # type: ignore[arg-type]
    except Exception:
        return 0


def _normalize_public_url(raw: str) -> str:
    value = (raw or "").strip()
    if value == "":
        return "http://localhost:3000"
    return value.rstrip("/")


def _get_request_client_ip(request: Request) -> str:
    return extract_request_client_ip_or_localhost(request)


def _translate_zhupay_error(exc: ZhupayError) -> HTTPException:
    message = str(exc).strip().lower()
    if any(
        token in message
        for token in (
            "configured",
            "private key",
            "public key",
            "conversion rate",
            "pid",
        )
    ):
        return HTTPException(status_code=503, detail="billing not configured")
    return HTTPException(status_code=502, detail="payment provider error")


def _creem_is_configured() -> bool:
    return all(
        (
            (settings.creem_api_key or "").strip(),
            (settings.creem_product_id or "").strip(),
        )
    )


def _creem_base_url() -> str:
    api_key = (settings.creem_api_key or "").strip()
    if api_key.startswith("creem_test_"):
        return "https://test-api.creem.io"
    return "https://api.creem.io"


def _llm_upstream_timeout_seconds() -> float:
    raw_value = int(settings.llm_upstream_timeout_seconds)
    if raw_value < 1:
        return 1.0
    return float(raw_value)


def _llm_upstream_timeout() -> httpx.Timeout:
    seconds = _llm_upstream_timeout_seconds()
    return httpx.Timeout(seconds, connect=min(10.0, seconds))


def _translate_upstream_http_error(exc: httpx.HTTPError) -> HTTPException:
    if isinstance(exc, httpx.ReadTimeout):
        seconds = int(_llm_upstream_timeout_seconds())
        return HTTPException(status_code=504, detail=f"upstream read timeout after {seconds}s")
    if isinstance(exc, httpx.ConnectTimeout):
        return HTTPException(status_code=504, detail="upstream connect timeout")
    if isinstance(exc, httpx.ConnectError):
        return HTTPException(status_code=503, detail="upstream unavailable")
    if isinstance(
        exc,
        (
            httpx.ReadError,
            httpx.WriteError,
            httpx.RemoteProtocolError,
            httpx.LocalProtocolError,
            httpx.ProtocolError,
        ),
    ):
        return HTTPException(status_code=502, detail="upstream communication error")
    if isinstance(exc, httpx.StreamError):
        return HTTPException(status_code=502, detail="upstream stream error")
    return HTTPException(status_code=502, detail="upstream request failed")


def _verify_creem_signature(*, raw_body: bytes, signature: str) -> bool:
    secret = (settings.creem_webhook_secret or "").strip()
    if secret == "":
        return False
    computed = hmac.new(secret.encode("utf-8"), raw_body, hashlib.sha256).hexdigest()
    try:
        return hmac.compare_digest(computed, signature)
    except Exception:
        return False


def _extract_creem_currency(obj: dict) -> str | None:
    order = obj.get("order")
    if isinstance(order, dict):
        cur = order.get("currency")
        if isinstance(cur, str) and cur.strip():
            return cur.strip().upper()[:8]

    txn = obj.get("transaction")
    if isinstance(txn, dict):
        cur = txn.get("currency")
        if isinstance(cur, str) and cur.strip():
            return cur.strip().upper()[:8]

    product = obj.get("product")
    if isinstance(product, dict):
        cur = product.get("currency")
        if isinstance(cur, str) and cur.strip():
            return cur.strip().upper()[:8]

    return None


def _extract_creem_product_id(obj: dict) -> str | None:
    order = obj.get("order")
    if isinstance(order, dict):
        raw = order.get("product")
        if isinstance(raw, str) and raw.strip():
            return raw.strip()

    product = obj.get("product")
    if isinstance(product, dict):
        raw = product.get("id")
        if isinstance(raw, str) and raw.strip():
            return raw.strip()
    if isinstance(product, str) and product.strip():
        return product.strip()

    return None


def _extract_creem_amount_total_cents(obj: dict) -> int | None:
    raw_total = obj.get("amount_total")
    if raw_total is not None:
        try:
            return int(raw_total)
        except Exception:
            pass

    txn = obj.get("transaction")
    if isinstance(txn, dict):
        paid = txn.get("amount_paid")
        if paid is not None:
            try:
                return int(paid)
            except Exception:
                pass

    order = obj.get("order")
    if isinstance(order, dict):
        paid = order.get("amount_paid")
        if paid is not None:
            try:
                return int(paid)
            except Exception:
                pass

    return None


def _extract_creem_payer_email(obj: dict) -> str | None:
    customer = obj.get("customer")
    if isinstance(customer, dict):
        email = customer.get("email")
        if isinstance(email, str) and email.strip():
            return email.strip().lower()[:254]

    order = obj.get("order")
    if isinstance(order, dict):
        raw = order.get("customer_email")
        if isinstance(raw, str) and raw.strip():
            return raw.strip().lower()[:254]
        raw = order.get("email")
        if isinstance(raw, str) and raw.strip():
            return raw.strip().lower()[:254]
        cust = order.get("customer")
        if isinstance(cust, dict):
            email = cust.get("email")
            if isinstance(email, str) and email.strip():
                return email.strip().lower()[:254]

    txn = obj.get("transaction")
    if isinstance(txn, dict):
        raw = txn.get("customer_email")
        if isinstance(raw, str) and raw.strip():
            return raw.strip().lower()[:254]

    return None


def _topup_provider(topup) -> str | None:
    provider = str(getattr(topup, "provider", "") or "").strip().lower()
    if provider:
        return provider
    currency = str(getattr(topup, "currency", "") or "").strip().upper()
    if currency == "CNY":
        return "zhupay"
    if currency == "USD":
        return "creem"
    return None


async def _sync_pending_topup_from_zhupay(
    session: AsyncSession,
    *,
    topup,
):
    if str(getattr(topup, "status", "") or "").strip().lower() != "pending":
        return topup
    if _topup_provider(topup) != "zhupay":
        return topup
    if not zhupay_is_configured():
        return topup

    try:
        remote = await zhupay_query_order(out_trade_no=str(topup.request_id))
    except ZhupayError:
        return topup

    expected_cents = int(getattr(topup, "amount_total_cents", 0) or 0) or None
    if expected_cents is not None and remote.money_cents is not None and remote.money_cents != expected_cents:
        synced = await mark_billing_topup_failed(
            session,
            request_id=str(topup.request_id),
            provider="zhupay",
            checkout_id=remote.trade_no,
            order_id=remote.api_trade_no,
            currency="CNY",
            amount_total_cents=remote.money_cents,
        )
        return synced or topup

    if remote.status == 1:
        synced = await complete_billing_topup(
            session,
            request_id=str(topup.request_id),
            provider="zhupay",
            checkout_id=remote.trade_no,
            order_id=remote.api_trade_no,
            currency="CNY",
            amount_total_cents=remote.money_cents,
            payer_email=None,
        )
        return synced or topup

    if remote.status in {2, 3, 4}:
        synced = await mark_billing_topup_failed(
            session,
            request_id=str(topup.request_id),
            provider="zhupay",
            checkout_id=remote.trade_no,
            order_id=remote.api_trade_no,
            currency="CNY",
            amount_total_cents=remote.money_cents,
        )
        return synced or topup

    if (
        getattr(topup, "checkout_id", None) != remote.trade_no
        or (remote.api_trade_no and getattr(topup, "order_id", None) != remote.api_trade_no)
    ):
        synced = await set_billing_topup_status(
            session,
            request_id=str(topup.request_id),
            status="pending",
            provider="zhupay",
            checkout_id=remote.trade_no,
            order_id=remote.api_trade_no,
            currency="CNY",
            amount_total_cents=remote.money_cents,
        )
        return synced or topup

    return topup


async def _compute_cost_usd_micros(
    session: AsyncSession,
    *,
    org_id: uuid.UUID,
    model_id: str,
    input_tokens: int,
    cached_tokens: int,
    output_tokens: int,
) -> int:
    cfg = await get_model_config(session, org_id=org_id, model_id=model_id.strip())
    pricing = _resolve_usage_pricing(cfg, model_id=model_id.strip())
    return estimate_cost_usd_micros(
        pricing=pricing,
        input_tokens=input_tokens,
        cached_tokens=cached_tokens,
        output_tokens=output_tokens,
    )


async def _record_usage_event_best_effort(
    *,
    org_id: uuid.UUID,
    user_id: uuid.UUID,
    api_key_id: uuid.UUID | None,
    model_id: str,
    ok: bool,
    status_code: int,
    input_tokens: int,
    cached_tokens: int,
    output_tokens: int,
    total_tokens: int,
    cost_usd_micros: int,
    total_duration_ms: int,
    ttft_ms: int,
    source_ip: str | None,
    recompute_cost: bool = True,
) -> None:
    computed_cost = int(max(cost_usd_micros, 0))

    try:
        async with SessionLocal() as s:
            if recompute_cost and ok and total_tokens > 0 and computed_cost <= 0:
                try:
                    computed_cost = await _compute_cost_usd_micros(
                        s,
                        org_id=org_id,
                        model_id=model_id,
                        input_tokens=input_tokens,
                        cached_tokens=cached_tokens,
                        output_tokens=output_tokens,
                    )
                except Exception:
                    logger.exception("usage: cost compute failed")
                    computed_cost = 0

            await record_usage_event(
                s,
                org_id=org_id,
                user_id=user_id,
                api_key_id=api_key_id,
                model_id=model_id,
                ok=ok,
                status_code=status_code,
                input_tokens=input_tokens,
                cached_tokens=cached_tokens,
                output_tokens=output_tokens,
                total_tokens=total_tokens,
                cost_usd_micros=computed_cost,
                total_duration_ms=total_duration_ms,
                ttft_ms=ttft_ms,
                source_ip=source_ip,
            )
    except Exception:
        logger.exception("usage: record failed")


def _extract_usage_tokens(obj: dict) -> tuple[int, int, int, int] | None:
    usage = obj.get("usage")
    if not isinstance(usage, dict):
        response = obj.get("response")
        if isinstance(response, dict):
            usage = response.get("usage")
    if not isinstance(usage, dict):
        return None

    # OpenAI chat.completions usage
    if "prompt_tokens" in usage or "completion_tokens" in usage:
        prompt = _safe_int(usage.get("prompt_tokens") or 0)
        completion = _safe_int(usage.get("completion_tokens") or 0)
        total_raw = usage.get("total_tokens")
        total = _safe_int(total_raw) if total_raw is not None else 0
        details = usage.get("prompt_tokens_details")
        cached = 0
        if isinstance(details, dict):
            cached = _safe_int(details.get("cached_tokens") or 0)

        output = max(total - prompt, 0) if total > 0 else completion
        total_tokens = total if total > 0 else (prompt + output)
        cached_tokens = min(max(cached, 0), max(prompt, 0))
        return prompt, cached_tokens, output, total_tokens

    # OpenAI responses usage
    if "input_tokens" in usage or "output_tokens" in usage:
        input_tokens = _safe_int(usage.get("input_tokens") or 0)
        output_tokens = _safe_int(usage.get("output_tokens") or 0)
        total_raw = usage.get("total_tokens")
        total_tokens = _safe_int(total_raw) if total_raw is not None else 0
        details = usage.get("input_tokens_details")
        cached = 0
        if isinstance(details, dict):
            cached = _safe_int(details.get("cached_tokens") or 0)

        if output_tokens <= 0 and total_tokens > 0:
            output_tokens = max(total_tokens - input_tokens, 0)
        if total_tokens <= 0:
            total_tokens = input_tokens + output_tokens

        cached_tokens = min(max(cached, 0), max(input_tokens, 0))
        return input_tokens, cached_tokens, output_tokens, total_tokens

    return None


@dataclass(frozen=True)
class _ParsedLlmRequest:
    payload: dict[str, object]
    model_id: str


@dataclass(frozen=True)
class _ParsedProxyRequest:
    model_id: str
    stream: bool


def _parse_llm_request(raw: bytes) -> _ParsedLlmRequest:
    try:
        payload_obj = json.loads(raw.decode("utf-8"))
    except Exception as e:  # noqa: BLE001
        raise HTTPException(status_code=400, detail="invalid json") from e
    if not isinstance(payload_obj, dict):
        raise HTTPException(status_code=400, detail="invalid json")

    payload: dict[str, object] = payload_obj
    model_raw = payload.get("model")
    if not isinstance(model_raw, str) or not model_raw.strip():
        raise HTTPException(status_code=400, detail="missing model")

    return _ParsedLlmRequest(payload=payload, model_id=model_raw.strip())


def _coerce_form_bool(value: str | None) -> bool:
    if value is None:
        return False
    return value.strip().lower() in {"1", "true", "yes", "on"}


def _parse_multipart_text_fields(raw: bytes, *, content_type: str) -> dict[str, str]:
    try:
        header = f"Content-Type: {content_type}\r\nMIME-Version: 1.0\r\n\r\n".encode("latin-1")
    except UnicodeEncodeError as e:
        raise HTTPException(status_code=400, detail="invalid multipart content type") from e

    try:
        message = BytesParser(policy=email_policy).parsebytes(header + raw)
    except Exception as e:  # noqa: BLE001
        raise HTTPException(status_code=400, detail="invalid multipart form") from e

    if not message.is_multipart():
        raise HTTPException(status_code=400, detail="invalid multipart form")

    fields: dict[str, str] = {}
    for part in message.iter_parts():
        if part.get_content_disposition() != "form-data":
            continue
        name = part.get_param("name", header="content-disposition")
        if not isinstance(name, str) or not name:
            continue
        if part.get_filename() is not None:
            continue
        if name in fields:
            continue
        try:
            value = part.get_content()
        except Exception:
            payload = part.get_payload(decode=True)
            if not isinstance(payload, bytes):
                continue
            value = payload.decode("utf-8", errors="replace")
        if isinstance(value, str):
            fields[name] = value
    return fields


def _parse_proxy_request(raw: bytes, *, content_type: str, allow_multipart: bool = False) -> _ParsedProxyRequest:
    normalized_content_type = content_type.strip().lower()
    if allow_multipart and normalized_content_type.startswith("multipart/form-data"):
        fields = _parse_multipart_text_fields(raw, content_type=content_type)
        model_raw = fields.get("model")
        if not isinstance(model_raw, str) or not model_raw.strip():
            raise HTTPException(status_code=400, detail="missing model")
        return _ParsedProxyRequest(model_id=model_raw.strip(), stream=_coerce_form_bool(fields.get("stream")))

    parsed = _parse_llm_request(raw)
    return _ParsedProxyRequest(model_id=parsed.model_id, stream=bool(parsed.payload.get("stream")))


def _extract_source_ip(request: Request) -> str | None:
    return extract_request_client_ip(request)


def _log_llm_request_received(
    request: Request,
    *,
    context: LlmProxyContext,
    stream: bool,
) -> None:
    logger.info(
        "llm request: user_email=%s method=%s path=%s model=%s stream=%s source_ip=%s",
        context.user_email,
        request.method,
        request.url.path,
        context.model_id,
        "true" if stream else "false",
        context.source_ip or "-",
    )


def _resolve_usage_pricing(cfg: object | None, *, model_id: str) -> UsagePricing:
    from app.storage.models_db import default_price_for_model

    default_in, default_out = default_price_for_model(model_id.strip())
    input_price = getattr(cfg, "input_usd_micros_per_m", None)
    output_price = getattr(cfg, "output_usd_micros_per_m", None)

    return UsagePricing(
        input_usd_micros_per_m=input_price if input_price is not None else default_in,
        output_usd_micros_per_m=output_price if output_price is not None else default_out,
    )


def _auth_error_status(detail: str) -> int:
    if detail == ACCOUNT_TEMPORARILY_LIMITED_DETAIL:
        return 429
    if detail in {"banned", "api_key_spend_limit_exceeded"}:
        return 403
    return 401


async def _resolve_llm_proxy_context(
    request: Request,
    session: AsyncSession,
    *,
    model_id: str,
) -> LlmProxyContext:
    auth = request.headers.get("authorization")
    try:
        api_key, user = await authenticate_api_key(session, authorization=auth)
    except ValueError as e:
        detail = str(e) or "unauthorized"
        raise HTTPException(status_code=_auth_error_status(detail), detail=detail) from e

    from app.storage.balance_math import remaining_usd_micros

    credits_cents = int(getattr(user, "balance", 0) or 0)
    spend_micros_total = int(getattr(user, "spend_usd_micros_total", 0) or 0)
    if remaining_usd_micros(credits_usd_cents=credits_cents, spend_usd_micros_total=spend_micros_total) <= 0:
        raise HTTPException(status_code=402, detail="insufficient balance")

    membership = await _require_default_membership(session, user_id=user.id)

    cfg = await get_model_config(session, org_id=membership.org_id, model_id=model_id)
    if cfg and not cfg.enabled:
        raise HTTPException(status_code=403, detail="model disabled")

    channel = await pick_channel_for_group(session, org_id=membership.org_id, group_name=user.group_name)
    if not channel:
        raise HTTPException(status_code=503, detail="no channel configured")

    context = LlmProxyContext(
        api_key_id=api_key.id,
        user_id=user.id,
        user_email=str(user.email),
        org_id=membership.org_id,
        model_id=model_id,
        source_ip=_extract_source_ip(request),
        upstream_base_url=str(channel.base_url).rstrip("/"),
        upstream_api_key=str(channel.api_key),
        pricing=_resolve_usage_pricing(cfg, model_id=model_id),
    )

    # Streaming responses can stay open for a long time; release the request-scoped
    # SQLAlchemy session before any upstream I/O so we do not pin a DB transaction.
    await session.close()
    return context


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
    return {
        "registrationEnabled": bool(getattr(org, "registration_enabled", True)),
        "billingTopupEnabled": bool(getattr(org, "billing_topup_enabled", True)),
    }


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
    if payload.billing_topup_enabled is not None:
        org.billing_topup_enabled = bool(payload.billing_topup_enabled)
    if payload.registration_enabled is not None or payload.billing_topup_enabled is not None:
        await session.commit()
        await session.refresh(org)
    return {
        "registrationEnabled": bool(getattr(org, "registration_enabled", True)),
        "billingTopupEnabled": bool(getattr(org, "billing_topup_enabled", True)),
    }


@router.get("/admin/overview", response_model=AdminOverviewResponse)
async def admin_overview(
    session: AsyncSession = Depends(get_db_session),
    admin_user=Depends(require_admin),
    membership=Depends(get_current_membership),
) -> dict:
    _ = admin_user
    return await get_admin_overview(session, org_id=membership.org_id)


@router.get("/admin/analytics", response_model=AdminAnalyticsResponse)
async def admin_analytics(
    from_: dt.datetime = Query(..., alias="from"),
    to: dt.datetime = Query(...),
    tz: str = "UTC",
    granularity: str = "day",
    limit: int = 10,
    session: AsyncSession = Depends(get_db_session),
    admin_user=Depends(require_admin),
    membership=Depends(get_current_membership),
) -> dict:
    _ = admin_user
    try:
        return await get_admin_analytics(
            session,
            org_id=membership.org_id,
            start=from_,
            end=to,
            tz=tz,
            granularity=granularity,
            limit=limit,
        )
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e)) from e


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

    client_ip = _extract_source_ip(request)
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
        from app.constants import DEVICE_ID_COOKIE_NAME

        signup_ip = _extract_source_ip(request)
        signup_device_id = request.cookies.get(DEVICE_ID_COOKIE_NAME)
        signup_user_agent = request.headers.get("user-agent")
        return await register_and_login(
            session,
            RegisterRequest(
                email=payload.email,
                password=payload.password,
                adminBootstrapToken=payload.admin_bootstrap_token,
                inviteCode=payload.invite_code,
            ),
            signup_ip=signup_ip,
            signup_device_id=signup_device_id,
            signup_user_agent=signup_user_agent,
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
    from app.storage.balance_math import remaining_usd_2

    def _dt_iso(value: dt.datetime | None) -> str | None:
        if not value:
            return None
        return value.astimezone(dt.timezone.utc).isoformat()

    credits_cents = int(getattr(current_user, "balance", 0) or 0)
    spend_micros_total = int(getattr(current_user, "spend_usd_micros_total", 0) or 0)
    balance_usd = remaining_usd_2(credits_usd_cents=credits_cents, spend_usd_micros_total=spend_micros_total)

    return UserPublic(
        id=str(current_user.id),
        email=current_user.email,
        role=membership.role,
        group=current_user.group_name,
        balance=balance_usd,
        orgId=str(membership.org_id),
        createdAt=_dt_iso(current_user.created_at) or dt.datetime.now(dt.timezone.utc).isoformat(),
        lastLoginAt=_dt_iso(current_user.last_login_at),
    )


def _dt_iso(value: dt.datetime | None) -> str | None:
    if not value:
        return None
    return value.astimezone(dt.timezone.utc).isoformat()


def _get_request_session_token(request: Request) -> str | None:
    from app.constants import SESSION_COOKIE_NAME

    auth = request.headers.get("authorization")
    token = _extract_bearer_token(auth)
    if not token:
        token = request.cookies.get(SESSION_COOKIE_NAME)
    return token


async def _auth_methods(session: AsyncSession, *, user_id: uuid.UUID) -> list[OAuthIdentity]:
    return (
        await session.execute(select(OAuthIdentity).where(OAuthIdentity.user_id == user_id))
    ).scalars().all()


async def _build_auth_methods_response(session: AsyncSession, *, current_user) -> AuthMethodsResponse:
    identities = await _auth_methods(session, user_id=current_user.id)
    oauth = [
        {
            "id": str(identity.id),
            "provider": identity.provider,
            "email": identity.email,
            "createdAt": _dt_iso(identity.created_at) or dt.datetime.now(dt.timezone.utc).isoformat(),
        }
        for identity in identities
    ]
    return AuthMethodsResponse(passwordSet=bool(current_user.password_set_at), oauth=oauth)


def _validate_password_value(password: str) -> None:
    if len(password) < 6:
        raise HTTPException(status_code=400, detail="password too small (min 6)")
    if len(password) > 128:
        raise HTTPException(status_code=400, detail="password too large (max 128)")


@router.get("/auth/methods", response_model=AuthMethodsResponse)
async def auth_methods(
    session: AsyncSession = Depends(get_db_session),
    current_user=Depends(get_current_user),
) -> AuthMethodsResponse:
    return await _build_auth_methods_response(session, current_user=current_user)


@router.post("/auth/password/request-code", response_model=PasswordRequestCodeResponse)
async def password_request_code(
    request: Request,
    session: AsyncSession = Depends(get_db_session),
    current_user=Depends(get_current_user),
) -> PasswordRequestCodeResponse:
    client_ip = _extract_source_ip(request)
    try:
        expires = await request_email_code(
            session,
            email=str(current_user.email),
            purpose="password",
            client_ip=client_ip,
        )
    except ValueError as e:
        msg = str(e)
        if msg in {"too many requests", "please wait"}:
            raise HTTPException(status_code=429, detail=msg) from e
        raise HTTPException(status_code=400, detail=msg) from e
    return PasswordRequestCodeResponse(ok=True, expiresInSeconds=expires)


@router.post("/auth/password/set", response_model=AuthMethodsResponse)
async def password_set(
    payload: PasswordSetRequest,
    request: Request,
    session: AsyncSession = Depends(get_db_session),
    current_user=Depends(get_current_user),
) -> AuthMethodsResponse:
    try:
        await verify_email_code(
            session,
            email=str(current_user.email),
            purpose="password",
            code=str(payload.code),
        )
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e)) from e

    _validate_password_value(payload.password)
    now = dt.datetime.now(dt.timezone.utc)
    current_user.password_hash = hash_password(payload.password)
    current_user.password_set_at = now
    await session.commit()
    await session.refresh(current_user)

    token = _get_request_session_token(request)
    if token:
        await revoke_other_sessions(session, user_id=current_user.id, current_token=token)

    return await _build_auth_methods_response(session, current_user=current_user)


@router.post("/auth/password/change", response_model=AuthMethodsResponse)
async def password_change(
    payload: PasswordChangeRequest,
    request: Request,
    session: AsyncSession = Depends(get_db_session),
    current_user=Depends(get_current_user),
) -> AuthMethodsResponse:
    if not verify_password(payload.current_password, current_user.password_hash):
        raise HTTPException(status_code=400, detail="invalid current password")

    _validate_password_value(payload.new_password)
    now = dt.datetime.now(dt.timezone.utc)
    current_user.password_hash = hash_password(payload.new_password)
    current_user.password_set_at = now
    await session.commit()
    await session.refresh(current_user)

    token = _get_request_session_token(request)
    if token:
        await revoke_other_sessions(session, user_id=current_user.id, current_token=token)

    return await _build_auth_methods_response(session, current_user=current_user)


@router.post("/auth/password/remove", response_model=AuthMethodsResponse)
async def password_remove(
    payload: PasswordRemoveRequest,
    request: Request,
    session: AsyncSession = Depends(get_db_session),
    current_user=Depends(get_current_user),
) -> AuthMethodsResponse:
    identities = await _auth_methods(session, user_id=current_user.id)
    if len(identities) == 0:
        raise HTTPException(status_code=400, detail="no oauth identity")

    try:
        await verify_email_code(
            session,
            email=str(current_user.email),
            purpose="password",
            code=str(payload.code),
        )
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e)) from e

    current_user.password_hash = hash_password(secrets.token_urlsafe(32))
    current_user.password_set_at = None
    await session.commit()
    await session.refresh(current_user)

    token = _get_request_session_token(request)
    if token:
        await revoke_other_sessions(session, user_id=current_user.id, current_token=token)

    return await _build_auth_methods_response(session, current_user=current_user)


@router.post("/auth/email/change/request-code", response_model=PasswordRequestCodeResponse)
async def email_change_request_code(
    payload: EmailChangeRequestCodeRequest,
    request: Request,
    session: AsyncSession = Depends(get_db_session),
    current_user=Depends(get_current_user),
) -> PasswordRequestCodeResponse:
    new_email = str(payload.new_email).strip().lower()
    if new_email == str(current_user.email).strip().lower():
        raise HTTPException(status_code=400, detail="same email")

    existing = (await session.execute(select(User).where(User.email == new_email))).scalar_one_or_none()
    if existing and existing.id != current_user.id:
        raise HTTPException(status_code=400, detail="email already registered")

    client_ip = _extract_source_ip(request)
    try:
        expires = await request_email_code(
            session,
            email=new_email,
            purpose="email_change_new",
            client_ip=client_ip,
        )
    except ValueError as e:
        msg = str(e)
        if msg in {"too many requests", "please wait"}:
            raise HTTPException(status_code=429, detail=msg) from e
        raise HTTPException(status_code=400, detail=msg) from e
    return PasswordRequestCodeResponse(ok=True, expiresInSeconds=expires)


@router.post("/auth/email/change/request-current-code", response_model=PasswordRequestCodeResponse)
async def email_change_request_current_code(
    request: Request,
    session: AsyncSession = Depends(get_db_session),
    current_user=Depends(get_current_user),
) -> PasswordRequestCodeResponse:
    client_ip = _extract_source_ip(request)
    try:
        expires = await request_email_code(
            session,
            email=str(current_user.email),
            purpose="email_change_current",
            client_ip=client_ip,
        )
    except ValueError as e:
        msg = str(e)
        if msg in {"too many requests", "please wait"}:
            raise HTTPException(status_code=429, detail=msg) from e
        raise HTTPException(status_code=400, detail=msg) from e
    return PasswordRequestCodeResponse(ok=True, expiresInSeconds=expires)


@router.post("/auth/email/change/confirm", response_model=EmailChangeConfirmResponse)
async def email_change_confirm(
    payload: EmailChangeConfirmRequest,
    request: Request,
    session: AsyncSession = Depends(get_db_session),
    current_user=Depends(get_current_user),
) -> EmailChangeConfirmResponse:
    new_email = str(payload.new_email).strip().lower()
    if new_email == str(current_user.email).strip().lower():
        raise HTTPException(status_code=400, detail="same email")

    existing = (await session.execute(select(User).where(User.email == new_email))).scalar_one_or_none()
    if existing and existing.id != current_user.id:
        raise HTTPException(status_code=400, detail="email already registered")

    if not payload.current_email_code:
        raise HTTPException(status_code=400, detail="missing current email code")
    try:
        await verify_email_code(
            session,
            email=str(current_user.email),
            purpose="email_change_current",
            code=str(payload.current_email_code),
        )
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e)) from e

    try:
        await verify_email_code(
            session,
            email=new_email,
            purpose="email_change_new",
            code=str(payload.new_email_code),
        )
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e)) from e

    current_user.email = new_email
    await session.commit()
    await session.refresh(current_user)

    token = _get_request_session_token(request)
    if token:
        await revoke_other_sessions(session, user_id=current_user.id, current_token=token)

    return EmailChangeConfirmResponse(ok=True, email=current_user.email)


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
    payload: GoogleOAuthExchangeRequest, request: Request, session: AsyncSession = Depends(get_db_session)
) -> AuthResponse:
    from app.core.config import settings

    if payload.redirect_uri != settings.google_redirect_uri:
        raise HTTPException(status_code=400, detail="invalid redirect_uri")
    try:
        from app.constants import DEVICE_ID_COOKIE_NAME

        signup_ip = _extract_source_ip(request)
        signup_device_id = request.cookies.get(DEVICE_ID_COOKIE_NAME)
        signup_user_agent = request.headers.get("user-agent")
        return await login_with_google(
            session,
            code=payload.code,
            code_verifier=payload.code_verifier,
            redirect_uri=payload.redirect_uri,
            invite_code=payload.invite_code,
            signup_ip=signup_ip,
            signup_device_id=signup_device_id,
            signup_user_agent=signup_user_agent,
        )
    except ValueError as e:
        if str(e) == "banned":
            raise HTTPException(status_code=403, detail="banned") from e
        if str(e) == "registration disabled":
            raise HTTPException(status_code=403, detail="registration disabled") from e
        raise HTTPException(status_code=400, detail=str(e)) from e


@router.post("/auth/oauth/google/link", response_model=AuthMethodsResponse)
async def oauth_google_link(
    payload: GoogleOAuthExchangeRequest, session: AsyncSession = Depends(get_db_session), current_user=Depends(get_current_user)
) -> AuthMethodsResponse:
    from app.core.config import settings

    if payload.redirect_uri != settings.google_redirect_uri:
        raise HTTPException(status_code=400, detail="invalid redirect_uri")

    try:
        await link_google_identity(
            session,
            user=current_user,
            code=payload.code,
            code_verifier=payload.code_verifier,
            redirect_uri=payload.redirect_uri,
        )
    except ValueError as e:
        raise HTTPException(status_code=400, detail=str(e)) from e

    return await _build_auth_methods_response(session, current_user=current_user)


@router.delete("/auth/oauth/{oauth_id}", response_model=AuthMethodsResponse)
async def oauth_unlink(
    oauth_id: str,
    session: AsyncSession = Depends(get_db_session),
    current_user=Depends(get_current_user),
) -> AuthMethodsResponse:
    try:
        parsed = uuid.UUID(oauth_id)
    except ValueError:
        raise HTTPException(status_code=400, detail="invalid oauth id") from None

    identity = await session.get(OAuthIdentity, parsed)
    if not identity or identity.user_id != current_user.id:
        raise HTTPException(status_code=404, detail="not found")

    identities = await _auth_methods(session, user_id=current_user.id)
    if identity.provider == "google":
        provider_identities = [row for row in identities if row.provider == "google"]
        remaining = len(identities) - len(provider_identities)
        if current_user.password_set_at is None and remaining <= 0:
            raise HTTPException(status_code=400, detail="cannot remove last sign-in method")
        for row in provider_identities:
            await session.delete(row)
    else:
        remaining = len(identities) - 1
        if current_user.password_set_at is None and remaining <= 0:
            raise HTTPException(status_code=400, detail="cannot remove last sign-in method")
        await session.delete(identity)

    await session.commit()
    return await _build_auth_methods_response(session, current_user=current_user)


def _extract_bearer_token(value: str | None) -> str | None:
    if not value:
        return None
    prefix = "bearer "
    if value.lower().startswith(prefix):
        token = value[len(prefix) :].strip()
        return token or None
    return None


async def _require_default_membership(session: AsyncSession, *, user_id: uuid.UUID) -> Membership:
    org = await ensure_default_org(session)
    return await ensure_membership(session, org_id=org.id, user_id=user_id, role="developer")


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
            raise HTTPException(status_code=_auth_error_status(detail), detail=detail) from e
    else:
        token = bearer or request.cookies.get(SESSION_COOKIE_NAME)
        if not token:
            raise HTTPException(status_code=401, detail="unauthorized")
        user = await get_user_by_token(session, token)
        if not user:
            raise HTTPException(status_code=401, detail="unauthorized")
        if user.banned_at is not None:
            raise HTTPException(status_code=403, detail="banned")

    membership = await _require_default_membership(session, user_id=user.id)
    return user, membership


@router.get("/console/models", response_model=ModelsListResponse)
async def console_list_models(request: Request, session: AsyncSession = Depends(get_db_session)) -> ModelsListResponse:
    user, membership = await _require_user_for_models(request, session)
    items = await list_user_models(
        session, org_id=membership.org_id, group_name=user.group_name, include_availability=True
    )
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


@router.get("/models/{model:path}", response_model=OpenAIModelItem)
async def retrieve_model(
    model: str, request: Request, session: AsyncSession = Depends(get_db_session)
) -> OpenAIModelItem:
    user, membership = await _require_user_for_models(request, session)

    model_id = model.strip()
    if model_id == "" or "\n" in model_id or "\r" in model_id or len(model_id) > 200:
        raise HTTPException(status_code=404, detail="not_found")

    items = await list_user_models(session, org_id=membership.org_id, group_name=user.group_name)

    def _model_id(value: object) -> str:
        if isinstance(value, dict):
            raw = value.get("model")
            return str(raw) if raw is not None else ""
        return str(getattr(value, "model", "") or "")

    allowed = {_model_id(item) for item in items}
    if model_id not in allowed:
        raise HTTPException(status_code=404, detail="not_found")

    return OpenAIModelItem(id=model_id)


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
    limit: int = 50,
    offset: int = 0,
    email: str | None = None,
    session: AsyncSession = Depends(get_db_session),
    admin_user=Depends(require_admin),
    membership=Depends(get_current_membership),
) -> AdminUsersListResponse:
    _ = admin_user
    return await list_admin_users(
        session,
        org_id=membership.org_id,
        limit=int(limit),
        offset=int(offset),
        email=email,
    )


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
    if parsed == admin_user.id and payload.soft_limited is True:
        raise HTTPException(status_code=400, detail="cannot limit self")

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
    raw = await request.body()
    parsed = _parse_llm_request(raw)
    payload = parsed.payload
    context = await _resolve_llm_proxy_context(request, session, model_id=parsed.model_id)
    stream = bool(payload.get("stream"))
    _log_llm_request_received(request, context=context, stream=stream)

    upstream_url = f"{context.upstream_base_url}/chat/completions"
    headers: dict[str, str] = {
        "authorization": f"Bearer {context.upstream_api_key}",
        "content-type": "application/json",
    }
    # Forward optional OpenAI compatibility headers if present.
    for name in ("openai-organization", "openai-project", "anthropic-version"):
        value = request.headers.get(name)
        if value:
            headers[name] = value

    timeout = _llm_upstream_timeout()

    started = time.perf_counter()
    ttft_ms = 0
    total_ms = 0

    if stream:
        # Important: do NOT use `async with client.stream(...)` here, otherwise the upstream
        # stream is closed immediately when the request handler returns. Keep the stream open
        # and close it inside the generator's `finally`.
        client = httpx.AsyncClient(timeout=timeout)
        try:
            req_up = client.build_request("POST", upstream_url, headers=headers, content=raw)
            res = await client.send(req_up, stream=True)
        except httpx.HTTPError as exc:
            try:
                await client.aclose()
            except Exception:
                pass
            raise _translate_upstream_http_error(exc) from exc
        content_type = res.headers.get("content-type") or "application/json"
        ok = res.status_code < 400
        input_tokens = 0
        cached_tokens = 0
        output_tokens = 0
        total_tokens = 0

        async def iterator():
            nonlocal ttft_ms, total_ms, input_tokens, cached_tokens, output_tokens, total_tokens
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
                                cached = 0
                                details = usage.get("prompt_tokens_details")
                                if isinstance(details, dict):
                                    cached_raw = details.get("cached_tokens")
                                    if cached_raw is not None:
                                        cached = int(cached_raw)
                                total_raw = usage.get("total_tokens")
                                total = int(total_raw) if total_raw is not None else 0
                                output = max(total - prompt, 0) if total > 0 else completion
                                input_tokens = prompt
                                cached_tokens = min(max(cached, 0), max(prompt, 0))
                                output_tokens = output
                                total_tokens = total if total > 0 else (input_tokens + output_tokens)
                            except Exception:
                                continue
                    yield chunk
            except (
                httpx.ReadError,
                httpx.ReadTimeout,
                httpx.ConnectError,
                httpx.RemoteProtocolError,
                httpx.LocalProtocolError,
                httpx.StreamError,
            ):
                # Treat upstream disconnects/cancellation as a normal stream termination.
                pass
            finally:
                total_ms_local = int((time.perf_counter() - started) * 1000)

                async def finalize() -> None:
                    try:
                        await res.aclose()
                    except Exception:
                        pass
                    try:
                        await client.aclose()
                    except Exception:
                        pass

                    cost_micros = estimate_cost_usd_micros(
                        pricing=context.pricing,
                        input_tokens=input_tokens,
                        cached_tokens=cached_tokens,
                        output_tokens=output_tokens,
                    )
                    await _record_usage_event_best_effort(
                        org_id=context.org_id,
                        user_id=context.user_id,
                        api_key_id=context.api_key_id,
                        model_id=context.model_id,
                        ok=ok,
                        status_code=int(res.status_code),
                        input_tokens=input_tokens,
                        cached_tokens=cached_tokens,
                        output_tokens=output_tokens,
                        total_tokens=total_tokens,
                        cost_usd_micros=cost_micros,
                        total_duration_ms=total_ms_local,
                        ttft_ms=ttft_ms,
                        source_ip=context.source_ip,
                        recompute_cost=False,
                    )

                asyncio.create_task(finalize())

        return StreamingResponse(
            iterator(),
            status_code=int(res.status_code),
            media_type=content_type,
            headers={"cache-control": "no-cache", "x-accel-buffering": "no"},
        )

    # Non-stream response: read full body then close the upstream response/client.
    try:
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
    except httpx.HTTPError as exc:
        raise _translate_upstream_http_error(exc) from exc

    # Record usage/spend for dashboard and logs.
    input_tokens = 0
    cached_tokens = 0
    output_tokens = 0
    total_tokens = 0
    if ok and content_type.startswith("application/json"):
        try:
            body = json.loads(body_bytes.decode("utf-8"))
            usage = body.get("usage") if isinstance(body, dict) else None
            if isinstance(usage, dict):
                input_tokens = int(usage.get("prompt_tokens") or 0)
                completion = int(usage.get("completion_tokens") or 0)
                details = usage.get("prompt_tokens_details")
                if isinstance(details, dict):
                    cached_raw = details.get("cached_tokens")
                    if cached_raw is not None:
                        cached_tokens = min(max(int(cached_raw), 0), max(input_tokens, 0))
                total_raw = usage.get("total_tokens")
                total_tokens = int(total_raw) if total_raw is not None else 0
                output_tokens = max(total_tokens - input_tokens, 0) if total_tokens > 0 else completion
                if total_tokens <= 0:
                    total_tokens = input_tokens + output_tokens
        except Exception:
            pass

    cost_micros = estimate_cost_usd_micros(
        pricing=context.pricing,
        input_tokens=input_tokens,
        cached_tokens=cached_tokens,
        output_tokens=output_tokens,
    )

    await _record_usage_event_best_effort(
        org_id=context.org_id,
        user_id=context.user_id,
        api_key_id=context.api_key_id,
        model_id=context.model_id,
        ok=ok,
        status_code=int(res.status_code),
        input_tokens=input_tokens,
        cached_tokens=cached_tokens,
        output_tokens=output_tokens,
        total_tokens=total_tokens,
        cost_usd_micros=cost_micros,
        total_duration_ms=total_ms,
        ttft_ms=ttft_ms,
        source_ip=context.source_ip,
        recompute_cost=False,
    )

    return Response(content=bytes(body_bytes), status_code=int(res.status_code), media_type=content_type)


async def _proxy_responses_request(
    request: Request,
    session: AsyncSession,
    *,
    upstream_path: str,
    allow_multipart: bool = False,
):
    raw = await request.body()
    parsed = _parse_proxy_request(
        raw,
        content_type=request.headers.get("content-type") or "",
        allow_multipart=allow_multipart,
    )
    context = await _resolve_llm_proxy_context(request, session, model_id=parsed.model_id)
    _log_llm_request_received(request, context=context, stream=parsed.stream)

    upstream_url = f"{context.upstream_base_url}{upstream_path}"
    headers = _build_upstream_headers(request, upstream_api_key=context.upstream_api_key)

    timeout = _llm_upstream_timeout()

    started = time.perf_counter()
    ttft_ms = 0
    total_ms = 0

    if parsed.stream:
        client = httpx.AsyncClient(timeout=timeout)
        try:
            req_up = client.build_request("POST", upstream_url, headers=headers, content=raw)
            res = await client.send(req_up, stream=True)
        except httpx.HTTPError as exc:
            try:
                await client.aclose()
            except Exception:
                pass
            raise _translate_upstream_http_error(exc) from exc
        content_type = res.headers.get("content-type") or "application/json"
        ok = res.status_code < 400
        input_tokens = 0
        cached_tokens = 0
        output_tokens = 0
        total_tokens = 0
        upstream_headers = _filter_upstream_response_headers(dict(res.headers))
        upstream_headers.setdefault("cache-control", "no-cache")
        upstream_headers.setdefault("x-accel-buffering", "no")

        async def iterator():
            nonlocal ttft_ms, total_ms, input_tokens, cached_tokens, output_tokens, total_tokens
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
                            parsed = _extract_usage_tokens(obj)
                            if not parsed:
                                continue
                            input_tokens, cached_tokens, output_tokens, total_tokens = parsed
                    yield chunk
            except (
                httpx.ReadError,
                httpx.ReadTimeout,
                httpx.ConnectError,
                httpx.RemoteProtocolError,
                httpx.LocalProtocolError,
                httpx.StreamError,
            ):
                pass
            finally:
                total_ms_local = int((time.perf_counter() - started) * 1000)

                async def finalize() -> None:
                    try:
                        await res.aclose()
                    except Exception:
                        pass
                    try:
                        await client.aclose()
                    except Exception:
                        pass

                    cost_micros = estimate_cost_usd_micros(
                        pricing=context.pricing,
                        input_tokens=input_tokens,
                        cached_tokens=cached_tokens,
                        output_tokens=output_tokens,
                    )
                    await _record_usage_event_best_effort(
                        org_id=context.org_id,
                        user_id=context.user_id,
                        api_key_id=context.api_key_id,
                        model_id=context.model_id,
                        ok=ok,
                        status_code=int(res.status_code),
                        input_tokens=input_tokens,
                        cached_tokens=cached_tokens,
                        output_tokens=output_tokens,
                        total_tokens=total_tokens,
                        cost_usd_micros=cost_micros,
                        total_duration_ms=total_ms_local,
                        ttft_ms=ttft_ms,
                        source_ip=context.source_ip,
                        recompute_cost=False,
                    )

                asyncio.create_task(finalize())

        return StreamingResponse(
            iterator(),
            status_code=int(res.status_code),
            media_type=content_type,
            headers=upstream_headers,
        )

    try:
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
                upstream_headers = _filter_upstream_response_headers(dict(res.headers))
    except httpx.HTTPError as exc:
        raise _translate_upstream_http_error(exc) from exc

    input_tokens = 0
    cached_tokens = 0
    output_tokens = 0
    total_tokens = 0
    if ok and content_type.startswith("application/json"):
        try:
            body = json.loads(body_bytes.decode("utf-8"))
            if isinstance(body, dict):
                parsed = _extract_usage_tokens(body)
                if parsed:
                    input_tokens, cached_tokens, output_tokens, total_tokens = parsed
        except Exception:
            pass

    cost_micros = estimate_cost_usd_micros(
        pricing=context.pricing,
        input_tokens=input_tokens,
        cached_tokens=cached_tokens,
        output_tokens=output_tokens,
    )

    await _record_usage_event_best_effort(
        org_id=context.org_id,
        user_id=context.user_id,
        api_key_id=context.api_key_id,
        model_id=context.model_id,
        ok=ok,
        status_code=int(res.status_code),
        input_tokens=input_tokens,
        cached_tokens=cached_tokens,
        output_tokens=output_tokens,
        total_tokens=total_tokens,
        cost_usd_micros=cost_micros,
        total_duration_ms=total_ms,
        ttft_ms=ttft_ms,
        source_ip=context.source_ip,
        recompute_cost=False,
    )

    upstream_headers.setdefault("cache-control", "no-cache")
    return Response(
        content=bytes(body_bytes),
        status_code=int(res.status_code),
        media_type=content_type,
        headers=upstream_headers,
    )


@router.post("/responses")
async def responses(request: Request, session: AsyncSession = Depends(get_db_session)):
    return await _proxy_responses_request(request, session, upstream_path="/responses")


@router.post("/responses/compact")
async def responses_compact(request: Request, session: AsyncSession = Depends(get_db_session)):
    return await _proxy_responses_request(request, session, upstream_path="/responses/compact")


@router.post("/images/generations")
async def image_generations(request: Request, session: AsyncSession = Depends(get_db_session)):
    return await _proxy_responses_request(request, session, upstream_path="/images/generations")


@router.post("/images/edits")
async def image_edits(request: Request, session: AsyncSession = Depends(get_db_session)):
    return await _proxy_responses_request(
        request,
        session,
        upstream_path="/images/edits",
        allow_multipart=True,
    )


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
    tz: str = "UTC",
    session: AsyncSession = Depends(get_db_session),
    current_user=Depends(get_current_user),
    membership=Depends(get_current_membership),
) -> dict:
    from app.storage.usage_db import get_usage_response

    return await get_usage_response(session, org_id=membership.org_id, user_id=current_user.id, tz=tz)


@router.post("/billing/topup/checkout", response_model=BillingTopupCheckoutResponse)
async def billing_topup_checkout(
    payload: BillingTopupCheckoutRequest,
    request: Request,
    session: AsyncSession = Depends(get_db_session),
    current_user=Depends(get_current_user),
    membership=Depends(get_current_membership),
) -> dict:
    org = await session.get(Organization, membership.org_id)
    if not org:
        raise HTTPException(status_code=404, detail="not found")
    if not bool(getattr(org, "billing_topup_enabled", True)):
        raise HTTPException(status_code=403, detail="billing topup disabled")

    units = int(payload.amount_usd)
    if units < 5 or units > 5000:
        raise HTTPException(status_code=400, detail="invalid amount")
    payment_method = str(payload.payment_method).strip().lower()
    if payment_method not in {"card", "alipay", "wxpay"}:
        raise HTTPException(status_code=400, detail="invalid payment method")

    request_id = generate_topup_request_id()
    from app.constants import DEVICE_ID_COOKIE_NAME

    client_ip = _get_request_client_ip(request)
    client_device_id = request.cookies.get(DEVICE_ID_COOKIE_NAME)
    public_url = _normalize_public_url(settings.app_public_url)
    product_name = f"{(settings.app_name or 'Uni API').strip()[:48]} API Credits"

    if payment_method == "card":
        if not _creem_is_configured():
            raise HTTPException(status_code=503, detail="billing not configured")

        await create_billing_topup(
            session,
            org_id=membership.org_id,
            user_id=current_user.id,
            request_id=request_id,
            units=units,
            provider="creem",
            client_ip=client_ip,
            client_device_id=client_device_id,
        )

        success_url = f"{public_url}/billing?request_id={request_id}"
        body = {
            "product_id": str(settings.creem_product_id).strip(),
            "units": units,
            "request_id": request_id,
            "success_url": success_url,
            "customer": {"email": str(current_user.email)},
            "metadata": {
                "purpose": "top_up",
                "userId": str(current_user.id),
                "orgId": str(membership.org_id),
                "units": units,
            },
        }

        try:
            async with httpx.AsyncClient(timeout=httpx.Timeout(20.0, connect=10.0)) as client:
                upstream = await client.post(
                    f"{_creem_base_url()}/v1/checkouts",
                    headers={"x-api-key": str(settings.creem_api_key).strip(), "content-type": "application/json"},
                    json=body,
                )
        except Exception:
            logger.exception("creem: checkout create failed")
            await mark_billing_topup_failed(
                session,
                request_id=request_id,
                provider="creem",
                checkout_id=None,
                order_id=None,
                currency=None,
                amount_total_cents=None,
            )
            raise HTTPException(status_code=502, detail="payment provider error") from None

        if upstream.status_code >= 400:
            logger.warning("creem: checkout create error status=%s body=%s", upstream.status_code, upstream.text[:500])
            await mark_billing_topup_failed(
                session,
                request_id=request_id,
                provider="creem",
                checkout_id=None,
                order_id=None,
                currency=None,
                amount_total_cents=None,
            )
            raise HTTPException(status_code=502, detail="payment provider error")

        checkout_url = None
        try:
            j = upstream.json()
            if isinstance(j, dict):
                raw = j.get("checkout_url")
                if isinstance(raw, str) and raw.strip():
                    checkout_url = raw.strip()
        except Exception:
            checkout_url = None

        if not checkout_url:
            await mark_billing_topup_failed(
                session,
                request_id=request_id,
                provider="creem",
                checkout_id=None,
                order_id=None,
                currency=None,
                amount_total_cents=None,
            )
            raise HTTPException(status_code=502, detail="payment provider error")

        return {"checkoutUrl": checkout_url, "requestId": request_id}

    if not zhupay_is_configured():
        raise HTTPException(status_code=503, detail="billing not configured")

    try:
        payable_cny, payable_cents = convert_credits_to_money(credits=units)
    except ZhupayError as exc:
        raise _translate_zhupay_error(exc) from None

    await create_billing_topup(
        session,
        org_id=membership.org_id,
        user_id=current_user.id,
        request_id=request_id,
        units=units,
        provider="zhupay",
        client_ip=client_ip,
        client_device_id=client_device_id,
    )

    notify_url = f"{public_url}/api/webhook/zhupay"
    return_url = f"{public_url}/billing"
    try:
        created = await create_jump_order(
            payment_method=payment_method,
            out_trade_no=request_id,
            notify_url=notify_url,
            return_url=return_url,
            name=product_name,
            money=payable_cny,
            client_ip=client_ip,
            param=request_id,
        )
    except ZhupayError as exc:
        logger.exception("zhupay: checkout create failed")
        await mark_billing_topup_failed(
            session,
            request_id=request_id,
            provider="zhupay",
            checkout_id=None,
            order_id=None,
            currency="CNY",
            amount_total_cents=payable_cents,
        )
        raise _translate_zhupay_error(exc) from None

    await set_billing_topup_status(
        session,
        request_id=request_id,
        status="pending",
        provider="zhupay",
        checkout_id=created.trade_no,
        order_id=None,
        currency="CNY",
        amount_total_cents=payable_cents,
    )

    return {"checkoutUrl": created.pay_info, "requestId": request_id}


@router.get("/billing/settings", response_model=BillingSettingsResponse)
async def billing_settings(
    session: AsyncSession = Depends(get_db_session),
    current_user=Depends(get_current_user),
    membership=Depends(get_current_membership),
) -> dict:
    _ = current_user
    org = await session.get(Organization, membership.org_id)
    if not org:
        raise HTTPException(status_code=404, detail="not found")
    return {"billingTopupEnabled": bool(getattr(org, "billing_topup_enabled", True))}


@router.get("/billing/topup/status", response_model=BillingTopupStatusResponse)
async def billing_topup_status(
    request_id: str | None = None,
    request_id_alt: str | None = Query(default=None, alias="requestId"),
    session: AsyncSession = Depends(get_db_session),
    current_user=Depends(get_current_user),
    membership=Depends(get_current_membership),
) -> dict:
    raw_request_id = request_id_alt or request_id
    if not isinstance(raw_request_id, str) or raw_request_id.strip() == "":
        raise HTTPException(status_code=400, detail="missing request id")

    topup = await get_billing_topup_for_user(
        session,
        org_id=membership.org_id,
        user_id=current_user.id,
        request_id=raw_request_id.strip(),
    )
    if not topup:
        raise HTTPException(status_code=404, detail="not found")

    topup = await _sync_pending_topup_from_zhupay(session, topup=topup)

    status = str(topup.status or "pending")
    units = int(getattr(topup, "units", 0) or 0)
    out: dict[str, object] = {"requestId": str(topup.request_id), "status": status, "units": units}
    if status == "completed":
        from app.storage.balance_math import remaining_usd_2

        try:
            await session.refresh(current_user)
        except Exception:
            pass
        credits_cents = int(getattr(current_user, "balance", 0) or 0)
        spend_micros_total = int(getattr(current_user, "spend_usd_micros_total", 0) or 0)
        out["newBalance"] = remaining_usd_2(credits_usd_cents=credits_cents, spend_usd_micros_total=spend_micros_total)
    return out


@router.get("/webhook/zhupay")
async def zhupay_webhook(
    request: Request,
    session: AsyncSession = Depends(get_db_session),
) -> Response:
    payload = {key: value for key, value in request.query_params.items()}
    if not payload:
        raise HTTPException(status_code=400, detail="missing payload")
    if not zhupay_verify_payload(payload):
        raise HTTPException(status_code=401, detail="invalid signature")

    expected_pid = (settings.zhupay_pid or "").strip()
    pid = str(payload.get("pid") or "").strip()
    if expected_pid == "" or pid != expected_pid:
        raise HTTPException(status_code=401, detail="invalid pid")

    if str(payload.get("trade_status") or "").strip() != "TRADE_SUCCESS":
        raise HTTPException(status_code=400, detail="invalid trade status")

    request_id = str(payload.get("out_trade_no") or "").strip()
    if request_id == "":
        raise HTTPException(status_code=400, detail="missing request id")

    topup = await get_billing_topup_by_request_id(session, request_id=request_id)
    if not topup:
        raise HTTPException(status_code=404, detail="not found")

    trade_no = str(payload.get("trade_no") or "").strip() or None
    api_trade_no = str(payload.get("api_trade_no") or "").strip() or None
    money_cents = zhupay_money_to_cents(str(payload.get("money") or "").strip() or None)
    expected_cents = int(getattr(topup, "amount_total_cents", 0) or 0) or None
    if expected_cents is not None and money_cents is not None and money_cents != expected_cents:
        await mark_billing_topup_failed(
            session,
            request_id=request_id,
            provider="zhupay",
            checkout_id=trade_no,
            order_id=api_trade_no,
            currency="CNY",
            amount_total_cents=money_cents,
        )
        raise HTTPException(status_code=400, detail="amount mismatch")

    completed = await complete_billing_topup(
        session,
        request_id=request_id,
        provider="zhupay",
        checkout_id=trade_no,
        order_id=api_trade_no,
        currency="CNY",
        amount_total_cents=money_cents,
        payer_email=None,
    )
    if not completed:
        raise HTTPException(status_code=404, detail="not found")

    return Response(content="success", media_type="text/plain")


@router.post("/webhook/creem")
async def creem_webhook(
    request: Request,
    session: AsyncSession = Depends(get_db_session),
) -> dict:
    signature = request.headers.get("creem-signature") or ""
    raw = await request.body()
    if signature.strip() == "":
        raise HTTPException(status_code=401, detail="missing signature")
    if not _verify_creem_signature(raw_body=raw, signature=signature.strip()):
        raise HTTPException(status_code=401, detail="invalid signature")

    try:
        payload = json.loads(raw.decode("utf-8"))
    except Exception:
        raise HTTPException(status_code=400, detail="invalid json") from None
    if not isinstance(payload, dict):
        raise HTTPException(status_code=400, detail="invalid json")

    event_id = payload.get("id")
    event_type = payload.get("eventType")
    if not isinstance(event_id, str) or not event_id.strip():
        raise HTTPException(status_code=400, detail="missing event id")
    if not isinstance(event_type, str) or not event_type.strip():
        raise HTTPException(status_code=400, detail="missing event type")

    creem_event_id = event_id.strip()
    if await creem_event_exists(session, creem_event_id=creem_event_id):
        return {"ok": True}

    obj = payload.get("object")
    if not isinstance(obj, dict):
        await record_creem_event(
            session,
            creem_event_id=creem_event_id,
            event_type=event_type,
            status="failed",
            raw_payload=payload,
        )
        return {"ok": True}

    request_id = obj.get("request_id")
    checkout_id = obj.get("id") if isinstance(obj.get("id"), str) else None
    order_id = None
    order = obj.get("order")
    if isinstance(order, dict) and isinstance(order.get("id"), str):
        order_id = order.get("id")

    currency = _extract_creem_currency(obj)
    product_id = _extract_creem_product_id(obj)
    amount_total_cents = _extract_creem_amount_total_cents(obj)
    payer_email = _extract_creem_payer_email(obj)
    metadata = obj.get("metadata")

    purpose = None
    if isinstance(metadata, dict):
        raw_purpose = metadata.get("purpose")
        if isinstance(raw_purpose, str) and raw_purpose.strip():
            purpose = raw_purpose.strip()

    org_id = None
    user_id = None
    if isinstance(metadata, dict):
        raw_org = metadata.get("orgId")
        raw_user = metadata.get("userId")
        try:
            if isinstance(raw_org, str) and raw_org.strip():
                org_id = uuid.UUID(raw_org.strip())
        except Exception:
            org_id = None
        try:
            if isinstance(raw_user, str) and raw_user.strip():
                user_id = uuid.UUID(raw_user.strip())
        except Exception:
            user_id = None

    normalized_event_type = event_type.strip().lower()
    if normalized_event_type != "checkout.completed":
        if purpose == "top_up" and any(k in normalized_event_type for k in ("refund", "chargeback", "dispute")):
            topup = None
            if isinstance(request_id, str) and request_id.strip():
                topup = await get_billing_topup_by_request_id(session, request_id=request_id.strip())
            if not topup and isinstance(order_id, str) and order_id.strip():
                topup = await get_billing_topup_by_order_id(session, order_id=order_id.strip())

            checkout_id_alt = None
            raw_checkout = obj.get("checkout_id") or obj.get("checkoutId") or obj.get("checkout")
            if isinstance(raw_checkout, str) and raw_checkout.strip():
                checkout_id_alt = raw_checkout.strip()
            elif isinstance(raw_checkout, dict) and isinstance(raw_checkout.get("id"), str):
                checkout_id_alt = raw_checkout.get("id")
            if not topup and checkout_id_alt:
                topup = await get_billing_topup_by_checkout_id(session, checkout_id=checkout_id_alt.strip())

            if topup:
                try:
                    await process_referral_refund(session, topup=topup)
                except Exception:
                    logger.exception("referral: refund processing failed")

        await record_creem_event(
            session,
            creem_event_id=creem_event_id,
            event_type=event_type,
            status="processed",
            raw_payload=payload,
            org_id=org_id,
            user_id=user_id,
            amount_units=0,
            amount_total_cents=amount_total_cents,
            currency=currency,
        )
        return {"ok": True}

    if not isinstance(request_id, str) or not request_id.strip():
        await record_creem_event(
            session,
            creem_event_id=creem_event_id,
            event_type=event_type,
            status="failed",
            raw_payload=payload,
            org_id=org_id,
            user_id=user_id,
            amount_units=0,
            amount_total_cents=amount_total_cents,
            currency=currency,
        )
        return {"ok": True}

    if purpose != "top_up":
        await record_creem_event(
            session,
            creem_event_id=creem_event_id,
            event_type=event_type,
            status="processed",
            raw_payload=payload,
            org_id=org_id,
            user_id=user_id,
            amount_units=0,
            amount_total_cents=amount_total_cents,
            currency=currency,
        )
        return {"ok": True}

    expected_product_id = (settings.creem_product_id or "").strip()
    if expected_product_id == "" or not product_id or product_id != expected_product_id:
        await record_creem_event(
            session,
            creem_event_id=creem_event_id,
            event_type=event_type,
            status="failed",
            raw_payload=payload,
            org_id=org_id,
            user_id=user_id,
            amount_units=0,
            amount_total_cents=amount_total_cents,
            currency=currency,
        )
        return {"ok": True}

    if currency != "USD":
        await mark_billing_topup_failed(
            session,
            request_id=request_id.strip(),
            provider="creem",
            checkout_id=checkout_id,
            order_id=order_id,
            currency=currency,
            amount_total_cents=amount_total_cents,
        )
        await record_creem_event(
            session,
            creem_event_id=creem_event_id,
            event_type=event_type,
            status="failed",
            raw_payload=payload,
            org_id=org_id,
            user_id=user_id,
            amount_units=0,
            amount_total_cents=amount_total_cents,
            currency=currency,
        )
        return {"ok": True}

    topup = await complete_billing_topup(
        session,
        request_id=request_id.strip(),
        provider="creem",
        checkout_id=checkout_id,
        order_id=order_id,
        currency=currency,
        amount_total_cents=amount_total_cents,
        payer_email=payer_email,
    )
    if not topup:
        await record_creem_event(
            session,
            creem_event_id=creem_event_id,
            event_type=event_type,
            status="failed",
            raw_payload=payload,
            org_id=org_id,
            user_id=user_id,
            amount_units=0,
            amount_total_cents=amount_total_cents,
            currency=currency,
        )
        return {"ok": True}

    await record_creem_event(
        session,
        creem_event_id=creem_event_id,
        event_type=event_type,
        status="processed",
        raw_payload=payload,
        org_id=topup.org_id,
        user_id=topup.user_id,
        amount_units=int(topup.units),
        amount_total_cents=amount_total_cents,
        currency=currency,
    )
    return {"ok": True}


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


@router.post("/invite/visit")
async def invite_visit(
    payload: InviteVisitRequest,
    request: Request,
    session: AsyncSession = Depends(get_db_session),
) -> dict:
    raw_code = str(getattr(payload, "invite_code", None) or "").strip()
    if raw_code == "":
        return {"ok": True}

    from app.constants import DEVICE_ID_COOKIE_NAME
    from app.storage.invite_visits_db import record_invite_visit

    client_ip = _extract_source_ip(request)
    device_id = request.cookies.get(DEVICE_ID_COOKIE_NAME)
    user_agent = request.headers.get("user-agent")

    try:
        await record_invite_visit(
            session,
            invite_code=raw_code,
            visitor_device_id=device_id,
            visitor_ip=client_ip,
            visitor_user_agent=user_agent,
        )
    except Exception:
        logger.exception("invite: visit record failed")

    return {"ok": True}


@router.get("/invite/summary", response_model=InviteSummaryResponse)
async def invite_summary(
    session: AsyncSession = Depends(get_db_session),
    current_user=Depends(get_current_user),
    membership=Depends(get_current_membership),
) -> dict:
    _ = membership
    from decimal import Decimal

    from app.models.invite_visit import InviteVisit
    from app.models.referral_bonus_event import ReferralBonusEvent
    from app.storage.invites_db import ensure_user_invite_code
    from app.storage.referrals_db import referral_bonus_event_to_received_reward

    invite_code = (await ensure_user_invite_code(session, current_user)).upper()

    invited_total = (
        await session.execute(select(func.count()).select_from(User).where(User.invited_by_user_id == current_user.id))
    ).scalar_one()

    visits_total = (
        await session.execute(
            select(func.count()).select_from(InviteVisit).where(InviteVisit.inviter_user_id == current_user.id)
        )
    ).scalar_one()

    rewards_pending = (
        await session.execute(
            select(func.count())
            .select_from(ReferralBonusEvent)
            .where(ReferralBonusEvent.inviter_user_id == current_user.id, ReferralBonusEvent.status == "pending")
        )
    ).scalar_one()

    rewards_confirmed = (
        await session.execute(
            select(func.count())
            .select_from(ReferralBonusEvent)
            .where(ReferralBonusEvent.inviter_user_id == current_user.id, ReferralBonusEvent.status == "confirmed")
        )
    ).scalar_one()

    invitees = (
        await session.execute(
            select(User)
            .where(User.invited_by_user_id == current_user.id)
            .order_by(User.created_at.desc())
            .limit(200)
        )
    ).scalars().all()

    invitee_ids = [u.id for u in invitees]
    latest_by_invitee: dict[uuid.UUID, ReferralBonusEvent] = {}
    if invitee_ids:
        events = (
            await session.execute(
                select(ReferralBonusEvent)
                .where(
                    ReferralBonusEvent.inviter_user_id == current_user.id,
                    ReferralBonusEvent.invitee_user_id.in_(invitee_ids),
                )
                .order_by(ReferralBonusEvent.created_at.desc())
            )
        ).scalars().all()
        for ev in events:
            if ev.invitee_user_id not in latest_by_invitee:
                latest_by_invitee[ev.invitee_user_id] = ev

    received_reward_event = (
        (
            await session.execute(
                select(ReferralBonusEvent)
                .where(ReferralBonusEvent.invitee_user_id == current_user.id)
                .order_by(ReferralBonusEvent.created_at.desc())
                .limit(1)
            )
        )
        .scalars()
        .first()
    )
    received_reward = (
        referral_bonus_event_to_received_reward(received_reward_event)
        if received_reward_event is not None
        else None
    )

    def _dt_iso(value: dt.datetime | None) -> str:
        if not value:
            return dt.datetime.now(dt.timezone.utc).isoformat()
        return value.astimezone(dt.timezone.utc).isoformat()

    items: list[dict[str, object]] = []
    for u in invitees:
        ev = latest_by_invitee.get(u.id)
        status = str(getattr(ev, "status", "") or "none") if ev else "none"
        reward_usd = None
        if ev and status in {"pending", "confirmed"}:
            reward_usd = float((Decimal(int(ev.bonus_usd_cents)) / Decimal("100")).quantize(Decimal("0.01")))
        invited_at = getattr(u, "invited_at", None) or getattr(u, "created_at", None)
        items.append(
            {
                "id": str(u.id),
                "email": str(u.email),
                "invitedAt": _dt_iso(invited_at),
                "rewardStatus": status,
                "rewardUsd": reward_usd,
            }
        )

    return {
        "inviteCode": invite_code,
        "invitedTotal": int(invited_total or 0),
        "visitsTotal": int(visits_total or 0),
        "rewardsPending": int(rewards_pending or 0),
        "rewardsConfirmed": int(rewards_confirmed or 0),
        "receivedReward": received_reward,
        "items": items,
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
