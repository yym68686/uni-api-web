from __future__ import annotations

import asyncio
import datetime as dt
import logging
import re
from contextlib import asynccontextmanager, suppress
from typing import Any

from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware
from sqlalchemy import text

from app.core.config import settings
from app.api.router import router as api_router
from app.db import SessionLocal, engine
from app.models.base import Base
from app.storage.announcements_db import ensure_seed_announcements
from app.storage.analytics_outbox import run_dataocean_outbox_worker
from app.storage.models_db import ensure_default_model_pricing_rules
from app.storage.orgs_db import ensure_default_org, ensure_membership
from app.storage.referrals_db import confirm_due_referral_bonuses

import app.models  # noqa: F401

logger = logging.getLogger(__name__)

_STARTUP_MIGRATION_LOCK_ID = 177895882971965
_USAGE_MAINTENANCE_LOCK_ID = 177895882971966
_MIN_USAGE_EVENTS_RETENTION_DAYS = 7
_MIN_USAGE_HOURLY_STATS_RETENTION_DAYS = 30
_MIN_USAGE_RETENTION_BATCH_SIZE = 1000
_MAX_USAGE_RETENTION_BATCH_SIZE = 100000
_MIN_USAGE_RETENTION_MAX_BATCHES = 1
_MAX_USAGE_RETENTION_MAX_BATCHES = 100
_ADD_COLUMN_IF_MISSING_RE = re.compile(
    r"^\s*ALTER\s+TABLE\s+IF\s+EXISTS\s+([a-zA-Z_][a-zA-Z0-9_]*)\s+"
    r"ADD\s+COLUMN\s+IF\s+NOT\s+EXISTS\s+([a-zA-Z_][a-zA-Z0-9_]*)\b",
    re.IGNORECASE,
)
_CREATE_INDEX_IF_MISSING_RE = re.compile(
    r"^\s*CREATE\s+(?:UNIQUE\s+)?INDEX\s+IF\s+NOT\s+EXISTS\s+([a-zA-Z_][a-zA-Z0-9_]*)\b",
    re.IGNORECASE,
)
_ALTER_COLUMN_SET_DEFAULT_RE = re.compile(
    r"^\s*ALTER\s+TABLE\s+IF\s+EXISTS\s+([a-zA-Z_][a-zA-Z0-9_]*)\s+"
    r"ALTER\s+COLUMN\s+([a-zA-Z_][a-zA-Z0-9_]*)\s+SET\s+DEFAULT\s+(.+?)\s*;?\s*$",
    re.IGNORECASE,
)
_ALTER_COLUMN_SET_NOT_NULL_RE = re.compile(
    r"^\s*ALTER\s+TABLE\s+IF\s+EXISTS\s+([a-zA-Z_][a-zA-Z0-9_]*)\s+"
    r"ALTER\s+COLUMN\s+([a-zA-Z_][a-zA-Z0-9_]*)\s+SET\s+NOT\s+NULL\s*;?\s*$",
    re.IGNORECASE,
)
_ALTER_COLUMN_TYPE_RE = re.compile(
    r"^\s*ALTER\s+TABLE\s+IF\s+EXISTS\s+([a-zA-Z_][a-zA-Z0-9_]*)\s+"
    r"ALTER\s+COLUMN\s+([a-zA-Z_][a-zA-Z0-9_]*)\s+TYPE\s+(.+?)\s*;?\s*$",
    re.IGNORECASE,
)
_USAGE_TOTAL_BACKFILL_RE = re.compile(
    r"^\s*WITH\s+sums\s+AS\s*\(.+FROM\s+llm_usage_events\b.+GROUP\s+BY\s+"
    r"(?:user_id|api_key_id)\s*\)\s+UPDATE\s+(?:users|api_keys)\b.+"
    r"spend_usd_micros_total\b",
    re.IGNORECASE | re.DOTALL,
)


