from __future__ import annotations

import datetime as dt
import unittest
import uuid

from app.models.balance_ledger_entry import BalanceLedgerEntry
from app.models.billing_topup import BillingTopup
from app.models.referral_bonus_event import ReferralBonusEvent
from app.models.user import User
from app.storage.referrals_db import (
    _backfill_missing_invitee_bonus,
    _confirm_pending_referral_bonus_event,
    _reverse_confirmed_referral_bonus_event,
    evaluate_referral_risk,
    referral_bonus_event_to_received_reward,
)


class _FakeSession:
    def __init__(self, *, users: list[User], topups: list[BillingTopup]) -> None:
        self._users = {user.id: user for user in users}
        self._topups = {topup.id: topup for topup in topups}
        self.added: list[object] = []

    def add(self, obj: object) -> None:
        self.added.append(obj)

    async def get(
        self,
        model: type[object],
        ident: uuid.UUID,
        *,
        populate_existing: bool | None = None,
        with_for_update: bool | None = None,
    ) -> object | None:
        _ = populate_existing
        _ = with_for_update
        if model is User:
            return self._users.get(ident)
        if model is BillingTopup:
            return self._topups.get(ident)
        raise AssertionError(f"unexpected model lookup: {model!r}")


def _ledger_entries(session: _FakeSession) -> list[BalanceLedgerEntry]:
    return [item for item in session.added if isinstance(item, BalanceLedgerEntry)]


def _build_user(
    *,
    email: str,
    balance_cents: int,
    created_at: dt.datetime | None = None,
    signup_ip: str | None = None,
    signup_device_id: str | None = None,
    first_payment_email: str | None = None,
    first_payment_ip: str | None = None,
    first_payment_device_id: str | None = None,
    spend_usd_micros_total: int = 0,
) -> User:
    return User(
        id=uuid.uuid4(),
        email=email,
        password_hash="pw",
        balance=balance_cents,
        created_at=created_at or dt.datetime(2026, 4, 1, 12, 0, tzinfo=dt.timezone.utc),
        signup_ip=signup_ip,
        signup_device_id=signup_device_id,
        first_payment_email=first_payment_email,
        first_payment_ip=first_payment_ip,
        first_payment_device_id=first_payment_device_id,
        spend_usd_micros_total=spend_usd_micros_total,
    )


def _build_topup(
    *,
    org_id: uuid.UUID,
    user_id: uuid.UUID,
    units: int,
    refunded_at: dt.datetime | None = None,
    client_ip: str | None = None,
    client_device_id: str | None = None,
    payer_email: str | None = None,
    created_at: dt.datetime | None = None,
    completed_at: dt.datetime | None = None,
) -> BillingTopup:
    return BillingTopup(
        id=uuid.uuid4(),
        org_id=org_id,
        user_id=user_id,
        request_id=f"topup_{uuid.uuid4().hex}",
        units=units,
        status="completed",
        refunded_at=refunded_at,
        client_ip=client_ip,
        client_device_id=client_device_id,
        payer_email=payer_email,
        created_at=created_at or dt.datetime(2026, 4, 1, 13, 0, tzinfo=dt.timezone.utc),
        completed_at=completed_at or created_at or dt.datetime(2026, 4, 1, 13, 0, tzinfo=dt.timezone.utc),
    )


def _build_event(
    *,
    org_id: uuid.UUID,
    inviter_user_id: uuid.UUID,
    invitee_user_id: uuid.UUID,
    topup_id: uuid.UUID,
    status: str,
    bonus_cents: int,
    created_at: dt.datetime | None = None,
    confirmed_at: dt.datetime | None = None,
    invitee_confirmed_at: dt.datetime | None = None,
) -> ReferralBonusEvent:
    return ReferralBonusEvent(
        id=uuid.uuid4(),
        org_id=org_id,
        inviter_user_id=inviter_user_id,
        invitee_user_id=invitee_user_id,
        topup_id=topup_id,
        status=status,
        bonus_usd_cents=bonus_cents,
        created_at=created_at or dt.datetime(2026, 4, 1, 12, 0, tzinfo=dt.timezone.utc),
        confirmed_at=confirmed_at,
        invitee_confirmed_at=invitee_confirmed_at,
    )


