from __future__ import annotations

import asyncio
import logging
from contextlib import asynccontextmanager, suppress

from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware

from app.core.config import settings
from app.api.router import router as api_router
from app.db import SessionLocal, engine
from app.models.base import Base
from app.storage.announcements_db import ensure_seed_announcements
from app.storage.orgs_db import ensure_default_org, ensure_membership
from app.storage.referrals_db import confirm_due_referral_bonuses

import app.models  # noqa: F401

logger = logging.getLogger(__name__)


def create_app() -> FastAPI:
    @asynccontextmanager
    async def lifespan(_app: FastAPI):
        async with engine.begin() as conn:
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
                "ADD COLUMN IF NOT EXISTS balance_usd_cents integer NOT NULL DEFAULT 0"
            )
            await conn.exec_driver_sql(
                "UPDATE users "
                "SET balance_usd_cents = balance * 100 "
                "WHERE balance_usd_cents = 0 AND balance <> 0"
            )
            await conn.exec_driver_sql(
                "ALTER TABLE IF EXISTS users "
                "ADD COLUMN IF NOT EXISTS banned_at timestamptz"
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
                "CREATE INDEX IF NOT EXISTS ix_referral_bonus_events_invitee_user_id "
                "ON referral_bonus_events(invitee_user_id)"
            )
            await conn.exec_driver_sql(
                "CREATE UNIQUE INDEX IF NOT EXISTS uq_referral_bonus_active_invitee "
                "ON referral_bonus_events(invitee_user_id) "
                "WHERE status IN ('pending', 'confirmed')"
            )
        # Bootstrap the default org and backfill memberships for existing users.
        async with SessionLocal() as session:
            org = await ensure_default_org(session)
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

        task = asyncio.create_task(referral_worker())
        yield
        stop_event.set()
        task.cancel()
        with suppress(asyncio.CancelledError):
            await task

    app = FastAPI(title=settings.app_name, lifespan=lifespan)

    app.add_middleware(
        CORSMiddleware,
        allow_origins=["*"],
        allow_credentials=True,
        allow_methods=["*"],
        allow_headers=["*"],
    )

    app.include_router(api_router, prefix=settings.api_prefix)
    return app


app = create_app()