class _StartupMigrationConnection:
    def __init__(self, conn: Any):
        self._conn = conn

    async def run_sync(self, *args: Any, **kwargs: Any) -> Any:
        return await self._conn.run_sync(*args, **kwargs)

    async def exec_driver_sql(self, statement: str, *args: Any, **kwargs: Any) -> Any:
        if self._should_skip_usage_total_backfill(statement):
            return None
        if await self._should_skip_add_column(statement):
            return None
        if await self._should_skip_create_index(statement):
            return None
        if await self._should_skip_set_default(statement):
            return None
        if await self._should_skip_set_not_null(statement):
            return None
        if await self._should_skip_alter_type(statement):
            return None
        return await self._conn.exec_driver_sql(statement, *args, **kwargs)

    async def _should_skip_add_column(self, statement: str) -> bool:
        match = _ADD_COLUMN_IF_MISSING_RE.match(statement)
        if match is None:
            return False

        table_name, column_name = match.groups()
        result = await self._conn.execute(
            text(
                "SELECT 1 FROM information_schema.columns "
                "WHERE table_schema = current_schema() "
                "AND table_name = :table_name "
                "AND column_name = :column_name "
                "LIMIT 1"
            ),
            {"table_name": table_name, "column_name": column_name},
        )
        return result.first() is not None

    def _should_skip_usage_total_backfill(self, statement: str) -> bool:
        # Usage totals are incrementally maintained on each request. Historical
        # reconciliation scans live traffic tables and belongs in an offline job.
        return _USAGE_TOTAL_BACKFILL_RE.match(statement) is not None

    async def _column_metadata(self, table_name: str, column_name: str) -> Any | None:
        result = await self._conn.execute(
            text(
                "SELECT "
                "c.data_type, "
                "c.udt_name, "
                "c.character_maximum_length, "
                "c.is_nullable, "
                "pg_get_expr(d.adbin, d.adrelid) AS column_default "
                "FROM information_schema.columns c "
                "JOIN pg_class cls ON cls.relname = c.table_name "
                "JOIN pg_namespace n ON n.oid = cls.relnamespace "
                "AND n.nspname = c.table_schema "
                "LEFT JOIN pg_attribute a ON a.attrelid = cls.oid "
                "AND a.attname = c.column_name "
                "AND NOT a.attisdropped "
                "LEFT JOIN pg_attrdef d ON d.adrelid = cls.oid "
                "AND d.adnum = a.attnum "
                "WHERE c.table_schema = current_schema() "
                "AND c.table_name = :table_name "
                "AND c.column_name = :column_name "
                "LIMIT 1"
            ),
            {"table_name": table_name, "column_name": column_name},
        )
        return result.first()

    async def _should_skip_set_default(self, statement: str) -> bool:
        match = _ALTER_COLUMN_SET_DEFAULT_RE.match(statement)
        if match is None:
            return False

        table_name, column_name, expected_default = match.groups()
        row = await self._column_metadata(table_name, column_name)
        if row is None:
            return False

        current_default = row[4]
        if current_default is None:
            return False
        return _normalize_sql_expression(str(current_default)) == _normalize_sql_expression(
            expected_default
        )

    async def _should_skip_set_not_null(self, statement: str) -> bool:
        match = _ALTER_COLUMN_SET_NOT_NULL_RE.match(statement)
        if match is None:
            return False

        table_name, column_name = match.groups()
        row = await self._column_metadata(table_name, column_name)
        return row is not None and str(row[3]).upper() == "NO"

    async def _should_skip_alter_type(self, statement: str) -> bool:
        match = _ALTER_COLUMN_TYPE_RE.match(statement)
        if match is None:
            return False

        table_name, column_name, expected_type = match.groups()
        row = await self._column_metadata(table_name, column_name)
        return row is not None and _column_type_matches(row, expected_type)

    async def _should_skip_create_index(self, statement: str) -> bool:
        match = _CREATE_INDEX_IF_MISSING_RE.match(statement)
        if match is None:
            return False

        index_name = match.group(1)
        result = await self._conn.execute(
            text(
                "SELECT 1 "
                "FROM pg_class c "
                "JOIN pg_namespace n ON n.oid = c.relnamespace "
                "WHERE n.nspname = current_schema() "
                "AND c.relkind = 'i' "
                "AND c.relname = :index_name "
                "LIMIT 1"
            ),
            {"index_name": index_name},
        )
        return result.first() is not None


def _normalize_sql_expression(expression: str) -> str:
    normalized = expression.strip().rstrip(";")
    while normalized.startswith("(") and normalized.endswith(")"):
        inner = normalized[1:-1].strip()
        if not inner:
            break
        normalized = inner
    normalized = re.sub(
        r"::[a-zA-Z_][a-zA-Z0-9_]*(?:\s+[a-zA-Z_][a-zA-Z0-9_]*)*",
        "",
        normalized,
    )
    normalized = re.sub(r"\s+", "", normalized)
    return normalized.lower()


