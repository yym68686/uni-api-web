from __future__ import annotations

import unittest
import uuid

from app.models.api_key import ApiKey
from app.models.user import User
from app.storage import usage_db


class _FakeExecuteResult:
    def __init__(self, *, first_value: object | None = None) -> None:
        self._first_value = first_value

    def first(self) -> object | None:
        return self._first_value


class _FakeSession:
    def __init__(self, *, user: User) -> None:
        self.user = user
        self.added: list[object] = []
        self.executed: list[str] = []
        self.commits = 0

    def add(self, obj: object) -> None:
        self.added.append(obj)

    async def execute(self, statement: object) -> _FakeExecuteResult:
        statement_text = str(statement)
        self.executed.append(statement_text)

        if "UPDATE users" in statement_text and "RETURNING" in statement_text:
            if self.user.first_api_call_at is None:
                inserted_at = next((item.created_at for item in self.added if hasattr(item, "created_at")), None)
                self.user.first_api_call_at = inserted_at
                return _FakeExecuteResult(first_value=(self.user.id,))
            return _FakeExecuteResult(first_value=None)

        return _FakeExecuteResult(first_value=None)

    async def commit(self) -> None:
        self.commits += 1


class RecordUsageEventTests(unittest.IsolatedAsyncioTestCase):
    async def test_first_api_call_marker_is_written_once_without_count_scan(self) -> None:
        org_id = uuid.uuid4()
        user = User(
            id=uuid.uuid4(),
            email="alice@example.com",
            password_hash="hash",
            balance=0,
            spend_usd_micros_total=0,
        )
        api_key = ApiKey(
            id=uuid.uuid4(),
            user_id=user.id,
            name="primary",
            prefix="sk_test",
            key_hash="hash",
        )
        session = _FakeSession(user=user)
        events: list[dict[str, object]] = []
        original_enqueue = usage_db.enqueue_analytics_event

        async def fake_enqueue_analytics_event(session_arg: object, **kwargs: object) -> object:
            _ = session_arg
            events.append(kwargs)
            return object()

        usage_db.enqueue_analytics_event = fake_enqueue_analytics_event
        try:
            await usage_db.record_usage_event(
                session,
                org_id=org_id,
                user_id=user.id,
                api_key_id=api_key.id,
                model_id="gpt-4.1",
                ok=True,
                status_code=200,
                input_tokens=12,
                cached_tokens=2,
                output_tokens=4,
                total_tokens=16,
                cost_usd_micros=1200,
                total_duration_ms=350,
                ttft_ms=80,
                request_endpoint="/v1/responses",
            )
            await usage_db.record_usage_event(
                session,
                org_id=org_id,
                user_id=user.id,
                api_key_id=api_key.id,
                model_id="gpt-4.1",
                ok=True,
                status_code=200,
                input_tokens=9,
                cached_tokens=0,
                output_tokens=3,
                total_tokens=12,
                cost_usd_micros=800,
                total_duration_ms=200,
                ttft_ms=40,
                request_endpoint="/v1/responses",
            )
        finally:
            usage_db.enqueue_analytics_event = original_enqueue

        self.assertEqual(len(events), 1)
        self.assertEqual(events[0]["name"], "first_api_call")
        self.assertEqual(events[0]["occurred_at"], session.added[0].created_at)
        self.assertEqual(user.first_api_call_at, session.added[0].created_at)
        self.assertGreaterEqual(session.commits, 2)
        self.assertFalse(any("COUNT(" in text.upper() for text in session.executed))
        self.assertTrue(any("RETURNING" in text.upper() for text in session.executed))


if __name__ == "__main__":
    unittest.main()
