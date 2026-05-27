from __future__ import annotations

import unittest

from app.main import _StartupMigrationConnection, create_app


class _FakeResult:
    def __init__(self, value: object | None) -> None:
        self._value = value

    def first(self) -> object | None:
        return self._value


class _FakeConnection:
    def __init__(self, *, existing_indexes: set[str] | None = None) -> None:
        self.existing_indexes = existing_indexes or set()
        self.driver_sql: list[str] = []

    async def run_sync(self, *args: object, **kwargs: object) -> object:
        return None

    async def execute(self, statement: object, params: dict[str, object]) -> _FakeResult:
        _ = statement
        index_name = str(params.get("index_name") or "")
        return _FakeResult((1,) if index_name in self.existing_indexes else None)

    async def exec_driver_sql(self, statement: str, *args: object, **kwargs: object) -> str:
        _ = args
        _ = kwargs
        self.driver_sql.append(statement)
        return "executed"


class StartupMigrationTests(unittest.IsolatedAsyncioTestCase):
    async def test_existing_create_index_if_not_exists_is_skipped_before_postgres_lock(self) -> None:
        raw = _FakeConnection(existing_indexes={"ix_llm_usage_events_org_created_at"})
        conn = _StartupMigrationConnection(raw)

        result = await conn.exec_driver_sql(
            "CREATE INDEX IF NOT EXISTS ix_llm_usage_events_org_created_at "
            "ON llm_usage_events(org_id, created_at)"
        )

        self.assertIsNone(result)
        self.assertEqual(raw.driver_sql, [])

    async def test_missing_create_index_if_not_exists_still_runs(self) -> None:
        raw = _FakeConnection()
        conn = _StartupMigrationConnection(raw)

        result = await conn.exec_driver_sql(
            "CREATE INDEX IF NOT EXISTS ix_llm_usage_events_org_created_at "
            "ON llm_usage_events(org_id, created_at)"
        )

        self.assertEqual(result, "executed")
        self.assertEqual(len(raw.driver_sql), 1)

    def test_healthz_route_is_registered_without_api_prefix(self) -> None:
        app = create_app()
        paths = {route.path for route in app.routes}

        self.assertIn("/healthz", paths)


if __name__ == "__main__":
    unittest.main()
