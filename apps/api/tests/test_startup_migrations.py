from __future__ import annotations

import unittest

from app.main import _StartupMigrationConnection, create_app

_ColumnMetadata = tuple[str, str, int | None, str, str | None]


class _FakeResult:
    def __init__(self, value: object | None) -> None:
        self._value = value

    def first(self) -> object | None:
        return self._value


class _FakeConnection:
    def __init__(
        self,
        *,
        existing_columns: set[tuple[str, str]] | None = None,
        existing_indexes: set[str] | None = None,
        column_metadata: dict[tuple[str, str], _ColumnMetadata] | None = None,
    ) -> None:
        self.existing_columns = existing_columns or set()
        self.existing_indexes = existing_indexes or set()
        self.column_metadata = column_metadata or {}
        self.driver_sql: list[str] = []

    async def run_sync(self, *args: object, **kwargs: object) -> object:
        return None

    async def execute(self, statement: object, params: dict[str, object]) -> _FakeResult:
        query = str(statement)
        table_name = str(params.get("table_name") or "")
        column_name = str(params.get("column_name") or "")
        index_name = str(params.get("index_name") or "")
        if "c.data_type" in query:
            return _FakeResult(self.column_metadata.get((table_name, column_name)))
        if "information_schema.columns" in query:
            return _FakeResult(
                (1,) if (table_name, column_name) in self.existing_columns else None
            )
        return _FakeResult((1,) if index_name in self.existing_indexes else None)

    async def exec_driver_sql(self, statement: str, *args: object, **kwargs: object) -> str:
        _ = args
        _ = kwargs
        self.driver_sql.append(statement)
        return "executed"


class StartupMigrationTests(unittest.IsolatedAsyncioTestCase):
    async def test_existing_add_column_if_not_exists_is_skipped_before_postgres_lock(self) -> None:
        raw = _FakeConnection(existing_columns={("users", "balance")})
        conn = _StartupMigrationConnection(raw)

        result = await conn.exec_driver_sql(
            "ALTER TABLE IF EXISTS users "
            "ADD COLUMN IF NOT EXISTS balance integer NOT NULL DEFAULT 0"
        )

        self.assertIsNone(result)
        self.assertEqual(raw.driver_sql, [])

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

    async def test_usage_total_backfill_is_skipped_on_startup(self) -> None:
        raw = _FakeConnection()
        conn = _StartupMigrationConnection(raw)

        result = await conn.exec_driver_sql(
            "WITH sums AS ("
            "  SELECT api_key_id, COALESCE(SUM(cost_usd_micros), 0) AS cost_micros "
            "  FROM llm_usage_events "
            "  WHERE api_key_id IS NOT NULL "
            "  GROUP BY api_key_id"
            ") "
            "UPDATE api_keys k "
            "SET spend_usd_micros_total = sums.cost_micros "
            "FROM sums "
            "WHERE k.id = sums.api_key_id"
        )

        self.assertIsNone(result)
        self.assertEqual(raw.driver_sql, [])

    async def test_existing_set_default_is_skipped_before_postgres_lock(self) -> None:
        raw = _FakeConnection(
            column_metadata={
                ("organizations", "registration_enabled"): (
                    "boolean",
                    "bool",
                    None,
                    "NO",
                    "true",
                )
            }
        )
        conn = _StartupMigrationConnection(raw)

        result = await conn.exec_driver_sql(
            "ALTER TABLE IF EXISTS organizations "
            "ALTER COLUMN registration_enabled SET DEFAULT true"
        )

        self.assertIsNone(result)
        self.assertEqual(raw.driver_sql, [])

    async def test_missing_set_default_still_runs(self) -> None:
        raw = _FakeConnection(
            column_metadata={
                ("organizations", "registration_enabled"): (
                    "boolean",
                    "bool",
                    None,
                    "NO",
                    None,
                )
            }
        )
        conn = _StartupMigrationConnection(raw)

        result = await conn.exec_driver_sql(
            "ALTER TABLE IF EXISTS organizations "
            "ALTER COLUMN registration_enabled SET DEFAULT true"
        )

        self.assertEqual(result, "executed")
        self.assertEqual(len(raw.driver_sql), 1)

    async def test_existing_set_not_null_is_skipped_before_postgres_lock(self) -> None:
        raw = _FakeConnection(
            column_metadata={
                ("organizations", "registration_enabled"): (
                    "boolean",
                    "bool",
                    None,
                    "NO",
                    "true",
                )
            }
        )
        conn = _StartupMigrationConnection(raw)

        result = await conn.exec_driver_sql(
            "ALTER TABLE IF EXISTS organizations "
            "ALTER COLUMN registration_enabled SET NOT NULL"
        )

        self.assertIsNone(result)
        self.assertEqual(raw.driver_sql, [])

    async def test_existing_alter_type_is_skipped_before_postgres_lock(self) -> None:
        raw = _FakeConnection(
            column_metadata={
                ("announcements", "content"): (
                    "character varying",
                    "varchar",
                    2000,
                    "YES",
                    None,
                )
            }
        )
        conn = _StartupMigrationConnection(raw)

        result = await conn.exec_driver_sql(
            "ALTER TABLE IF EXISTS announcements "
            "ALTER COLUMN content TYPE varchar(2000)"
        )

        self.assertIsNone(result)
        self.assertEqual(raw.driver_sql, [])

    def test_healthz_route_is_registered_without_api_prefix(self) -> None:
        app = create_app()
        paths = {route.path for route in app.routes}

        self.assertIn("/healthz", paths)


if __name__ == "__main__":
    unittest.main()
