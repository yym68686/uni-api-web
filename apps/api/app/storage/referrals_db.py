from __future__ import annotations

import datetime as dt
import ipaddress
import uuid
from dataclasses import dataclass
from decimal import Decimal, ROUND_HALF_UP

from sqlalchemy import and_, or_, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.models.billing_topup import BillingTopup
from app.models.referral_bonus_event import ReferralBonusEvent
from app.models.user import User
from app.storage.billing_db import stage_balance_adjustment_ledger_entry


REFERRAL_RATE = Decimal("0.25")
REFERRAL_CAP_USD_CENTS = 10_000
REFERRAL_PENDING_HOURS = 72
REFERRAL_REVIEW_HOURS = 168
REFERRAL_REVIEW_SCORE = 50
REFERRAL_BLOCK_SCORE = 90
REFERRAL_FAST_TOPUP_MINUTES = 60
REFERRAL_SMALL_TOPUP_UNITS = 10
REFERRAL_REVIEW_MIN_SPEND_RATIO = Decimal("0.20")
USD_MICROS_PER_CREDIT = 1_000_000

_CLOUDFLARE_EDGE_NETWORKS = tuple(
    ipaddress.ip_network(raw)
    for raw in (
        "173.245.48.0/20",
        "103.21.244.0/22",
        "103.22.200.0/22",
        "103.31.4.0/22",
        "141.101.64.0/18",
        "108.162.192.0/18",
        "190.93.240.0/20",
        "188.114.96.0/20",
        "197.234.240.0/22",
        "198.41.128.0/17",
        "162.158.0.0/15",
        "104.16.0.0/13",
        "104.24.0.0/14",
        "172.64.0.0/13",
        "131.0.72.0/22",
    )
)


@dataclass(frozen=True)
class ReferralRiskSignal:
    code: str
    score: int
    severity: str


@dataclass(frozen=True)
class ReferralRiskDecision:
    status: str
    blocked_reason: str | None
    score: int
    evidence: dict[str, object]


def _dt_iso(value: dt.datetime) -> str:
    return value.astimezone(dt.timezone.utc).isoformat()


def _normalize_email(value: str | None) -> str | None:
    if not isinstance(value, str):
        return None
    v = value.strip().lower()
    return v or None


def _normalize_token(value: str | None, *, max_len: int) -> str | None:
    if not isinstance(value, str):
        return None
    v = value.strip()
    if v == "":
        return None
    return v[:max_len]


def _normalize_high_confidence_ip(value: str | None) -> str | None:
    raw = _normalize_token(value, max_len=64)
    if raw is None:
        return None
    try:
        parsed = ipaddress.ip_address(raw)
    except ValueError:
        return None
    if (
        parsed.is_loopback
        or parsed.is_private
        or parsed.is_link_local
        or parsed.is_multicast
        or parsed.is_reserved
        or parsed.is_unspecified
    ):
        return None
    if any(parsed in network for network in _CLOUDFLARE_EDGE_NETWORKS):
        return None
    return str(parsed)


def _event_timestamp(value: object) -> dt.datetime | None:
    return value if isinstance(value, dt.datetime) else None


def _topup_reference_at(topup: BillingTopup) -> dt.datetime | None:
    return (
        _event_timestamp(getattr(topup, "completed_at", None))
        or _event_timestamp(getattr(topup, "updated_at", None))
        or _event_timestamp(getattr(topup, "created_at", None))
    )


def _minutes_between(start: dt.datetime | None, end: dt.datetime | None) -> float | None:
    if start is None or end is None:
        return None
    delta = end - start
    return max(delta.total_seconds() / 60, 0.0)


