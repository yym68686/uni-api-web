from __future__ import annotations

import datetime as dt
import unittest
import uuid

from app.models.llm_channel import LlmChannel
from app.storage.channels_db import list_channels


class _FakeScalars:
    def __init__(self, items: list[object]) -> None:
        self._items = items

    def all(self) -> list[object]:
        return self._items


class _FakeResult:
    def __init__(self, *, scalars: list[object] | None = None, rows: list[object] | None = None) -> None:
        self._scalars = scalars or []
        self._rows = rows or []

    def scalars(self) -> _FakeScalars:
        return _FakeScalars(self._scalars)

    def all(self) -> list[object]:
        return self._rows


class _FakeSession:
    def __init__(self, results: list[_FakeResult]) -> None:
        self._results = results
        self.executed = 0

    async def execute(self, statement: object) -> _FakeResult:
        _ = statement
        self.executed += 1
        return self._results.pop(0)


class ChannelDbTests(unittest.IsolatedAsyncioTestCase):
    async def test_list_channels_batches_group_lookup_once(self) -> None:
        org_id = uuid.uuid4()
        now = dt.datetime(2026, 5, 1, 12, 0, tzinfo=dt.timezone.utc)
        channels = [
            LlmChannel(
                id=uuid.uuid4(),
                org_id=org_id,
                name="alpha",
                base_url="https://alpha.example.com",
                api_key="sk-alpha",
                created_at=now,
                updated_at=now,
            ),
            LlmChannel(
                id=uuid.uuid4(),
                org_id=org_id,
                name="beta",
                base_url="https://beta.example.com",
                api_key="sk-beta",
                created_at=now - dt.timedelta(days=1),
                updated_at=now - dt.timedelta(days=1),
            ),
        ]
        group_rows = [
            (channels[0].id, "public"),
            (channels[0].id, "default"),
            (channels[1].id, "*"),
        ]
        session = _FakeSession(
            [
                _FakeResult(scalars=channels),
                _FakeResult(rows=group_rows),
            ]
        )

        response = await list_channels(session, org_id=org_id)

        self.assertEqual(session.executed, 2)
        self.assertEqual([item.id for item in response.items], [str(channels[0].id), str(channels[1].id)])
        self.assertEqual(response.items[0].allow_groups, ["default", "public"])
        self.assertEqual(response.items[1].allow_groups, ["*"])


if __name__ == "__main__":
    unittest.main()