def _column_type_matches(row: Any, expected_type: str) -> bool:
    data_type = str(row[0] or "").lower()
    udt_name = str(row[1] or "").lower()
    char_length = row[2]
    normalized_expected = re.sub(r"\s+", " ", expected_type.strip().lower())

    varchar_match = re.fullmatch(
        r"(?:character varying|varchar)\s*\(\s*(\d+)\s*\)", normalized_expected
    )
    if varchar_match is not None:
        expected_length = int(varchar_match.group(1))
        return data_type == "character varying" and int(char_length or 0) == expected_length

    if normalized_expected in {"timestamptz", "timestamp with time zone"}:
        return data_type == "timestamp with time zone"
    if normalized_expected in {"timestamp", "timestamp without time zone"}:
        return data_type == "timestamp without time zone"
    if normalized_expected in {"bool", "boolean"}:
        return data_type == "boolean"
    if normalized_expected in {"int", "integer", "int4"}:
        return data_type == "integer" or udt_name == "int4"
    if normalized_expected in {"bigint", "int8"}:
        return data_type == "bigint" or udt_name == "int8"
    if normalized_expected == "uuid":
        return data_type == "uuid" or udt_name == "uuid"
    if normalized_expected == "text":
        return data_type == "text"

    return data_type == normalized_expected or udt_name == normalized_expected


def _clamp_int(value: int, *, minimum: int, maximum: int) -> int:
    return min(max(int(value), minimum), maximum)


async def _ensure_usage_maintenance_indexes(conn: Any) -> None:
    await conn.execute(
        text(
            "CREATE INDEX CONCURRENTLY IF NOT EXISTS ix_llm_usage_events_created_at "
            "ON llm_usage_events(created_at)"
        )
    )
    await conn.execute(
        text(
            "CREATE INDEX CONCURRENTLY IF NOT EXISTS ix_llm_usage_hourly_org_bucket "
            "ON llm_usage_hourly_stats(org_id, bucket_start)"
        )
    )


async def _run_usage_table_maintenance_once() -> None:
    try:
        async with engine.connect() as raw_conn:
            conn = await raw_conn.execution_options(isolation_level="AUTOCOMMIT")
            locked = (
                await conn.execute(
                    text("SELECT pg_try_advisory_lock(:lock_id)"),
                    {"lock_id": _USAGE_MAINTENANCE_LOCK_ID},
                )
            ).scalar()
            if not bool(locked):
                return
            try:
                retention_days = max(
                    int(settings.usage_events_retention_days),
                    _MIN_USAGE_EVENTS_RETENTION_DAYS,
                )
                hourly_retention_days = max(
                    int(settings.usage_hourly_stats_retention_days),
                    retention_days,
                    _MIN_USAGE_HOURLY_STATS_RETENTION_DAYS,
                )
                batch_size = _clamp_int(
                    int(settings.usage_retention_batch_size),
                    minimum=_MIN_USAGE_RETENTION_BATCH_SIZE,
                    maximum=_MAX_USAGE_RETENTION_BATCH_SIZE,
                )
                max_batches = _clamp_int(
                    int(settings.usage_retention_max_batches),
                    minimum=_MIN_USAGE_RETENTION_MAX_BATCHES,
                    maximum=_MAX_USAGE_RETENTION_MAX_BATCHES,
                )
                now = dt.datetime.now(dt.timezone.utc)
                raw_cutoff = now - dt.timedelta(days=retention_days)
                hourly_cutoff = now - dt.timedelta(days=hourly_retention_days)

                await _ensure_usage_maintenance_indexes(conn)
                await conn.execute(
                    text(
                        """
                        WITH rollup AS (
                          SELECT
                            org_id,
                            user_id,
                            model_id,
                            date_trunc('hour', created_at) AS bucket_start,
                            COUNT(*)::bigint AS requests,
                            COALESCE(SUM(CASE WHEN ok IS FALSE THEN 1 ELSE 0 END), 0)::bigint AS errors,
                            COALESCE(SUM(input_tokens), 0)::bigint AS input_tokens,
                            COALESCE(SUM(cached_tokens), 0)::bigint AS cached_tokens,
                            COALESCE(SUM(output_tokens), 0)::bigint AS output_tokens,
                            COALESCE(SUM(total_tokens), 0)::bigint AS total_tokens,
                            COALESCE(SUM(cost_usd_micros), 0)::bigint AS cost_usd_micros
                          FROM llm_usage_events
                          WHERE created_at >= :hourly_cutoff
                            AND created_at < date_trunc('hour', now())
                          GROUP BY org_id, user_id, model_id, date_trunc('hour', created_at)
                        )
                        INSERT INTO llm_usage_hourly_stats (
                          org_id,
                          user_id,
                          model_id,
                          bucket_start,
                          requests,
                          errors,
                          input_tokens,
                          cached_tokens,
                          output_tokens,
                          total_tokens,
                          cost_usd_micros,
                          updated_at
                        )
                        SELECT
                          org_id,
                          user_id,
                          model_id,
                          bucket_start,
                          requests,
                          errors,
                          input_tokens,
                          cached_tokens,
                          output_tokens,
                          total_tokens,
                          cost_usd_micros,
                          now()
                        FROM rollup
                        ON CONFLICT (org_id, user_id, model_id, bucket_start) DO UPDATE SET
                          requests = EXCLUDED.requests,
                          errors = EXCLUDED.errors,
                          input_tokens = EXCLUDED.input_tokens,
                          cached_tokens = EXCLUDED.cached_tokens,
                          output_tokens = EXCLUDED.output_tokens,
                          total_tokens = EXCLUDED.total_tokens,
                          cost_usd_micros = EXCLUDED.cost_usd_micros,
                          updated_at = now()
                        """
                    ),
                    {"hourly_cutoff": hourly_cutoff},
                )
                deleted_total = 0
                for _ in range(max_batches):
                    result = await conn.execute(
                        text(
                            """
                            WITH doomed AS (
                              SELECT id
                              FROM llm_usage_events
                              WHERE created_at < :raw_cutoff
                              ORDER BY created_at
                              LIMIT :batch_size
                            )
                            DELETE FROM llm_usage_events e
                            USING doomed
                            WHERE e.id = doomed.id
                            """
                        ),
                        {"raw_cutoff": raw_cutoff, "batch_size": batch_size},
                    )
                    deleted = max(int(result.rowcount or 0), 0)
                    deleted_total += deleted
                    if deleted < batch_size:
                        break
                await conn.execute(
                    text("DELETE FROM llm_usage_hourly_stats WHERE bucket_start < :hourly_cutoff"),
                    {"hourly_cutoff": hourly_cutoff},
                )
                await conn.execute(text("ANALYZE llm_usage_events"))
                await conn.execute(text("ANALYZE llm_usage_hourly_stats"))
                stats = (
                    await conn.execute(
                        text(
                            "SELECT COALESCE(n_dead_tup, 0), COALESCE(n_mod_since_analyze, 0) "
                            "FROM pg_stat_user_tables "
                            "WHERE relname = 'llm_usage_events'"
                        )
                    )
                ).first()
                if stats is not None:
                    dead_tuples = int(stats[0] or 0)
                    modified_tuples = int(stats[1] or 0)
                    if dead_tuples >= 50000 or modified_tuples >= 50000:
                        await conn.execute(text("VACUUM (ANALYZE) llm_usage_events"))
                if deleted_total > 0:
                    logger.info(
                        "usage table maintenance deleted %s raw events older than %s days",
                        deleted_total,
                        retention_days,
                    )
            finally:
                await conn.execute(
                    text("SELECT pg_advisory_unlock(:lock_id)"),
                    {"lock_id": _USAGE_MAINTENANCE_LOCK_ID},
                )
    except Exception:
        logger.exception("usage table maintenance failed")