def _signals_to_evidence(
    *,
    signals: list[ReferralRiskSignal],
    score: int,
    decision: str,
    include_usage_review: bool,
) -> dict[str, object]:
    return {
        "version": 1,
        "decision": decision,
        "score": int(score),
        "signals": [
            {"code": signal.code, "score": int(signal.score), "severity": signal.severity}
            for signal in signals
        ],
        "thresholds": {
            "reviewScore": REFERRAL_REVIEW_SCORE,
            "blockScore": REFERRAL_BLOCK_SCORE,
            "pendingHours": REFERRAL_PENDING_HOURS,
            "reviewHours": REFERRAL_REVIEW_HOURS,
            "reviewMinSpendRatio": str(REFERRAL_REVIEW_MIN_SPEND_RATIO),
        },
        "includeUsageReview": bool(include_usage_review),
    }


def compute_referral_bonus_usd_cents(units: int) -> int:
    safe_units = int(max(int(units), 0))
    # units are integer USD credits; compute 25% with 2-decimal precision (cents).
    bonus = Decimal(safe_units) * REFERRAL_RATE
    bonus_usd = bonus.quantize(Decimal("0.01"), rounding=ROUND_HALF_UP)
    cents = int((bonus_usd * Decimal("100")).to_integral_value(rounding=ROUND_HALF_UP))
    return int(min(max(cents, 0), REFERRAL_CAP_USD_CENTS))


def _safe_bonus_cents(value: int) -> int:
    return int(max(int(value), 0))


def _bonus_cents_to_usd_2(value: int) -> float:
    return float((Decimal(_safe_bonus_cents(value)) / Decimal("100")).quantize(Decimal("0.01")))


def referral_bonus_event_to_received_reward(event: ReferralBonusEvent) -> dict[str, object]:
    created_at = getattr(event, "created_at", None)
    if not isinstance(created_at, dt.datetime):
        created_at = dt.datetime.now(dt.timezone.utc)

    status = str(getattr(event, "status", "") or "none")
    reward_usd: float | None = None
    if status in {"pending", "pending_review", "confirmed"}:
        reward_usd = _bonus_cents_to_usd_2(int(getattr(event, "bonus_usd_cents", 0) or 0))

    available_at: str | None = None
    if status in {"pending", "pending_review"}:
        hours = REFERRAL_REVIEW_HOURS if status == "pending_review" else REFERRAL_PENDING_HOURS
        available_at = _dt_iso(created_at + dt.timedelta(hours=hours))

    confirmed_at = getattr(event, "confirmed_at", None)
    return {
        "id": str(getattr(event, "id", None) or getattr(event, "topup_id", "")),
        "status": status,
        "rewardUsd": reward_usd,
        "createdAt": _dt_iso(created_at),
        "availableAt": available_at,
        "confirmedAt": _dt_iso(confirmed_at) if isinstance(confirmed_at, dt.datetime) else None,
    }


async def _load_locked_user(session: AsyncSession, *, user_id: uuid.UUID) -> User | None:
    return await session.get(User, user_id, populate_existing=True, with_for_update=True)


def _mark_referral_event_blocked(
    event: ReferralBonusEvent,
    *,
    reason: str,
    ts: dt.datetime,
    risk_decision: ReferralRiskDecision | None = None,
) -> None:
    event.status = "blocked"
    event.blocked_reason = event.blocked_reason or reason
    event.reversed_at = ts
    if risk_decision is not None:
        event.risk_score = risk_decision.score
        event.risk_evidence = risk_decision.evidence


def _stage_referral_bonus_credit(
    session: AsyncSession,
    *,
    org_id: uuid.UUID,
    user: User,
    delta_cents: int,
) -> None:
    safe_delta = _safe_bonus_cents(delta_cents)
    balance_before = int(user.balance)
    user.balance = balance_before + safe_delta
    stage_balance_adjustment_ledger_entry(
        session,
        org_id=org_id,
        user_id=user.id,
        actor_user_id=None,
        balance_before=balance_before,
        balance_after=int(user.balance),
        entry_type="referral_bonus",
    )


def _stage_referral_bonus_reversal(
    session: AsyncSession,
    *,
    org_id: uuid.UUID,
    user: User,
    delta_cents: int,
) -> None:
    safe_delta = _safe_bonus_cents(delta_cents)
    balance_before = int(user.balance)
    user.balance = balance_before - safe_delta
    stage_balance_adjustment_ledger_entry(
        session,
        org_id=org_id,
        user_id=user.id,
        actor_user_id=None,
        balance_before=balance_before,
        balance_after=int(user.balance),
        entry_type="referral_bonus",
    )


