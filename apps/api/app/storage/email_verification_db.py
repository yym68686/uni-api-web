from __future__ import annotations

import datetime as dt
import secrets
from typing import Final

import httpx
from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.core.config import settings
from app.models.email_verification_code import EmailVerificationCode
from app.security import sha256_hex


PURPOSE_REGISTER: Final[str] = "register"


def _normalize_email(value: str) -> str:
    return value.strip().lower()


def _normalize_purpose(value: str) -> str:
    p = value.strip().lower() or PURPOSE_REGISTER
    if p not in {PURPOSE_REGISTER}:
        raise ValueError("invalid purpose")
    return p


def _normalize_code(value: str) -> str:
    raw = value.strip()
    if len(raw) != 6 or not raw.isdigit():
        raise ValueError("invalid code")
    return raw


def _extract_client_ip(x_forwarded_for: str | None) -> str | None:
    if not x_forwarded_for:
        return None
    first = x_forwarded_for.split(",")[0].strip()
    return first[:64] if first else None


def _generate_code() -> str:
    # 6-digit numeric code.
    return f"{secrets.randbelow(1_000_000):06d}"


async def _send_resend_email(*, to_email: str, subject: str, text: str) -> None:
    api_key = settings.resend_api_key.strip()
    if not api_key:
        raise ValueError("resend not configured")

    payload = {
        "from": settings.resend_from_email,
        "to": [to_email],
        "subject": subject,
        "text": text,
    }
    headers = {"authorization": f"Bearer {api_key}", "content-type": "application/json"}
    timeout = httpx.Timeout(12.0, connect=6.0)
    async with httpx.AsyncClient(timeout=timeout) as client:
        res = await client.post("https://api.resend.com/emails", json=payload, headers=headers)
    if res.status_code >= 400:
        raise ValueError("failed to send email")


async def request_email_code(
    session: AsyncSession,
    *,
    email: str,
    purpose: str,
    client_ip: str | None,
) -> int:
    normalized_email = _normalize_email(email)
    normalized_purpose = _normalize_purpose(purpose)

    now = dt.datetime.now(dt.timezone.utc)
    window_start = now - dt.timedelta(hours=1)
    recent_count = (
        await session.execute(
            select(func.count())
            .select_from(EmailVerificationCode)
            .where(
                EmailVerificationCode.email == normalized_email,
                EmailVerificationCode.purpose == normalized_purpose,
                EmailVerificationCode.created_at >= window_start,
            )
        )
    ).scalar_one()
    if int(recent_count) >= 5:
        raise ValueError("too many requests")

    latest = (
        await session.execute(
            select(EmailVerificationCode.created_at)
            .where(
                EmailVerificationCode.email == normalized_email,
                EmailVerificationCode.purpose == normalized_purpose,
            )
            .order_by(EmailVerificationCode.created_at.desc())
            .limit(1)
        )
    ).scalar_one_or_none()
    if isinstance(latest, dt.datetime):
        if (now - latest) < dt.timedelta(seconds=30):
            raise ValueError("please wait")

    code = _generate_code()
    expires_at = now + dt.timedelta(minutes=int(settings.email_verification_ttl_minutes))
    row = EmailVerificationCode(
        email=normalized_email,
        purpose=normalized_purpose,
        code_hash=sha256_hex(code),
        expires_at=expires_at,
        source_ip=(client_ip.strip()[:64] if client_ip else None),
    )
    session.add(row)
    await session.commit()

    subject = "Your verification code"
    text = (
        "Your verification code is:\n\n"
        f"{code}\n\n"
        f"This code expires in {settings.email_verification_ttl_minutes} minutes."
    )
    try:
        await _send_resend_email(to_email=normalized_email, subject=subject, text=text)
    except Exception:
        row.used_at = now
        await session.commit()
        raise
    return int((expires_at - now).total_seconds())


async def verify_email_code(
    session: AsyncSession,
    *,
    email: str,
    purpose: str,
    code: str,
) -> None:
    normalized_email = _normalize_email(email)
    normalized_purpose = _normalize_purpose(purpose)
    normalized_code = _normalize_code(code)
    now = dt.datetime.now(dt.timezone.utc)

    row = (
        await session.execute(
            select(EmailVerificationCode)
            .where(
                EmailVerificationCode.email == normalized_email,
                EmailVerificationCode.purpose == normalized_purpose,
                EmailVerificationCode.used_at.is_(None),
                EmailVerificationCode.expires_at > now,
            )
            .order_by(EmailVerificationCode.created_at.desc())
            .limit(1)
        )
    ).scalars().first()
    if not row:
        raise ValueError("invalid code")

    if not secrets.compare_digest(row.code_hash, sha256_hex(normalized_code)):
        raise ValueError("invalid code")

    row.used_at = now
    await session.commit()
