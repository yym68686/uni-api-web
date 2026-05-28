from __future__ import annotations

import datetime as dt
import uuid

from sqlalchemy import BigInteger, DateTime, ForeignKey, Index, String
from sqlalchemy.dialects.postgresql import UUID
from sqlalchemy.orm import Mapped, mapped_column

from app.models.base import Base


class LlmUsageHourlyStat(Base):
    __tablename__ = "llm_usage_hourly_stats"
    __table_args__ = (
        Index("ix_llm_usage_hourly_org_bucket", "org_id", "bucket_start"),
        Index("ix_llm_usage_hourly_org_user_bucket", "org_id", "user_id", "bucket_start"),
    )

    org_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), ForeignKey("organizations.id", ondelete="CASCADE"), primary_key=True
    )
    user_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), ForeignKey("users.id", ondelete="CASCADE"), primary_key=True
    )
    model_id: Mapped[str] = mapped_column(String(200), primary_key=True)
    bucket_start: Mapped[dt.datetime] = mapped_column(DateTime(timezone=True), primary_key=True)

    requests: Mapped[int] = mapped_column(BigInteger, nullable=False, default=0)
    errors: Mapped[int] = mapped_column(BigInteger, nullable=False, default=0)
    input_tokens: Mapped[int] = mapped_column(BigInteger, nullable=False, default=0)
    cached_tokens: Mapped[int] = mapped_column(BigInteger, nullable=False, default=0)
    output_tokens: Mapped[int] = mapped_column(BigInteger, nullable=False, default=0)
    total_tokens: Mapped[int] = mapped_column(BigInteger, nullable=False, default=0)
    cost_usd_micros: Mapped[int] = mapped_column(BigInteger, nullable=False, default=0)
    updated_at: Mapped[dt.datetime] = mapped_column(
        DateTime(timezone=True),
        default=lambda: dt.datetime.now(dt.timezone.utc),
        onupdate=lambda: dt.datetime.now(dt.timezone.utc),
        nullable=False,
    )