async def _confirm_pending_referral_bonus_event(
    session: AsyncSession,
    *,
    event: ReferralBonusEvent,
    ts: dt.datetime,
) -> bool:
    topup = await session.get(BillingTopup, event.topup_id)
    if not topup or getattr(topup, "refunded_at", None) is not None:
        _mark_referral_event_blocked(event, reason="refunded", ts=ts)
        return False

    inviter = await _load_locked_user(session, user_id=event.inviter_user_id)
    if not inviter:
        _mark_referral_event_blocked(event, reason="missing_inviter", ts=ts)
        return False

    invitee = await _load_locked_user(session, user_id=event.invitee_user_id)
    if not invitee:
        _mark_referral_event_blocked(event, reason="missing_invitee", ts=ts)
        return False

    if event.status == "pending_review":
        risk_decision = evaluate_referral_risk(
            inviter=inviter,
            invitee=invitee,
            topup=topup,
            include_usage_review=True,
        )
        event.risk_score = risk_decision.score
        event.risk_evidence = risk_decision.evidence
        if risk_decision.blocked_reason or risk_decision.score >= REFERRAL_BLOCK_SCORE:
            _mark_referral_event_blocked(
                event,
                reason=risk_decision.blocked_reason or "risk_score",
                ts=ts,
                risk_decision=risk_decision,
            )
            return False

    delta_cents = _safe_bonus_cents(event.bonus_usd_cents)
    _stage_referral_bonus_credit(session, org_id=event.org_id, user=inviter, delta_cents=delta_cents)
    if invitee.id != inviter.id:
        _stage_referral_bonus_credit(session, org_id=event.org_id, user=invitee, delta_cents=delta_cents)
    event.status = "confirmed"
    event.confirmed_at = ts
    event.invitee_confirmed_at = ts
    return True


async def _backfill_missing_invitee_bonus(
    session: AsyncSession,
    *,
    event: ReferralBonusEvent,
    ts: dt.datetime,
) -> bool:
    if event.status != "confirmed" or event.invitee_confirmed_at is not None:
        return False
    if event.invitee_user_id == event.inviter_user_id:
        event.invitee_confirmed_at = event.confirmed_at or ts
        return False

    topup = await session.get(BillingTopup, event.topup_id)
    if not topup or getattr(topup, "refunded_at", None) is not None:
        return False

    invitee = await _load_locked_user(session, user_id=event.invitee_user_id)
    if not invitee:
        return False

    delta_cents = _safe_bonus_cents(event.bonus_usd_cents)
    _stage_referral_bonus_credit(session, org_id=event.org_id, user=invitee, delta_cents=delta_cents)
    event.invitee_confirmed_at = event.confirmed_at or ts
    return True


async def _reverse_confirmed_referral_bonus_event(
    session: AsyncSession,
    *,
    event: ReferralBonusEvent,
    ts: dt.datetime,
) -> None:
    delta_cents = _safe_bonus_cents(event.bonus_usd_cents)
    inviter = await _load_locked_user(session, user_id=event.inviter_user_id)
    if inviter:
        _stage_referral_bonus_reversal(session, org_id=event.org_id, user=inviter, delta_cents=delta_cents)

    if event.invitee_confirmed_at is not None and event.invitee_user_id != event.inviter_user_id:
        invitee = await _load_locked_user(session, user_id=event.invitee_user_id)
        if invitee:
            _stage_referral_bonus_reversal(session, org_id=event.org_id, user=invitee, delta_cents=delta_cents)

    event.status = "reversed"
    event.reversed_at = ts


