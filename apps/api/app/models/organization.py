from __future__ import annotations

import datetime as dt
import uuid

from sqlalchemy import Boolean, DateTime, Integer, String
from sqlalchemy.dialects.postgresql import UUID
from sqlalchemy.orm import Mapped, mapped_column

from app.models.base import Base


class Organization(Base):
    __tablename__ = "organizations"

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    name: Mapped[str] = mapped_column(String(120), nullable=False, default="Default")
    registration_enabled: Mapped[bool] = mapped_column(Boolean, nullable=False, default=True)
    billing_topup_enabled: Mapped[bool] = mapped_column(Boolean, nullable=False, default=True)
    billing_payment_card_enabled: Mapped[bool] = mapped_column(Boolean, nullable=False, default=True)
    billing_payment_alipay_enabled: Mapped[bool] = mapped_column(Boolean, nullable=False, default=True)
    billing_payment_wxpay_enabled: Mapped[bool] = mapped_column(Boolean, nullable=False, default=True)
    model_pricing_initialized: Mapped[bool] = mapped_column(Boolean, nullable=False, default=False)
    new_user_trial_enabled: Mapped[bool] = mapped_column(Boolean, nullable=False, default=False)
    new_user_trial_balance_usd_cents: Mapped[int] = mapped_column(Integer, nullable=False, default=0)

    created_at: Mapped[dt.datetime] = mapped_column(
        DateTime(timezone=True), default=lambda: dt.datetime.now(dt.timezone.utc), nullable=False
    )