def create_app() -> FastAPI:
    @asynccontextmanager
    async def lifespan(_app: FastAPI):
        async with engine.begin() as raw_conn:
            await raw_conn.exec_driver_sql(
                f"SELECT pg_advisory_xact_lock({_STARTUP_MIGRATION_LOCK_ID})"
            )
            conn = _StartupMigrationConnection(raw_conn)
            await conn.run_sync(Base.metadata.create_all)
            # Minimal dev-time migration for early-stage schema changes.
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS users "
                "ADD COLUMN IF NOT EXISTS role varchar(16) NOT NULL DEFAULT 'user'"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS users "
                "ADD COLUMN IF NOT EXISTS balance integer NOT NULL DEFAULT 0"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS users "
                "ALTER COLUMN balance SET DEFAULT 0"
            )
            await conn.exec_driver_sql(
                "UPDATE users SET balance = 0 WHERE balance IS NULL"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS users "
                "ADD COLUMN IF NOT EXISTS balance_usd_cents integer NOT NULL DEFAULT 0"
            )
            await conn.exec_driver_sql(
                "UPDATE users "
                "SET balance_usd_cents = balance * 100 "
                "WHERE balance_usd_cents = 0 AND balance <> 0"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS users "
                "ADD COLUMN IF NOT EXISTS spend_usd_micros_total bigint NOT NULL DEFAULT 0"
            )
            await conn.exec_driver_sql(
                "UPDATE users "
                "SET spend_usd_micros_total = 0 "
                "WHERE spend_usd_micros_total IS NULL"
            )
            await conn.exec_driver_sql(
                "WITH sums AS ("
                "  SELECT user_id, COALESCE(SUM(cost_usd_micros), 0) AS cost_micros "
                "  FROM llm_usage_events "
                "  GROUP BY user_id"
                ") "
                "UPDATE users u "
                "SET spend_usd_micros_total = sums.cost_micros "
                "FROM sums "
                "WHERE u.id = sums.user_id"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS users "
                "ADD COLUMN IF NOT EXISTS banned_at timestamptz"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS users "
                "ADD COLUMN IF NOT EXISTS soft_limited_at timestamptz"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS users "
                "ADD COLUMN IF NOT EXISTS first_api_call_at timestamptz"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS users "
                "ADD COLUMN IF NOT EXISTS group_name varchar(64) NOT NULL DEFAULT 'default'"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS users "
                "ADD COLUMN IF NOT EXISTS password_set_at timestamptz"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS users "
                "ADD COLUMN IF NOT EXISTS invite_code varchar(16)"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS users "
                "ADD COLUMN IF NOT EXISTS invited_by_user_id uuid"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS users "
                "ADD COLUMN IF NOT EXISTS invited_at timestamptz"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS users "
                "ADD COLUMN IF NOT EXISTS signup_ip varchar(64)"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS users "
                "ADD COLUMN IF NOT EXISTS signup_device_id varchar(64)"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS users "
                "ADD COLUMN IF NOT EXISTS signup_user_agent varchar(255)"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS users "
                "ADD COLUMN IF NOT EXISTS first_payment_email varchar(254)"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS users "
                "ADD COLUMN IF NOT EXISTS first_payment_ip varchar(64)"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS users "
                "ADD COLUMN IF NOT EXISTS first_payment_device_id varchar(64)"
            )
            await conn.exec_driver_sql(
                "DO $$ BEGIN "
                "IF NOT EXISTS ("
                "  SELECT 1 FROM pg_constraint WHERE conname = 'users_invited_by_user_id_fkey'"
                ") THEN "
                "  ALTER TABLE users "
                "  ADD CONSTRAINT users_invited_by_user_id_fkey "
                "  FOREIGN KEY (invited_by_user_id) REFERENCES users(id) ON DELETE SET NULL; "
                "END IF; "
                "END $$;"
            )
            await conn.exec_driver_sql(
                "CREATE INDEX IF NOT EXISTS ix_users_invited_by_user_id "
                "ON users(invited_by_user_id)"
            )
            await conn.exec_driver_sql(
                "CREATE UNIQUE INDEX IF NOT EXISTS uq_users_invite_code "
                "ON users(invite_code) "
                "WHERE invite_code IS NOT NULL"
            )
            await conn.exec_driver_sql(
                "UPDATE users u "
                "SET password_set_at = u.created_at "
                "WHERE password_set_at IS NULL "
                "AND NOT EXISTS (SELECT 1 FROM oauth_identities oi WHERE oi.user_id = u.id)"
            )
            await conn.exec_driver_sql(
                "DELETE FROM oauth_identities WHERE id IN ("
                "  SELECT id FROM ("
                "    SELECT id, ROW_NUMBER() OVER (PARTITION BY user_id ORDER BY created_at DESC) AS rn "
                "    FROM oauth_identities WHERE provider = 'google'"
                "  ) t WHERE t.rn > 1"
                ")"
            )
            await conn.exec_driver_sql(
                "CREATE UNIQUE INDEX IF NOT EXISTS uq_oauth_user_google "
                "ON oauth_identities(user_id) WHERE provider = 'google'"
            )

            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS llm_usage_events "
                "ADD COLUMN IF NOT EXISTS total_duration_ms integer NOT NULL DEFAULT 0"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS llm_usage_events "
                "ADD COLUMN IF NOT EXISTS cached_tokens integer NOT NULL DEFAULT 0"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS llm_usage_events "
                "ADD COLUMN IF NOT EXISTS ttft_ms integer NOT NULL DEFAULT 0"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS llm_usage_events "
                "ADD COLUMN IF NOT EXISTS source_ip varchar(64)"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS llm_usage_events "
                "ADD COLUMN IF NOT EXISTS request_endpoint varchar(255)"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS llm_usage_events "
                "ADD COLUMN IF NOT EXISTS is_streaming boolean NOT NULL DEFAULT false"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS llm_usage_events "
                "ADD COLUMN IF NOT EXISTS api_key_id uuid"
            )
            await conn.exec_driver_sql(
                "DO $$ BEGIN "
                "IF NOT EXISTS ("
                "  SELECT 1 FROM pg_constraint WHERE conname = 'llm_usage_events_api_key_id_fkey'"
                ") THEN "
                "  ALTER TABLE llm_usage_events "
                "  ADD CONSTRAINT llm_usage_events_api_key_id_fkey "
                "  FOREIGN KEY (api_key_id) REFERENCES api_keys(id) ON DELETE SET NULL; "
                "END IF; "
                "END $$;"
            )
            await conn.exec_driver_sql(
                "CREATE INDEX IF NOT EXISTS ix_llm_usage_events_org_created_at "
                "ON llm_usage_events(org_id, created_at)"
            )
            await conn.exec_driver_sql(
                "CREATE INDEX IF NOT EXISTS ix_llm_usage_events_org_user_created_at "
                "ON llm_usage_events(org_id, user_id, created_at)"
            )
            await conn.exec_driver_sql(
                "CREATE INDEX IF NOT EXISTS ix_llm_usage_events_org_model_created_at "
                "ON llm_usage_events(org_id, model_id, created_at)"
            )

            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS email_verification_codes "
                "ALTER COLUMN email TYPE varchar(254)"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS email_verification_codes "
                "ADD COLUMN IF NOT EXISTS source_ip varchar(64)"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS email_verification_codes "
                "ADD COLUMN IF NOT EXISTS used_at timestamptz"
            )

            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS api_keys "
                "ADD COLUMN IF NOT EXISTS user_id uuid"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS api_keys "
                "ADD COLUMN IF NOT EXISTS key_plaintext text"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS api_keys "
                "ADD COLUMN IF NOT EXISTS spend_usd_micros_total bigint NOT NULL DEFAULT 0"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS api_keys "
                "ADD COLUMN IF NOT EXISTS spend_limit_usd_micros bigint"
            )
            await conn.exec_driver_sql(
                "DO $$ BEGIN "
                "IF NOT EXISTS ("
                "  SELECT 1 FROM pg_constraint WHERE conname = 'api_keys_user_id_fkey'"
                ") THEN "
                "  ALTER TABLE api_keys "
                "  ADD CONSTRAINT api_keys_user_id_fkey "
                "  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE; "
                "END IF; "
                "END $$;"
            )
            await conn.exec_driver_sql(
                "UPDATE api_keys "
                "SET user_id = (SELECT id FROM users ORDER BY created_at ASC LIMIT 1) "
                "WHERE user_id IS NULL"
            )
            await conn.exec_driver_sql(
                "UPDATE api_keys "
                "SET spend_usd_micros_total = 0 "
                "WHERE spend_usd_micros_total IS NULL"
            )
            await conn.exec_driver_sql(
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

            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS organizations "
                "ADD COLUMN IF NOT EXISTS registration_enabled boolean"
            )
            await conn.exec_driver_sql(
                "UPDATE organizations SET registration_enabled = true WHERE registration_enabled IS NULL"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS organizations "
                "ALTER COLUMN registration_enabled SET DEFAULT true"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS organizations "
                "ALTER COLUMN registration_enabled SET NOT NULL"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS organizations "
                "ADD COLUMN IF NOT EXISTS billing_topup_enabled boolean"
            )
            await conn.exec_driver_sql(
                "UPDATE organizations SET billing_topup_enabled = true WHERE billing_topup_enabled IS NULL"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS organizations "
                "ALTER COLUMN billing_topup_enabled SET DEFAULT true"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS organizations "
                "ALTER COLUMN billing_topup_enabled SET NOT NULL"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS organizations "
                "ADD COLUMN IF NOT EXISTS billing_payment_card_enabled boolean"
            )
            await conn.exec_driver_sql(
                "UPDATE organizations SET billing_payment_card_enabled = true "
                "WHERE billing_payment_card_enabled IS NULL"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS organizations "
                "ALTER COLUMN billing_payment_card_enabled SET DEFAULT true"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS organizations "
                "ALTER COLUMN billing_payment_card_enabled SET NOT NULL"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS organizations "
                "ADD COLUMN IF NOT EXISTS billing_payment_alipay_enabled boolean"
            )
            await conn.exec_driver_sql(
                "UPDATE organizations SET billing_payment_alipay_enabled = true "
                "WHERE billing_payment_alipay_enabled IS NULL"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS organizations "
                "ALTER COLUMN billing_payment_alipay_enabled SET DEFAULT true"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS organizations "
                "ALTER COLUMN billing_payment_alipay_enabled SET NOT NULL"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS organizations "
                "ADD COLUMN IF NOT EXISTS billing_payment_wxpay_enabled boolean"
            )
            await conn.exec_driver_sql(
                "UPDATE organizations SET billing_payment_wxpay_enabled = true "
                "WHERE billing_payment_wxpay_enabled IS NULL"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS organizations "
                "ALTER COLUMN billing_payment_wxpay_enabled SET DEFAULT true"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS organizations "
                "ALTER COLUMN billing_payment_wxpay_enabled SET NOT NULL"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS organizations "
                "ADD COLUMN IF NOT EXISTS new_user_trial_enabled boolean"
            )
            await conn.exec_driver_sql(
                "UPDATE organizations SET new_user_trial_enabled = false "
                "WHERE new_user_trial_enabled IS NULL"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS organizations "
                "ALTER COLUMN new_user_trial_enabled SET DEFAULT false"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS organizations "
                "ALTER COLUMN new_user_trial_enabled SET NOT NULL"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS organizations "
                "ADD COLUMN IF NOT EXISTS new_user_trial_balance_usd_cents integer"
            )
            await conn.exec_driver_sql(
                "UPDATE organizations SET new_user_trial_balance_usd_cents = 0 "
                "WHERE new_user_trial_balance_usd_cents IS NULL"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS organizations "
                "ALTER COLUMN new_user_trial_balance_usd_cents SET DEFAULT 0"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS organizations "
                "ALTER COLUMN new_user_trial_balance_usd_cents SET NOT NULL"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS organizations "
                "ADD COLUMN IF NOT EXISTS model_pricing_initialized boolean"
            )
            await conn.exec_driver_sql(
                "UPDATE organizations SET model_pricing_initialized = false "
                "WHERE model_pricing_initialized IS NULL"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS organizations "
                "ALTER COLUMN model_pricing_initialized SET DEFAULT false"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS organizations "
                "ALTER COLUMN model_pricing_initialized SET NOT NULL"
            )

            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS billing_topups "
                "ADD COLUMN IF NOT EXISTS payer_email varchar(254)"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS billing_topups "
                "ADD COLUMN IF NOT EXISTS client_ip varchar(64)"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS billing_topups "
                "ADD COLUMN IF NOT EXISTS client_device_id varchar(64)"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS billing_topups "
                "ADD COLUMN IF NOT EXISTS refunded_at timestamptz"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS billing_topups "
                "ADD COLUMN IF NOT EXISTS provider varchar(24)"
            )
            await conn.exec_driver_sql(
                "UPDATE billing_topups "
                "SET provider = 'zhupay' "
                "WHERE provider IS NULL AND currency = 'CNY'"
            )
            await conn.exec_driver_sql(
                "UPDATE billing_topups "
                "SET provider = 'creem' "
                "WHERE provider IS NULL AND (currency = 'USD' OR currency IS NULL)"
            )
            await conn.exec_driver_sql(
                "CREATE INDEX IF NOT EXISTS ix_billing_topups_order_id "
                "ON billing_topups(order_id)"
            )
            await conn.exec_driver_sql(
                "CREATE INDEX IF NOT EXISTS ix_billing_topups_checkout_id "
                "ON billing_topups(checkout_id)"
            )

            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS announcements "
                "ADD COLUMN IF NOT EXISTS title_zh varchar(180)"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS announcements "
                "ADD COLUMN IF NOT EXISTS title_en varchar(180)"
            )

            # Rename legacy columns (meta/meta_zh/meta_en) -> (content/content_zh/content_en).
            await conn.exec_driver_sql(
                "DO $$ BEGIN "
                "IF EXISTS ("
                "  SELECT 1 FROM information_schema.columns "
                "  WHERE table_name='announcements' AND column_name='meta'"
                ") AND NOT EXISTS ("
                "  SELECT 1 FROM information_schema.columns "
                "  WHERE table_name='announcements' AND column_name='content'"
                ") THEN "
                "  ALTER TABLE announcements RENAME COLUMN meta TO content; "
                "END IF; "
                "END $$;"
            )
            await conn.exec_driver_sql(
                "DO $$ BEGIN "
                "IF EXISTS ("
                "  SELECT 1 FROM information_schema.columns "
                "  WHERE table_name='announcements' AND column_name='meta_zh'"
                ") AND NOT EXISTS ("
                "  SELECT 1 FROM information_schema.columns "
                "  WHERE table_name='announcements' AND column_name='content_zh'"
                ") THEN "
                "  ALTER TABLE announcements RENAME COLUMN meta_zh TO content_zh; "
                "END IF; "
                "END $$;"
            )
            await conn.exec_driver_sql(
                "DO $$ BEGIN "
                "IF EXISTS ("
                "  SELECT 1 FROM information_schema.columns "
                "  WHERE table_name='announcements' AND column_name='meta_en'"
                ") AND NOT EXISTS ("
                "  SELECT 1 FROM information_schema.columns "
                "  WHERE table_name='announcements' AND column_name='content_en'"
                ") THEN "
                "  ALTER TABLE announcements RENAME COLUMN meta_en TO content_en; "
                "END IF; "
                "END $$;"
            )

            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS announcements "
                "ADD COLUMN IF NOT EXISTS content_zh varchar(2000)"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS announcements "
                "ADD COLUMN IF NOT EXISTS content_en varchar(2000)"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS announcements "
                "ALTER COLUMN content TYPE varchar(2000)"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS announcements "
                "ALTER COLUMN content_zh TYPE varchar(2000)"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS announcements "
                "ALTER COLUMN content_en TYPE varchar(2000)"
            )
            await conn.exec_driver_sql(
                "UPDATE announcements SET title_zh = title WHERE title_zh IS NULL"
            )
            await conn.exec_driver_sql(
                "UPDATE announcements SET content_zh = content WHERE content_zh IS NULL"
            )

            await conn.exec_driver_sql(
                "CREATE INDEX IF NOT EXISTS ix_referral_bonus_events_inviter_user_id "
                "ON referral_bonus_events(inviter_user_id)"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS referral_bonus_events "
                "ADD COLUMN IF NOT EXISTS invitee_confirmed_at timestamptz"
            )
            await conn.exec_driver_sql(
                "CREATE INDEX IF NOT EXISTS ix_referral_bonus_events_invitee_user_id "
                "ON referral_bonus_events(invitee_user_id)"
            )
            await conn.exec_driver_sql(
                "CREATE UNIQUE INDEX IF NOT EXISTS uq_referral_bonus_active_invitee "
                "ON referral_bonus_events(invitee_user_id) "
                "WHERE status IN ('pending', 'confirmed')"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS analytics_outbox_events "
                "ADD COLUMN IF NOT EXISTS updated_at timestamptz NOT NULL DEFAULT now()"
            )
            await conn.exec_driver_sql(
                "CREATE INDEX IF NOT EXISTS ix_analytics_outbox_status_next "
                "ON analytics_outbox_events(status, next_attempt_at)"
            )
            await conn.exec_driver_sql(
                "CREATE INDEX IF NOT EXISTS ix_analytics_outbox_created_at "
                "ON analytics_outbox_events(created_at DESC)"
            )
        # Bootstrap the default org and backfill memberships for existing users.
        async with SessionLocal() as session:
            org = await ensure_default_org(session)
            await ensure_default_model_pricing_rules(session, org_id=org.id)
            from sqlalchemy import select
            from app.models.user import User
            from app.models.membership import Membership

            users = (await session.execute(select(User).order_by(User.created_at.asc()))).scalars().all()
            existing = (
                await session.execute(
                    select(Membership.user_id).where(Membership.org_id == org.id)
                )
            ).scalars().all()
            existing_ids = set(existing)
            for idx, user in enumerate(users):
                if user.id in existing_ids:
                    continue
                role = "owner" if idx == 0 else "developer"
                await ensure_membership(session, org_id=org.id, user_id=user.id, role=role)
        if settings.app_env == "dev" and settings.seed_demo_data:
            async with SessionLocal() as session:
                await ensure_seed_announcements(session)

        stop_event = asyncio.Event()

        async def referral_worker():
            while not stop_event.is_set():
                try:
                    async with SessionLocal() as session:
                        await confirm_due_referral_bonuses(session)
                except Exception:
                    logger.exception("referral bonus worker failed")
                try:
                    await asyncio.wait_for(stop_event.wait(), timeout=300)
                except asyncio.TimeoutError:
                    continue

        referral_task = asyncio.create_task(referral_worker())
        dataocean_task = asyncio.create_task(run_dataocean_outbox_worker(stop_event))
        usage_maintenance_task = asyncio.create_task(_run_usage_table_maintenance_once())
        yield
        stop_event.set()
        referral_task.cancel()
        dataocean_task.cancel()
        usage_maintenance_task.cancel()
        with suppress(asyncio.CancelledError):
            await referral_task
        with suppress(asyncio.CancelledError):
            await dataocean_task
        with suppress(asyncio.CancelledError):
            await usage_maintenance_task

    app = FastAPI(title=settings.app_name, lifespan=lifespan)

    app.add_middleware(
        CORSMiddleware,
        allow_origins=["*"],
        allow_credentials=True,
        allow_methods=["*"],
        allow_headers=["*"],
    )

    @app.get("/healthz", include_in_schema=False)
    async def healthz() -> dict[str, bool]:
        return {"ok": True}

    app.include_router(api_router, prefix=settings.api_prefix)
    return app


app = create_app()