def evaluate_referral_risk(
    *,
    inviter: User,
    invitee: User,
    topup: BillingTopup,
    include_usage_review: bool = False,
) -> ReferralRiskDecision:
    signals: list[ReferralRiskSignal] = []
    hard_reason: str | None = None

    def add_signal(code: str, score: int, severity: str) -> None:
        nonlocal hard_reason
        signals.append(ReferralRiskSignal(code=code, score=score, severity=severity))
        if severity == "block" and hard_reason is None:
            hard_reason = code

    if inviter.id == invitee.id:
        add_signal("self_invite", 100, "block")

    payer_email = _normalize_email(getattr(topup, "payer_email", None))
    inviter_email = _normalize_email(getattr(inviter, "email", None))
    inviter_payment_email = _normalize_email(getattr(inviter, "first_payment_email", None))
    if payer_email and ((inviter_email and payer_email == inviter_email) or (inviter_payment_email and payer_email == inviter_payment_email)):
        add_signal("same_payment_email", 100, "block")

    invitee_device_ids = {
        _normalize_token(getattr(invitee, "signup_device_id", None), max_len=64),
        _normalize_token(getattr(topup, "client_device_id", None), max_len=64),
    }
    inviter_device_ids = {
        _normalize_token(getattr(inviter, "signup_device_id", None), max_len=64),
        _normalize_token(getattr(inviter, "first_payment_device_id", None), max_len=64),
    }
    invitee_device_ids.discard(None)
    inviter_device_ids.discard(None)
    if invitee_device_ids and inviter_device_ids and invitee_device_ids.intersection(inviter_device_ids):
        add_signal("same_device", 100, "block")

    invitee_ips = {
        _normalize_high_confidence_ip(getattr(invitee, "signup_ip", None)),
        _normalize_high_confidence_ip(getattr(topup, "client_ip", None)),
    }
    inviter_ips = {
        _normalize_high_confidence_ip(getattr(inviter, "signup_ip", None)),
        _normalize_high_confidence_ip(getattr(inviter, "first_payment_ip", None)),
    }
    invitee_ips.discard(None)
    inviter_ips.discard(None)
    if invitee_ips and inviter_ips and invitee_ips.intersection(inviter_ips):
        add_signal("same_ip", 20, "weak")

    minutes_to_topup = _minutes_between(
        _event_timestamp(getattr(invitee, "created_at", None)),
        _topup_reference_at(topup),
    )
    if minutes_to_topup is not None and minutes_to_topup <= REFERRAL_FAST_TOPUP_MINUTES:
        add_signal("fast_topup_after_signup", 20, "weak")

    if int(getattr(topup, "units", 0) or 0) <= REFERRAL_SMALL_TOPUP_UNITS:
        add_signal("small_first_topup", 10, "weak")

    if include_usage_review:
        topup_units = int(max(int(getattr(topup, "units", 0) or 0), 0))
        required_spend_micros = int((Decimal(topup_units * USD_MICROS_PER_CREDIT) * REFERRAL_REVIEW_MIN_SPEND_RATIO).to_integral_value(rounding=ROUND_HALF_UP))
        actual_spend_micros = int(max(int(getattr(invitee, "spend_usd_micros_total", 0) or 0), 0))
        if required_spend_micros > 0 and actual_spend_micros < required_spend_micros:
            add_signal("low_invitee_usage_after_review", 45, "review")

    score = int(sum(signal.score for signal in signals))
    if hard_reason or score >= REFERRAL_BLOCK_SCORE:
        status = "blocked"
        blocked_reason = hard_reason or "risk_score"
    elif score >= REFERRAL_REVIEW_SCORE:
        status = "pending_review"
        blocked_reason = None
    else:
        status = "pending"
        blocked_reason = None

    return ReferralRiskDecision(
        status=status,
        blocked_reason=blocked_reason,
        score=score,
        evidence=_signals_to_evidence(
            signals=signals,
            score=score,
            decision=status,
            include_usage_review=include_usage_review,
        ),
    )


def detect_referral_block_reason(*, inviter: User, invitee: User, topup: BillingTopup) -> str | None:
    decision = evaluate_referral_risk(inviter=inviter, invitee=invitee, topup=topup)
    return decision.blocked_reason


