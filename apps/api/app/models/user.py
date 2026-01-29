from __future__ import annotations

import datetime as dt
import uuid

from sqlalchemy import DateTime, ForeignKey, Integer, String
from sqlalchemy.dialects.postgresql import UUID
from sqlalchemy.orm import Mapped, mapped_column

from app.models.base import Base


class User(Base):
    __tablename__ = "users"

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    email: Mapped[str] = mapped_column(String(254), unique=True, nullable=False)
    password_hash: Mapped[str] = mapped_column(String(255), nullable=False)
    password_set_at: Mapped[dt.datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    role: Mapped[str] = mapped_column(String(16), nullable=False, default="user")
    group_name: Mapped[str] = mapped_column(String(64), nullable=False, default="default")
    balance: Mapped[int] = mapped_column("balance_usd_cents", Integer, nullable=False, default=0)
    banned_at: Mapped[dt.datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)

    invite_code: Mapped[str | None] = mapped_column(String(16), nullable=True, unique=True)
    invited_by_user_id: Mapped[uuid.UUID | None] = mapped_column(
        UUID(as_uuid=True), ForeignKey("users.id", ondelete="SET NULL"), nullable=True
    )
    invited_at: Mapped[dt.datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)

    signup_ip: Mapped[str | None] = mapped_column(String(64), nullable=True)
    signup_device_id: Mapped[str | None] = mapped_column(String(64), nullable=True)
    signup_user_agent: Mapped[str | None] = mapped_column(String(255), nullable=True)

    first_payment_email: Mapped[str | None] = mapped_column(String(254), nullable=True)
    first_payment_ip: Mapped[str | None] = mapped_column(String(64), nullable=True)
    first_payment_device_id: Mapped[str | None] = mapped_column(String(64), nullable=True)

    created_at: Mapped[dt.datetime] = mapped_column(
        DateTime(timezone=True), default=lambda: dt.datetime.now(dt.timezone.utc), nullable=False
    )
    last_login_at: Mapped[dt.datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
