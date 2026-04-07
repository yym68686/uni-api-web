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


def _build_user(*, email: str, balance_cents: int) -> User:
    return User(
        id=uuid.uuid4(),
        email=email,
        password_hash="pw",
        balance=balance_cents,
    )


def _build_topup(*, org_id: uuid.UUID, user_id: uuid.UUID, units: int, refunded_at: dt.datetime | None = None) -> BillingTopup:
    return BillingTopup(
        id=uuid.uuid4(),
        org_id=org_id,
        user_id=user_id,
        request_id=f"topup_{uuid.uuid4().hex}",
        units=units,
        status="completed",
        refunded_at=refunded_at,
    )


def _build_event(
    *,
    org_id: uuid.UUID,
    inviter_user_id: uuid.UUID,
    invitee_user_id: uuid.UUID,
    topup_id: uuid.UUID,
    status: str,
    bonus_cents: int,
    confirmed_at: dt.datetime | None = None,
    invitee_confirmed_at: dt.datetime | None = None,
) -> ReferralBonusEvent:
    return ReferralBonusEvent(
        org_id=org_id,
        inviter_user_id=inviter_user_id,
        invitee_user_id=invitee_user_id,
        topup_id=topup_id,
        status=status,
        bonus_usd_cents=bonus_cents,
        confirmed_at=confirmed_at,
        invitee_confirmed_at=invitee_confirmed_at,
    )


class ReferralBonusLogicTests(unittest.IsolatedAsyncioTestCase):
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