async def maybe_create_referral_bonus_event(
    session: AsyncSession,
    *,
    org_id: uuid.UUID,
    topup: BillingTopup,
    invitee: User,
    now: dt.datetime | None = None,
) -> ReferralBonusEvent | None:
    inviter_user_id = getattr(invitee, "invited_by_user_id", None)
    if not inviter_user_id:
        return None

    existing_active = (
        await session.execute(
            select(ReferralBonusEvent.id)
            .where(
                ReferralBonusEvent.invitee_user_id == invitee.id,
                ReferralBonusEvent.status.in_(["pending", "pending_review", "confirmed"]),
            )
            .limit(1)
        )
    ).scalar_one_or_none()
    if existing_active is not None:
        return None

    inviter = await session.get(User, inviter_user_id)
    if not inviter:
        return None

    created_at = now or dt.datetime.now(dt.timezone.utc)
    bonus_cents = compute_referral_bonus_usd_cents(int(getattr(topup, "units", 0) or 0))
    decision = evaluate_referral_risk(inviter=inviter, invitee=invitee, topup=topup)

    row = ReferralBonusEvent(
        org_id=org_id,
        inviter_user_id=inviter.id,
        invitee_user_id=invitee.id,
        topup_id=topup.id,
        status=decision.status,
        bonus_usd_cents=bonus_cents,
        blocked_reason=decision.blocked_reason,
        risk_score=decision.score,
        risk_evidence=decision.evidence,
        created_at=created_at,
    )
    session.add(row)
    await session.flush()
    return row


async def confirm_due_referral_bonuses(session: AsyncSession, *, now: dt.datetime | None = None, limit: int = 200) -> int:
    safe_limit = min(max(int(limit), 1), 500)
    ts = now or dt.datetime.now(dt.timezone.utc)
    cutoff = ts - dt.timedelta(hours=REFERRAL_PENDING_HOURS)

    pending_rows = (
        await session.execute(
            select(ReferralBonusEvent)
            .where(
                or_(
                    and_(ReferralBonusEvent.status == "pending", ReferralBonusEvent.created_at <= cutoff),
                    and_(
                        ReferralBonusEvent.status == "pending_review",
                        ReferralBonusEvent.created_at <= ts - dt.timedelta(hours=REFERRAL_REVIEW_HOURS),
                    ),
                )
            )
            .with_for_update(skip_locked=True)
            .limit(safe_limit)
        )
    ).scalars().all()

    confirmed = 0
    for event in pending_rows:
        if await _confirm_pending_referral_bonus_event(session, event=event, ts=ts):
            confirmed += 1

    await session.flush()

    backfill_rows = (
        await session.execute(
            select(ReferralBonusEvent)
            .where(
                ReferralBonusEvent.status == "confirmed",
                ReferralBonusEvent.invitee_confirmed_at.is_(None),
                ReferralBonusEvent.reversed_at.is_(None),
            )
            .with_for_update(skip_locked=True)
            .limit(safe_limit)
        )
    ).scalars().all()

    backfilled = 0
    for event in backfill_rows:
        if await _backfill_missing_invitee_bonus(session, event=event, ts=ts):
            backfilled += 1

    if confirmed > 0 or backfilled > 0 or len(pending_rows) > 0 or len(backfill_rows) > 0:
        await session.commit()
    return confirmed + backfilled


async def process_referral_refund(session: AsyncSession, *, topup: BillingTopup, now: dt.datetime | None = None) -> None:
    ts = now or dt.datetime.now(dt.timezone.utc)
    if getattr(topup, "refunded_at", None) is None:
        topup.refunded_at = ts

    event = (
        (
            await session.execute(
                select(ReferralBonusEvent).where(ReferralBonusEvent.topup_id == topup.id).with_for_update()
            )
        )
        .scalars()
        .first()
    )
    if not event:
        await session.commit()
        return

    if event.status in {"pending", "pending_review"}:
        _mark_referral_event_blocked(event, reason="refunded", ts=ts)
        await session.commit()
        return

    if event.status != "confirmed":
        await session.commit()
        return

    await _reverse_confirmed_referral_bonus_event(session, event=event, ts=ts)
    await session.commit()