class ReferralBonusLogicTests(unittest.IsolatedAsyncioTestCase):
    async def test_received_reward_summary_exposes_pending_invitee_reward(self) -> None:
        org_id = uuid.uuid4()
        created_at = dt.datetime(2026, 4, 7, 12, 0, tzinfo=dt.timezone.utc)
        event = _build_event(
            org_id=org_id,
            inviter_user_id=uuid.uuid4(),
            invitee_user_id=uuid.uuid4(),
            topup_id=uuid.uuid4(),
            status="pending",
            bonus_cents=1250,
            created_at=created_at,
        )

        summary = referral_bonus_event_to_received_reward(event)

        self.assertEqual(summary["id"], str(event.id))
        self.assertEqual(summary["status"], "pending")
        self.assertEqual(summary["rewardUsd"], 12.5)
        self.assertEqual(summary["createdAt"], "2026-04-07T12:00:00+00:00")
        self.assertEqual(summary["availableAt"], "2026-04-10T12:00:00+00:00")
        self.assertIsNone(summary["confirmedAt"])

    async def test_received_reward_summary_exposes_review_window(self) -> None:
        org_id = uuid.uuid4()
        created_at = dt.datetime(2026, 4, 7, 12, 0, tzinfo=dt.timezone.utc)
        event = _build_event(
            org_id=org_id,
            inviter_user_id=uuid.uuid4(),
            invitee_user_id=uuid.uuid4(),
            topup_id=uuid.uuid4(),
            status="pending_review",
            bonus_cents=250,
            created_at=created_at,
        )

        summary = referral_bonus_event_to_received_reward(event)

        self.assertEqual(summary["status"], "pending_review")
        self.assertEqual(summary["rewardUsd"], 2.5)
        self.assertEqual(summary["availableAt"], "2026-04-14T12:00:00+00:00")

    async def test_same_ip_alone_is_pending_not_blocked(self) -> None:
        org_id = uuid.uuid4()
        inviter = _build_user(
            email="inviter@example.com",
            balance_cents=0,
            first_payment_ip="8.8.8.8",
        )
        invitee = _build_user(
            email="invitee@example.com",
            balance_cents=0,
            created_at=dt.datetime(2026, 4, 1, 12, 0, tzinfo=dt.timezone.utc),
            signup_ip="8.8.8.8",
        )
        topup = _build_topup(
            org_id=org_id,
            user_id=invitee.id,
            units=20,
            client_ip="8.8.8.8",
            completed_at=dt.datetime(2026, 4, 2, 12, 0, tzinfo=dt.timezone.utc),
        )

        decision = evaluate_referral_risk(inviter=inviter, invitee=invitee, topup=topup)

        self.assertEqual(decision.status, "pending")
        self.assertIsNone(decision.blocked_reason)
        self.assertEqual(decision.score, 20)
        self.assertEqual([signal["code"] for signal in decision.evidence["signals"]], ["same_ip"])

    async def test_cloudflare_edge_ip_is_not_used_as_same_ip_signal(self) -> None:
        org_id = uuid.uuid4()
        inviter = _build_user(
            email="inviter@example.com",
            balance_cents=0,
            first_payment_ip="104.23.251.107",
        )
        invitee = _build_user(
            email="invitee@example.com",
            balance_cents=0,
            signup_ip="104.23.251.107",
        )
        topup = _build_topup(
            org_id=org_id,
            user_id=invitee.id,
            units=20,
            client_ip="104.23.251.107",
            completed_at=dt.datetime(2026, 4, 2, 12, 0, tzinfo=dt.timezone.utc),
        )

        decision = evaluate_referral_risk(inviter=inviter, invitee=invitee, topup=topup)

        self.assertEqual(decision.status, "pending")
        self.assertEqual(decision.score, 0)
        self.assertEqual(decision.evidence["signals"], [])

    async def test_same_ip_with_fast_small_topup_goes_to_review(self) -> None:
        org_id = uuid.uuid4()
        created_at = dt.datetime(2026, 4, 1, 12, 0, tzinfo=dt.timezone.utc)
        inviter = _build_user(
            email="inviter@example.com",
            balance_cents=0,
            first_payment_ip="8.8.8.8",
        )
        invitee = _build_user(
            email="invitee@example.com",
            balance_cents=0,
            created_at=created_at,
            signup_ip="8.8.8.8",
        )
        topup = _build_topup(
            org_id=org_id,
            user_id=invitee.id,
            units=10,
            client_ip="8.8.8.8",
            completed_at=created_at + dt.timedelta(minutes=30),
        )

        decision = evaluate_referral_risk(inviter=inviter, invitee=invitee, topup=topup)

        self.assertEqual(decision.status, "pending_review")
        self.assertIsNone(decision.blocked_reason)
        self.assertEqual(decision.score, 50)
        self.assertEqual(
            [signal["code"] for signal in decision.evidence["signals"]],
            ["same_ip", "fast_topup_after_signup", "small_first_topup"],
        )

    async def test_same_device_still_blocks_immediately(self) -> None:
        org_id = uuid.uuid4()
        inviter = _build_user(
            email="inviter@example.com",
            balance_cents=0,
            signup_device_id="device-a",
        )
        invitee = _build_user(
            email="invitee@example.com",
            balance_cents=0,
            signup_device_id="device-a",
        )
        topup = _build_topup(
            org_id=org_id,
            user_id=invitee.id,
            units=20,
            client_device_id="device-b",
        )

        decision = evaluate_referral_risk(inviter=inviter, invitee=invitee, topup=topup)

        self.assertEqual(decision.status, "blocked")
        self.assertEqual(decision.blocked_reason, "same_device")
        self.assertGreaterEqual(decision.score, 100)

    async def test_confirm_pending_event_credits_inviter_and_invitee(self) -> None:
        org_id = uuid.uuid4()
        now = dt.datetime(2026, 4, 7, 12, 0, tzinfo=dt.timezone.utc)
        inviter = _build_user(email="inviter@example.com", balance_cents=0)
        invitee = _build_user(email="invitee@example.com", balance_cents=500)
        topup = _build_topup(org_id=org_id, user_id=invitee.id, units=5)
        event = _build_event(
            org_id=org_id,
            inviter_user_id=inviter.id,
            invitee_user_id=invitee.id,
            topup_id=topup.id,
            status="pending",
            bonus_cents=125,
        )
        session = _FakeSession(users=[inviter, invitee], topups=[topup])

        changed = await _confirm_pending_referral_bonus_event(session, event=event, ts=now)

        self.assertTrue(changed)
        self.assertEqual(event.status, "confirmed")
        self.assertEqual(event.confirmed_at, now)
        self.assertEqual(event.invitee_confirmed_at, now)
        self.assertEqual(inviter.balance, 125)
        self.assertEqual(invitee.balance, 625)
        ledger = _ledger_entries(session)
        self.assertEqual(len(ledger), 2)
        self.assertEqual({entry.user_id for entry in ledger}, {inviter.id, invitee.id})
        self.assertTrue(all(entry.entry_type == "referral_bonus" for entry in ledger))
        self.assertEqual({entry.delta_usd_micros for entry in ledger}, {1_250_000})

    async def test_confirm_review_event_credits_when_usage_is_real(self) -> None:
        org_id = uuid.uuid4()
        now = dt.datetime(2026, 4, 8, 12, 0, tzinfo=dt.timezone.utc)
        created_at = dt.datetime(2026, 4, 1, 12, 0, tzinfo=dt.timezone.utc)
        inviter = _build_user(
            email="inviter@example.com",
            balance_cents=0,
            first_payment_ip="8.8.8.8",
        )
        invitee = _build_user(
            email="invitee@example.com",
            balance_cents=500,
            created_at=created_at,
            signup_ip="8.8.8.8",
            spend_usd_micros_total=2_000_000,
        )
        topup = _build_topup(
            org_id=org_id,
            user_id=invitee.id,
            units=10,
            client_ip="8.8.8.8",
            completed_at=created_at + dt.timedelta(minutes=30),
        )
        event = _build_event(
            org_id=org_id,
            inviter_user_id=inviter.id,
            invitee_user_id=invitee.id,
            topup_id=topup.id,
            status="pending_review",
            bonus_cents=250,
            created_at=created_at,
        )
        session = _FakeSession(users=[inviter, invitee], topups=[topup])

        changed = await _confirm_pending_referral_bonus_event(session, event=event, ts=now)

        self.assertTrue(changed)
        self.assertEqual(event.status, "confirmed")
        self.assertEqual(event.confirmed_at, now)
        self.assertEqual(event.risk_score, 50)
        self.assertIsInstance(event.risk_evidence, dict)
        self.assertEqual(inviter.balance, 250)
        self.assertEqual(invitee.balance, 750)

    async def test_confirm_review_event_blocks_when_usage_stays_low(self) -> None:
        org_id = uuid.uuid4()
        now = dt.datetime(2026, 4, 8, 12, 0, tzinfo=dt.timezone.utc)
        created_at = dt.datetime(2026, 4, 1, 12, 0, tzinfo=dt.timezone.utc)
        inviter = _build_user(
            email="inviter@example.com",
            balance_cents=0,
            first_payment_ip="8.8.8.8",
        )
        invitee = _build_user(
            email="invitee@example.com",
            balance_cents=500,
            created_at=created_at,
            signup_ip="8.8.8.8",
            spend_usd_micros_total=0,
        )
        topup = _build_topup(
            org_id=org_id,
            user_id=invitee.id,
            units=10,
            client_ip="8.8.8.8",
            completed_at=created_at + dt.timedelta(minutes=30),
        )
        event = _build_event(
            org_id=org_id,
            inviter_user_id=inviter.id,
            invitee_user_id=invitee.id,
            topup_id=topup.id,
            status="pending_review",
            bonus_cents=250,
            created_at=created_at,
        )
        session = _FakeSession(users=[inviter, invitee], topups=[topup])

        changed = await _confirm_pending_referral_bonus_event(session, event=event, ts=now)

        self.assertFalse(changed)
        self.assertEqual(event.status, "blocked")
        self.assertEqual(event.blocked_reason, "risk_score")
        self.assertEqual(event.reversed_at, now)
        self.assertEqual(event.risk_score, 95)
        self.assertEqual(inviter.balance, 0)
        self.assertEqual(invitee.balance, 500)
        self.assertEqual(_ledger_entries(session), [])

    async def test_backfill_missing_invitee_bonus_is_idempotent(self) -> None:
        org_id = uuid.uuid4()
        confirmed_at = dt.datetime(2026, 4, 3, 12, 0, tzinfo=dt.timezone.utc)
        now = dt.datetime(2026, 4, 7, 12, 0, tzinfo=dt.timezone.utc)
        inviter = _build_user(email="inviter@example.com", balance_cents=125)
        invitee = _build_user(email="invitee@example.com", balance_cents=500)
        topup = _build_topup(org_id=org_id, user_id=invitee.id, units=5)
        event = _build_event(
            org_id=org_id,
            inviter_user_id=inviter.id,
            invitee_user_id=invitee.id,
            topup_id=topup.id,
            status="confirmed",
            bonus_cents=125,
            confirmed_at=confirmed_at,
        )
        session = _FakeSession(users=[inviter, invitee], topups=[topup])

        first_changed = await _backfill_missing_invitee_bonus(session, event=event, ts=now)
        ledger_after_first = list(_ledger_entries(session))
        second_changed = await _backfill_missing_invitee_bonus(session, event=event, ts=now)

        self.assertTrue(first_changed)
        self.assertFalse(second_changed)
        self.assertEqual(inviter.balance, 125)
        self.assertEqual(invitee.balance, 625)
        self.assertEqual(event.invitee_confirmed_at, confirmed_at)
        self.assertEqual(len(ledger_after_first), 1)
        self.assertEqual(len(_ledger_entries(session)), 1)
        self.assertEqual(ledger_after_first[0].user_id, invitee.id)
        self.assertEqual(ledger_after_first[0].delta_usd_micros, 1_250_000)

    async def test_reverse_confirmed_event_reverts_both_sides_when_invitee_was_confirmed(self) -> None:
        org_id = uuid.uuid4()
        confirmed_at = dt.datetime(2026, 4, 3, 12, 0, tzinfo=dt.timezone.utc)
        now = dt.datetime(2026, 4, 7, 12, 0, tzinfo=dt.timezone.utc)
        inviter = _build_user(email="inviter@example.com", balance_cents=125)
        invitee = _build_user(email="invitee@example.com", balance_cents=625)
        topup = _build_topup(org_id=org_id, user_id=invitee.id, units=5)
        event = _build_event(
            org_id=org_id,
            inviter_user_id=inviter.id,
            invitee_user_id=invitee.id,
            topup_id=topup.id,
            status="confirmed",
            bonus_cents=125,
            confirmed_at=confirmed_at,
            invitee_confirmed_at=confirmed_at,
        )
        session = _FakeSession(users=[inviter, invitee], topups=[topup])

        await _reverse_confirmed_referral_bonus_event(session, event=event, ts=now)

        self.assertEqual(event.status, "reversed")
        self.assertEqual(event.reversed_at, now)
        self.assertEqual(inviter.balance, 0)
        self.assertEqual(invitee.balance, 500)
        ledger = _ledger_entries(session)
        self.assertEqual(len(ledger), 2)
        self.assertEqual({entry.user_id for entry in ledger}, {inviter.id, invitee.id})
        self.assertEqual({entry.delta_usd_micros for entry in ledger}, {-1_250_000})

    async def test_reverse_confirmed_event_skips_invitee_when_bonus_was_never_backfilled(self) -> None:
        org_id = uuid.uuid4()
        confirmed_at = dt.datetime(2026, 4, 3, 12, 0, tzinfo=dt.timezone.utc)
        now = dt.datetime(2026, 4, 7, 12, 0, tzinfo=dt.timezone.utc)
        inviter = _build_user(email="inviter@example.com", balance_cents=125)
        invitee = _build_user(email="invitee@example.com", balance_cents=500)
        topup = _build_topup(org_id=org_id, user_id=invitee.id, units=5)
        event = _build_event(
            org_id=org_id,
            inviter_user_id=inviter.id,
            invitee_user_id=invitee.id,
            topup_id=topup.id,
            status="confirmed",
            bonus_cents=125,
            confirmed_at=confirmed_at,
            invitee_confirmed_at=None,
        )
        session = _FakeSession(users=[inviter, invitee], topups=[topup])

        await _reverse_confirmed_referral_bonus_event(session, event=event, ts=now)

        self.assertEqual(inviter.balance, 0)
        self.assertEqual(invitee.balance, 500)
        ledger = _ledger_entries(session)
        self.assertEqual(len(ledger), 1)
        self.assertEqual(ledger[0].user_id, inviter.id)
        self.assertEqual(ledger[0].delta_usd_micros, -1_250_000)


if __name__ == "__main__":
    unittest.main()
